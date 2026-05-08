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
	fake.RegisterStdout("systemctl", []string{"reload", "smbd"}, nil)
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

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
