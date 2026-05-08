package storage

import (
	"context"
	"fmt"
	"strings"
)

// Write-side of engine/storage. Each entry point (CreatePool / CreateVolume /
// SetQuota / CreateSnapshot / Rollback) builds an exec command from its
// validated input and shells out via the Executor.
//
// The validation layer lives in validation.go and is called *before* any
// CLI is invoked so that an invalid input never reaches zpool/zfs. This is
// load-bearing for safety: ZFS itself will happily create a single-SSD
// special vdev (DESIGN_PRINCIPLES forbidden #3) — the rejection has to
// happen here.

// CreatePool runs `zpool create` after ValidatePoolConfig succeeds. The
// command shape is:
//
//	zpool create [-f if forced] -o ashift=12 \
//	    [-O dataset properties...] \
//	    <name> <data-vdev> [special <vdev>] [spare <disks...>]
//
// Each leaf disk path is passed as-is. Callers should canonicalize to
// /dev/disk/by-id paths upstream so pool import survives kernel disk renames.
func (c *CLI) CreatePool(ctx context.Context, cfg PoolConfig) error {
	if err := ValidatePoolConfig(cfg); err != nil {
		return err
	}
	args := buildCreatePoolArgs(cfg)
	if _, _, err := c.exec.Run(ctx, "zpool", args...); err != nil {
		return fmt.Errorf("storage: zpool create %s: %w", cfg.Name, err)
	}
	return nil
}

// buildCreatePoolArgs is the deterministic argv builder used both by
// CreatePool and the table-driven tests. Keeping it pure-function makes the
// test "we send exactly this command" assertion trivial.
func buildCreatePoolArgs(cfg PoolConfig) []string {
	args := []string{"create"}
	ashift := cfg.Ashift
	if ashift == 0 {
		ashift = 12
	}
	args = append(args, "-o", fmt.Sprintf("ashift=%d", ashift))
	if cfg.SmallBlockThreshold > 0 {
		args = append(args, "-o", fmt.Sprintf("special_small_blocks=%d", cfg.SmallBlockThreshold))
	}
	args = append(args, cfg.Name)
	args = append(args, vdevArgs(cfg.Data)...)
	if cfg.Special != nil {
		args = append(args, "special")
		args = append(args, vdevArgs(*cfg.Special)...)
	}
	if len(cfg.Spares) > 0 {
		args = append(args, "spare")
		args = append(args, cfg.Spares...)
	}
	return args
}

// vdevArgs serializes a vdev as zpool expects it: `mirror disk1 disk2 ...`,
// `raidz2 disk1 ...`, or just the disk for LayoutSingle.
func vdevArgs(v VdevSpec) []string {
	if v.Layout == LayoutSingle {
		return append([]string(nil), v.Disks...)
	}
	out := []string{string(v.Layout)}
	out = append(out, v.Disks...)
	return out
}

// CreateVolume creates a ZFS dataset. Dataset is the full path
// (`pool/parent/leaf`). The caller already split `pool` from `name`; the
// engine doesn't try to be clever with parent dataset auto-creation
// (`zfs create -p`) — that's a separate decision the UI surfaces explicitly
// via a checkbox.
func (c *CLI) CreateVolume(ctx context.Context, dataset string, opts VolumeOpts) error {
	if !isValidDatasetName(strings.TrimPrefix(dataset, datasetPool(dataset)+"/")) && !isValidDatasetName(dataset) {
		return fmt.Errorf("%w: %q", ErrVolumeNameInvalid, dataset)
	}
	if err := applyPresetToOpts(&opts); err != nil {
		return err
	}
	args := buildCreateVolumeArgs(dataset, opts)
	if _, _, err := c.exec.Run(ctx, "zfs", args...); err != nil {
		return fmt.Errorf("storage: zfs create %s: %w", dataset, err)
	}
	if opts.QuotaBytes > 0 {
		if err := c.SetQuota(ctx, dataset, opts.QuotaBytes); err != nil {
			return fmt.Errorf("storage: set quota on %s: %w", dataset, err)
		}
	}
	return nil
}

func buildCreateVolumeArgs(dataset string, opts VolumeOpts) []string {
	args := []string{"create"}
	if opts.RecordSize != "" {
		args = append(args, "-o", "recordsize="+opts.RecordSize)
	}
	if opts.Compression != "" {
		args = append(args, "-o", "compression="+opts.Compression)
	}
	if opts.SpecialSmallBlocks != "" && opts.SpecialSmallBlocks != "0" {
		args = append(args, "-o", "special_small_blocks="+opts.SpecialSmallBlocks)
	}
	if opts.LogBias != "" {
		args = append(args, "-o", "logbias="+opts.LogBias)
	}
	if opts.MountPoint != "" {
		args = append(args, "-o", "mountpoint="+opts.MountPoint)
	}
	args = append(args, dataset)
	return args
}

