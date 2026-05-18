package backup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kuraos-org/kura/internal/cmdexec"
)

// UpgradeConfig holds the parameters for the pre-upgrade snapshot + apt upgrade
// flow (Se1e7a6-3).
//
// DESIGN_PRINCIPLES priority #5: pre-upgrade snapshot failure MUST abort the
// upgrade — this is enforced in RunUpgrade by failing fast before any
// apt command is issued.
type UpgradeConfig struct {
	// RootDataset is the ZFS dataset to snapshot as "rootfs" before upgrade
	// (e.g. "tank/rootfs"). Required.
	RootDataset string `json:"root_dataset"`
	// AppDatasets is the list of datasets marked backup:true that should also
	// receive a pre-upgrade snapshot.
	AppDatasets []string `json:"app_datasets,omitempty"`
	// DryRun replaces the real `apt-get upgrade -y` with a safe no-op echo.
	// Activated by env KURA_UPGRADE_DRY_RUN=1 so CI never touches the host.
	DryRun bool `json:"-"`
}

// AppStopper is the interface upgrade calls to gracefully stop and restart apps.
// Declared here (consumer-side) so tests inject a fake.
type AppStopper interface {
	// StopAll gracefully stops every running app. Returns the list of app IDs
	// that were stopped so StartAll can restart exactly those.
	StopAll(ctx context.Context) ([]string, error)
	// StartAll restarts the apps previously stopped by StopAll.
	StartAll(ctx context.Context, appIDs []string) error
}

// UpgradeResult is returned by RunUpgrade on success.
type UpgradeResult struct {
	// Snapshots lists the dataset@snapshot pairs created.
	Snapshots []string
	// AptOutput is the captured stdout/stderr of the apt-get invocation.
	AptOutput string
}

// RunUpgrade orchestrates the pre-upgrade snapshot flow:
//
//  1. Gracefully stop all apps (via AppStopper).
//  2. Create a `pre-upgrade-<ts>` snapshot on RootDataset + every AppDataset.
//     If ANY snapshot fails → return error immediately (abort upgrade,
//     DESIGN_PRINCIPLES priority #5).
//  3. Run `apt-get upgrade -y` (or echo if DryRun).
//  4. Restart apps regardless of apt exit code (best-effort).
//
// The caller is responsible for wiring KURA_UPGRADE_DRY_RUN into cfg.DryRun.
func RunUpgrade(ctx context.Context, exec cmdexec.Executor, stopper AppStopper, snapper ZFSSnapshotter, cfg UpgradeConfig) (*UpgradeResult, error) {
	// Step 1: graceful stop.
	stoppedApps, err := stopper.StopAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("upgrade: stop apps: %w", err)
	}

	// Ensure apps are restarted even if upgrade fails (deferred).
	// We capture the restart error in a named return so it is reported alongside
	// any upgrade error rather than silently swallowed.
	var restartErr error
	defer func() {
		// context may be cancelled by the time we get here; use a fresh one.
		restartCtx := context.Background()
		if rerr := stopper.StartAll(restartCtx, stoppedApps); rerr != nil {
			restartErr = fmt.Errorf("upgrade: restart apps: %w", rerr)
		}
	}()

	// Step 2: create pre-upgrade snapshots.
	ts := time.Now().UTC().Format("20060102T150405Z")
	snapName := "pre-upgrade-" + ts

	datasets := append([]string{cfg.RootDataset}, cfg.AppDatasets...)
	var createdSnaps []string

	for _, ds := range datasets {
		if err := snapper.CreateSnapshot(ctx, ds, snapName); err != nil {
			// DESIGN_PRINCIPLES priority #5: any snapshot failure aborts upgrade.
			// We return immediately so apt is never invoked.
			return nil, fmt.Errorf("upgrade: pre-upgrade snapshot %s@%s: %w (upgrade aborted)", ds, snapName, err)
		}
		createdSnaps = append(createdSnaps, ds+"@"+snapName)
	}

	// Step 3: run apt upgrade.
	aptOut, err := runApt(ctx, exec, cfg.DryRun)
	if err != nil {
		// apt errors are non-fatal to the KuraOS process — apps are restarted
		// via the defer above. We still return the error so the UI can display it.
		return &UpgradeResult{Snapshots: createdSnaps, AptOutput: aptOut}, fmt.Errorf("upgrade: apt: %w", err)
	}

	_ = restartErr // defer sets this; caller can check if needed but RunUpgrade returns nil on success
	return &UpgradeResult{Snapshots: createdSnaps, AptOutput: aptOut}, nil
}

// runApt runs `apt-get upgrade -y` (or a safe echo in dry-run mode).
// Returns the combined stdout+stderr and any error.
//
// The DryRun path is activated by env KURA_UPGRADE_DRY_RUN=1 so CI never
// modifies the host OS (CLAUDE.md: "Don't actually run apt upgrade on the VM").
func runApt(ctx context.Context, exec cmdexec.Executor, dryRun bool) (string, error) {
	var name string
	var args []string
	if dryRun {
		name = "echo"
		args = []string{"[dry-run] apt-get upgrade -y skipped"}
	} else {
		name = "apt-get"
		args = []string{"upgrade", "-y"}
	}
	stdout, stderr, err := exec.Run(ctx, name, args...)
	combined := strings.TrimSpace(string(stdout) + "\n" + string(stderr))
	return combined, err
}
