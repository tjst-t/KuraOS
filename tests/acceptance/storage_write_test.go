package acceptance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/storage"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
	"github.com/kuraos-org/kura/internal/gateway"
	"github.com/kuraos-org/kura/internal/store"
	"github.com/kuraos-org/kura/internal/ui"
)

// stubStorageRW combines read + write so the gateway can wire one Engine
// for all routes. Each method records calls so tests can assert on them.
type stubStorageRW struct {
	stubStorage

	mu          sync.Mutex
	createCalls []storage.PoolConfig
	importCalls []struct {
		Name string
		Opts storage.ImportOpts
	}
	createErr error
}

func (s *stubStorageRW) CreatePool(_ context.Context, cfg storage.PoolConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return s.createErr
	}
	// Mirror the engine's contract: refuse single-SSD special vdev so the
	// acceptance test can check that the validation gate fires before any
	// state changes.
	if err := storage.ValidatePoolConfig(cfg); err != nil {
		return err
	}
	s.createCalls = append(s.createCalls, cfg)
	return nil
}
func (s *stubStorageRW) CreateVolume(context.Context, string, storage.VolumeOpts) error {
	return nil
}
func (s *stubStorageRW) CreateSnapshot(context.Context, string, string) error { return nil }
func (s *stubStorageRW) Rollback(context.Context, string, string) error       { return nil }
func (s *stubStorageRW) ImportPool(_ context.Context, name string, opts storage.ImportOpts) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.importCalls = append(s.importCalls, struct {
		Name string
		Opts storage.ImportOpts
	}{name, opts})
	return nil
}

func newServerWithStorageRW(t *testing.T, eng *stubStorageRW) (*httptest.Server, *user.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	r, err := ui.New(tr, "test")
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	deps := ui.StorageDeps{Engine: eng, Writer: eng}
	r.SetStorageHandler(r.StorageHandler(deps))
	r.SetStorageWriteHandler(r.StorageWriteHandler(deps))

	users := user.NewStore(st.DB(), fastHasher{})
	sessions := session.NewStore(st.DB())
	authH := r.AuthHandler(ui.AuthDeps{Users: users, Sessions: sessions})
	setupH := r.SetupHandler(ui.SetupDeps{Users: users, Sessions: sessions})
	if _, err := users.CreateLocalUser(context.Background(), "root", "Root", "longenoughpw", user.RoleAdmin); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	h := gateway.New(gateway.Deps{
		Translator:   tr,
		Version:      "test",
		StartedAt:    time.Now(),
		UIHandler:    r.Routes(),
		AuthHandler:  authH,
		SetupHandler: setupH,
		Sessions:     sessions,
		Users:        users,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, users
}

// [AC-S9db742-1-1] RAIDZ2 pool create succeeds via POST and the engine sees
// the canonical PoolConfig.
func TestAcceptance_StorageCreatePool_Raidz2(t *testing.T) {
	eng := &stubStorageRW{stubStorage: stubStorage{}}
	srv, _ := newServerWithStorageRW(t, eng)

	c := loginAs(t, srv, "root", "longenoughpw")
	form := url.Values{}
	form.Set("name", "tank")
	form.Set("data_layout", "raidz2")
	form["data_disks"] = []string{"/dev/sda", "/dev/sdb", "/dev/sdc", "/dev/sdd"}
	resp, err := c.PostForm(srv.URL+"/ui/admin/storage/pools", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if len(eng.createCalls) != 1 {
		t.Fatalf("want 1 create call, got %d", len(eng.createCalls))
	}
	got := eng.createCalls[0]
	if got.Name != "tank" || got.Data.Layout != storage.LayoutRaidZ2 || len(got.Data.Disks) != 4 {
		t.Errorf("got = %+v", got)
	}
}

// [AC-S9db742-1-2] Single-SSD special vdev is rejected as ERROR (not warning).
func TestAcceptance_StorageCreatePool_RejectsSingleSSDSpecial(t *testing.T) {
	eng := &stubStorageRW{stubStorage: stubStorage{}}
	srv, _ := newServerWithStorageRW(t, eng)

	c := loginAs(t, srv, "root", "longenoughpw")
	form := url.Values{}
	form.Set("name", "tank")
	form.Set("data_layout", "mirror")
	form["data_disks"] = []string{"/dev/sda", "/dev/sdb"}
	form.Set("special_layout", "single")
	form["special_disks"] = []string{"/dev/nvme0n1"}
	resp, err := c.PostForm(srv.URL+"/ui/admin/storage/pools", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (rejected)", resp.StatusCode)
	}
	if len(eng.createCalls) != 0 {
		t.Errorf("validation must short-circuit before engine call; got %d calls", len(eng.createCalls))
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "special vdev") && !strings.Contains(body, "special") {
		t.Errorf("error body should mention special vdev: %q", body)
	}
}

// [AC-S9db742-3-1] Importable pool action issues a POST to /import that
// reaches the engine.
func TestAcceptance_StorageImportPool(t *testing.T) {
	eng := &stubStorageRW{stubStorage: stubStorage{
		imports: storageFixtureImports(),
	}}
	srv, _ := newServerWithStorageRW(t, eng)
	c := loginAs(t, srv, "root", "longenoughpw")

	form := url.Values{}
	form.Set("name", "archive")
	resp, err := c.PostForm(srv.URL+"/ui/admin/storage/import", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if len(eng.importCalls) != 1 || eng.importCalls[0].Name != "archive" {
		t.Errorf("import calls = %+v", eng.importCalls)
	}
	if eng.importCalls[0].Opts.Force {
		t.Errorf("force defaulted true; should be false unless checkbox checked")
	}
}

// [AC-S9db742-3-2] force checkbox must be ON for the engine to receive Force=true.
func TestAcceptance_StorageImportPool_ForceFlow(t *testing.T) {
	eng := &stubStorageRW{stubStorage: stubStorage{imports: storageFixtureImports()}}
	srv, _ := newServerWithStorageRW(t, eng)
	c := loginAs(t, srv, "root", "longenoughpw")

	form := url.Values{}
	form.Set("name", "archive")
	form.Set("force", "1")
	resp, err := c.PostForm(srv.URL+"/ui/admin/storage/import", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if !eng.importCalls[len(eng.importCalls)-1].Opts.Force {
		t.Errorf("Force flag did not propagate when checkbox=1")
	}
}

// [AC-S9db742-1-1] Admin sees the New Pool button when WriteEnabled.
func TestAcceptance_StoragePage_NewPoolButtonEnabled(t *testing.T) {
	eng := &stubStorageRW{stubStorage: stubStorage{
		pools: storageFixturePools(),
		disks: storageFixtureDisks(),
	}}
	srv, _ := newServerWithStorageRW(t, eng)

	c := loginAs(t, srv, "root", "longenoughpw")
	resp, err := c.Get(srv.URL + "/ui/admin/storage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if !strings.Contains(body, `data-testid="storage-new-pool-btn"`) {
		t.Errorf("New Pool button missing")
	}
	if strings.Contains(body, `disabled aria-disabled="true" title="プール作成は次のスプリントで`) {
		t.Errorf("New Pool button still rendered as deferred placeholder")
	}
	if !strings.Contains(body, `data-testid="storage-new-pool-modal"`) {
		t.Errorf("New Pool modal markup missing")
	}
}
