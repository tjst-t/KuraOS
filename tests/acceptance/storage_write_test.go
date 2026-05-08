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
	volumeCalls []struct {
		Dataset string
		Opts    storage.VolumeOpts
	}
	snapshotCalls []struct {
		Dataset string
		Name    string
	}
	rollbackCalls []struct {
		Dataset  string
		Snapshot string
	}
	quotaCalls []struct {
		Dataset string
		Bytes   int64
	}
	destroyPoolCalls []struct {
		Name  string
		Force bool
	}
	destroyVolumeCalls []struct {
		Dataset   string
		Recursive bool
	}
	destroySnapCalls []struct {
		Dataset string
		Name    string
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
func (s *stubStorageRW) CreateVolume(_ context.Context, dataset string, opts storage.VolumeOpts) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.volumeCalls = append(s.volumeCalls, struct {
		Dataset string
		Opts    storage.VolumeOpts
	}{dataset, opts})
	return nil
}
func (s *stubStorageRW) CreateSnapshot(_ context.Context, dataset, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotCalls = append(s.snapshotCalls, struct {
		Dataset string
		Name    string
	}{dataset, name})
	return nil
}
func (s *stubStorageRW) Rollback(_ context.Context, dataset, snapshot string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollbackCalls = append(s.rollbackCalls, struct {
		Dataset  string
		Snapshot string
	}{dataset, snapshot})
	return nil
}
func (s *stubStorageRW) SetQuota(_ context.Context, dataset string, bytes int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.quotaCalls = append(s.quotaCalls, struct {
		Dataset string
		Bytes   int64
	}{dataset, bytes})
	return nil
}
func (s *stubStorageRW) DestroyPool(_ context.Context, name string, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.destroyPoolCalls = append(s.destroyPoolCalls, struct {
		Name  string
		Force bool
	}{name, force})
	return nil
}
func (s *stubStorageRW) DestroyVolume(_ context.Context, dataset string, recursive bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.destroyVolumeCalls = append(s.destroyVolumeCalls, struct {
		Dataset   string
		Recursive bool
	}{dataset, recursive})
	return nil
}
func (s *stubStorageRW) DestroySnapshot(_ context.Context, dataset, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.destroySnapCalls = append(s.destroySnapCalls, struct {
		Dataset string
		Name    string
	}{dataset, name})
	return nil
}
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

// Regression for the modal-partial refactor: the {{ tpl }} template func used
// to execute against the master template tree, which marked it as "escaped"
// after the first request. Subsequent calls to template.Clone() in
// renderToBuffer would then fail with "cannot Clone after it has executed",
// and the page returned the body "template clone error" with a status that
// was already 200 (because render() eagerly wrote the header). This test
// hits the storage page three times in a row to catch that regression — the
// first request used to succeed and only the second/third surfaced the bug.
func TestAcceptance_StoragePage_RepeatedRequestsRender(t *testing.T) {
	eng := &stubStorageRW{stubStorage: stubStorage{
		pools: storageFixturePools(),
		disks: storageFixtureDisks(),
	}}
	srv, _ := newServerWithStorageRW(t, eng)

	c := loginAs(t, srv, "root", "longenoughpw")
	for i := 0; i < 3; i++ {
		resp, err := c.Get(srv.URL + "/ui/admin/storage")
		if err != nil {
			t.Fatalf("request %d: GET: %v", i+1, err)
		}
		body := readBody(t, resp)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200", i+1, resp.StatusCode)
		}
		if strings.Contains(body, "template clone error") || strings.Contains(body, "template error:") {
			t.Fatalf("request %d: response body contains template error: %q", i+1, body[:min(len(body), 400)])
		}
		if !strings.Contains(body, `data-testid="storage-new-pool-modal"`) {
			t.Errorf("request %d: New Pool modal markup missing", i+1)
		}
		if !strings.Contains(body, `data-testid="storage-new-pool-btn"`) {
			t.Errorf("request %d: New Pool button missing", i+1)
		}
	}
}

func min(a, b int) int { if a < b { return a }; return b }

