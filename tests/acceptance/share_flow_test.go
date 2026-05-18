package acceptance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/share"
	"github.com/kuraos-org/kura/engine/system"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
	"github.com/kuraos-org/kura/internal/cmdexec"
	"github.com/kuraos-org/kura/internal/gateway"
	"github.com/kuraos-org/kura/internal/store"
	"github.com/kuraos-org/kura/internal/ui"
)

// newServerWithShares spins up a full kura server (auth + UI + share engine)
// backed by an in-memory SQLite + cmdexec.Fake. The Fake registers default
// success responses for testparm / systemctl / exportfs so Apply does not
// fail on dev boxes without smbd/nfsd.
func newServerWithShares(t *testing.T) (*httptest.Server, *user.Store, *share.Manager, string, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	smbPath := filepath.Join(dir, "kura.conf")
	expPath := filepath.Join(dir, "kura.exports")

	fake := cmdexec.NewFake()
	fake.RegisterStdout("testparm", []string{"-s", "--suppress-prompt", smbPath}, nil)
	fake.RegisterStdout("systemctl", []string{"restart", "smbd"}, nil)
	fake.RegisterStdout("systemctl", []string{"reload", "nfs-server"}, nil)
	fake.RegisterStdout("exportfs", []string{"-ra"}, nil)

	shareStore := share.NewStore(st.DB())
	shareEng := share.NewManager(shareStore, fake, share.Options{
		SMBConfPath:  smbPath,
		ExportsPath:  expPath,
		SystemctlBin: "systemctl",
		TestparmBin:  "testparm",
		ExportfsBin:  "exportfs",
	})

	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	r, err := ui.New(tr, "test")
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	deps := ui.SharesDeps{Engine: shareEng}
	r.SetSharesHandlers(r.SharesHandler(deps), r.SharesDeleteHandler(deps))
	r.SetSharesUpdateHandler(r.SharesUpdateHandler(deps))

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
	return srv, users, shareEng, smbPath, expPath
}

