package storage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/internal/cmdexec"
)

// Tests for CreatePool / CreateVolume / CreateSnapshot / SetQuota / Rollback.
// All commands are expected to issue a deterministic argv against the
// cmdexec.Fake, and the test asserts on it directly. This keeps the
// production "we shell out" code one line and gives us full coverage on the
// argument-building logic where mistakes (e.g., wrong special_small_blocks
// value) would silently corrupt data.

func TestBuildCreatePoolArgs(t *testing.T) {
	cases := []struct {
		name string
		cfg  PoolConfig
		want []string
	}{
		{
			name: "raidz2 default ashift",
			cfg: PoolConfig{
				Name: "tank",
				Data: VdevSpec{Layout: LayoutRaidZ2, Disks: []string{"/dev/sda", "/dev/sdb", "/dev/sdc", "/dev/sdd"}},
			},
			want: []string{"create", "-o", "ashift=12", "tank", "raidz2", "/dev/sda", "/dev/sdb", "/dev/sdc", "/dev/sdd"},
		},
		{
			name: "mirror data + mirror special with small_block",
			cfg: PoolConfig{
				Name:                "tank",
				Data:                VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
				Special:             &VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/nvme0n1", "/dev/nvme1n1"}},
				SmallBlockThreshold: 32768,
			},
			want: []string{"create", "-o", "ashift=12", "-o", "special_small_blocks=32768",
				"tank", "mirror", "/dev/sda", "/dev/sdb", "special", "mirror", "/dev/nvme0n1", "/dev/nvme1n1"},
		},
		{
			name: "single disk pool + spare",
			cfg: PoolConfig{
				Name:   "scratch",
				Data:   VdevSpec{Layout: LayoutSingle, Disks: []string{"/dev/sda"}},
				Spares: []string{"/dev/sdb"},
			},
			want: []string{"create", "-o", "ashift=12", "scratch", "/dev/sda", "spare", "/dev/sdb"},
		},
		{
			name: "explicit ashift=9",
			cfg: PoolConfig{
				Name:   "old",
				Data:   VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
				Ashift: 9,
			},
			want: []string{"create", "-o", "ashift=9", "old", "mirror", "/dev/sda", "/dev/sdb"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildCreatePoolArgs(tc.cfg)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv mismatch:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestCreatePool_validatesBeforeShell(t *testing.T) {
	fake := cmdexec.NewFake()
	c := NewCLI(fake)

	// Single SSD special vdev — must error before hitting zpool.
	err := c.CreatePool(context.Background(), PoolConfig{
		Name:    "tank",
		Data:    VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
		Special: &VdevSpec{Layout: LayoutSingle, Disks: []string{"/dev/nvme0n1"}},
	})
	if !errors.Is(err, ErrSpecialNotRedundant) {
		t.Fatalf("want ErrSpecialNotRedundant, got %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("expected no zpool call when validation rejects; got %d calls", len(calls))
	}
}

func TestCreatePool_happyPath(t *testing.T) {
	fake := cmdexec.NewFake()
	cfg := PoolConfig{
		Name: "tank",
		Data: VdevSpec{Layout: LayoutRaidZ2, Disks: []string{"/dev/sda", "/dev/sdb", "/dev/sdc", "/dev/sdd"}},
	}
	args := buildCreatePoolArgs(cfg)
	fake.RegisterStdout("zpool", args, []byte(""))
	for _, d := range cfg.Data.Disks {
		fake.RegisterStdout("zpool", []string{"labelclear", "-f", d}, []byte(""))
	}

	c := NewCLI(fake)
	if err := c.CreatePool(context.Background(), cfg); err != nil {
		t.Fatalf("CreatePool: %v", err)
	}

	calls := fake.Calls()
	if len(calls) != len(cfg.Data.Disks)+1 {
		t.Fatalf("want %d calls (labelclear×%d + create), got %d", len(cfg.Data.Disks)+1, len(cfg.Data.Disks), len(calls))
	}
	for i, d := range cfg.Data.Disks {
		want := []string{"labelclear", "-f", d}
		if !reflect.DeepEqual(calls[i].Args, want) {
			t.Errorf("call %d argv = %q, want %q", i, calls[i].Args, want)
		}
	}
	last := calls[len(calls)-1]
	if !reflect.DeepEqual(last.Args, args) {
		t.Errorf("create argv mismatch: got %q, want %q", last.Args, args)
	}
}

// Foreign disks (stale ZFS labels from a previously-destroyed pool) must still
// flow through CreatePool: labelclear failure is non-fatal so the create
// proceeds even when one of the disks has nothing to clear.
func TestCreatePool_foreignDisksLabelclearBestEffort(t *testing.T) {
	fake := cmdexec.NewFake()
	cfg := PoolConfig{
		Name: "tank",
		Data: VdevSpec{Layout: LayoutSingle, Disks: []string{"/dev/sdb"}},
	}
	args := buildCreatePoolArgs(cfg)
	fake.RegisterStdout("zpool", args, []byte(""))
	// labelclear deliberately not registered → returns error → swallowed.

	c := NewCLI(fake)
	if err := c.CreatePool(context.Background(), cfg); err != nil {
		t.Fatalf("CreatePool: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("want 2 calls (labelclear, create), got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].Args, []string{"labelclear", "-f", "/dev/sdb"}) {
		t.Errorf("first call argv = %q, want labelclear", calls[0].Args)
	}
	if !reflect.DeepEqual(calls[1].Args, args) {
		t.Errorf("second call argv = %q, want create", calls[1].Args)
	}
}

func TestBuildCreateVolumeArgs_presetExpansion(t *testing.T) {
	cases := []struct {
		name    string
		dataset string
		opts    VolumeOpts
		want    []string
	}{
		{
			name:    "general preset",
			dataset: "tank/docs",
			opts:    VolumeOpts{Preset: PresetGeneral},
			want:    []string{"create", "-o", "recordsize=128K", "-o", "compression=zstd", "tank/docs"},
		},
		{
			name:    "media preset",
			dataset: "tank/media",
			opts:    VolumeOpts{Preset: PresetMedia},
			want:    []string{"create", "-o", "recordsize=1M", "-o", "compression=zstd", "tank/media"},
		},
		{
			name:    "database preset (special_small_blocks + logbias)",
			dataset: "fast/apps/db",
			opts:    VolumeOpts{Preset: PresetDatabase},
			want: []string{"create",
				"-o", "recordsize=16K",
				"-o", "compression=zstd",
				"-o", "special_small_blocks=64K",
				"-o", "logbias=latency",
				"fast/apps/db",
			},
		},
		{
			name:    "raw fields override preset",
			dataset: "tank/photos",
			opts:    VolumeOpts{Preset: PresetMedia, RecordSize: "512K"},
			want:    []string{"create", "-o", "recordsize=512K", "-o", "compression=zstd", "tank/photos"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts
			if err := applyPresetToOpts(&opts); err != nil {
				t.Fatalf("applyPresetToOpts: %v", err)
			}
			got := buildCreateVolumeArgs(tc.dataset, opts)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv mismatch:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestCreateVolume_setsQuotaWhenProvided(t *testing.T) {
	fake := cmdexec.NewFake()
	dataset := "tank/docs"
	opts := VolumeOpts{Preset: PresetGeneral, QuotaBytes: 1 << 30} // 1 GiB

	expanded := opts
	if err := applyPresetToOpts(&expanded); err != nil {
		t.Fatalf("applyPresetToOpts: %v", err)
	}
	createArgs := buildCreateVolumeArgs(dataset, expanded)
	fake.RegisterStdout("zfs", createArgs, []byte(""))
	fake.RegisterStdout("zfs", []string{"set", "quota=1073741824", dataset}, []byte(""))

	c := NewCLI(fake)
	if err := c.CreateVolume(context.Background(), dataset, opts); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("want 2 calls (create + quota), got %d", len(calls))
	}
}

func TestCreateSnapshot(t *testing.T) {
	fake := cmdexec.NewFake()
	fake.RegisterStdout("zfs", []string{"snapshot", "tank/docs@auto-2026-05-08-03"}, []byte(""))

	c := NewCLI(fake)
	if err := c.CreateSnapshot(context.Background(), "tank/docs", "auto-2026-05-08-03"); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
}

func TestCreateSnapshot_invalidName(t *testing.T) {
	fake := cmdexec.NewFake()
	c := NewCLI(fake)
	if err := c.CreateSnapshot(context.Background(), "tank/docs", "bad name"); !errors.Is(err, ErrSnapshotNameInvalid) {
		t.Errorf("want ErrSnapshotNameInvalid, got %v", err)
	}
	if err := c.CreateSnapshot(context.Background(), "/abs/path", "ok"); !errors.Is(err, ErrVolumeNameInvalid) {
		t.Errorf("want ErrVolumeNameInvalid, got %v", err)
	}
}

func TestRollback(t *testing.T) {
	fake := cmdexec.NewFake()
	fake.RegisterStdout("zfs", []string{"rollback", "tank/docs@auto-2026-04-26-03"}, []byte(""))

	c := NewCLI(fake)
	if err := c.Rollback(context.Background(), "tank/docs", "auto-2026-04-26-03"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if !strings.Contains(strings.Join(calls[0].Args, " "), "@auto-2026-04-26-03") {
		t.Errorf("rollback argv missing snapshot suffix: %q", calls[0].Args)
	}
}

func TestSetQuota_unsetsWhenZero(t *testing.T) {
	fake := cmdexec.NewFake()
	fake.RegisterStdout("zfs", []string{"set", "quota=none", "tank/docs"}, []byte(""))

	c := NewCLI(fake)
	if err := c.SetQuota(context.Background(), "tank/docs", 0); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
}

func TestListSnapshots_parsesTSV(t *testing.T) {
	fake := cmdexec.NewFake()
	fake.RegisterStdout("zfs",
		[]string{"list", "-H", "-p", "-t", "snapshot", "-o", "name,used,refer,creation", "-r", "tank/docs"},
		[]byte("tank/docs@auto-2026-05-08-03\t1024\t2048\t1715169600\n"+
			"tank/docs@auto-2026-05-07-03\t512\t2048\t1715083200\n"))

	c := NewCLI(fake)
	snaps, err := c.ListSnapshots(context.Background(), "tank/docs")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("want 2 snapshots, got %d", len(snaps))
	}
	if snaps[0].Dataset != "tank/docs" || snaps[0].Name != "auto-2026-05-08-03" {
		t.Errorf("snap[0] = %+v", snaps[0])
	}
	if snaps[0].UsedBytes != 1024 {
		t.Errorf("snap[0].used = %d", snaps[0].UsedBytes)
	}
}
