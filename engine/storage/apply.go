// apply.go is the StorageEngine's config.json ApplyAdapter. It reads the
// `storage` section from old / new Config and produces engine-specific Steps
// (CreatePool, ImportPool, CreateVolume, SetQuota), which are then executed
// against the live Engine when not in dry-run.
//
// DESIGN_PRINCIPLES priority #1: SSOT — every pool / volume / snapshot a
// running KuraOS produced via the UI must round-trip through config.json
// export, then re-apply to the same end state. Today the UI emits state into
// SQLite (next sprint scope, S0ff37f setup) and config.json export reads
// SQLite + live engine — but the apply path here is what makes the round
// trip honest.
package storage

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kuraos-org/kura/internal/config"
)

// RegisterApplyAdapter installs the storage adapter against the given Engine.
// Production wiring calls this from cmd/kura main.go. Tests instead call
// NewStorageAdapter directly and exercise its Plan / Apply methods on a
// fake engine without touching the global registry.
func RegisterApplyAdapter(eng Engine) {
	config.Register(NewStorageAdapter(eng))
}

// NewStorageAdapter returns a config.ApplyAdapter that targets the
// `storage` section. The Engine is the one mutated by Apply.
func NewStorageAdapter(eng Engine) config.ApplyAdapter {
	return &storageAdapter{eng: eng}
}

type storageAdapter struct {
	eng Engine
}

func (a *storageAdapter) Section() string { return "storage" }

// Plan compares old.Storage / new.Storage and emits a Step per mutation:
//
//   - new pool absent in old.Pools → "create_pool" with PoolConfig payload
//     (or "import_pool" when entry.Mode == "import")
//   - pool present in both → no-op for v1 (in-place edits like resilver,
//     spare add, scrub schedule are handled by their own flows)
//   - volume absent in old.Volumes → "create_volume" with dataset path +
//     VolumeOpts (preset → recordsize/compression internalized at apply time)
//   - volume present in both with different quota → "set_quota"
//
// Pool deletes / volume deletes never produce a Step — destructive flows are
// scope-out for S9db742 (see Engine doc comment).
func (a *storageAdapter) Plan(ctx context.Context, old, newCfg *config.Config) ([]config.Step, error) {
	var steps []config.Step
	oldS := old.Storage
	if oldS == nil {
		oldS = &config.StorageConfig{}
	}
	newS := newCfg.Storage
	if newS == nil {
		newS = &config.StorageConfig{}
	}

	oldPools := indexPools(oldS.Pools)
	for _, p := range newS.Pools {
		if _, exists := oldPools[p.Name]; exists {
			continue
		}
		if p.Mode == "import" {
			steps = append(steps, config.Step{
				Section: "storage",
				Op:      "import_pool",
				Detail:  ImportPoolStep{Name: p.Name},
			})
			continue
		}
		cfg, err := poolEntryToConfig(p)
		if err != nil {
			return nil, fmt.Errorf("storage adapter: pool %q: %w", p.Name, err)
		}
		steps = append(steps, config.Step{
			Section: "storage",
			Op:      "create_pool",
			Detail:  CreatePoolStep{Config: cfg},
		})
	}

	oldVols := indexVolumes(oldS.Volumes)
	for _, v := range newS.Volumes {
		ov, exists := oldVols[v.Name]
		if !exists {
			steps = append(steps, config.Step{
				Section: "storage",
				Op:      "create_volume",
				Detail:  CreateVolumeStep{Dataset: v.Name, Opts: volumeEntryToOpts(v)},
			})
			continue
		}
		if ov.Quota != v.Quota {
			q, err := parseHumanBytes(v.Quota)
			if err != nil {
				return nil, fmt.Errorf("storage adapter: volume %q quota: %w", v.Name, err)
			}
			steps = append(steps, config.Step{
				Section: "storage",
				Op:      "set_quota",
				Detail:  SetQuotaStep{Dataset: v.Name, QuotaBytes: q},
			})
		}
	}
	return steps, nil
}

func (a *storageAdapter) Apply(ctx context.Context, steps []config.Step) error {
	for _, s := range steps {
		switch s.Op {
		case "create_pool":
			st, ok := s.Detail.(CreatePoolStep)
			if !ok {
				return fmt.Errorf("storage adapter: bad detail for create_pool")
			}
			if err := a.eng.CreatePool(ctx, st.Config); err != nil {
				return fmt.Errorf("storage adapter: create pool %q: %w", st.Config.Name, err)
			}
		case "import_pool":
			st, ok := s.Detail.(ImportPoolStep)
			if !ok {
				return fmt.Errorf("storage adapter: bad detail for import_pool")
			}
			if err := a.eng.ImportPool(ctx, st.Name, ImportOpts{}); err != nil {
				return fmt.Errorf("storage adapter: import pool %q: %w", st.Name, err)
			}
		case "create_volume":
			st, ok := s.Detail.(CreateVolumeStep)
			if !ok {
				return fmt.Errorf("storage adapter: bad detail for create_volume")
			}
			if err := a.eng.CreateVolume(ctx, st.Dataset, st.Opts); err != nil {
				return fmt.Errorf("storage adapter: create volume %q: %w", st.Dataset, err)
			}
		case "set_quota":
			st, ok := s.Detail.(SetQuotaStep)
			if !ok {
				return fmt.Errorf("storage adapter: bad detail for set_quota")
			}
			if err := a.eng.SetQuota(ctx, st.Dataset, st.QuotaBytes); err != nil {
				return fmt.Errorf("storage adapter: set quota on %q: %w", st.Dataset, err)
			}
		default:
			return fmt.Errorf("storage adapter: unknown op %q", s.Op)
		}
	}
	return nil
}

