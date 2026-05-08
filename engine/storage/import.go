package storage

import (
	"context"
	"fmt"
	"strings"
)

// ImportPool runs the production import flow:
//
//  1. `zpool import -N -d /dev/disk/by-id <name>` — verify the pool can be
//     imported without mounting datasets. -N keeps mounts deferred so a
//     half-broken pool's stale mount entries don't trip the operator.
//  2. If step 1 succeeds, run `zpool import -d /dev/disk/by-id [-f] [-R alt]
//     [-N] <name>` to actually import. The -d preference for /dev/disk/by-id
//     follows ZFS recommended practice — names survive kernel reorderings
//     across reboots.
//
// Force / read-only / altroot map to -f / -o readonly=on / -R. Force is only
// set when ImportOpts.Force is true; UI never reaches this with Force=true
// on a single click — the storage handler requires the operator to tick a
// dedicated checkbox, satisfying DESIGN_PRINCIPLES forbidden #14.
func (c *CLI) ImportPool(ctx context.Context, name string, opts ImportOpts) error {
	if !isValidPoolName(name) && opts.ByGUID == "" {
		return fmt.Errorf("%w: %q", ErrPoolNameInvalid, name)
	}

	target := name
	if opts.ByGUID != "" {
		target = opts.ByGUID
	}

	dryRunArgs := []string{"import", "-d", "/dev/disk/by-id", "-N"}
	if opts.Force {
		dryRunArgs = append(dryRunArgs, "-f")
	}
	if opts.ReadOnly {
		dryRunArgs = append(dryRunArgs, "-o", "readonly=on")
	}
	if opts.AltRoot != "" {
		dryRunArgs = append(dryRunArgs, "-R", opts.AltRoot)
	}
	dryRunArgs = append(dryRunArgs, target)
	if _, _, err := c.exec.Run(ctx, "zpool", dryRunArgs...); err != nil {
		return fmt.Errorf("storage: zpool import dry-run %s: %w", name, err)
	}

	// Step 2: actual import. We re-issue the same flag set without -N so the
	// datasets are mounted (unless ReadOnly suppressed it implicitly via
	// readonly=on, which still allows mount).
	args := buildImportArgs(target, opts, false)
	if _, _, err := c.exec.Run(ctx, "zpool", args...); err != nil {
		return fmt.Errorf("storage: zpool import %s: %w", name, err)
	}
	return nil
}

// buildImportArgs is the deterministic argv builder. dryRun toggles -N (no
// mount). The function is exported for tests via the storage_internal_test
// package (kept unexported here, accessed via test file in same pkg).
func buildImportArgs(target string, opts ImportOpts, dryRun bool) []string {
	args := []string{"import", "-d", "/dev/disk/by-id"}
	if dryRun {
		args = append(args, "-N")
	}
	if opts.Force {
		args = append(args, "-f")
	}
	if opts.ReadOnly {
		args = append(args, "-o", "readonly=on")
	}
	if opts.AltRoot != "" {
		args = append(args, "-R", opts.AltRoot)
	}
	args = append(args, target)
	return args
}

// canonicalDiskPath ensures a path is suitable for `zpool create` / spare
// arguments. Operators may paste either /dev/sda or /dev/disk/by-id/wwn-0x...
// The engine accepts both — production setups should pass by-id strings, but
// the dev box / test fixtures use plain /dev/sdN.
func canonicalDiskPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		return "/dev/" + p
	}
	return p
}
