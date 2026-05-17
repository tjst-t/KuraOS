package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
)

// fakeAuthUserStore / fakeAuthSessionStore replace the real DB-backed
// stores so login flow tests run without SQLite. Behaviour mirrors the
// production semantics closely enough to exercise the handler logic.

type fakeAuthUserStore struct {
	verify func(ctx context.Context, username, password string) (user.User, bool, error)
}

func (f *fakeAuthUserStore) VerifyPassword(ctx context.Context, username, password string) (user.User, bool, error) {
	return f.verify(ctx, username, password)
}

type fakeAuthSessionStore struct {
	issued map[string]session.Session
	ttl    time.Duration
}

func (f *fakeAuthSessionStore) Issue(_ context.Context, userID, ua, ip string) (session.Session, error) {
	tok := "tok-" + userID
	s := session.Session{ID: tok, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}
	if f.issued == nil {
		f.issued = map[string]session.Session{}
	}
	f.issued[tok] = s
	return s, nil
}
func (f *fakeAuthSessionStore) Revoke(_ context.Context, tok string) error {
	delete(f.issued, tok)
	return nil
}
func (f *fakeAuthSessionStore) TTL() time.Duration {
	if f.ttl == 0 {
		return time.Hour
	}
	return f.ttl
}

func newAuthServer(t *testing.T, users AuthUserStore, sessions AuthSessionStore) (*httptest.Server, *Renderer) {
	t.Helper()
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.AuthHandler(AuthDeps{Users: users, Sessions: sessions}))
	t.Cleanup(srv.Close)
	return srv, r
}

// fakeProviderLister is a tiny stub for AuthDeps.Providers so the
// /login render path can be tested with and without federation
// configured.
type fakeProviderLister struct{ rows []UsersProvider }

func (f *fakeProviderLister) ListProviders(_ context.Context) ([]UsersProvider, error) {
	return f.rows, nil
}

// [AC-Sfix002-2-1] Google button visible iff google provider enabled.
func TestLogin_GETShowsGoogleButtonWhenEnabled(t *testing.T) {
	users := &fakeAuthUserStore{verify: func(_ context.Context, _, _ string) (user.User, bool, error) {
		return user.User{}, false, nil
	}}
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.AuthHandler(AuthDeps{
		Users:    users,
		Sessions: &fakeAuthSessionStore{},
		Providers: &fakeProviderLister{rows: []UsersProvider{
			{Name: "google", Enabled: true},
		}},
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	for _, want := range []string{
		`data-testid="login-google-btn"`,
		`href="/federation/google/start"`,
		`Google で続行`, // AC-S413bd5-2-1: label updated from "でログイン" to "で続行"
		`data-testid="login-fed-divider"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// [AC-Sfix002-2-1] No Google button when no provider is configured.
func TestLogin_GETHidesGoogleButtonWhenDisabled(t *testing.T) {
	users := &fakeAuthUserStore{verify: func(_ context.Context, _, _ string) (user.User, bool, error) {
		return user.User{}, false, nil
	}}
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.AuthHandler(AuthDeps{
		Users:    users,
		Sessions: &fakeAuthSessionStore{},
		// No Providers wired — federation off.
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if strings.Contains(body, `data-testid="login-google-btn"`) {
		t.Fatalf("Google button rendered with no providers configured")
	}
}

func TestLogin_GETReturnsForm(t *testing.T) {
	users := &fakeAuthUserStore{verify: func(_ context.Context, _, _ string) (user.User, bool, error) {
		return user.User{}, false, nil
	}}
	srv, _ := newAuthServer(t, users, &fakeAuthSessionStore{})
	resp, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	for _, want := range []string{`name="username"`, `name="password"`, `data-testid="login-form"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// [AC-S1e7eeb-2-1] /login で username+password を送信するとセッション cookie が発行される.
func TestLogin_POSTSuccessSetsCookieAndRedirects(t *testing.T) {
	wantUser := user.User{ID: "u1", Username: "admin", Role: user.RoleAdmin}
	users := &fakeAuthUserStore{verify: func(_ context.Context, u, p string) (user.User, bool, error) {
		if u == "admin" && p == "secret" {
			return wantUser, true, nil
		}
		return user.User{}, false, nil
	}}
	sessions := &fakeAuthSessionStore{}
	srv, _ := newAuthServer(t, users, sessions)
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(srv.URL+"/login", url.Values{"username": {"admin"}, "password": {"secret"}})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/ui/admin/dashboard" {
		t.Fatalf("Location = %q, want /ui/admin/dashboard", loc)
	}
	var found bool
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			found = true
			if !c.HttpOnly {
				t.Errorf("cookie not HttpOnly")
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, want Lax", c.SameSite)
			}
		}
	}
	if !found {
		t.Fatalf("kura_session cookie not set")
	}
}

func TestLogin_UserRoleRedirectsToUI(t *testing.T) {
	users := &fakeAuthUserStore{verify: func(_ context.Context, u, p string) (user.User, bool, error) {
		return user.User{ID: "u2", Username: "alice", Role: user.RoleUser}, true, nil
	}}
	srv, _ := newAuthServer(t, users, &fakeAuthSessionStore{})
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(srv.URL+"/login", url.Values{"username": {"alice"}, "password": {"x"}})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/ui" {
		t.Fatalf("Location = %q, want /ui", loc)
	}
}

func TestLogin_POSTWrongPasswordReturns401(t *testing.T) {
	users := &fakeAuthUserStore{verify: func(_ context.Context, _, _ string) (user.User, bool, error) {
		return user.User{}, false, nil
	}}
	srv, r := newAuthServer(t, users, &fakeAuthSessionStore{})
	resp, err := http.PostForm(srv.URL+"/login", url.Values{"username": {"admin"}, "password": {"wrong"}})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	body := readBody(t, resp)
	wantMsg := r.tr.T(i18n.MsgLoginInvalidCreds)
	if !strings.Contains(body, wantMsg) {
		t.Errorf("body missing translated invalid-creds msg %q", wantMsg)
	}
}

func TestLogout_RevokesAndClearsCookie(t *testing.T) {
	sessions := &fakeAuthSessionStore{
		issued: map[string]session.Session{"tok-u1": {ID: "tok-u1", UserID: "u1"}},
	}
	users := &fakeAuthUserStore{verify: func(_ context.Context, _, _ string) (user.User, bool, error) {
		return user.User{}, false, nil
	}}
	srv, _ := newAuthServer(t, users, sessions)

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/logout", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "tok-u1"})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if _, ok := sessions.issued["tok-u1"]; ok {
		t.Errorf("session not revoked after /logout")
	}
	var cleared bool
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("session cookie not cleared")
	}
}
