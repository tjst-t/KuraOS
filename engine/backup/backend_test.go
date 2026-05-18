package backup_test

// [AC-Se1e7a6-2-2] zfs_send / restic / rclone backend Send / Restore / List
// tested via cmdexec.Fake — no real CLI required.

import (
	"context"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/backup"
	"github.com/kuraos-org/kura/internal/cmdexec"
)

// --------------------------------------------------------------------------
// ZFSSendBackend
// --------------------------------------------------------------------------

func TestZFSSendBackend_Send(t *testing.T) {
	// [AC-Se1e7a6-2-2]
	t.Run("issues shell pipeline", func(t *testing.T) {
		fk := cmdexec.NewFake()
		fk.RegisterStdout("sh", []string{"-c", ""}, nil)
		// We can't predict the exact pipeline string without rebuilding it, so
		// register a wildcard by using RegisterStdout with an empty expected key
		// and then just assert no error through a real path below.
		_ = fk

		fk2 := &recordingExec{}
		b := backup.NewZFSSendBackend(fk2, backup.ZFSSendConfig{
			Host:          "user@remote",
			RemoteDataset: "backup/tank",
		})
		err := b.Send(context.Background(), "tank/photos", "snap-001")
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if len(fk2.calls) == 0 {
			t.Fatal("expected at least one exec call")
		}
		call := fk2.calls[0]
		if call.name != "sh" || call.args[0] != "-c" {
			t.Fatalf("expected sh -c, got %s %v", call.name, call.args)
		}
		pipeline := call.args[1]
		if pipeline == "" {
			t.Fatal("empty pipeline")
		}
		// Pipeline must reference the snapshot and ssh.
		for _, substr := range []string{"zfs send", "tank/photos@snap-001", "user@remote", "zfs receive"} {
			if !containsStr(pipeline, substr) {
				t.Errorf("pipeline %q missing %q", pipeline, substr)
			}
		}
	})
}

func TestZFSSendBackend_Restore(t *testing.T) {
	// [AC-Se1e7a6-2-2]
	t.Run("issues restore pipeline", func(t *testing.T) {
		fk := &recordingExec{}
		b := backup.NewZFSSendBackend(fk, backup.ZFSSendConfig{
			Host:          "user@remote",
			RemoteDataset: "backup/tank",
		})
		opts := backup.RestoreOpts{SnapshotID: "snap-001", TargetDataset: "tank/photos"}
		if err := b.Restore(context.Background(), opts); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if len(fk.calls) == 0 {
			t.Fatal("expected exec call")
		}
		pipeline := fk.calls[0].args[1]
		for _, substr := range []string{"zfs send", "snap-001", "zfs receive"} {
			if !containsStr(pipeline, substr) {
				t.Errorf("restore pipeline missing %q", substr)
			}
		}
	})
}

func TestZFSSendBackend_List(t *testing.T) {
	// [AC-Se1e7a6-2-2]
	t.Run("parses snapshot list", func(t *testing.T) {
		fk := &recordingExec{
			stdout: []byte("backup/tank@snap-001\t1716000000\nbackup/tank@snap-002\t1716100000\n"),
		}
		b := backup.NewZFSSendBackend(fk, backup.ZFSSendConfig{
			Host:          "user@remote",
			RemoteDataset: "backup/tank",
		})
		entries, err := b.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
		if entries[0].ID != "snap-001" {
			t.Errorf("entry[0].ID = %q, want snap-001", entries[0].ID)
		}
	})
}

// --------------------------------------------------------------------------
// ResticBackend
// --------------------------------------------------------------------------

func TestResticBackend_Send(t *testing.T) {
	// [AC-Se1e7a6-2-2]
	t.Run("calls restic backup", func(t *testing.T) {
		fk := &recordingExec{}
		b := backup.NewResticBackend(fk, backup.ResticConfig{Repo: "s3:bucket/kura"})
		if err := b.Send(context.Background(), "tank/photos", "snap-001"); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if len(fk.calls) == 0 {
			t.Fatal("expected exec call")
		}
		call := fk.calls[0]
		if call.name != "restic" {
			t.Fatalf("expected restic, got %s", call.name)
		}
		argsStr := joinArgs(call.args)
		if !containsStr(argsStr, "backup") {
			t.Errorf("restic args missing 'backup': %v", call.args)
		}
		if !containsStr(argsStr, "/tank/photos") {
			t.Errorf("restic args missing mountpoint: %v", call.args)
		}
	})
}

