package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kuraos-org/kura/internal/cmdexec"
)

// readFixture loads a testdata file. Failing the test on a missing file is
// the right behavior — the parsers can't be exercised without realistic
// inputs, and the dev box has no ZFS so we can't regenerate them on demand.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	p := filepath.Join("testdata", name)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture %s: %v", p, err)
	}
	return raw
}

// [AC-Se3b190-1-1] zpool list -H -p output round-trips into Pool structs.
func TestParseZpoolList(t *testing.T) {
	cases := []struct {
		name      string
		fixture   string
		wantPools int
		check     func(*testing.T, []Pool)
	}{
		{
			name:      "empty",
			fixture:   "zpool-list-empty.txt",
			wantPools: 0,
		},
		{
			name:      "two pools",
			fixture:   "zpool-list-two-pools.txt",
			wantPools: 2,
			check: func(t *testing.T, p []Pool) {
				if p[0].Name != "tank" {
					t.Errorf("p[0].Name = %q, want tank", p[0].Name)
				}
				if p[0].SizeBytes != 26388279066624 {
					t.Errorf("p[0].SizeBytes = %d", p[0].SizeBytes)
				}
				if p[0].FragPercent != 12 || p[0].CapPercent != 76 {
					t.Errorf("p[0] frag/cap = %d/%d", p[0].FragPercent, p[0].CapPercent)
				}
				if p[0].Health != HealthOnline {
					t.Errorf("p[0].Health = %q", p[0].Health)
				}
				if p[0].GUID != "17215394218034012345" {
					t.Errorf("p[0].GUID = %q", p[0].GUID)
				}
				if p[1].Name != "fast" {
					t.Errorf("p[1].Name = %q", p[1].Name)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pools, err := parseZpoolList(readFixture(t, tc.fixture))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(pools) != tc.wantPools {
				t.Fatalf("len(pools) = %d, want %d", len(pools), tc.wantPools)
			}
			if tc.check != nil {
				tc.check(t, pools)
			}
		})
	}
}

// [AC-Se3b190-1-1] zpool status -P -L output produces a topology tree
// covering single / mirror / raidz2 / degraded-with-special-and-spare.
func TestParseZpoolStatus(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		check   func(*testing.T, []Vdev)
	}{
		{
			name:    "single disk",
			fixture: "zpool-status-single.txt",
			check: func(t *testing.T, vs []Vdev) {
				if len(vs) != 1 {
					t.Fatalf("len = %d, want 1", len(vs))
				}
				if vs[0].Type != VdevTypeData || vs[0].Layout != LayoutUnknown {
					t.Errorf("type/layout = %s/%s", vs[0].Type, vs[0].Layout)
				}
				if vs[0].Path == "" {
					t.Errorf("expected path on leaf")
				}
				if vs[0].Health != HealthOnline {
					t.Errorf("health = %s", vs[0].Health)
				}
			},
		},
		{
			name:    "mirror",
			fixture: "zpool-status-mirror.txt",
			check: func(t *testing.T, vs []Vdev) {
				if len(vs) != 1 || vs[0].Layout != LayoutMirror {
					t.Fatalf("top-level not mirror: %+v", vs)
				}
				if len(vs[0].Children) != 2 {
					t.Errorf("mirror children = %d", len(vs[0].Children))
				}
				for _, c := range vs[0].Children {
					if c.Health != HealthOnline {
						t.Errorf("child health = %s", c.Health)
					}
				}
			},
		},
		{
			name:    "raidz2",
			fixture: "zpool-status-raidz2.txt",
			check: func(t *testing.T, vs []Vdev) {
				if len(vs) != 1 || vs[0].Layout != LayoutRaidZ2 {
					t.Fatalf("top-level not raidz2: %+v", vs)
				}
				if len(vs[0].Children) != 4 {
					t.Errorf("raidz2 children = %d", len(vs[0].Children))
				}
			},
		},
		{
			name:    "degraded with special + spare",
			fixture: "zpool-status-degraded-with-special-and-spare.txt",
			check: func(t *testing.T, vs []Vdev) {
				// data raidz1 + special mirror + cache leaf + log leaf + spare leaf
				kinds := map[VdevType]int{}
				for _, v := range vs {
					kinds[v.Type]++
				}
				if kinds[VdevTypeData] == 0 {
					t.Errorf("missing data vdev")
				}
				if kinds[VdevTypeSpecial] == 0 {
					t.Errorf("missing special vdev")
				}
				if kinds[VdevTypeCache] == 0 {
					t.Errorf("missing cache vdev")
				}
				if kinds[VdevTypeLog] == 0 {
					t.Errorf("missing log vdev")
				}
				if kinds[VdevTypeSpare] == 0 {
					t.Errorf("missing spare vdev")
				}
				// Find the data raidz and check it has a faulted child.
				var faulted bool
				for _, v := range vs {
					if v.Type != VdevTypeData {
						continue
					}
					for _, c := range v.Children {
						if c.Health == HealthFaulted && c.Read == 6 && c.Write == 124 {
							faulted = true
						}
					}
				}
				if !faulted {
					t.Errorf("expected a faulted leaf with R=6 W=124")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vs, _, err := parseZpoolStatus(readFixture(t, tc.fixture))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			tc.check(t, vs)
		})
	}
}

// [AC-Se3b190-1-1] zpool import (no args) output produces ImportablePool list.
func TestParseZpoolImport(t *testing.T) {
	cases := []struct {
		name      string
		fixture   string
		wantPools int
		check     func(*testing.T, []ImportablePool)
	}{
		{
			name:      "empty",
			fixture:   "zpool-import-empty.txt",
			wantPools: 0,
		},
		{
			name:      "two importable",
			fixture:   "zpool-import-two-importable.txt",
			wantPools: 2,
			check: func(t *testing.T, ps []ImportablePool) {
				if ps[0].Name != "archive" || ps[0].GUID != "14882371020481034512" {
					t.Errorf("p[0] = %+v", ps[0])
				}
				if ps[0].State != HealthOnline {
					t.Errorf("p[0].State = %s", ps[0].State)
				}
				if len(ps[0].Topology) != 1 || ps[0].Topology[0].Layout != LayoutRaidZ1 {
					t.Errorf("p[0].Topology = %+v", ps[0].Topology)
				}
				if len(ps[0].Topology[0].Children) != 3 {
					t.Errorf("p[0] raidz children = %d", len(ps[0].Topology[0].Children))
				}
				if ps[1].Name != "photos" || ps[1].State != HealthDegraded {
					t.Errorf("p[1] = %+v", ps[1])
				}
				if ps[1].Action == "" {
					t.Errorf("p[1].Action empty (multi-line should have folded)")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pools, err := parseZpoolImportScan(readFixture(t, tc.fixture))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(pools) != tc.wantPools {
				t.Fatalf("len = %d, want %d", len(pools), tc.wantPools)
			}
			if tc.check != nil {
				tc.check(t, pools)
			}
		})
	}
}

// CLI.ListPools wires parseZpoolList + parseZpoolStatus through a Fake exec.
func TestCLI_ListPools(t *testing.T) {
	fx := cmdexec.NewFake()
	fx.RegisterStdout("zpool", []string{"list", "-H", "-p", "-o", "name,size,allocated,free,frag,cap,dedup,health,guid"},
		readFixture(t, "zpool-list-two-pools.txt"))
	fx.RegisterStdout("zpool", []string{"status", "-P", "-L", "tank"}, readFixture(t, "zpool-status-raidz2.txt"))
	fx.RegisterStdout("zpool", []string{"status", "-P", "-L", "fast"}, readFixture(t, "zpool-status-mirror.txt"))

	c := NewCLI(fx)
	pools, err := c.ListPools(context.Background())
	if err != nil {
		t.Fatalf("ListPools: %v", err)
	}
	if len(pools) != 2 {
		t.Fatalf("len = %d", len(pools))
	}
	if len(pools[0].Topology) == 0 || pools[0].Topology[0].Layout != LayoutRaidZ2 {
		t.Errorf("tank topology not raidz2: %+v", pools[0].Topology)
	}
	if len(pools[1].Topology) == 0 || pools[1].Topology[0].Layout != LayoutMirror {
		t.Errorf("fast topology not mirror: %+v", pools[1].Topology)
	}
}

func TestCLI_ListImportable_Empty(t *testing.T) {
	fx := cmdexec.NewFake()
	fx.RegisterStdout("zpool", []string{"import"}, readFixture(t, "zpool-import-empty.txt"))
	c := NewCLI(fx)
	got, err := c.ListImportable(context.Background())
	if err != nil {
		t.Fatalf("ListImportable: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestCLI_ListImportable_Found(t *testing.T) {
	fx := cmdexec.NewFake()
	fx.RegisterStdout("zpool", []string{"import"}, readFixture(t, "zpool-import-two-importable.txt"))
	c := NewCLI(fx)
	got, err := c.ListImportable(context.Background())
	if err != nil {
		t.Fatalf("ListImportable: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}
