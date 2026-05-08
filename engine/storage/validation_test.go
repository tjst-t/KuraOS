package storage

import (
	"errors"
	"testing"
)

// Validation rules are pure functions, so the test table is exhaustive on
// the *categories* of input rather than every concrete combination. The
// special-vdev redundancy rule (priority #5 / forbidden #3) is the bedrock
// case; tests for other categories piggy-back on the same table format.
func TestValidatePoolConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     PoolConfig
		wantErr error
	}{
		{
			name: "ok: raidz2 data",
			cfg: PoolConfig{
				Name: "tank",
				Data: VdevSpec{Layout: LayoutRaidZ2, Disks: []string{"/dev/sda", "/dev/sdb", "/dev/sdc", "/dev/sdd"}},
			},
		},
		{
			name: "ok: mirror data + mirror special",
			cfg: PoolConfig{
				Name:                "tank",
				Data:                VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
				Special:             &VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/nvme0n1", "/dev/nvme1n1"}},
				SmallBlockThreshold: 32 * 1024,
			},
		},
		{
			name: "ok: single disk pool (Layout=single)",
			cfg: PoolConfig{
				Name: "scratch",
				Data: VdevSpec{Layout: LayoutSingle, Disks: []string{"/dev/sda"}},
			},
		},
		{
			name: "err: empty pool name",
			cfg: PoolConfig{
				Name: "",
				Data: VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
			},
			wantErr: ErrPoolNameInvalid,
		},
		{
			name: "err: pool name shadows ZFS keyword",
			cfg: PoolConfig{
				Name: "mirror",
				Data: VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
			},
			wantErr: ErrPoolNameInvalid,
		},
		{
			name: "err: data vdev empty",
			cfg: PoolConfig{
				Name: "tank",
				Data: VdevSpec{Layout: LayoutMirror, Disks: nil},
			},
			wantErr: ErrDataVdevEmpty,
		},
		{
			name: "err: raidz2 with 3 disks (needs 4)",
			cfg: PoolConfig{
				Name: "tank",
				Data: VdevSpec{Layout: LayoutRaidZ2, Disks: []string{"/dev/sda", "/dev/sdb", "/dev/sdc"}},
			},
			wantErr: ErrLayoutMinDisks,
		},
		{
			name: "err: unknown layout",
			cfg: PoolConfig{
				Name: "tank",
				Data: VdevSpec{Layout: VdevLayout("dance"), Disks: []string{"/dev/sda"}},
			},
			wantErr: ErrUnknownLayout,
		},
		{
			name: "err: special vdev single SSD (no force) — DESIGN_PRINCIPLES forbidden #3",
			cfg: PoolConfig{
				Name:    "tank",
				Data:    VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
				Special: &VdevSpec{Layout: LayoutSingle, Disks: []string{"/dev/nvme0n1"}},
			},
			wantErr: ErrSpecialNotRedundant,
		},
		{
			name: "ok: special vdev single SSD with force flag (CLI escape hatch)",
			cfg: PoolConfig{
				Name:              "tank",
				Data:              VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
				Special:           &VdevSpec{Layout: LayoutSingle, Disks: []string{"/dev/nvme0n1"}},
				ForceNoRedundancy: true,
			},
		},
		{
			name: "err: duplicate disk between data and special",
			cfg: PoolConfig{
				Name:    "tank",
				Data:    VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
				Special: &VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/nvme0n1"}},
			},
			wantErr: ErrDuplicateDisk,
		},
		{
			name: "err: duplicate disk in spares",
			cfg: PoolConfig{
				Name:   "tank",
				Data:   VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
				Spares: []string{"/dev/sda"},
			},
			wantErr: ErrDuplicateDisk,
		},
		{
			name: "err: empty disk path",
			cfg: PoolConfig{
				Name: "tank",
				Data: VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", ""}},
			},
			wantErr: ErrDiskPathEmpty,
		},
		{
			name: "err: small_block_threshold without special vdev",
			cfg: PoolConfig{
				Name:                "tank",
				Data:                VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
				SmallBlockThreshold: 32 * 1024,
			},
			wantErr: ErrSmallBlocksWithoutSpecial,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePoolConfig(tc.cfg)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %v, got nil", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error wrapping %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestMinDisksForLayout(t *testing.T) {
	cases := []struct {
		l    VdevLayout
		want int
		ok   bool
	}{
		{LayoutSingle, 1, true},
		{LayoutMirror, 2, true},
		{LayoutRaidZ1, 3, true},
		{LayoutRaidZ2, 4, true},
		{LayoutRaidZ3, 5, true},
		{LayoutUnknown, 0, false},
	}
	for _, tc := range cases {
		got, ok := MinDisksForLayout(tc.l)
		if got != tc.want || ok != tc.ok {
			t.Errorf("MinDisksForLayout(%q) = (%d, %v), want (%d, %v)", tc.l, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLayoutIsRedundant(t *testing.T) {
	cases := []struct {
		l    VdevLayout
		want bool
	}{
		{LayoutSingle, false},
		{LayoutMirror, true},
		{LayoutRaidZ1, true},
		{LayoutRaidZ2, true},
		{LayoutRaidZ3, true},
		{LayoutUnknown, false},
	}
	for _, tc := range cases {
		if got := LayoutIsRedundant(tc.l); got != tc.want {
			t.Errorf("LayoutIsRedundant(%q) = %v, want %v", tc.l, got, tc.want)
		}
	}
}