func TestResticBackend_List(t *testing.T) {
	// [AC-Se1e7a6-2-2]
	t.Run("parses JSON snapshots", func(t *testing.T) {
		ts, _ := time.Parse(time.RFC3339, "2026-05-18T00:00:00Z")
		jsonOut := `[{"id":"abc123","time":"2026-05-18T00:00:00Z","tags":["snap"]}]`
		fk := &recordingExec{stdout: []byte(jsonOut)}
		b := backup.NewResticBackend(fk, backup.ResticConfig{Repo: "s3:bucket/kura"})
		entries, err := b.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1, got %d", len(entries))
		}
		if entries[0].ID != "abc123" {
			t.Errorf("ID = %q, want abc123", entries[0].ID)
		}
		if !entries[0].CreatedAt.Equal(ts) {
			t.Errorf("CreatedAt = %v, want %v", entries[0].CreatedAt, ts)
		}
	})
}

// --------------------------------------------------------------------------
// RcloneBackend
// --------------------------------------------------------------------------

func TestRcloneBackend_Send(t *testing.T) {
	// [AC-Se1e7a6-2-2]
	t.Run("calls rclone sync", func(t *testing.T) {
		fk := &recordingExec{}
		b := backup.NewRcloneBackend(fk, backup.RcloneConfig{Remote: "gdrive:backups/kura"})
		if err := b.Send(context.Background(), "tank/photos", "snap-001"); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if len(fk.calls) == 0 {
			t.Fatal("expected exec call")
		}
		call := fk.calls[0]
		if call.name != "rclone" {
			t.Fatalf("expected rclone, got %s", call.name)
		}
		if call.args[0] != "sync" {
			t.Errorf("first arg = %q, want sync", call.args[0])
		}
	})
}

func TestRcloneBackend_List(t *testing.T) {
	// [AC-Se1e7a6-2-2]
	t.Run("parses directory listing", func(t *testing.T) {
		fk := &recordingExec{stdout: []byte("snap-001\nsnap-002\n")}
		b := backup.NewRcloneBackend(fk, backup.RcloneConfig{Remote: "gdrive:backups/kura"})
		entries, err := b.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2, got %d", len(entries))
		}
	})
}

// --------------------------------------------------------------------------
// Fake backend (used by Story 3 upgrade tests)
// --------------------------------------------------------------------------

// FakeBackend records calls to Send / Restore / List for assertion in tests.
// Exported so upgrade_test.go can reference it across packages.
type fakeBackend struct {
	sendCalls    []string
	restoreCalls []backup.RestoreOpts
}

func (f *fakeBackend) Send(_ context.Context, dataset, snap string) error {
	f.sendCalls = append(f.sendCalls, dataset+"@"+snap)
	return nil
}
func (f *fakeBackend) Restore(_ context.Context, opts backup.RestoreOpts) error {
	f.restoreCalls = append(f.restoreCalls, opts)
	return nil
}
func (f *fakeBackend) List(_ context.Context) ([]backup.BackupEntry, error) {
	return nil, nil
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

type execCall struct {
	name string
	args []string
}

type recordingExec struct {
	calls  []execCall
	stdout []byte
	stderr []byte
	err    error
}

func (r *recordingExec) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	r.calls = append(r.calls, execCall{name: name, args: append([]string(nil), args...)})
	return r.stdout, r.stderr, r.err
}

func containsStr(haystack, needle string) bool {
	return len(haystack) > 0 && len(needle) > 0 &&
		(haystack == needle || len(haystack) >= len(needle) &&
			func() bool {
				for i := 0; i <= len(haystack)-len(needle); i++ {
					if haystack[i:i+len(needle)] == needle {
						return true
					}
				}
				return false
			}())
}

func joinArgs(args []string) string {
	result := ""
	for _, a := range args {
		result += " " + a
	}
	return result
}
