package gateway

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/app"
	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/user"

	_ "modernc.org/sqlite"
)

// fakeUserStore implements UserLookup for the forward_auth gating test.
type fakeUserStore struct {
	users map[string]user.User
}

func (f *fakeUserStore) GetByID(_ context.Context, id string) (user.User, error) {
	u, ok := f.users[id]
	if !ok {
		return user.User{}, user.ErrNotFound
	}
	return u, nil
}

func (f *fakeUserStore) CountByRole(_ context.Context, _ user.Role) (int, error) {
	return len(f.users), nil
}

// AC-S822961-2-2: forward_auth gateway resolves the operator session
// and forwards X-Forwarded-User to the upstream container.
func TestForwardAuth_InjectsXForwardedUser_AC_S822961_2_2(t *testing.T) {
	// Capture upstream-side headers for assertion.
	gotHeader := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader <- r.Header.Get("Remote-User")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	port := atoiHost(upstream.URL)

	// Wire a real session store so the gating + cookie resolution
	// path runs end-to-end.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE sessions (id TEXT PRIMARY KEY, user_id TEXT, issued_at TEXT,
		    expires_at TEXT, revoked_at TEXT, user_agent TEXT, remote_addr TEXT);
	`); err != nil {
		t.Fatalf("create: %v", err)
	}
	sessions := session.NewStore(db).WithTTL(time.Hour)
	sess, err := sessions.Issue(context.Background(), "user-admin", "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}

	users := &fakeUserStore{users: map[string]user.User{
		"user-admin": {ID: "user-admin", Username: "admin", Role: user.RoleAdmin},
	}}

	reg := app.NewMemoryRouteRegistry()
	_ = reg.RegisterAppRoute(app.AppRoute{
		AppID:       "navidrome.0001",
		AppName:     "navidrome",
		Mode:        app.RoutingModePath,
		HostPort:    port,
		StripPrefix: true,
		AuthMode:    app.AuthModeForwardAuth,
		HeaderUser:  "Remote-User",
	})

	h := NewAppRouteHandlerWithAuth(reg, sessions, users)
	h.ProxyHost = "127.0.0.1"
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Without a session cookie -> 302 to /login.
	noredir := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := noredir.Get(srv.URL + "/apps/navidrome/song")
	if err != nil {
		t.Fatalf("GET unauth: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("unauth status = %d, want 302", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("Location = %q, want /login...", loc)
	}

	// With the session cookie -> upstream sees Remote-User: admin.
	req, _ := http.NewRequest("GET", srv.URL+"/apps/navidrome/song", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: sess.ID})
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET auth: %v", err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res2.Body)
		t.Fatalf("auth status = %d, body = %s", res2.StatusCode, body)
	}
	select {
	case got := <-gotHeader:
		if got != "admin" {
			t.Fatalf("X-Forwarded-User (Remote-User) = %q, want admin", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("upstream never received the request")
	}

	// Header smuggling defence: a client-supplied Remote-User must be
	// stripped before the gateway sets its own value.
	req2, _ := http.NewRequest("GET", srv.URL+"/apps/navidrome/song2", nil)
	req2.AddCookie(&http.Cookie{Name: session.CookieName, Value: sess.ID})
	req2.Header.Set("Remote-User", "evil-attacker")
	res3, _ := http.DefaultClient.Do(req2)
	res3.Body.Close()
	select {
	case got := <-gotHeader:
		if got != "admin" {
			t.Fatalf("upstream header = %q, want admin (smuggling not stripped)", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("upstream never received second request")
	}
}

// auth.mode=oidc routes do NOT inject any header — the upstream is
// expected to call our OIDC OP itself.
func TestOIDC_Mode_NoHeaderInjection(t *testing.T) {
	gotHeader := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader <- r.Header.Get("X-Forwarded-User")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	port := atoiHost(upstream.URL)

	reg := app.NewMemoryRouteRegistry()
	_ = reg.RegisterAppRoute(app.AppRoute{
		AppID:       "calibre.001",
		AppName:     "calibre",
		Mode:        app.RoutingModePath,
		HostPort:    port,
		StripPrefix: true,
		AuthMode:    app.AuthModeOIDC,
	})
	h := NewAppRouteHandler(reg)
	h.ProxyHost = "127.0.0.1"
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, _ := http.Get(srv.URL + "/apps/calibre/")
	res.Body.Close()
	got := <-gotHeader
	if got != "" {
		t.Fatalf("X-Forwarded-User = %q, want empty for auth.mode=oidc", got)
	}
}

func atoiHost(u string) int {
	host := strings.TrimPrefix(u, "http://")
	colon := strings.Index(host, ":")
	if colon < 0 {
		return 0
	}
	return atoi(host[colon+1:])
}
