package storage

import (
	"errors"
	"fmt"
	"strings"
)

// Validation rules live in their own file so the table-driven test suite can
// hammer each rule without booting an Executor. The rules implement
// DESIGN_PRINCIPLES priority #5 (信頼性 > 機能) and forbidden #3 (single SSD
// special vdev rejected as ERROR, never warning) and #14 (force flow only
// reachable from CLI flags, never from UI input).

// Validation errors. Engines, the UI, and config.json apply all match on
// these via errors.Is so the operator-visible copy stays under i18n control.
var (
	ErrPoolNameInvalid           = errors.New("storage: pool name invalid")
	ErrDataVdevEmpty             = errors.New("storage: data vdev requires at least one disk")
	ErrLayoutMinDisks            = errors.New("storage: vdev layout requires more disks")
	ErrSpecialNotRedundant       = errors.New("storage: special vdev must be redundant (mirror or raidz)")
	ErrDuplicateDisk             = errors.New("storage: disk used more than once")
	ErrDiskPathEmpty             = errors.New("storage: disk path is empty")
	ErrUnknownLayout             = errors.New("storage: unknown vdev layout")
	ErrVolumeNameInvalid         = errors.New("storage: volume name invalid")
	ErrSnapshotNameInvalid       = errors.New("storage: snapshot name invalid")
	ErrPresetUnknown             = errors.New("storage: preset unknown")
	ErrSmallBlocksWithoutSpecial = errors.New("storage: small_block_threshold requires a special vdev")
)

// MinDisksForLayout returns the minimum number of leaf disks a layout
// requires. Single = 1, Mirror = 2, RaidZ1/2/3 = 3/4/5 (one parity disk + at
// least two data disks for raidz1; ZFS itself accepts smaller raidz but the
// product policy is "raidz means redundant", so we enforce the practical
// minimums the operator manuals recommend).
func MinDisksForLayout(l VdevLayout) (int, bool) {
	switch l {
	case LayoutSingle:
		return 1, true
	case LayoutMirror:
		return 2, true
	case LayoutRaidZ1:
		return 3, true
	case LayoutRaidZ2:
		return 4, true
	case LayoutRaidZ3:
		return 5, true
	}
	return 0, false
}

// LayoutIsRedundant reports whether a layout, on its own, provides
// fault-tolerance. Single is the only layout that does not.
func LayoutIsRedundant(l VdevLayout) bool {
	switch l {
	case LayoutMirror, LayoutRaidZ1, LayoutRaidZ2, LayoutRaidZ3:
		return true
	}
	return false
}

// ValidatePoolConfig is a pure function — the same input always produces the
// same error or nil. Tests drive every branch with no Executor in sight.
//
// Rules, in order of evaluation:
//
//  1. Pool name must be a non-empty ZFS-safe identifier.
//  2. Data vdev must have a known layout and at least the minimum disks.
//  3. No disk path may appear twice across the entire pool (data + special
//     + spares). ZFS itself catches some duplicates but not all (e.g. /dev/sda
//     vs /dev/disk/by-id/sda-id pointing at the same drive); we only see paths
//     so we rely on the caller to canonicalize first.
//  4. If a special vdev is present, it must be redundant unless
//     ForceNoRedundancy is explicitly set (CLI escape hatch). UI never sets it.
//  5. SmallBlockThreshold > 0 requires a special vdev (otherwise the property
//     has no effect and silently drops user data into the data vdev).
func ValidatePoolConfig(cfg PoolConfig) error {
	if !isValidPoolName(cfg.Name) {
		return fmt.Errorf("%w: %q", ErrPoolNameInvalid, cfg.Name)
	}
	if err := validateVdevSpec(cfg.Data, "data"); err != nil {
		return err
	}

	seen := map[string]string{}
	if err := claimDisks(seen, cfg.Data.Disks, "data"); err != nil {
		return err
	}

	if cfg.Special != nil {
		if err := validateVdevSpec(*cfg.Special, "special"); err != nil {
			return err
		}
		if !LayoutIsRedundant(cfg.Special.Layout) && !cfg.ForceNoRedundancy {
			return fmt.Errorf("%w: layout=%s", ErrSpecialNotRedundant, cfg.Special.Layout)
		}
		if err := claimDisks(seen, cfg.Special.Disks, "special"); err != nil {
			return err
		}
	}
	if cfg.SmallBlockThreshold > 0 && cfg.Special == nil {
		return ErrSmallBlocksWithoutSpecial
	}
	for _, sp := range cfg.Spares {
		if sp == "" {
			return fmt.Errorf("%w: spare", ErrDiskPathEmpty)
		}
		if where, dup := seen[sp]; dup {
			return fmt.Errorf("%w: %q already used by %s", ErrDuplicateDisk, sp, where)
		}
		seen[sp] = "spare"
	}
	return nil
}

func validateVdevSpec(v VdevSpec, role string) error {
	min, ok := MinDisksForLayout(v.Layout)
	if !ok {
		return fmt.Errorf("%w: layout=%q (role=%s)", ErrUnknownLayout, v.Layout, role)
	}
	if len(v.Disks) == 0 {
		return fmt.Errorf("%w: role=%s", ErrDataVdevEmpty, role)
	}
	if len(v.Disks) < min {
		return fmt.Errorf("%w: layout=%s requires %d disks, got %d", ErrLayoutMinDisks, v.Layout, min, len(v.Disks))
	}
	for _, d := range v.Disks {
		if strings.TrimSpace(d) == "" {
			return fmt.Errorf("%w: role=%s", ErrDiskPathEmpty, role)
		}
	}
	return nil
}

func claimDisks(seen map[string]string, disks []string, role string) error {
	for _, d := range disks {
		if where, dup := seen[d]; dup {
			return fmt.Errorf("%w: %q already used by %s (now requested by %s)", ErrDuplicateDisk, d, where, role)
		}
		seen[d] = role
	}
	return nil
}

// isValidPoolName mirrors the ZFS rule: starts with a letter, body is
// [A-Za-z0-9_.-], length 1..50. We deliberately reject names beginning with
// "log", "mirror", "raidz" etc. that ZFS itself rejects to fail fast.
func isValidPoolName(name string) bool {
	if name == "" || len(name) > 50 {
		return false
	}
	first := name[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')) {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	switch strings.ToLower(name) {
	case "log", "mirror", "raidz", "raidz1", "raidz2", "raidz3", "spare", "cache":
		return false
	}
	return true
}

// isValidDatasetName checks the part after the pool name in pool/parent/leaf.
// ZFS allows / as the path separator and disallows leading slashes / blanks.
func isValidDatasetName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return false
	}
	if strings.Contains(name, "@") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/':
		default:
			return false
		}
	}
	return true
}

// isValidSnapshotName forbids @, /, whitespace.
func isValidSnapshotName(name string) bool {
	if name == "" || len(name) > 80 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}