// [AC-S9db742-2-1] Volume can be created from the UI New Volume form, and
// the chosen preset propagates as VolumeOpts.Preset to the engine — which
// in turn applies recordsize / compression / special_small_blocks per
// engine/storage/presets.go (covered by engine unit tests). Here we verify
// the UI wiring: form submission → engine.CreateVolume call.
func TestAcceptance_StorageCreateVolumeFromUI(t *testing.T) {
	eng := &stubStorageRW{stubStorage: stubStorage{
		pools: storageFixturePools(),
	}}
	srv, _ := newServerWithStorageRW(t, eng)

	c := loginAs(t, srv, "root", "longenoughpw")

	// New form shape: pool dropdown + path text. The handler combines
	// "tank" + "photos" → "tank/photos" before calling engine.CreateVolume.
	form := url.Values{}
	form.Set("pool", "tank")
	form.Set("path", "photos")
	form.Set("preset", "media")
	form.Set("quota", "1073741824")
	form.Set("mountpoint", "/tank/photos")
	resp, err := c.PostForm(srv.URL+"/ui/admin/storage/volumes", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if len(eng.volumeCalls) != 1 {
		t.Fatalf("want 1 volume call, got %d", len(eng.volumeCalls))
	}
	got := eng.volumeCalls[0]
	if got.Dataset != "tank/photos" || got.Opts.Preset != "media" || got.Opts.QuotaBytes != 1073741824 || got.Opts.MountPoint != "/tank/photos" {
		t.Errorf("got = %+v", got)
	}

	// Backwards-compat: the older `dataset` field still works (handler
	// falls through to it when pool+path are absent).
	form = url.Values{}
	form.Set("dataset", "tank/legacy")
	resp, err = c.PostForm(srv.URL+"/ui/admin/storage/volumes", form)
	if err != nil {
		t.Fatalf("POST (legacy): %v", err)
	}
	resp.Body.Close()
	if len(eng.volumeCalls) != 2 || eng.volumeCalls[1].Dataset != "tank/legacy" {
		t.Errorf("legacy dataset field not honored: %+v", eng.volumeCalls)
	}
}

// [AC-S9db742-2-2] Snapshot create / list / rollback are reachable from the
// UI. The Snapshot tab's New Snapshot form posts to the snapshots endpoint;
// the per-row Rollback button posts to /snapshots/rollback. Both must round-
// trip the dataset+name fields untouched.
func TestAcceptance_StorageSnapshotFromUI(t *testing.T) {
	eng := &stubStorageRW{stubStorage: stubStorage{
		pools: storageFixturePools(),
		volumes: []storage.VolumeInfo{
			{Name: "tank/photos", UsedBytes: 1024 * 1024, AvailableBytes: 1024 * 1024 * 1024},
		},
		snapshots: map[string][]storage.SnapshotInfo{
			"tank/photos": {{Dataset: "tank/photos", Name: "auto-2026-05-08", UsedBytes: 1024}},
		},
	}}
	srv, _ := newServerWithStorageRW(t, eng)

	c := loginAs(t, srv, "root", "longenoughpw")

	// Snapshot tab renders the existing snapshot row + New Snapshot button.
	resp, err := c.Get(srv.URL + "/ui/admin/storage?tab=snapshots")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := readBody(t, resp)
	resp.Body.Close()
	if !strings.Contains(body, `data-testid="storage-snapshots-panel"`) {
		t.Errorf("snapshot panel missing")
	}
	if !strings.Contains(body, `data-testid="storage-snapshot-new-btn"`) {
		t.Errorf("new snapshot button missing")
	}
	if !strings.Contains(body, "auto-2026-05-08") {
		t.Errorf("existing snapshot row missing")
	}
	if !strings.Contains(body, `data-testid="storage-snapshot-rollback-btn"`) {
		t.Errorf("rollback button missing")
	}

	// Create snapshot via the form.
	form := url.Values{"dataset": {"tank/photos"}, "name": {"manual-1"}}
	resp, err = c.PostForm(srv.URL+"/ui/admin/storage/snapshots", form)
	if err != nil {
		t.Fatalf("POST snap: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("snap status = %d, want 303", resp.StatusCode)
	}
	if len(eng.snapshotCalls) != 1 ||
		eng.snapshotCalls[0].Dataset != "tank/photos" ||
		eng.snapshotCalls[0].Name != "manual-1" {
		t.Errorf("snapshot call mismatch: %+v", eng.snapshotCalls)
	}

	// Rollback via the per-snapshot modal.
	form = url.Values{"dataset": {"tank/photos"}, "snapshot": {"auto-2026-05-08"}}
	resp, err = c.PostForm(srv.URL+"/ui/admin/storage/snapshots/rollback", form)
	if err != nil {
		t.Fatalf("POST rollback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("rollback status = %d, want 303", resp.StatusCode)
	}
	if len(eng.rollbackCalls) != 1 ||
		eng.rollbackCalls[0].Dataset != "tank/photos" ||
		eng.rollbackCalls[0].Snapshot != "auto-2026-05-08" {
		t.Errorf("rollback call mismatch: %+v", eng.rollbackCalls)
	}
}

// [AC-S9db742-2-4] Destroy operations from the UI require name retyping
// (pool / volume) or an acknowledgement checkbox (snapshot). Form values
// that don't match the resource name are rejected before the engine is
// called — the UI's confirm modal is the safety net.
func TestAcceptance_StorageDestroyFromUI(t *testing.T) {
	eng := &stubStorageRW{stubStorage: stubStorage{
		pools: storageFixturePools(),
	}}
	srv, _ := newServerWithStorageRW(t, eng)
	c := loginAs(t, srv, "root", "longenoughpw")

	// Pool destroy with mismatched confirm — must be rejected (no engine call).
	form := url.Values{"name": {"tank"}, "confirm": {"WRONG"}}
	resp, err := c.PostForm(srv.URL+"/ui/admin/storage/pools/destroy", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusSeeOther {
		t.Errorf("destroy with mismatched confirm should NOT redirect (would mean engine ran)")
	}
	if len(eng.destroyPoolCalls) != 0 {
		t.Errorf("engine.DestroyPool must not run with mismatched confirm; calls=%+v", eng.destroyPoolCalls)
	}

	// Pool destroy with matching confirm + force=on.
	form = url.Values{"name": {"tank"}, "confirm": {"tank"}, "force": {"on"}}
	resp, err = c.PostForm(srv.URL+"/ui/admin/storage/pools/destroy", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if len(eng.destroyPoolCalls) != 1 ||
		eng.destroyPoolCalls[0].Name != "tank" ||
		!eng.destroyPoolCalls[0].Force {
		t.Errorf("destroy pool call mismatch: %+v", eng.destroyPoolCalls)
	}

	// Volume destroy with matching confirm + recursive=on.
	form = url.Values{"dataset": {"tank/photos"}, "confirm": {"tank/photos"}, "recursive": {"on"}}
	resp, err = c.PostForm(srv.URL+"/ui/admin/storage/volumes/destroy", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if len(eng.destroyVolumeCalls) != 1 ||
		eng.destroyVolumeCalls[0].Dataset != "tank/photos" ||
		!eng.destroyVolumeCalls[0].Recursive {
		t.Errorf("destroy volume call mismatch: %+v", eng.destroyVolumeCalls)
	}

	// Snapshot destroy: only needs dataset+snapshot fields.
	form = url.Values{"dataset": {"tank/photos"}, "snapshot": {"snap1"}}
	resp, err = c.PostForm(srv.URL+"/ui/admin/storage/snapshots/destroy", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if len(eng.destroySnapCalls) != 1 ||
		eng.destroySnapCalls[0].Dataset != "tank/photos" ||
		eng.destroySnapCalls[0].Name != "snap1" {
		t.Errorf("destroy snapshot call mismatch: %+v", eng.destroySnapCalls)
	}
}

// [AC-S9db742-2-3] Set Quota from the UI. quota=0 unsets via SetQuota.
func TestAcceptance_StorageSetQuotaFromUI(t *testing.T) {
	eng := &stubStorageRW{stubStorage: stubStorage{
		pools: storageFixturePools(),
		volumes: []storage.VolumeInfo{
			{Name: "tank/photos", UsedBytes: 0, AvailableBytes: 1024 * 1024},
		},
	}}
	srv, _ := newServerWithStorageRW(t, eng)

	c := loginAs(t, srv, "root", "longenoughpw")
	form := url.Values{"dataset": {"tank/photos"}, "quota": {"5368709120"}}
	resp, err := c.PostForm(srv.URL+"/ui/admin/storage/quota", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if len(eng.quotaCalls) != 1 ||
		eng.quotaCalls[0].Dataset != "tank/photos" ||
		eng.quotaCalls[0].Bytes != 5368709120 {
		t.Errorf("quota call mismatch: %+v", eng.quotaCalls)
	}

	// 0 unsets.
	form = url.Values{"dataset": {"tank/photos"}, "quota": {"0"}}
	resp, err = c.PostForm(srv.URL+"/ui/admin/storage/quota", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if len(eng.quotaCalls) != 2 || eng.quotaCalls[1].Bytes != 0 {
		t.Errorf("quota=0 call mismatch: %+v", eng.quotaCalls)
	}
}
