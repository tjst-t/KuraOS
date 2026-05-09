package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/system"
	_ "modernc.org/sqlite"
)

// fakeSystemEngine satisfies the SystemEngine surface SecretStore needs.
type fakeSystemEngine struct {
	store map[string]string
}

func (f *fakeSystemEngine) SetCredential(_ context.Context, c system.Credential) error {
	if f.store == nil {
		f.store = map[string]string{}
	}
	f.store[string(c.Kind)+"/"+string(c.OwnerKind)+"/"+c.OwnerID] = c.Value
	return nil
}

func (f *fakeSystemEngine) LookupCredential(_ context.Context, k system.CredentialKind, o system.CredentialOwnerKind, id string) (system.Credential, error) {
	v, ok := f.store[string(k)+"/"+string(o)+"/"+id]
	if !ok {
		return system.Credential{}, system.ErrUserNotFound
	}
	return system.Credential{Kind: k, OwnerKind: o, OwnerID: id, Value: v}, nil
}

// newTestDB opens an in-memory SQLite and applies the migrations app/lifecycle
// depends on (port reservations, dataset plan, app installs, kv).
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/state.db?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations := []string{
		`CREATE TABLE app_port_reservations (
			app_id TEXT NOT NULL, container TEXT NOT NULL, manifest_port INTEGER NOT NULL,
			host_port INTEGER NOT NULL, allocated_at TEXT,
			PRIMARY KEY (app_id, container, manifest_port));
		 CREATE UNIQUE INDEX app_port_reservations_host_port_uidx ON app_port_reservations (host_port);`,
		`CREATE TABLE app_dataset_plan (
			app_id TEXT NOT NULL, dataset_name TEXT NOT NULL, pool_hint TEXT NOT NULL,
			chosen_pool TEXT NOT NULL, dataset_path TEXT NOT NULL, quota TEXT,
			backup INTEGER NOT NULL DEFAULT 0, planned_at TEXT,
			PRIMARY KEY (app_id, dataset_name));`,
		`CREATE TABLE app_installs (
			app_id TEXT PRIMARY KEY, name TEXT NOT NULL, version TEXT NOT NULL,
			registry TEXT NOT NULL, state TEXT NOT NULL,
			setup_json TEXT NOT NULL DEFAULT '{}', settings_json TEXT NOT NULL DEFAULT '{}',
			installed_at TEXT, updated_at TEXT);`,
		`CREATE TABLE kura_kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return db
}

// startTestRegistry spins up an httptest.Server that serves a manifest +
// signed bundle pair. Returns the source plus the manifest bytes for
// assertions.
func startTestRegistry(t *testing.T, manifestYAML []byte) (RegistrySource, *httptest.Server) {
	t.Helper()
	verifier := NewFakeVerifier()
	verifier.Allow(manifestYAML, "kuraos-test:installer", "kuraos-test")

	digest := sha256.Sum256(manifestYAML)
	registry := Registry{
		SchemaVersion: "v1",
		UpdatedAt:     time.Now(),
		Apps: map[string]RegistryApp{
			"immich": {
				Latest: "1.111.0",
				Versions: map[string]RegistryVersion{
					"1.111.0": {ManifestSHA256: hex.EncodeToString(digest[:])},
				},
			},
		},
	}
	regJSON, _ := json.Marshal(registry)
	registryDigest := sha256.Sum256(regJSON)
	verifier.Allow(regJSON, "kuraos-test:installer", "kuraos-test")
	_ = registryDigest

	mux := http.NewServeMux()
	mux.HandleFunc("/registry.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(regJSON)
	})
	mux.HandleFunc("/registry.json.sig", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("dummy-sig"))
	})
	mux.HandleFunc("/apps/immich/1.111.0/manifest.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(manifestYAML)
	})
	mux.HandleFunc("/apps/immich/1.111.0/manifest.yaml.sig", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("dummy-sig"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	src := RegistrySource{
		Name:          "test-registry",
		URL:           srv.URL,
		IdentityRegex: "kuraos-test:.*",
		Issuer:        "kuraos-test",
	}
	// Re-use the FakeVerifier through HTTPRegistryClient.
	return src, srv
}

// loadFixture reads engine/app/testdata/<name>.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// TestLifecycleInstallEndToEnd covers AC-S65b510-1-1 in an in-process world:
// fake docker, fake storage, fake verifier, real lifecycle. Verifies that
// the install pipeline pulls images, reserves ports, ensures secrets, starts
// containers, waits for healthy, registers a route, and persists the record.
func TestLifecycleInstallEndToEnd(t *testing.T) {
	manifest := loadFixture(t, "immich.yaml")
	src, _ := startTestRegistry(t, manifest)
	verifier := NewFakeVerifier()
	verifier.Allow(manifest, "kuraos-test:installer", "kuraos-test")

	db := newTestDB(t)
	planner := NewDatasetPlanner(db, &StaticPoolLister{Pools: []PoolInfo{
		{Name: "tank", Role: PoolHintTank},
		{Name: "fast", Role: PoolHintSSD},
	}})
	ports := NewPortAllocator(db)
	sys := &fakeSystemEngine{}
	secrets := NewSecretStore(sys)
	docker := NewFakeDockerClient()
	storage := NewFakeStorageWriter(t.TempDir())
	routes := NewMemoryRouteRegistry()

	regCli := NewHTTPRegistryClient(verifier, nil)
	// Re-fetch the registry JSON from the server so the verifier records both
	// payloads. The server we started uses its own FakeVerifier instance, but
	// HTTPRegistryClient calls the verifier we just constructed, so we must
	// pre-register both payloads with our verifier:
	regBody, regErr := http.Get(src.URL + "/registry.json")
	if regErr != nil {
		t.Fatalf("get registry: %v", regErr)
	}
	defer regBody.Body.Close()
	registryJSON := readAll(t, regBody.Body)
	verifier.Allow(registryJSON, "kuraos-test:installer", "kuraos-test")

	lc := NewLifecycle(regCli, planner, ports, secrets, docker, storage, routes, db)
	lc.HealthTimeout = 5 * time.Second
	lc.HealthPoll = 10 * time.Millisecond
	lc.ConfigRoot = t.TempDir()

	ev := []ProgressEvent{}
	ch, cancel := lc.Subscribe("immich.aaaaaa")
	defer cancel()
	go func() {
		for e := range ch {
			ev = append(ev, e)
		}
	}()

	req := InstallRequest{
		Source:  src,
		AppName: "immich", Version: "1.111.0",
		AppID: "immich.aaaaaa",
		SetupValues: map[string]string{
			"photo_share": "/tank/photos",
		},
	}
	appID, err := lc.Install(context.Background(), req)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if appID != "immich.aaaaaa" {
		t.Fatalf("appID = %q; want immich.aaaaaa", appID)
	}

	// Container started?
	if len(docker.Containers) == 0 {
		t.Fatalf("no containers started")
	}
	// Route registered?
	if _, ok := routes.LookupByName("immich"); !ok {
		t.Fatalf("route not registered")
	}
	// Persisted?
	rec, err := lc.LookupApp(context.Background(), appID)
	if err != nil || rec.AppID == "" {
		t.Fatalf("LookupApp: %v rec=%+v", err, rec)
	}
	if rec.State != "running" {
		t.Fatalf("state = %s; want running", rec.State)
	}
	// Secret stored in vault, NOT in settings?
	for _, s := range rec.Settings {
		if strings.Contains(strings.ToLower(s), "password") {
			t.Fatalf("settings leaked secret: %v", rec.Settings)
		}
	}
	if sys.store["app_db_password/app/immich.aaaaaa:db_password"] == "" {
		t.Fatalf("secret not stored in vault: %#v", sys.store)
	}
}

// TestLifecycleInstallRollback covers AC-S65b510-1-2: when healthcheck fails,
// the install rolls back containers, ports, datasets, and the route.
func TestLifecycleInstallRollback(t *testing.T) {
	manifest := loadFixture(t, "immich.yaml")
	src, _ := startTestRegistry(t, manifest)
	verifier := NewFakeVerifier()
	verifier.Allow(manifest, "kuraos-test:installer", "kuraos-test")
	regBody, regErr := http.Get(src.URL + "/registry.json")
	if regErr != nil {
		t.Fatalf("get registry: %v", regErr)
	}
	defer regBody.Body.Close()
	registryJSON := readAll(t, regBody.Body)
	verifier.Allow(registryJSON, "kuraos-test:installer", "kuraos-test")

	db := newTestDB(t)
	planner := NewDatasetPlanner(db, &StaticPoolLister{Pools: []PoolInfo{{Name: "tank", Role: PoolHintTank}}})
	ports := NewPortAllocator(db)
	sys := &fakeSystemEngine{}
	secrets := NewSecretStore(sys)
	docker := NewFakeDockerClient()
	docker.FailHealthNames = map[string]bool{"kura-immich_aaaaaa-server": true}
	storage := NewFakeStorageWriter(t.TempDir())
	routes := NewMemoryRouteRegistry()

	lc := NewLifecycle(NewHTTPRegistryClient(verifier, nil), planner, ports, secrets, docker, storage, routes, db)
	lc.HealthTimeout = 200 * time.Millisecond
	lc.HealthPoll = 10 * time.Millisecond
	lc.ConfigRoot = t.TempDir()

	_, err := lc.Install(context.Background(), InstallRequest{
		Source: src, AppName: "immich", Version: "1.111.0",
		AppID:       "immich.aaaaaa",
		SetupValues: map[string]string{"photo_share": "/tank/photos"},
	})
	if err == nil {
		t.Fatalf("expected install to fail on healthcheck")
	}
	if !errors.Is(err, ErrHealthcheckTimeout) && !strings.Contains(err.Error(), "healthcheck") && !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("wrong error: %v", err)
	}
	// Containers removed?
	if len(docker.Containers) != 0 {
		t.Fatalf("containers not removed: %v", docker.Containers)
	}
	// Route NOT registered?
	if _, ok := routes.LookupByName("immich"); ok {
		t.Fatalf("route should not be registered after rollback")
	}
	// Port reservations gone?
	row := db.QueryRow(`SELECT count(*) FROM app_port_reservations WHERE app_id = ?`, "immich.aaaaaa")
	var n int
	if err := row.Scan(&n); err != nil || n != 0 {
		t.Fatalf("port reservations not freed: n=%d err=%v", n, err)
	}
	// app_installs row not committed?
	rec, _ := lc.LookupApp(context.Background(), "immich.aaaaaa")
	if rec.AppID != "" {
		t.Fatalf("app record should not be persisted on rollback")
	}
}

// TestLifecycleUninstallKeepsData covers AC-S65b510-3-2: container removed,
// dataset retained.
func TestLifecycleUninstallKeepsData(t *testing.T) {
	manifest := loadFixture(t, "immich.yaml")
	src, _ := startTestRegistry(t, manifest)
	verifier := NewFakeVerifier()
	verifier.Allow(manifest, "kuraos-test:installer", "kuraos-test")
	regBody, regErr := http.Get(src.URL + "/registry.json")
	if regErr != nil {
		t.Fatalf("get registry: %v", regErr)
	}
	defer regBody.Body.Close()
	registryJSON := readAll(t, regBody.Body)
	verifier.Allow(registryJSON, "kuraos-test:installer", "kuraos-test")

	db := newTestDB(t)
	planner := NewDatasetPlanner(db, &StaticPoolLister{Pools: []PoolInfo{{Name: "tank", Role: PoolHintTank}}})
	ports := NewPortAllocator(db)
	sys := &fakeSystemEngine{}
	secrets := NewSecretStore(sys)
	docker := NewFakeDockerClient()
	storage := NewFakeStorageWriter(t.TempDir())
	routes := NewMemoryRouteRegistry()

	lc := NewLifecycle(NewHTTPRegistryClient(verifier, nil), planner, ports, secrets, docker, storage, routes, db)
	lc.HealthTimeout = 1 * time.Second
	lc.HealthPoll = 10 * time.Millisecond
	lc.ConfigRoot = t.TempDir()

	appID, err := lc.Install(context.Background(), InstallRequest{
		Source: src, AppName: "immich", Version: "1.111.0",
		AppID:       "immich.aaaaaa",
		SetupValues: map[string]string{"photo_share": "/tank/photos"},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := lc.Uninstall(context.Background(), UninstallRequest{AppID: appID, DeleteData: false}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if len(docker.Containers) != 0 {
		t.Fatalf("containers not removed")
	}
	if len(storage.Destroyed) != 0 {
		t.Fatalf("datasets destroyed despite DeleteData=false: %v", storage.Destroyed)
	}
	rec, _ := lc.LookupApp(context.Background(), appID)
	if rec.AppID != "" {
		t.Fatalf("app record not deleted: %+v", rec)
	}
}

// TestLifecycleUpdateSnapshotsBackupDatasets covers AC-S65b510-3-1:
// pre-update snapshot; healthcheck failure -> rollback (snapshot restored).
func TestLifecycleUpdateSnapshotsBackupDatasets(t *testing.T) {
	manifest := loadFixture(t, "immich.yaml")
	src, _ := startTestRegistry(t, manifest)
	verifier := NewFakeVerifier()
	verifier.Allow(manifest, "kuraos-test:installer", "kuraos-test")
	regBody, regErr := http.Get(src.URL + "/registry.json")
	if regErr != nil {
		t.Fatalf("get registry: %v", regErr)
	}
	defer regBody.Body.Close()
	registryJSON := readAll(t, regBody.Body)
	verifier.Allow(registryJSON, "kuraos-test:installer", "kuraos-test")

	db := newTestDB(t)
	planner := NewDatasetPlanner(db, &StaticPoolLister{Pools: []PoolInfo{{Name: "tank", Role: PoolHintTank}}})
	ports := NewPortAllocator(db)
	sys := &fakeSystemEngine{}
	secrets := NewSecretStore(sys)
	docker := NewFakeDockerClient()
	storage := NewFakeStorageWriter(t.TempDir())
	routes := NewMemoryRouteRegistry()

	lc := NewLifecycle(NewHTTPRegistryClient(verifier, nil), planner, ports, secrets, docker, storage, routes, db)
	lc.HealthTimeout = 500 * time.Millisecond
	lc.HealthPoll = 10 * time.Millisecond
	lc.ConfigRoot = t.TempDir()

	appID, err := lc.Install(context.Background(), InstallRequest{
		Source: src, AppName: "immich", Version: "1.111.0",
		AppID:       "immich.aaaaaa",
		SetupValues: map[string]string{"photo_share": "/tank/photos"},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// Now run update with healthcheck failure.
	docker.FailHealthNames = map[string]bool{"kura-immich_aaaaaa-server": true}
	err = lc.Update(context.Background(), UpdateRequest{
		AppID: appID, Source: src, NewVersion: "1.111.0",
	})
	if err == nil {
		t.Fatalf("expected update to fail on healthcheck")
	}

	// Pre-update snapshot was created on backup:true dataset (db).
	dbDataset := "tank/apps/immich/db"
	if len(storage.Snaps[dbDataset]) == 0 {
		t.Fatalf("expected snapshot on %s; snaps=%v", dbDataset, storage.Snaps)
	}
	// Rollback recorded too.
	hasRollback := false
	for _, s := range storage.Snaps[dbDataset] {
		if strings.HasPrefix(s, "rollback:") {
			hasRollback = true
		}
	}
	if !hasRollback {
		t.Fatalf("expected rollback snapshot on %s; got %v", dbDataset, storage.Snaps[dbDataset])
	}
}

// readAll is a tiny helper so the tests don't need to import io/ioutil.
func readAll(t *testing.T, r interface{ Read([]byte) (int, error) }) []byte {
	t.Helper()
	out := []byte{}
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return out
}
