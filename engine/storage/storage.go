package storage

import (
	"context"
	"fmt"

	"github.com/kuraos-org/kura/internal/cmdexec"
)

// Engine is the read-only surface the Sprint Se3b190 UI consumes. Write
// operations (CreatePool, ImportPool, AddSpare, ...) are intentionally absent
// — they belong to the next sprint, where validation rules from
// DESIGN_PRINCIPLES priority #5 (信頼性 > 機能) get their own thorough test
// table. Splitting read from write keeps this sprint's surface tight.
type Engine interface {
	ListPools(ctx context.Context) ([]Pool, error)
	ListDisks(ctx context.Context) ([]Disk, error)
	ListImportable(ctx context.Context) ([]ImportablePool, error)
}

// CLI is the production Engine: it shells out to zpool / zfs / lsblk /
// smartctl through an Executor. Constructed with NewCLI; tests use a fake
// Executor or a stub Engine.
//
// The Executor is the only seam — there's no direct exec.Command anywhere in
// engine/storage so tests stay deterministic across CI environments that may
// or may not have ZFS installed (CLAUDE.md test_environment note).
type CLI struct {
	exec cmdexec.Executor
}

// NewCLI returns a production-ready Engine. The caller passes the Executor
// (cmdexec.NewReal() in production) so the same constructor serves tests.
func NewCLI(exec cmdexec.Executor) *CLI {
	return &CLI{exec: exec}
}

// ListPools runs zpool list / status / zfs list and merges the results into a
// []Pool. Any one CLI failing returns the error wrapped with %w so callers
// can inspect via errors.As / errors.Is.
func (c *CLI) ListPools(ctx context.Context) ([]Pool, error) {
	listOut, _, err := c.exec.Run(ctx, "zpool", "list", "-H", "-p", "-o", "name,size,allocated,free,frag,cap,dedup,health,guid")
	if err != nil {
		return nil, fmt.Errorf("storage: zpool list: %w", err)
	}
	pools, err := parseZpoolList(listOut)
	if err != nil {
		return nil, fmt.Errorf("storage: parse zpool list: %w", err)
	}
	for i := range pools {
		statusOut, _, sErr := c.exec.Run(ctx, "zpool", "status", "-P", "-L", pools[i].Name)
		if sErr != nil {
			// A single pool failing status shouldn't black-hole the rest.
			// Mark it suspended and continue so the operator at least sees
			// the row.
			pools[i].Health = HealthSuspended
			continue
		}
		topo, _, pErr := parseZpoolStatus(statusOut)
		if pErr != nil {
			pools[i].Health = HealthSuspended
			continue
		}
		pools[i].Topology = topo
	}
	return pools, nil
}

// ListImportable runs `zpool import` (no pool name = scan) and parses the
// per-pool blocks. Empty output (no importable pools) returns nil, nil — the
// CLI exits non-zero with that message on some distros so we treat the
// "no pools available for import" stderr text as success.
func (c *CLI) ListImportable(ctx context.Context) ([]ImportablePool, error) {
	stdout, stderr, err := c.exec.Run(ctx, "zpool", "import")
	if err != nil {
		// zpool import returns rc=1 on "no pools available for import".
		// That is the empty-set case, not an error.
		if isZpoolImportEmpty(stderr) || isZpoolImportEmpty(stdout) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: zpool import (scan): %w", err)
	}
	if isZpoolImportEmpty(stdout) {
		return nil, nil
	}
	out, err := parseZpoolImportScan(stdout)
	if err != nil {
		return nil, fmt.Errorf("storage: parse zpool import: %w", err)
	}
	return out, nil
}
