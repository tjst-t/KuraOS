package backup

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/kuraos-org/kura/internal/cmdexec"
	"github.com/robfig/cron/v3"
)

// ScheduleConfig is one entry in config.json snapshots.schedule.
// It binds a cron expression + retention policy to one or more ZFS datasets.
//
// DESIGN_PRINCIPLES priority #1: config.json is SSOT. Scheduler reads this
// struct, not SQLite.
type ScheduleConfig struct {
	// Name is a human-readable label used in snapshot names (e.g. "hourly").
	Name string `json:"name"`
	// Cron is a standard 5-field cron expression ("0 * * * *" = every hour).
	Cron string `json:"cron"`
	// Datasets is the list of ZFS datasets to snapshot (e.g. ["tank/photos"]).
	Datasets []string `json:"datasets"`
	// Retention configures how many snapshots to keep per time bucket.
	Retention RetentionPolicy `json:"retention"`
}

// RetentionPolicy specifies how many snapshots to keep per time bucket.
// Zero means "keep all" (no deletion) for that bucket.
type RetentionPolicy struct {
	// Hourly is the number of hourly snapshots to keep.
	Hourly int `json:"hourly,omitempty"`
	// Daily is the number of daily snapshots to keep.
	Daily int `json:"daily,omitempty"`
	// Monthly is the number of monthly snapshots to keep.
	Monthly int `json:"monthly,omitempty"`
}

// SnapshotInfo is the information returned by the ZFS snapshot lister.
type SnapshotInfo struct {
	// Dataset is the ZFS dataset (e.g. "tank/photos").
	Dataset string
	// Name is the snapshot name suffix (e.g. "hourly-2026-05-18T00:00:00Z").
	Name string
	// CreatedAt is the snapshot creation time.
	CreatedAt time.Time
}

// ZFSSnapshotter is the interface the Scheduler needs to create and list/destroy
// ZFS snapshots. Declared here (consumer-side) so tests inject a fake
// (DESIGN_PRINCIPLES priority #9).
type ZFSSnapshotter interface {
	CreateSnapshot(ctx context.Context, dataset, name string) error
	ListSnapshots(ctx context.Context, dataset string) ([]SnapshotInfo, error)
	DestroySnapshot(ctx context.Context, dataset, name string) error
}

// Scheduler runs cron-driven ZFS snapshots and applies retention after each
// snapshot run. Powered by github.com/robfig/cron/v3
// (DESIGN_PRINCIPLES priority #6: existing library > custom scheduler).
type Scheduler struct {
	cr         *cron.Cron
	snapshotter ZFSSnapshotter
	schedules  []ScheduleConfig
}

// NewScheduler constructs a Scheduler from a set of schedule configs and a
// ZFSSnapshotter implementation. Call Start to begin cron dispatch.
func NewScheduler(snapper ZFSSnapshotter, schedules []ScheduleConfig) *Scheduler {
	return &Scheduler{
		cr: cron.New(
			// Use the standard 5-field cron spec (not seconds-precision),
			// matching the UNIX cron convention users expect.
			cron.WithParser(cron.NewParser(
				cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
			)),
		),
		snapshotter: snapper,
		schedules:   schedules,
	}
}

// Start registers all schedules with the cron engine and starts it.
// The returned cancel function tears down the cron scheduler gracefully.
func (s *Scheduler) Start(ctx context.Context) (cancel func()) {
	for _, sched := range s.schedules {
		sched := sched // capture loop variable
		_, err := s.cr.AddFunc(sched.Cron, func() {
			s.runSchedule(ctx, sched)
		})
		if err != nil {
			log.Printf("backup: scheduler: invalid cron %q for %q: %v", sched.Cron, sched.Name, err)
		}
	}
	s.cr.Start()
	return func() {
		stopCtx := s.cr.Stop()
		<-stopCtx.Done()
	}
}

// RunNow immediately runs a named schedule (used for testing and manual triggers).
func (s *Scheduler) RunNow(ctx context.Context, name string) error {
	for _, sched := range s.schedules {
		if sched.Name == name {
			s.runSchedule(ctx, sched)
			return nil
		}
	}
	return fmt.Errorf("backup: scheduler: no schedule named %q", name)
}

// runSchedule executes one snapshot run for a schedule config: create snapshots,
// then apply retention. Any error is logged but does not abort sibling datasets.
func (s *Scheduler) runSchedule(ctx context.Context, sched ScheduleConfig) {
	ts := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	snapName := fmt.Sprintf("%s-%s", sched.Name, ts)
	for _, dataset := range sched.Datasets {
		if err := s.snapshotter.CreateSnapshot(ctx, dataset, snapName); err != nil {
			log.Printf("backup: scheduler: create snapshot %s@%s: %v", dataset, snapName, err)
			continue
		}
		if err := s.applyRetention(ctx, dataset, sched.Name, sched.Retention); err != nil {
			log.Printf("backup: scheduler: retention %s (sched=%s): %v", dataset, sched.Name, err)
		}
	}
}

