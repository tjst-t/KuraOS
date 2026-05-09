// app_wiring.go: helpers used by main.go to wire the engine/app stack into
// the running daemon. These pieces are split out so app.go (the CLI command
// dispatch) and main.go (the daemon bootstrap) read top-to-bottom without
// each tripping over the other's helpers.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/kuraos-org/kura/engine/app"
	"github.com/kuraos-org/kura/engine/share"
	"github.com/kuraos-org/kura/engine/storage"
	"github.com/kuraos-org/kura/internal/ui"
)

// buildDockerClient picks the production HTTPDockerClient unless the env var
// KURA_DOCKER_FAKE=1 is set, in which case a process-local Fake is used (for
// dev boxes / unit tests / the autopilot E2E spec that stubs Docker).
func buildDockerClient(_ context.Context) app.DockerClient {
	if os.Getenv("KURA_DOCKER_FAKE") == "1" {
		// Single shared Fake so successive Install/Uninstall calls see
		// each other's containers. Goroutine-safe by construction (the
		// FakeDockerClient holds its own mutex).
		return sharedFakeDocker()
	}
	host := os.Getenv("DOCKER_HOST")
	return app.NewHTTPDockerClient(host)
}

var (
	fakeDockerOnce sync.Once
	fakeDockerInst *app.FakeDockerClient
)

func sharedFakeDocker() *app.FakeDockerClient {
	fakeDockerOnce.Do(func() {
		fakeDockerInst = app.NewFakeDockerClient()
	})
	return fakeDockerInst
}

// FakeDockerForTests exposes the shared Fake so e2e harnesses can pre-load
// healthy/unhealthy registries before the install kicks off. Returns nil
// when Docker fakery is disabled.
func FakeDockerForTests() *app.FakeDockerClient {
	if os.Getenv("KURA_DOCKER_FAKE") != "1" {
		return nil
	}
	return sharedFakeDocker()
}

// storageDatasetAdapter bridges engine/storage.CLI (which exposes
// CreateVolume / SetQuota / Snapshot / Rollback) to app.StorageDatasetEngine
// (the consumer-side interface). Methods translate option types between the
// two APIs.
type storageDatasetAdapter struct {
	engine *storage.CLI
}

func (a storageDatasetAdapter) CreateVolume(ctx context.Context, dataset string, opts app.StorageVolumeOpts) error {
	return a.engine.CreateVolume(ctx, dataset, storage.VolumeOpts{
		QuotaBytes: opts.QuotaBytes,
		MountPoint: opts.Mountpoint,
	})
}

func (a storageDatasetAdapter) SetQuota(ctx context.Context, dataset string, quotaBytes int64) error {
	return a.engine.SetQuota(ctx, dataset, quotaBytes)
}

func (a storageDatasetAdapter) CreateSnapshot(ctx context.Context, dataset, name string) error {
	return a.engine.CreateSnapshot(ctx, dataset, name)
}

func (a storageDatasetAdapter) Rollback(ctx context.Context, dataset, snap string) error {
	return a.engine.Rollback(ctx, dataset, snap)
}

func (a storageDatasetAdapter) DestroyVolume(_ context.Context, _ string, _ app.StorageDestroyOpts) error {
	// engine/storage deliberately omits Destroy* methods from its public
	// surface (DESIGN_PRINCIPLES priority #5: destructive ops require an
	// explicit confirm UI). For app uninstall+deleteData we tolerate the
	// absence and log; the operator can clean up manually.
	return fmt.Errorf("destroy app dataset: storage engine destroy not exposed in v1; remove via 'zfs destroy' manually")
}

// discoverPools returns the [pool name -> role] list for the planner. v1
// classifies pools by name convention: 'fast', 'ssd', 'nvme' -> ssd; rest -> tank.
func discoverPools(ctx context.Context, eng *storage.CLI) []app.PoolInfo {
	if eng == nil {
		return nil
	}
	pools, err := eng.ListPools(ctx)
	if err != nil {
		return nil
	}
	out := make([]app.PoolInfo, 0, len(pools))
	for _, p := range pools {
		role := app.PoolHintTank
		switch strings.ToLower(p.Name) {
		case "fast", "ssd", "nvme", "flash":
			role = app.PoolHintSSD
		}
		out = append(out, app.PoolInfo{Name: p.Name, Role: role})
	}
	return out
}

// loadAppSources reads the operator-trusted registries from the kura_kv table
// (key='apps.registries', value=JSON). cmd/kura writes through here when the
// operator runs `kura app registry add`. config.json is the SSOT but the
// runtime cache lets the UI render without re-loading the config file.
func loadAppSources(ctx context.Context, db *sql.DB) []app.RegistrySource {
	var raw string
	row := db.QueryRowContext(ctx, `SELECT value FROM kura_kv WHERE key = ?`, "apps.registries")
	if err := row.Scan(&raw); err != nil {
		return nil
	}
	var configs []app.AppRegistryConfig
	if err := json.Unmarshal([]byte(raw), &configs); err != nil {
		return nil
	}
	out, err := app.LoadRegistrySources(configs)
	if err != nil {
		return nil
	}
	return out
}

// bootstrapAppRegistries replays the persisted apps.installed list against
// the route registry so a kura restart recovers the gateway routes and the
// SQLite-backed app_installs rows agree with config.json's intended state.
//
// The full Reconcile loop (start/stop containers to match the desired state)
// is left to a future Sprint; v1 just rebuilds the in-memory route table.
func bootstrapAppRegistries(ctx context.Context, db *sql.DB, lc *app.AppLifecycle) error {
	recs, err := lc.ListApps(ctx)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		// Look up port reservations to reconstruct the route. We can't
		// call the manifest fetch here (would block on network) — instead
		// derive a generic /apps/<name>/ path-mode route. The operator can
		// re-install the app to re-fetch manifest if mode changed.
		if lc.Routes == nil {
			continue
		}
		ports, err := lc.Ports.LookupReservations(ctx, rec.AppID)
		if err != nil || len(ports) == 0 {
			continue
		}
		host := ports[0].HostPort
		_ = lc.Routes.RegisterAppRoute(app.AppRoute{
			AppID:       rec.AppID,
			AppName:     rec.Name,
			Mode:        app.RoutingModePath,
			HostPort:    host,
			StripPrefix: true,
			Container:   ports[0].Container,
		})
	}
	return nil
}

// shareLister is the AppsSharePathLister implementation backed by share.Manager.
type shareLister struct {
	m *share.Manager
}

func sharePathListerFromEngine(m *share.Manager) ui.AppsSharePathLister {
	return shareLister{m: m}
}

func (s shareLister) ListSharePaths(ctx context.Context) ([]ui.AppsShareOption, error) {
	if s.m == nil {
		return nil, nil
	}
	list, err := s.m.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ui.AppsShareOption, 0, len(list))
	for _, sh := range list {
		out = append(out, ui.AppsShareOption{Name: sh.Name, Path: sh.Path})
	}
	return out, nil
}

// kura_kv is a generic key/value table the daemon uses for small persisted
// settings (registry list, port-range overrides, ...). We create it lazily
// on first read; the table is also defined in the migration 0008_app_kv.sql
// so a fresh install picks it up immediately.
var _ = http.StatusOK
