package app

import (
	"context"
	"testing"

	"github.com/kuraos-org/kura/internal/store"
)

func mustOpenStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// openStoreAt is the variant the reopen-survives test uses: caller takes
// responsibility for Close so the test can re-open the same path twice.
func openStoreAt(path string) (*store.Store, error) {
	return store.Open(context.Background(), path)
}

// TestDatasetPlannerPoolHintFastTier covers AC-S1bccf5-3-1: pool_hint:ssd
// must select the SSD pool when it exists, fall back to tank otherwise.
func TestDatasetPlannerPoolHintFastTier(t *testing.T) {
	// [AC-S1bccf5-3-1]
	st := mustOpenStore(t)

	cases := []struct {
		name     string
		pools    []PoolInfo
		hint     PoolHint
		wantPool string
		wantPath string
	}{
		{
			name:     "ssd hint picks ssd pool when present",
			pools:    []PoolInfo{{Name: "fast", Role: PoolHintSSD}, {Name: "tank", Role: PoolHintTank}},
			hint:     PoolHintSSD,
			wantPool: "fast",
			wantPath: "fast/apps/immich/db",
		},
		{
			name:     "ssd hint falls back to tank when no ssd pool",
			pools:    []PoolInfo{{Name: "tank", Role: PoolHintTank}},
			hint:     PoolHintSSD,
			wantPool: "tank",
			wantPath: "tank/apps/immich/db",
		},
		{
			name:     "no pools at all uses fallback constant",
			pools:    nil,
			hint:     PoolHintSSD,
			wantPool: "tank",
			wantPath: "tank/apps/immich/db",
		},
		{
			name:     "tank hint picks tank pool when present",
			pools:    []PoolInfo{{Name: "fast", Role: PoolHintSSD}, {Name: "tank", Role: PoolHintTank}},
			hint:     PoolHintTank,
			wantPool: "tank",
			wantPath: "tank/apps/immich/db",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			planner := NewDatasetPlanner(st.DB(), &StaticPoolLister{Pools: tc.pools})
			m := &Manifest{
				Name: "immich",
				Storage: StorageBlock{
					Datasets: []Dataset{{Name: "db", PoolHint: tc.hint, Quota: "5G", Backup: true}},
				},
			}
			// Use a unique appID per test case so the per-app cache does
			// not poison cross-case runs through the same SQLite file.
			appID := "immich-" + tc.name
			plans, err := planner.Plan(context.Background(), appID, m)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if len(plans) != 1 {
				t.Fatalf("len(plans) = %d, want 1", len(plans))
			}
			got := plans[0]
			if got.ChosenPool != tc.wantPool {
				t.Errorf("ChosenPool = %q, want %q", got.ChosenPool, tc.wantPool)
			}
			if got.DatasetPath != tc.wantPath {
				t.Errorf("DatasetPath = %q, want %q", got.DatasetPath, tc.wantPath)
			}
			if got.Quota != "5G" {
				t.Errorf("Quota = %q, want 5G", got.Quota)
			}
			if !got.Backup {
				t.Errorf("Backup = false, want true")
			}
		})
	}
}

// TestDatasetPlannerStableAcrossRuns proves the cached plan from app_dataset_plan
// is returned verbatim on the second Plan call, even if the pool topology
// changes underneath. (Operators must explicitly migrate; planner does not
// silently move data.)
func TestDatasetPlannerStableAcrossRuns(t *testing.T) {
	// [AC-S1bccf5-3-1] (stability complement)
	st := mustOpenStore(t)
	first := NewDatasetPlanner(st.DB(), &StaticPoolLister{Pools: []PoolInfo{{Name: "fast", Role: PoolHintSSD}}})
	m := &Manifest{
		Name:    "immich",
		Storage: StorageBlock{Datasets: []Dataset{{Name: "db", PoolHint: PoolHintSSD, Backup: true, Quota: "5G"}}},
	}
	plans1, err := first.Plan(context.Background(), "immich-prod", m)
	if err != nil {
		t.Fatalf("first plan: %v", err)
	}
	if plans1[0].ChosenPool != "fast" {
		t.Fatalf("first.ChosenPool = %q, want fast", plans1[0].ChosenPool)
	}

	// Now wipe the SSD pool; planner must still return the cached plan,
	// not silently move the dataset to tank.
	second := NewDatasetPlanner(st.DB(), &StaticPoolLister{Pools: []PoolInfo{{Name: "tank", Role: PoolHintTank}}})
	plans2, err := second.Plan(context.Background(), "immich-prod", m)
	if err != nil {
		t.Fatalf("second plan: %v", err)
	}
	if plans2[0].ChosenPool != "fast" {
		t.Errorf("cached ChosenPool = %q, want fast (planner must not auto-migrate)", plans2[0].ChosenPool)
	}
	if plans2[0].DatasetPath != "fast/apps/immich/db" {
		t.Errorf("cached DatasetPath = %q", plans2[0].DatasetPath)
	}
}
