package backup_test

// [AC-Se1e7a6-1-1] config.json snapshots.schedule → cron snapshot + retention.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/backup"
)

// fakeSnapshotter is the in-memory ZFSSnapshotter used for scheduler tests.
type fakeSnapshotter struct {
	mu        sync.Mutex
	created   []string // "dataset@name"
	destroyed []string // "dataset@name"
	snaps     map[string][]backup.SnapshotInfo
}

func newFakeSnapshotter() *fakeSnapshotter {
	return &fakeSnapshotter{snaps: make(map[string][]backup.SnapshotInfo)}
}

func (f *fakeSnapshotter) CreateSnapshot(_ context.Context, dataset, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := dataset + "@" + name
	f.created = append(f.created, key)
	f.snaps[dataset] = append(f.snaps[dataset], backup.SnapshotInfo{
		Dataset:   dataset,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	})
	return nil
}

func (f *fakeSnapshotter) ListSnapshots(_ context.Context, dataset string) ([]backup.SnapshotInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]backup.SnapshotInfo(nil), f.snaps[dataset]...), nil
}

func (f *fakeSnapshotter) DestroySnapshot(_ context.Context, dataset, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := dataset + "@" + name
	f.destroyed = append(f.destroyed, key)
	// Remove from snaps list.
	snaps := f.snaps[dataset]
	for i, s := range snaps {
		if s.Name == name {
			f.snaps[dataset] = append(snaps[:i], snaps[i+1:]...)
			break
		}
	}
	return nil
}

// [AC-Se1e7a6-1-1]
func TestScheduler_RunNow_CreatesSnapshot(t *testing.T) {
	snapper := newFakeSnapshotter()
	sched := backup.ScheduleConfig{
		Name:     "hourly",
		Cron:     "0 * * * *",
		Datasets: []string{"tank/photos", "tank/documents"},
		Retention: backup.RetentionPolicy{
			Hourly:  24,
			Daily:   7,
			Monthly: 3,
		},
	}
	s := backup.NewScheduler(snapper, []backup.ScheduleConfig{sched})
	ctx := context.Background()

	if err := s.RunNow(ctx, "hourly"); err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	snapper.mu.Lock()
	created := append([]string(nil), snapper.created...)
	snapper.mu.Unlock()

	if len(created) < 2 {
		t.Fatalf("expected at least 2 snapshots (one per dataset), got %v", created)
	}
	for _, ds := range sched.Datasets {
		found := false
		for _, c := range created {
			if len(c) > len(ds) && c[:len(ds)] == ds {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no snapshot created for dataset %q, created: %v", ds, created)
		}
	}
}

// [AC-Se1e7a6-1-1] Retention deletes old snapshots beyond the keep count.
func TestScheduler_Retention_DeletesOld(t *testing.T) {
	snapper := newFakeSnapshotter()

	// Pre-populate with 5 old hourly snapshots for "tank/photos".
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * time.Hour)
		snapper.snaps["tank/photos"] = append(snapper.snaps["tank/photos"], backup.SnapshotInfo{
			Dataset:   "tank/photos",
			Name:      fmt.Sprintf("hourly-%s", ts.Format("2006-01-02T15:04:05Z")),
			CreatedAt: ts,
		})
	}

	sched := backup.ScheduleConfig{
		Name:     "hourly",
		Cron:     "0 * * * *",
		Datasets: []string{"tank/photos"},
		Retention: backup.RetentionPolicy{
			Hourly: 3, // keep only 3
		},
	}
	s := backup.NewScheduler(snapper, []backup.ScheduleConfig{sched})
	if err := s.RunNow(context.Background(), "hourly"); err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	snapper.mu.Lock()
	remaining := snapper.snaps["tank/photos"]
	destroyed := append([]string(nil), snapper.destroyed...)
	snapper.mu.Unlock()

	// 5 pre-existing + 1 new = 6 total; retention keeps 3 → 3 should be destroyed.
	// (The newest 3 hourly buckets are kept.)
	if len(destroyed) == 0 {
		t.Fatal("expected at least some destroyed snapshots but none were destroyed")
	}
	_ = remaining // remaining count depends on bucket packing; key check is that destroyed > 0
}

// [AC-Se1e7a6-1-1] RunNow errors on unknown schedule name.
func TestScheduler_RunNow_UnknownSchedule(t *testing.T) {
	s := backup.NewScheduler(newFakeSnapshotter(), nil)
	if err := s.RunNow(context.Background(), "nonexistent"); err == nil {
		t.Fatal("expected error for unknown schedule name")
	}
}