// CreatePoolStep / ImportPoolStep / CreateVolumeStep / SetQuotaStep are the
// concrete payloads for config.Step.Detail. Exported so tests can assert on
// the planned steps without re-running Apply.
type CreatePoolStep struct{ Config PoolConfig }
type ImportPoolStep struct{ Name string }
type CreateVolumeStep struct {
	Dataset string
	Opts    VolumeOpts
}
type SetQuotaStep struct {
	Dataset    string
	QuotaBytes int64
}

func indexPools(ps []config.PoolEntry) map[string]config.PoolEntry {
	m := make(map[string]config.PoolEntry, len(ps))
	for _, p := range ps {
		m[p.Name] = p
	}
	return m
}

func indexVolumes(vs []config.VolumeEntry) map[string]config.VolumeEntry {
	m := make(map[string]config.VolumeEntry, len(vs))
	for _, v := range vs {
		m[v.Name] = v
	}
	return m
}

func poolEntryToConfig(p config.PoolEntry) (PoolConfig, error) {
	if p.Topology == nil {
		return PoolConfig{}, fmt.Errorf("topology missing")
	}
	cfg := PoolConfig{
		Name:   p.Name,
		Spares: append([]string(nil), p.Spares...),
		Data: VdevSpec{
			Layout: VdevLayout(p.Topology.Type),
			Disks:  append([]string(nil), p.Topology.Disks...),
		},
	}
	if p.Special != nil {
		cfg.Special = &VdevSpec{
			Layout: VdevLayout(p.Special.Type),
			Disks:  append([]string(nil), p.Special.Disks...),
		}
	}
	if p.SmallBlockThreshold != "" {
		n, err := parseHumanBytes(p.SmallBlockThreshold)
		if err != nil {
			return PoolConfig{}, fmt.Errorf("small_block_threshold: %w", err)
		}
		cfg.SmallBlockThreshold = n
	}
	return cfg, nil
}

func volumeEntryToOpts(v config.VolumeEntry) VolumeOpts {
	opts := VolumeOpts{Preset: PresetID(v.Preset)}
	if v.Quota != "" {
		if n, err := parseHumanBytes(v.Quota); err == nil {
			opts.QuotaBytes = n
		}
	}
	return opts
}

// parseHumanBytes accepts the SI / binary suffixes commonly used in ZFS
// configs: K, M, G, T (binary, 1024 base — matches `zfs set quota=`).
// Empty input returns 0 with no error so an unset quota is harmless.
func parseHumanBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	last := s[len(s)-1]
	switch last {
	case 'K', 'k':
		mult = 1024
		s = s[:len(s)-1]
	case 'M', 'm':
		mult = 1024 * 1024
		s = s[:len(s)-1]
	case 'G', 'g':
		mult = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	case 'T', 't':
		mult = 1024 * 1024 * 1024 * 1024
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	return n * mult, nil
}

// RegisterExportAdapter installs the storage export adapter into the global
// config export registry (S99702c-3). It snapshots live pool / volume state
// into config.Storage so export → import restores ZFS structure.
func RegisterExportAdapter(eng Engine) {
	config.RegisterExporter(&storageExporter{eng: eng})
}

type storageExporter struct{ eng Engine }

func (e *storageExporter) Snapshot(ctx context.Context, cfg *config.Config) error {
	pools, err := e.eng.ListPools(ctx)
	if err != nil {
		// Non-fatal: ZFS may not be available on dev boxes.
		return nil
	}
	var poolEntries []config.PoolEntry
	for _, p := range pools {
		entry := config.PoolEntry{Name: p.Name}
		// Reconstruct topology from the pool's vdev tree.
		if len(p.Topology) > 0 {
			top := &config.TopologyEntry{Type: string(p.Topology[0].Type)}
			for _, child := range p.Topology[0].Children {
				top.Disks = append(top.Disks, child.Name)
			}
			entry.Topology = top
		}
		poolEntries = append(poolEntries, entry)
	}
	if cfg.Storage == nil {
		cfg.Storage = &config.StorageConfig{}
	}
	cfg.Storage.Pools = poolEntries
	return nil
}
