// Package acceptance hosts Sprint-level acceptance tests that boot the real
// SQLite store + gateway and exercise the full login / setup flow end to end.
// These supplement the Playwright .spec.ts files which require a runner not
// installed in this sandbox (same pattern as S464e47's acceptance shell scripts).
package acceptance

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
	"github.com/kuraos-org/kura/internal/gateway"
	"github.com/kuraos-org/kura/internal/store"
	"github.com/kuraos-org/kura/internal/ui"
)

// fastHasher swaps argon2id for a trivial hash so the integration test
// suite stays fast. Production wiring (cmd/kura/main.go) uses the default
// argon2id Hasher.
type fastHasher struct{}

func (fastHasher) Hash(pw string) (string, error)          { return "fast$" + pw, nil }
func (fastHasher) Verify(pw, encoded string) (bool, error) { return encoded == "fast$"+pw, nil }

func newServer(t *testing.T) (*httptest.Server, *user.Store, *session.Store) {
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
	users := user.NewStore(st.DB(), fastHasher{})
	sessions := session.NewStore(st.DB())
	authH := r.AuthHandler(ui.AuthDeps{Users: users, Sessions: sessions})
	setupH := r.SetupHandler(ui.SetupDeps{Users: users, Sessions: sessions})

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

func newClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// [AC-S1e7eeb-3-1] Fresh DB GET / redirects to /setup.
func TestAcceptance_FreshDB_RootRedirectsToSetup(t *testing.T) {
	srv, _, _ := newServer(t)
	c := newClient(t, srv)
	resp, err := c.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/setup" {
		t.Fatalf("status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// [AC-S1e7eeb-3-1] / [AC-S1e7eeb-3-2] full setup wizard happy path: form ->
// admin created -> session cookie -> dashboard. Then /setup is no longer reachable.
func TestAcceptance_SetupWizard_HappyPath(t *testing.T) {
	srv, users, _ := newServer(t)
	c := newClient(t, srv)

	// Step 1: GET /setup renders the form.
	resp, err := c.Get(srv.URL + "/setup")
	if err != nil {
		t.Fatalf("GET /setup: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup GET status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	resp.Body.Close()
	if !strings.Contains(body, `data-testid="setup-form"`) {
		t.Fatalf("setup body missing form")
	}

	// Step 2: POST creates admin and redirects to dashboard.
	resp, err = c.PostForm(srv.URL+"/setup/admin", url.Values{
		"username":         {"root"},
		"display_name":     {"Root"},
		"password":         {"longenoughpw"},
		"password_confirm": {"longenoughpw"},
	})
	if err != nil {
		t.Fatalf("POST /setup/admin: %v", err)
	}
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ui/admin/dashboard" {
		t.Fatalf("post status=%d loc=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp.Body.Close()

	if n, _ := users.CountByRole(context.Background(), user.RoleAdmin); n != 1 {
		t.Fatalf("admin count = %d, want 1", n)
	}

	// Step 3: cookie jar now carries kura_session — dashboard should be 200.
	resp, err = c.Get(srv.URL + "/ui/admin/dashboard")
	if err != nil {
		t.Fatalf("GET dashboard: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// Step 4: /setup is now blocked.
	c2 := newClient(t, srv) // fresh client (no cookies) so middleware decides on admin existence
	resp, err = c2.Get(srv.URL + "/setup")
	if err != nil {
		t.Fatalf("GET /setup after admin: %v", err)
	}
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
		t.Fatalf("blocked /setup status=%d loc=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp.Body.Close()
}

// [AC-S1e7eeb-2-1] / [AC-S1e7eeb-2-2] / [AC-S1e7eeb-2-3] login flow against
// the real gateway with a real session store.
func TestAcceptance_LoginFlow(t *testing.T) {
	srv, users, _ := newServer(t)
	ctx := context.Background()
	if _, err := users.CreateLocalUser(ctx, "root", "Root", "longenoughpw", user.RoleAdmin); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := users.CreateLocalUser(ctx, "alice", "Alice", "longenoughpw", user.RoleUser); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	t.Run("AC-S1e7eeb-2-2 unauth -> /login", func(t *testing.T) {
		c := newClient(t, srv)
		resp, err := c.Get(srv.URL + "/ui/admin/dashboard")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
			t.Fatalf("status=%d loc=%q", resp.StatusCode, resp.Header.Get("Location"))
		}
	})

	t.Run("AC-S1e7eeb-2-1 admin login -> dashboard", func(t *testing.T) {
		c := newClient(t, srv)
		resp, err := c.PostForm(srv.URL+"/login", url.Values{
			"username": {"root"}, "password": {"longenoughpw"},
		})
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ui/admin/dashboard" {
			t.Fatalf("status=%d loc=%q", resp.StatusCode, resp.Header.Get("Location"))
		}
		// The cookie jar now has the session — verify dashboard is reachable.
		dashResp, err := c.Get(srv.URL + "/ui/admin/dashboard")
		if err != nil {
			t.Fatalf("GET dashboard: %v", err)
		}
		defer dashResp.Body.Close()
		if dashResp.StatusCode != http.StatusOK {
			t.Fatalf("dashboard status = %d, want 200", dashResp.StatusCode)
		}
	})

	t.Run("AC-S1e7eeb-2-3 user role: /ui 200, /ui/admin 403", func(t *testing.T) {
		c := newClient(t, srv)
		resp, err := c.PostForm(srv.URL+"/login", url.Values{
			"username": {"alice"}, "password": {"longenoughpw"},
		})
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		resp.Body.Close()
		ui1, err := c.Get(srv.URL + "/ui")
		if err != nil {
			t.Fatalf("GET /ui: %v", err)
		}
		ui1.Body.Close()
		if ui1.StatusCode != http.StatusOK {
			t.Fatalf("/ui status = %d, want 200", ui1.StatusCode)
		}
		admin, err := c.Get(srv.URL + "/ui/admin/dashboard")
		if err != nil {
			t.Fatalf("GET /ui/admin: %v", err)
		}
		admin.Body.Close()
		if admin.StatusCode != http.StatusForbidden {
			t.Fatalf("/ui/admin status = %d, want 403", admin.StatusCode)
		}
	})

	t.Run("wrong password -> 401 with translated msg", func(t *testing.T) {
		resp, err := http.PostForm(srv.URL+"/login", url.Values{
			"username": {"root"}, "password": {"nope"},
		})
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		body := readBody(t, resp)
		if !strings.Contains(body, "ユーザー名またはパスワードが違います") {
			t.Errorf("body missing translated invalid-creds msg")
		}
	})
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return sb.String()
}
