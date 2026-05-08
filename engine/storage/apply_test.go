package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/kuraos-org/kura/internal/config"
)

// fakeEngine implements Engine. The read methods return canned values so
// tests of the adapter can focus on plan / apply behaviour.
type fakeEngine struct {
	createdPools  []PoolConfig
	importedNames []string
	createdVols   []struct {
		Dataset string
		Opts    VolumeOpts
	}
	quotas    map[string]int64
	createErr error
}

func (f *fakeEngine) ListPools(context.Context) ([]Pool, error)                { return nil, nil }
func (f *fakeEngine) ListDisks(context.Context) ([]Disk, error)                { return nil, nil }
func (f *fakeEngine) ListImportable(context.Context) ([]ImportablePool, error) { return nil, nil }
func (f *fakeEngine) CreatePool(_ context.Context, c PoolConfig) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.createdPools = append(f.createdPools, c)
	return nil
}
func (f *fakeEngine) CreateVolume(_ context.Context, ds string, opts VolumeOpts) error {
	f.createdVols = append(f.createdVols, struct {
		Dataset string
		Opts    VolumeOpts
	}{ds, opts})
	return nil
}
func (f *fakeEngine) SetQuota(_ context.Context, ds string, n int64) error {
	if f.quotas == nil {
		f.quotas = map[string]int64{}
	}
	f.quotas[ds] = n
	return nil
}
func (f *fakeEngine) CreateSnapshot(context.Context, string, string) error { return nil }
func (f *fakeEngine) ListSnapshots(context.Context, string) ([]SnapshotInfo, error) {
	return nil, nil
}
func (f *fakeEngine) Rollback(context.Context, string, string) error { return nil }
func (f *fakeEngine) ImportPool(_ context.Context, name string, _ ImportOpts) error {
	f.importedNames = append(f.importedNames, name)
	return nil
}

func TestStorageAdapter_PlanCreatePool(t *testing.T) {
	old := config.New()
	newCfg := config.New()
	newCfg.Storage = &config.StorageConfig{
		Pools: []config.PoolEntry{
			{
				Name:     "tank",
				Topology: &config.TopologyEntry{Type: "raidz2", Disks: []string{"/dev/sda", "/dev/sdb", "/dev/sdc", "/dev/sdd"}},
			},
		},
	}
	a := NewStorageAdapter(&fakeEngine{})
	steps, err := a.Plan(context.Background(), old, newCfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(steps) != 1 || steps[0].Op != "create_pool" {
		t.Fatalf("steps = %+v", steps)
	}
	st, ok := steps[0].Detail.(CreatePoolStep)
	if !ok {
		t.Fatalf("detail %T", steps[0].Detail)
	}
	if st.Config.Name != "tank" || st.Config.Data.Layout != LayoutRaidZ2 {
		t.Errorf("config = %+v", st.Config)
	}
}

func TestStorageAdapter_PlanImportPool(t *testing.T) {
	old := config.New()
	newCfg := config.New()
	newCfg.Storage = &config.StorageConfig{
		Pools: []config.PoolEntry{{Name: "archive", Mode: "import"}},
	}
	a := NewStorageAdapter(&fakeEngine{})
	steps, err := a.Plan(context.Background(), old, newCfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(steps) != 1 || steps[0].Op != "import_pool" {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestStorageAdapter_PlanCreateVolumeAndQuotaUpdate(t *testing.T) {
	old := config.New()
	old.Storage = &config.StorageConfig{
		Volumes: []config.VolumeEntry{{Name: "tank/docs", Quota: "500G", Preset: "general"}},
	}
	newCfg := config.New()
	newCfg.Storage = &config.StorageConfig{
		Volumes: []config.VolumeEntry{
			{Name: "tank/docs", Quota: "1T", Preset: "general"},
			{Name: "tank/photos", Preset: "media"},
		},
	}
	a := NewStorageAdapter(&fakeEngine{})
	steps, err := a.Plan(context.Background(), old, newCfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var sawQuota, sawCreate bool
	for _, s := range steps {
		if s.Op == "set_quota" {
			sawQuota = true
		}
		if s.Op == "create_volume" {
			sawCreate = true
		}
	}
	if !sawQuota || !sawCreate {
		t.Errorf("expected both create_volume and set_quota in steps: %+v", steps)
	}
}

func TestStorageAdapter_ApplyDispatches(t *testing.T) {
	eng := &fakeEngine{}
	a := NewStorageAdapter(eng)
	steps := []config.Step{
		{Section: "storage", Op: "create_pool", Detail: CreatePoolStep{Config: PoolConfig{
			Name: "tank",
			Data: VdevSpec{Layout: LayoutMirror, Disks: []string{"/dev/sda", "/dev/sdb"}},
		}}},
		{Section: "storage", Op: "import_pool", Detail: ImportPoolStep{Name: "archive"}},
		{Section: "storage", Op: "create_volume", Detail: CreateVolumeStep{Dataset: "tank/docs", Opts: VolumeOpts{Preset: PresetGeneral}}},
		{Section: "storage", Op: "set_quota", Detail: SetQuotaStep{Dataset: "tank/docs", QuotaBytes: 1 << 30}},
	}
	if err := a.Apply(context.Background(), steps); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(eng.createdPools) != 1 || eng.createdPools[0].Name != "tank" {
		t.Errorf("createdPools = %+v", eng.createdPools)
	}
	if len(eng.importedNames) != 1 || eng.importedNames[0] != "archive" {
		t.Errorf("importedNames = %+v", eng.importedNames)
	}
	if eng.quotas["tank/docs"] != 1<<30 {
		t.Errorf("quotas = %+v", eng.quotas)
	}
}

func TestStorageAdapter_ApplyPropagatesError(t *testing.T) {
	eng := &fakeEngine{createErr: errors.New("boom")}
	a := NewStorageAdapter(eng)
	steps := []config.Step{
		{Section: "storage", Op: "create_pool", Detail: CreatePoolStep{Config: PoolConfig{Name: "tank"}}},
	}
	if err := a.Apply(context.Background(), steps); err == nil {
		t.Fatalf("expected error from underlying engine")
	}
}

func TestParseHumanBytes(t *testing.T) {
	cases := map[string]int64{
		"":     0,
		"500":  500,
		"1K":   1024,
		"1k":   1024,
		"500G": 500 * 1024 * 1024 * 1024,
		"2T":   2 * 1024 * 1024 * 1024 * 1024,
	}
	for in, want := range cases {
		got, err := parseHumanBytes(in)
		if err != nil {
			t.Errorf("parseHumanBytes(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseHumanBytes(%q) = %d, want %d", in, got, want)
		}
	}
}