// SetQuota sets `zfs set quota=<n>` on a dataset. quotaBytes==0 unsets the
// quota (`quota=none`). DESIGN_PRINCIPLES priority #5 — refuse to silently
// shrink a quota below current `used` (ZFS itself accepts the call but the
// dataset becomes immediately full); we don't read `used` here so we surface
// the error from zfs as-is.
func (c *CLI) SetQuota(ctx context.Context, dataset string, quotaBytes int64) error {
	if !isValidDatasetName(dataset) {
		return fmt.Errorf("%w: %q", ErrVolumeNameInvalid, dataset)
	}
	val := "none"
	if quotaBytes > 0 {
		val = fmt.Sprintf("%d", quotaBytes)
	}
	if _, _, err := c.exec.Run(ctx, "zfs", "set", "quota="+val, dataset); err != nil {
		return fmt.Errorf("storage: zfs set quota %s: %w", dataset, err)
	}
	return nil
}

// ListVolumes returns the dataset rows from `zfs list -t filesystem`. When
// pool is non-empty, the listing is scoped to that pool with -r. Pool root
// datasets are included (caller decides whether to filter); recordsize +
// compression are surfaced so the UI can show which preset is in effect.
func (c *CLI) ListVolumes(ctx context.Context, pool string) ([]VolumeInfo, error) {
	args := []string{
		"list", "-H", "-p", "-t", "filesystem",
		"-o", "name,used,available,referenced,mountpoint,quota,recordsize,compression",
	}
	if pool != "" {
		args = append(args, "-r", pool)
	}
	stdout, _, err := c.exec.Run(ctx, "zfs", args...)
	if err != nil {
		return nil, fmt.Errorf("storage: zfs list filesystem: %w", err)
	}
	return parseVolumeList(stdout)
}

// CreateSnapshot runs `zfs snapshot pool/dataset@name`.
func (c *CLI) CreateSnapshot(ctx context.Context, dataset, name string) error {
	if !isValidDatasetName(dataset) {
		return fmt.Errorf("%w: %q", ErrVolumeNameInvalid, dataset)
	}
	if !isValidSnapshotName(name) {
		return fmt.Errorf("%w: %q", ErrSnapshotNameInvalid, name)
	}
	target := dataset + "@" + name
	if _, _, err := c.exec.Run(ctx, "zfs", "snapshot", target); err != nil {
		return fmt.Errorf("storage: zfs snapshot %s: %w", target, err)
	}
	return nil
}

// ListSnapshots returns the snapshots for a dataset (recursive: -r). Output
// is the tab-separated `name used refer creation` from `zfs list`.
func (c *CLI) ListSnapshots(ctx context.Context, dataset string) ([]SnapshotInfo, error) {
	if !isValidDatasetName(dataset) {
		return nil, fmt.Errorf("%w: %q", ErrVolumeNameInvalid, dataset)
	}
	stdout, _, err := c.exec.Run(ctx, "zfs", "list",
		"-H", "-p", "-t", "snapshot", "-o", "name,used,refer,creation", "-r", dataset)
	if err != nil {
		return nil, fmt.Errorf("storage: zfs list snapshots %s: %w", dataset, err)
	}
	return parseSnapshotList(stdout)
}

// Rollback runs `zfs rollback`. -r (recursively destroy intermediate
// snapshots) is left off — destructive operations require explicit user
// confirmation (priority #5). The UI surfaces a confirmation dialog before
// calling this; CLI users get the safer default and need to retry with -r
// themselves if a later snapshot stands in the way.
func (c *CLI) Rollback(ctx context.Context, dataset, snapshot string) error {
	if !isValidDatasetName(dataset) {
		return fmt.Errorf("%w: %q", ErrVolumeNameInvalid, dataset)
	}
	if !isValidSnapshotName(snapshot) {
		return fmt.Errorf("%w: %q", ErrSnapshotNameInvalid, snapshot)
	}
	target := dataset + "@" + snapshot
	if _, _, err := c.exec.Run(ctx, "zfs", "rollback", target); err != nil {
		return fmt.Errorf("storage: zfs rollback %s: %w", target, err)
	}
	return nil
}

// datasetPool extracts the pool name from a dataset path.
func datasetPool(dataset string) string {
	if i := strings.IndexByte(dataset, '/'); i > 0 {
		return dataset[:i]
	}
	return dataset
}