// applyRetention deletes old snapshots for one dataset according to the retention
// policy. It only considers snapshots whose name starts with the schedule name prefix
// (e.g. "hourly-") so it never deletes snapshots from other schedules.
//
// Retention buckets are independent:
//   - Hourly: keep the N most recent snapshots created in distinct clock-hours.
//   - Daily:  keep the N most recent snapshots created on distinct calendar days.
//   - Monthly: keep the N most recent snapshots created in distinct calendar months.
//
// When multiple retention buckets are configured, a snapshot is kept if it
// satisfies *any* bucket. This matches the Restic retention model users expect.
func (s *Scheduler) applyRetention(ctx context.Context, dataset, schedName string, ret RetentionPolicy) error {
	all, err := s.snapshotter.ListSnapshots(ctx, dataset)
	if err != nil {
		return fmt.Errorf("list snapshots: %w", err)
	}

	prefix := schedName + "-"
	var mine []SnapshotInfo
	for _, snap := range all {
		if strings.HasPrefix(snap.Name, prefix) {
			mine = append(mine, snap)
		}
	}

	// Sort newest first.
	sort.Slice(mine, func(i, j int) bool {
		return mine[i].CreatedAt.After(mine[j].CreatedAt)
	})

	keep := retentionKeepSet(mine, ret)

	for _, snap := range mine {
		if keep[snap.Name] {
			continue
		}
		if err := s.snapshotter.DestroySnapshot(ctx, dataset, snap.Name); err != nil {
			log.Printf("backup: retention destroy %s@%s: %v", dataset, snap.Name, err)
		}
	}
	return nil
}

// retentionKeepSet returns the set of snapshot names that should be kept
// according to the retention policy. A snapshot is kept if it is the first
// (newest) snapshot seen in any unclaimed hourly/daily/monthly bucket.
func retentionKeepSet(snapshots []SnapshotInfo, ret RetentionPolicy) map[string]bool {
	keep := make(map[string]bool)

	hourBuckets := make(map[string]bool) // "2026-05-18T14"
	dayBuckets := make(map[string]bool)  // "2026-05-18"
	monBuckets := make(map[string]bool)  // "2026-05"

	for _, snap := range snapshots {
		t := snap.CreatedAt

		if ret.Hourly > 0 && len(hourBuckets) < ret.Hourly {
			key := t.Format("2006-01-02T15")
			if !hourBuckets[key] {
				hourBuckets[key] = true
				keep[snap.Name] = true
			}
		}

		if ret.Daily > 0 && len(dayBuckets) < ret.Daily {
			key := t.Format("2006-01-02")
			if !dayBuckets[key] {
				dayBuckets[key] = true
				keep[snap.Name] = true
			}
		}

		if ret.Monthly > 0 && len(monBuckets) < ret.Monthly {
			key := t.Format("2006-01")
			if !monBuckets[key] {
				monBuckets[key] = true
				keep[snap.Name] = true
			}
		}
	}
	return keep
}

// --------------------------------------------------------------------------
// ZFSCLISnapshotter — production ZFSSnapshotter backed by cmdexec.Executor
// --------------------------------------------------------------------------

// ZFSCLISnapshotter implements ZFSSnapshotter by shelling out to the zfs CLI.
type ZFSCLISnapshotter struct {
	exec cmdexec.Executor
}

// NewZFSCLISnapshotter returns the production snapshotter.
func NewZFSCLISnapshotter(exec cmdexec.Executor) *ZFSCLISnapshotter {
	return &ZFSCLISnapshotter{exec: exec}
}

// CreateSnapshot runs `zfs snapshot dataset@name`.
func (z *ZFSCLISnapshotter) CreateSnapshot(ctx context.Context, dataset, name string) error {
	snapRef := dataset + "@" + name
	if _, _, err := z.exec.Run(ctx, "zfs", "snapshot", snapRef); err != nil {
		return fmt.Errorf("zfs snapshot %s: %w", snapRef, err)
	}
	return nil
}

// ListSnapshots runs `zfs list -H -t snapshot -o name,creation -p -r dataset`
// and returns the parsed entries belonging to that dataset.
func (z *ZFSCLISnapshotter) ListSnapshots(ctx context.Context, dataset string) ([]SnapshotInfo, error) {
	out, _, err := z.exec.Run(ctx, "zfs", "list", "-H", "-t", "snapshot", "-o", "name,creation", "-p", "-r", dataset)
	if err != nil {
		return nil, fmt.Errorf("zfs list snapshots %s: %w", dataset, err)
	}
	return parseSnapshotList(string(out), dataset)
}

// DestroySnapshot runs `zfs destroy dataset@name`.
func (z *ZFSCLISnapshotter) DestroySnapshot(ctx context.Context, dataset, name string) error {
	snapRef := dataset + "@" + name
	if _, _, err := z.exec.Run(ctx, "zfs", "destroy", snapRef); err != nil {
		return fmt.Errorf("zfs destroy %s: %w", snapRef, err)
	}
	return nil
}

// parseSnapshotList parses `zfs list -H -t snapshot -o name,creation -p` output.
// Returns only snapshots belonging to the given dataset (not children).
func parseSnapshotList(raw, dataset string) ([]SnapshotInfo, error) {
	var out []SnapshotInfo
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		nameField := fields[0] // "tank/photos@hourly-2026-05-18T00:00:00Z"
		parts := strings.SplitN(nameField, "@", 2)
		if len(parts) != 2 {
			continue
		}
		ds, snapName := parts[0], parts[1]
		if ds != dataset {
			continue
		}
		var ts int64
		if _, err := fmt.Sscanf(fields[1], "%d", &ts); err != nil {
			ts = 0
		}
		out = append(out, SnapshotInfo{
			Dataset:   ds,
			Name:      snapName,
			CreatedAt: time.Unix(ts, 0).UTC(),
		})
	}
	return out, nil
}