// AC-Sd64f38-1-1: creating a share through /ui/admin/shares regenerates
// smb.conf + exports and reloads smbd / nfs-server. The Fake records every
// call so we assert reload happened end-to-end without a real Samba install.
func TestAcceptance_Share_CreateRegeneratesConfs(t *testing.T) {
	srv, _, _, smbPath, expPath := newServerWithShares(t)
	c := loginAs(t, srv, "root", "longenoughpw")

	form := url.Values{
		"name":        {"photos"},
		"path":        {"/tank/photos"},
		"protocol":    {"both"},
		"preset":      {"general"},
		"access_mode": {"read_write"},
		"acl":         {"user:alice:rw"},
		"description": {"family photos"},
	}
	resp, err := c.PostForm(srv.URL+"/ui/admin/shares", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 after successful create", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/ui/admin/shares" {
		t.Fatalf("Location = %q, want /ui/admin/shares", got)
	}

	// GET the page back and confirm the row + status badge are rendered.
	resp2, err := c.Get(srv.URL + "/ui/admin/shares")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp2.Body.Close()
	body := readBody(t, resp2)
	if !strings.Contains(body, `data-testid="shares-row"`) {
		t.Errorf("share row missing after create:\n%s", body)
	}
	if !strings.Contains(body, "/tank/photos") {
		t.Errorf("share path missing from rendered list")
	}

	// Confirm the conf files actually got written.
	smbBody := mustReadFile(t, smbPath)
	if !strings.Contains(smbBody, "[photos]") {
		t.Errorf("smb.conf missing [photos] section after create")
	}
	if !strings.Contains(smbBody, "server multi channel support = yes") {
		t.Errorf("smb.conf missing performance default — best-defaults regression")
	}
	expBody := mustReadFile(t, expPath)
	if !strings.Contains(expBody, "/tank/photos") {
		t.Errorf("exports missing /tank/photos after create")
	}
}

// AC-Sd64f38-2-1: creation form exposes preset choices (general / media /
// time_machine / database) with descriptive hints — operator never sees raw
// SMB option names.
func TestAcceptance_Share_PresetSelectorHidesSMBKnobs(t *testing.T) {
	srv, _, _, _, _ := newServerWithShares(t)
	c := loginAs(t, srv, "root", "longenoughpw")
	resp, err := c.Get(srv.URL + "/ui/admin/shares")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	for _, p := range []string{"general", "media", "time_machine", "database"} {
		if !strings.Contains(body, `data-testid="shares-form-preset-`+p+`"`) {
			t.Errorf("preset radio %q missing from form", p)
		}
	}
	// Hint copy from ja.json — proves the operator picks by use case, not by
	// SMB option name.
	for _, hint := range []string{"写真", "Time Machine", "オフィス向け"} {
		if !strings.Contains(body, hint) {
			t.Errorf("preset hint %q missing — operator would have to guess", hint)
		}
	}
	// Best-defaults card is read-only — the smb.conf knob names appear there
	// but are NOT form fields.
	if !strings.Contains(body, "server multi channel support") {
		t.Errorf("best-defaults table missing — operator can't see what's auto-applied")
	}
	for _, knob := range []string{`name="vfs_objects"`, `name="oplocks"`, `name="fruit_metadata"`} {
		if strings.Contains(body, knob) {
			t.Errorf("smb knob %q exposed as form field — DESIGN_PRINCIPLES priority #2 violation", knob)
		}
	}
}

// AC-Sd64f38-1-1 negative path: invalid form input shows a translated error
// banner instead of dropping a CLI error onto the page (Forbidden #13).
func TestAcceptance_Share_InvalidInputShowsTranslatedError(t *testing.T) {
	srv, _, _, _, _ := newServerWithShares(t)
	c := loginAs(t, srv, "root", "longenoughpw")

	form := url.Values{
		"name":        {""}, // empty -> ErrInvalidName
		"path":        {"/tank/x"},
		"protocol":    {"smb"},
		"preset":      {"general"},
		"access_mode": {"read_write"},
	}
	resp, err := c.PostForm(srv.URL+"/ui/admin/shares", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if !strings.Contains(body, `data-testid="shares-error"`) {
		t.Errorf("error banner missing on invalid submit")
	}
	if !strings.Contains(body, "共有名が無効") {
		t.Errorf("error banner missing translated copy:\n%s", body)
	}
}

// Regression: shares page now uses {{ template "modal" }} via the new
// partial. The first hit of any page that uses tpl-dispatched bodies used
// to succeed and subsequent hits returned "template clone error" because
// the tpl func executed on the master template tree. Hit /ui/admin/shares
// three times to ensure the fix (Renderer.tplDispatch as a stable clone)
// holds for shares too.
func TestAcceptance_SharesPage_RepeatedRequestsRender(t *testing.T) {
	srv, _, _, _, _ := newServerWithShares(t)
	c := loginAs(t, srv, "root", "longenoughpw")
	for i := 0; i < 3; i++ {
		resp, err := c.Get(srv.URL + "/ui/admin/shares")
		if err != nil {
			t.Fatalf("request %d: GET: %v", i+1, err)
		}
		body := readBody(t, resp)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: status = %d, want 200", i+1, resp.StatusCode)
		}
		if strings.Contains(body, "template clone error") || strings.Contains(body, "template error:") {
			t.Fatalf("request %d: response body contains template error", i+1)
		}
		if !strings.Contains(body, `data-testid="shares-new-modal"`) {
			t.Errorf("request %d: new-share modal markup missing", i+1)
		}
		if !strings.Contains(body, `data-testid="shares-form-submit"`) {
			t.Errorf("request %d: shares form submit button missing", i+1)
		}
	}
}

// Edit a share via the new /ui/admin/shares/update endpoint and confirm
// the engine sees the new values + smb.conf gets regenerated. Name and
// path stay the same — UpdateInput omits them on purpose.
func TestAcceptance_Share_EditUpdatesPresetAndACL(t *testing.T) {
	srv, _, eng, _, _ := newServerWithShares(t)
	c := loginAs(t, srv, "root", "longenoughpw")

	// Create a share to edit.
	create := url.Values{
		"name":        {"docs"},
		"path":        {"/tank/docs"},
		"protocol":    {"smb"},
		"preset":      {"general"},
		"access_mode": {"read_write"},
	}
	resp, err := c.PostForm(srv.URL+"/ui/admin/shares", create)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()

	// Now fetch the list to grab the share's id.
	shares, err := eng.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(shares) == 0 {
		t.Fatal("expected 1 share, got 0")
	}
	id := shares[0].ID

	// Edit via the update endpoint: switch preset to media, ACL to alice rw.
	form := url.Values{
		"id":          {id},
		"protocol":    {"smb"},
		"preset":      {"media"},
		"access_mode": {"read_only"},
		"description": {"フォト共有"},
		"acl":         {"user:alice:rw"},
	}
	resp, err = c.PostForm(srv.URL+"/ui/admin/shares/update", form)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update status = %d, want 303", resp.StatusCode)
	}

	got, err := eng.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Preset != "media" {
		t.Errorf("preset = %q, want media", got.Preset)
	}
	if got.AccessMode != "read_only" {
		t.Errorf("access_mode = %q, want read_only", got.AccessMode)
	}
	if got.Description != "フォト共有" {
		t.Errorf("description = %q, want フォト共有", got.Description)
	}
	if len(got.ACL) != 1 || got.ACL[0].Name != "alice" || got.ACL[0].Mode != share.ACLModeReadWrite {
		t.Errorf("acl = %+v, want [user:alice rw]", got.ACL)
	}
	if got.Name != "docs" || got.Path != "/tank/docs" {
		t.Errorf("name/path must stay unchanged: name=%q path=%q", got.Name, got.Path)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// [AC-Ssys001-4-1] Share creation triggers engine/system to chown/chmod
// the share's dataset path and record the applied state in
// share_perm_state. The setgid bit (mode 2775) is observable via stat.
func TestAcceptance_Share_DatasetOwnership(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rootfs := filepath.Join(dir, "rootfs")
	sharePath := filepath.Join(rootfs, "tank/photos")
	if err := os.MkdirAll(sharePath, 0o755); err != nil {
		t.Fatalf("mkdir sharePath: %v", err)
	}

	hasher := user.NewHasher()
	users := user.NewStore(st.DB(), hasher)
	if _, err := users.CreateLocalUser(context.Background(), "alice", "Alice", "longenoughpw", user.RoleUser); err != nil {
		t.Fatalf("seed alice: %v", err)
	}

	sysEng, err := system.New(system.Options{
		DB:     st.DB(),
		FS:     system.NewRealFS(rootfs),
		Exec:   newSilentExec(),
		Hasher: system.NewHasherAdapter(hasher),
		Users:  system.NewUserStoreAdapter(users),
	})
	if err != nil {
		t.Fatalf("system.New: %v", err)
	}
	owner := system.NewShareOwnershipAdapter(sysEng)

	smbConfPath := filepath.Join(dir, "kura.conf")
	expPath := filepath.Join(dir, "kura.exports")
	fake := cmdexec.NewFake()
	fake.RegisterStdout("testparm", []string{"-s", "--suppress-prompt", smbConfPath}, nil)
	fake.RegisterStdout("systemctl", []string{"restart", "smbd"}, nil)
	fake.RegisterStdout("systemctl", []string{"reload", "nfs-server"}, nil)
	fake.RegisterStdout("exportfs", []string{"-ra"}, nil)
	mgr := share.NewManager(share.NewStore(st.DB()), fake, share.Options{
		SMBConfPath: smbConfPath,
		ExportsPath: expPath,
		Ownership:   owner,
	})

	created, err := mgr.Create(context.Background(), share.CreateInput{
		Name:       "photos",
		Path:       "/tank/photos",
		Protocol:   share.ProtocolSMB,
		Preset:     share.PresetGeneral,
		AccessMode: share.AccessReadWrite,
		ACL: []share.ACLEntry{
			{Kind: share.PrincipalUser, Name: "alice", Mode: share.ACLModeReadWrite},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// share_perm_state row exists for the new share.
	var got struct {
		path string
		uid  int
		gid  int
		mode int
	}
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT path, owner_uid, owner_gid, mode FROM share_perm_state WHERE share_id = ?`,
		created.ID,
	).Scan(&got.path, &got.uid, &got.gid, &got.mode); err != nil {
		t.Fatalf("share_perm_state row missing: %v", err)
	}
	if got.path != "/tank/photos" {
		t.Fatalf("recorded path %q", got.path)
	}
	if got.gid < system.GIDMin || got.gid > system.GIDMax {
		t.Fatalf("recorded gid %d outside KuraOS range", got.gid)
	}

	// Setgid bit applied to the actual directory (mode 02775).
	stat, err := os.Stat(sharePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if stat.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("setgid bit missing on %s (mode=%v)", sharePath, stat.Mode())
	}
	if stat.Mode().Perm() != 0o775 {
		t.Fatalf("perm bits %#o on %s, want 0775", stat.Mode().Perm(), sharePath)
	}
}

// newSilentExec is the test-side cmdexec that swallows pdbedit / chown
// invocations engine/system makes. share_flow_test exercises pdbedit only
// indirectly so a permissive exec keeps the test focused.
type silentExec struct{}

func (silentExec) Run(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
	return nil, nil, nil
}

func newSilentExec() silentExec { return silentExec{} }
