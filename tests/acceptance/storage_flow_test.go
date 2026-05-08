package acceptance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
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

// stubStorage is a deterministic ui.StorageEngineReader used by the storage
// flow acceptance tests. The dev box has no ZFS, so the only realistic
// option is to mock at this layer and assert the wiring (auth + handler +
// template).
type stubStorage struct {
	pools   []storage.Pool
	disks   []storage.Disk
	imports []storage.ImportablePool
}

func (s *stubStorage) ListPools(_ context.Context) ([]storage.Pool, error) {
	return s.pools, nil
}
func (s *stubStorage) ListDisks(_ context.Context) ([]storage.Disk, error) {
	return s.disks, nil
}
func (s *stubStorage) ListImportable(_ context.Context) ([]storage.ImportablePool, error) {
	return s.imports, nil
}

func storageFixturePools() []storage.Pool {
	return []storage.Pool{
		{
			Name:           "tank",
			GUID:           "12345678901234567890",
			Health:         storage.HealthOnline,
			SizeBytes:      26388279066624,
			AllocatedBytes: 20126457528320,
			CapPercent:     76,
			FragPercent:    12,
			Topology: []storage.Vdev{
				{
					Type: storage.VdevTypeData, Layout: storage.LayoutRaidZ2, Name: "raidz2-0", Health: storage.HealthOnline,
					Children: []storage.Vdev{
						{Path: "/dev/disk/by-id/sda", Health: storage.HealthOnline},
						{Path: "/dev/disk/by-id/sdb", Health: storage.HealthOnline},
						{Path: "/dev/disk/by-id/sdc", Health: storage.HealthOnline},
						{Path: "/dev/disk/by-id/sdd", Health: storage.HealthOnline},
					},
				},
			},
		},
	}
}
func storageFixtureDisks() []storage.Disk {
	return []storage.Disk{
		{Path: "/dev/sda", Model: "WDC", Serial: "VLH8", Pool: "tank", Usage: storage.DiskUsagePool, SizeBytes: 8001563222016,
			SMART: storage.SMARTInfo{Status: storage.SMARTStatusPassed, TemperatureC: 38, PowerOnHours: 14820, Source: "smartctl"}},
		{Path: "/dev/sdz", Model: "TOSHIBA", Usage: storage.DiskUsageFree, SizeBytes: 4001000000000,
			SMART: storage.SMARTInfo{Source: "unavailable"}},
	}
}
func storageFixtureImports() []storage.ImportablePool {
	return []storage.ImportablePool{{
		Name: "archive", GUID: "9999", State: storage.HealthOnline,
		Topology: []storage.Vdev{
			{Type: storage.VdevTypeData, Layout: storage.LayoutRaidZ1, Children: []storage.Vdev{
				{Path: "/dev/disk/by-id/sdx"}, {Path: "/dev/disk/by-id/sdy"}, {Path: "/dev/disk/by-id/sdz"},
			}},
		},
	}}
}

// newServerWithStorage stands up the full gateway with the given
// StorageEngineReader wired into the storage handler. Two seed users are
// created (root admin / alice user) so per-AC sub-tests can exercise role
// gating without each one re-registering.
func newServerWithStorage(t *testing.T, eng ui.StorageEngineReader) (*httptest.Server, *user.Store, *session.Store) {
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
	r.SetStorageHandler(r.StorageHandler(ui.StorageDeps{Engine: eng}))

	users := user.NewStore(st.DB(), fastHasher{})
	sessions := session.NewStore(st.DB())
	authH := r.AuthHandler(ui.AuthDeps{Users: users, Sessions: sessions})
	setupH := r.SetupHandler(ui.SetupDeps{Users: users, Sessions: sessions})

	if _, err := users.CreateLocalUser(context.Background(), "root", "Root", "longenoughpw", user.RoleAdmin); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := users.CreateLocalUser(context.Background(), "alice", "Alice", "longenoughpw", user.RoleUser); err != nil {
		t.Fatalf("seed user: %v", err)
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
	return srv, users, sessions
}

// loginAs issues POST /login and returns a client whose cookie jar carries
// the issued session.
func loginAs(t *testing.T, srv *httptest.Server, username, password string) *http.Client {
	t.Helper()
	c := newClient(t, srv)
	resp, err := c.PostForm(srv.URL+"/login", url.Values{"username": {username}, "password": {password}})
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login %s status = %d", username, resp.StatusCode)
	}
	return c
}

