package backup_test

// [AC-Se1e7a6-3-1] Pre-upgrade snapshot + apt flow.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/engine/backup"
)

// fakeAppStopper implements AppStopper for upgrade tests.
type fakeAppStopper struct {
	stopped      []string
	started      []string
	stopErr      error
	startErr     error
}

func (f *fakeAppStopper) StopAll(_ context.Context) ([]string, error) {
	if f.stopErr != nil {
		return nil, f.stopErr
	}
	f.stopped = []string{"immich", "nextcloud"}
	return f.stopped, nil
}

func (f *fakeAppStopper) StartAll(_ context.Context, appIDs []string) error {
	f.started = appIDs
	return f.startErr
}

// [AC-Se1e7a6-3-1]
func TestRunUpgrade_DryRun_HappyPath(t *testing.T) {
	snapper := newFakeSnapshotter()
	stopper := &fakeAppStopper{}
	exec := &recordingExec{stdout: []byte("[dry-run] apt-get upgrade -y skipped\n")}

	cfg := backup.UpgradeConfig{
		RootDataset: "tank/rootfs",
		AppDatasets: []string{"tank/apps/immich/data"},
		DryRun:      true,
	}

	res, err := backup.RunUpgrade(context.Background(), exec, stopper, snapper, cfg)
	if err != nil {
		t.Fatalf("RunUpgrade: %v", err)
	}

	// All datasets must have received a pre-upgrade snapshot.
	if len(res.Snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d: %v", len(res.Snapshots), res.Snapshots)
	}
	for _, snap := range res.Snapshots {
		if !strings.Contains(snap, "pre-upgrade-") {
			t.Errorf("snapshot %q missing 'pre-upgrade-' prefix", snap)
		}
	}

	// Apps must have been stopped and restarted.
	if len(stopper.stopped) == 0 {
		t.Error("expected apps to have been stopped")
	}
	if len(stopper.started) == 0 {
		t.Error("expected apps to have been restarted")
	}

	// AptOutput must contain the dry-run marker.
	if !strings.Contains(res.AptOutput, "dry-run") {
		t.Errorf("apt output missing 'dry-run': %q", res.AptOutput)
	}
}

// [AC-Se1e7a6-3-1] Snapshot failure must abort upgrade (DESIGN_PRINCIPLES priority #5).
func TestRunUpgrade_SnapshotFailure_AbortsUpgrade(t *testing.T) {
	// Snapshotter that returns an error on the first CreateSnapshot call.
	snapper := &failingSnapshotter{failAfter: 0}
	stopper := &fakeAppStopper{}
	exec := &recordingExec{}

	cfg := backup.UpgradeConfig{
		RootDataset: "tank/rootfs",
		DryRun:      true,
	}

	_, err := backup.RunUpgrade(context.Background(), exec, stopper, snapper, cfg)
	if err == nil {
		t.Fatal("expected error when snapshot fails, got nil")
	}
	if !strings.Contains(err.Error(), "pre-upgrade snapshot") {
		t.Errorf("error should mention pre-upgrade snapshot: %v", err)
	}

	// No apt call should have happened.
	for _, call := range exec.calls {
		if call.name == "apt-get" || call.name == "echo" {
			t.Errorf("apt/echo was called despite snapshot failure: %v", call)
		}
	}
}

// [AC-Se1e7a6-3-1] Apps must be restarted even if apt fails.
func TestRunUpgrade_AptFailure_AppsRestarted(t *testing.T) {
	snapper := newFakeSnapshotter()
	stopper := &fakeAppStopper{}
	exec := &recordingExec{err: errors.New("apt-get: simulated failure")}

	cfg := backup.UpgradeConfig{
		RootDataset: "tank/rootfs",
		DryRun:      false,
	}

	_, err := backup.RunUpgrade(context.Background(), exec, stopper, snapper, cfg)
	if err == nil {
		t.Fatal("expected error from apt failure")
	}

	// Restart must have been attempted regardless.
	if len(stopper.started) == 0 {
		t.Error("apps should be restarted even after apt failure")
	}
}

// failingSnapshotter returns an error after failAfter successful CreateSnapshot calls.
type failingSnapshotter struct {
	count     int
	failAfter int
}

func (f *failingSnapshotter) CreateSnapshot(_ context.Context, _, _ string) error {
	if f.count >= f.failAfter {
		return errors.New("failingSnapshotter: forced error")
	}
	f.count++
	return nil
}
func (f *failingSnapshotter) ListSnapshots(_ context.Context, _ string) ([]backup.SnapshotInfo, error) {
	return nil, nil
}
func (f *failingSnapshotter) DestroySnapshot(_ context.Context, _, _ string) error {
	return nil
}
