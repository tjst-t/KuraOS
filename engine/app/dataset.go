package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// PoolLister is the small surface engine/app needs from engine/storage to
// pick a target pool. We do not import engine/storage directly to keep the
// dependency graph one-way (storage does not depend on app, and app only
// depends on storage via this interface — DESIGN_PRINCIPLES priority #9).
//
// engine/storage.Engine.ListPools returns []storage.Pool; cmd/kura wires
// an adapter that flattens those into []PoolInfo.
type PoolLister interface {
	ListPools(ctx context.Context) ([]PoolInfo, error)
}

// PoolInfo is the planner's view of a pool. Role is normalized so the
// planner's matching logic does not need to know about pool name
// conventions ('fast', 'ssd', 'tank', etc.) — the lister adapter
// classifies pools and stamps the role.
type PoolInfo struct {
	Name string
	Role PoolHint
}

// DatasetPlan is the planner output for one app + one manifest dataset.
// dataset_path follows design.md §7.7: <pool>/apps/<app>/<dataset>.
type DatasetPlan struct {
	AppID       string   `json:"app_id"`
	DatasetName string   `json:"dataset_name"`
	PoolHint    PoolHint `json:"pool_hint"`
	ChosenPool  string   `json:"chosen_pool"`
	DatasetPath string   `json:"dataset_path"`
	Quota       string   `json:"quota,omitempty"`
	Backup      bool     `json:"backup"`
}

// DatasetPlanner produces a stable mapping from (app, manifest dataset) to
// (pool, dataset path). Re-running with the same inputs returns the same
// plan; SQLite-backed cache survives restarts.
type DatasetPlanner struct {
	DB    *sql.DB
	Pools PoolLister
	// FallbackPool is used when the lister returns no pools at all.
	// Production sets this to "tank"; tests can override.
	FallbackPool string
}

// NewDatasetPlanner returns a planner ready for use.
func NewDatasetPlanner(db *sql.DB, pools PoolLister) *DatasetPlanner {
	return &DatasetPlanner{DB: db, Pools: pools, FallbackPool: "tank"}
}

// Plan picks a pool for every dataset declared in the manifest, persists
// the plan in app_dataset_plan, and returns the result list. Calling
// Plan twice with the same (appID, manifest) returns identical paths.
//
// pool_hint resolution order:
//  1. Existing app_dataset_plan row for (appID, dataset_name) — if any,
//     use it verbatim. Migrations are explicit operator actions, never
//     a side effect of re-running Plan.
//  2. PoolLister returns a pool with role == hint → use it.
//  3. PoolLister returns any pool → use the lexicographically first one
//     so the choice is deterministic. (Most production setups have one
//     pool 'tank' and Plan picks it.)
//  4. PoolLister returns nothing → use FallbackPool ('tank').
func (p *DatasetPlanner) Plan(ctx context.Context, appID string, m *Manifest) ([]DatasetPlan, error) {
	if appID == "" {
		return nil, errors.New("app: appID is required")
	}
	if m == nil {
		return nil, ErrManifestEmpty
	}
	pools, err := p.listPools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DatasetPlan, 0, len(m.Storage.Datasets))
	for _, ds := range m.Storage.Datasets {
		existing, ok, err := p.lookup(ctx, appID, ds.Name)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, existing)
			continue
		}
		chosen := pickPool(pools, ds.PoolHint, p.FallbackPool)
		plan := DatasetPlan{
			AppID:       appID,
			DatasetName: ds.Name,
			PoolHint:    ds.PoolHint,
			ChosenPool:  chosen,
			DatasetPath: fmt.Sprintf("%s/apps/%s/%s", chosen, m.Name, ds.Name),
			Quota:       ds.Quota,
			Backup:      ds.Backup,
		}
		if err := p.persist(ctx, plan); err != nil {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, nil
}

// pickPool implements the resolution order documented on Plan.
func pickPool(pools []PoolInfo, hint PoolHint, fallback string) string {
	if len(pools) == 0 {
		return fallback
	}
	if hint != "" {
		for _, p := range pools {
			if p.Role == hint {
				return p.Name
			}
		}
	}
	// hint missing or unsatisfiable → first pool by name.
	sorted := make([]PoolInfo, len(pools))
	copy(sorted, pools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return sorted[0].Name
}

func (p *DatasetPlanner) listPools(ctx context.Context) ([]PoolInfo, error) {
	if p.Pools == nil {
		return nil, nil
	}
	pools, err := p.Pools.ListPools(ctx)
	if err != nil {
		return nil, fmt.Errorf("app: list pools: %w", err)
	}
	return pools, nil
}

func (p *DatasetPlanner) lookup(ctx context.Context, appID, datasetName string) (DatasetPlan, bool, error) {
	if p.DB == nil {
		return DatasetPlan{}, false, nil
	}
	row := p.DB.QueryRowContext(ctx, `
		SELECT app_id, dataset_name, pool_hint, chosen_pool, dataset_path,
		       COALESCE(quota, ''), backup
		FROM app_dataset_plan WHERE app_id = ? AND dataset_name = ?
	`, appID, datasetName)
	var d DatasetPlan
	var hint string
	var backup int
	if err := row.Scan(&d.AppID, &d.DatasetName, &hint, &d.ChosenPool, &d.DatasetPath, &d.Quota, &backup); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DatasetPlan{}, false, nil
		}
		return DatasetPlan{}, false, fmt.Errorf("app: lookup dataset plan: %w", err)
	}
	d.PoolHint = PoolHint(hint)
	d.Backup = backup != 0
	return d, true, nil
}

func (p *DatasetPlanner) persist(ctx context.Context, d DatasetPlan) error {
	if p.DB == nil {
		return nil
	}
	backup := 0
	if d.Backup {
		backup = 1
	}
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO app_dataset_plan
		    (app_id, dataset_name, pool_hint, chosen_pool, dataset_path, quota, backup)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, d.AppID, d.DatasetName, string(d.PoolHint), d.ChosenPool, d.DatasetPath,
		nullableString(d.Quota), backup)
	if err != nil {
		return fmt.Errorf("app: persist dataset plan: %w", err)
	}
	return nil
}

// nullableString returns nil for empty strings so the SQLite column stores
// NULL rather than ” — keeps the difference between 'no quota' and 'quota
// is the empty string' explicit when the row is read back.
func nullableString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// StaticPoolLister is the test-friendly PoolLister: returns the configured
// list verbatim. Production wiring uses an engine/storage adapter (added
// in S65b510 once dataset materialisation is wired).
type StaticPoolLister struct {
	Pools []PoolInfo
}

// ListPools implements PoolLister.
func (s *StaticPoolLister) ListPools(_ context.Context) ([]PoolInfo, error) {
	return s.Pools, nil
}
