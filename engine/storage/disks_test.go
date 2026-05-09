package storage

import (
	"context"
	"testing"

	"github.com/kuraos-org/kura/internal/cmdexec"
)

// [AC-Se3b190-2-1] lsblk JSON parses into Disk slice with the expected
// classification (system / pool / free).
func TestParseLsblk(t *testing.T) {
	disks, err := parseLsblk(readFixture(t, "lsblk-typical.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(disks) != 5 {
		t.Fatalf("len = %d, want 5: %+v", len(disks), disks)
	}
	by := map[string]Disk{}
	for _, d := range disks {
		by[d.Path] = d
	}
	if d := by["/dev/sda"]; d.Model != "WDC WD80EFZZ-68BTXN0" || d.Serial != "VLH8WMTL" {
		t.Errorf("sda model/serial = %q/%q", d.Model, d.Serial)
	}
	if by["/dev/sdsys"].Usage != DiskUsageSystem {
		t.Errorf("sdsys usage = %s, want system", by["/dev/sdsys"].Usage)
	}
	// sdz has no children — fully unclaimed.
	if by["/dev/sdz"].Usage != DiskUsageFree {
		t.Errorf("sdz usage = %s, want free", by["/dev/sdz"].Usage)
	}
	// sda has zfs_member partitions.
	if by["/dev/sda"].Usage != DiskUsagePool {
		t.Errorf("sda usage = %s, want pool", by["/dev/sda"].Usage)
	}
}

// [AC-Se3b190-2-1] smartctl -j JSON for a passed drive yields SMARTStatusPassed.
func TestParseSmartctl(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		want    SMARTStatus
		wantTC  int
	}{
		{"passed", "smartctl-passed.json", SMARTStatusPassed, 38},
		{"warning (passed but pending sec)", "smartctl-warning.json", SMARTStatusWarning, 42},
		{"failed", "smartctl-failed.json", SMARTStatusFailed, 51},
		// QEMU virtual disks return JSON without a smart_status block. The
		// previous parser defaulted Passed=false here and rendered "異常";
		// treat the missing block as "no failure signal" and report passed.
		{"no smart_status block (QEMU/USB)", "smartctl-no-status.json", SMARTStatusPassed, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSmartctl(readFixture(t, tc.fixture))
			if got.Status != tc.want {
				t.Errorf("status = %s, want %s", got.Status, tc.want)
			}
			if got.TemperatureC != tc.wantTC {
				t.Errorf("temp = %d, want %d", got.TemperatureC, tc.wantTC)
			}
		})
	}
}

// [AC-Se3b190-2-1] CLI.ListDisks merges lsblk + zpool membership + smartctl
// into a coherent []Disk; pool membership demotes ZFS-member disks not in
// any imported pool to DiskUsageForeign.
func TestCLI_ListDisks_FullPipeline(t *testing.T) {
	fx := cmdexec.NewFake()
	fx.RegisterStdout("lsblk", []string{"-J", "-O", "-b"}, readFixture(t, "lsblk-typical.json"))
	// Fixture has sda/sdb/nvme0n1 as zfs_member but only "tank" claims sda/sdb.
	// nvme0n1 should be demoted to foreign.
	fx.RegisterStdout("zpool", []string{"list", "-H", "-p", "-o", "name,size,allocated,free,frag,cap,dedup,health,guid"},
		[]byte("tank\t26388279066624\t20126457528320\t6261821538304\t12\t76\t1.00x\tONLINE\t12345\n"))
	fx.RegisterStdout("zpool", []string{"status", "-P", "-L", "tank"}, readFixture(t, "zpool-status-raidz2.txt"))
	for _, p := range []string{"/dev/sda", "/dev/sdb", "/dev/sdz", "/dev/nvme0n1", "/dev/sdsys"} {
		// Same canned response for all: keeps the test focused on the
		// merge logic, not on smartctl per-device variance.
		fx.RegisterStdout("smartctl", []string{"-a", "-j", p}, readFixture(t, "smartctl-passed.json"))
	}

	c := NewCLI(fx)
	disks, err := c.ListDisks(context.Background())
	if err != nil {
		t.Fatalf("ListDisks: %v", err)
	}
	by := map[string]Disk{}
	for _, d := range disks {
		by[d.Path] = d
	}
	if by["/dev/sda"].Pool != "tank" || by["/dev/sda"].Usage != DiskUsagePool {
		t.Errorf("sda = %+v", by["/dev/sda"])
	}
	if by["/dev/nvme0n1"].Usage != DiskUsageForeign {
		t.Errorf("nvme0n1 usage = %s, want foreign (zfs_member but no live pool claim)", by["/dev/nvme0n1"].Usage)
	}
	if by["/dev/sdz"].Usage != DiskUsageFree {
		t.Errorf("sdz usage = %s, want free", by["/dev/sdz"].Usage)
	}
	if by["/dev/sda"].SMART.Status != SMARTStatusPassed || by["/dev/sda"].SMART.TemperatureC != 38 {
		t.Errorf("sda SMART = %+v", by["/dev/sda"].SMART)
	}
}

// [AC-Se3b190-2-1] When smartctl is missing entirely, the SMART block falls
// back to {Source:"unavailable"} so the UI can render a neutral state instead
// of crashing.
func TestCLI_ListDisks_SmartctlMissing(t *testing.T) {
	fx := cmdexec.NewFake()
	fx.RegisterStdout("lsblk", []string{"-J", "-O", "-b"}, readFixture(t, "lsblk-typical.json"))
	// No zpool registrations — ListPools fails, that's fine, classification
	// just stays at lsblk-derived value.
	// No smartctl registrations either: every smartFor call falls into the
	// fake "no response" error path → SMARTInfo{Source:"unavailable"}.
	c := NewCLI(fx)
	disks, err := c.ListDisks(context.Background())
	if err != nil {
		t.Fatalf("ListDisks: %v", err)
	}
	for _, d := range disks {
		if d.SMART.Source != "unavailable" {
			t.Errorf("disk %s SMART.Source = %q, want unavailable", d.Path, d.SMART.Source)
		}
	}
}