// [AC-Se3b190-2-1] / [AC-Se3b190-3-1] Storage page is wired into the admin
// shell with role gating, and renders the disks table + import banner from
// a stub StorageEngine.
func TestAcceptance_Storage_Wiring(t *testing.T) {
	srv, _, _ := newServerWithStorage(t, &stubStorage{
		pools:   storageFixturePools(),
		disks:   storageFixtureDisks(),
		imports: storageFixtureImports(),
	})

	t.Run("unauth -> /login", func(t *testing.T) {
		c := newClient(t, srv)
		resp, err := c.Get(srv.URL + "/ui/admin/storage")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
			t.Fatalf("status=%d loc=%q", resp.StatusCode, resp.Header.Get("Location"))
		}
	})

	t.Run("AC-Se3b190-2-1 admin sees disks table with pool/SMART columns", func(t *testing.T) {
		c := loginAs(t, srv, "root", "longenoughpw")
		resp, err := c.Get(srv.URL + "/ui/admin/storage")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := readBody(t, resp)
		if !strings.Contains(body, `data-testid="storage-disks"`) {
			t.Errorf("disks section missing")
		}
		if got := strings.Count(body, `data-testid="storage-disk-row"`); got != 2 {
			t.Errorf("disk rows = %d, want 2", got)
		}
		if !strings.Contains(body, `data-testid="storage-pool-card"`) {
			t.Errorf("pool card missing")
		}
		// SMART/usage badges must be translated
		for _, w := range []string{"正常", "プール内", "未使用", "未取得"} {
			if !strings.Contains(body, w) {
				t.Errorf("body missing translated label %q", w)
			}
		}
	})

	t.Run("AC-Se3b190-3-1 import banner visible with deferred copy", func(t *testing.T) {
		c := loginAs(t, srv, "root", "longenoughpw")
		resp, err := c.Get(srv.URL + "/ui/admin/storage")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body := readBody(t, resp)
		if !strings.Contains(body, `data-testid="storage-import-banner"`) {
			t.Errorf("import banner missing")
		}
		if !strings.Contains(body, "次のスプリント") {
			t.Errorf("deferred-note copy missing")
		}
		if !strings.Contains(body, `data-testid="storage-import"`) {
			t.Errorf("importable-pools table missing")
		}
	})

	t.Run("user role gets 403", func(t *testing.T) {
		c := loginAs(t, srv, "alice", "longenoughpw")
		resp, err := c.Get(srv.URL + "/ui/admin/storage")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("no raw CLI tokens leak into HTML", func(t *testing.T) {
		c := loginAs(t, srv, "root", "longenoughpw")
		resp, err := c.Get(srv.URL + "/ui/admin/storage")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body := readBody(t, resp)
		for _, banned := range []string{"smart_status", "ata_smart_attributes", "no pools available for import"} {
			if strings.Contains(body, banned) {
				t.Errorf("body leaks raw token %q", banned)
			}
		}
	})
}

// Test the empty-state branch (no pools, no disks, no imports). The page
// still renders 200 and shows the translated empty copy.
func TestAcceptance_Storage_EmptyState(t *testing.T) {
	srv, _, _ := newServerWithStorage(t, &stubStorage{})
	c := loginAs(t, srv, "root", "longenoughpw")
	resp, err := c.Get(srv.URL + "/ui/admin/storage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `data-testid="storage-empty"`) {
		t.Errorf("empty state placeholder missing")
	}
}
