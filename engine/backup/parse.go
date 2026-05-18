package backup

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// parseZFSSnapList parses the TSV output of `zfs list -H -t snapshot -o name,creation -p`.
// The -p flag makes `creation` a Unix epoch integer so we parse it as int64.
func parseZFSSnapList(raw string) ([]BackupEntry, error) {
	var out []BackupEntry
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		nameAndSnap := fields[0] // e.g. "tank/photos@auto-2026-05"
		parts := strings.SplitN(nameAndSnap, "@", 2)
		dataset := parts[0]
		snapName := ""
		if len(parts) == 2 {
			snapName = parts[1]
		}

		var ts int64
		if _, err := fmt.Sscanf(fields[1], "%d", &ts); err != nil {
			ts = 0
		}
		out = append(out, BackupEntry{
			ID:        snapName,
			Dataset:   dataset,
			CreatedAt: time.Unix(ts, 0).UTC(),
		})
	}
	return out, nil
}

// resticSnapshotJSON mirrors the relevant fields of `restic snapshots --json` output.
type resticSnapshotJSON struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Tags     []string  `json:"tags,omitempty"`
	Hostname string    `json:"hostname,omitempty"`
}

// parseResticSnapshots deserialises the JSON array output of `restic snapshots --json`.
func parseResticSnapshots(raw []byte) ([]BackupEntry, error) {
	var snaps []resticSnapshotJSON
	if err := json.Unmarshal(raw, &snaps); err != nil {
		return nil, fmt.Errorf("backup: parse restic snapshots: %w", err)
	}
	out := make([]BackupEntry, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, BackupEntry{
			ID:        s.ID,
			CreatedAt: s.Time.UTC(),
		})
	}
	return out, nil
}

// parseRcloneDirs parses directory names from `rclone lsd --format=n` output.
// Each non-empty line is treated as one snapshot directory name.
func parseRcloneDirs(raw string) ([]BackupEntry, error) {
	var out []BackupEntry
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, BackupEntry{
			ID:        line,
			CreatedAt: time.Time{}, // rclone lsd --format=n gives no timestamp
		})
	}
	return out, nil
}
