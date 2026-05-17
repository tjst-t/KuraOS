package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
)

// fakeSessions / fakeUsers are in-memory implementations used by middleware
// tests. Keeping them here (rather than spinning up the real SQLite store)
// keeps gateway tests fast and isolates the assertion to the middleware
// itself, not the persistence layer.

type fakeSessions struct {
	sessions map[string]session.Session
	err      error
}

func (f *fakeSessions) Lookup(_ context.Context, token string) (session.Session, error) {
	if f.err != nil {
		return session.Session{}, f.err
	}
	s, ok := f.sessions[token]
	if !ok {
		return session.Session{}, session.ErrNotFound
	}
	return s, nil
}

type fakeUsers struct {
	byID  map[string]user.User
	count map[user.Role]int
	err   error
}

func (f *fakeUsers) GetByID(_ context.Context, id string) (user.User, error) {
	if f.err != nil {
		return user.User{}, f.err
	}
	u, ok := f.byID[id]
	if !ok {
		return user.User{}, user.ErrNotFound
	}
	return u, nil
}

func (f *fakeUsers) CountByRole(_ context.Context, role user.Role) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.count[role], nil
}

func newGateway(t *testing.T, sessions SessionResolver, users UserLookup, ui http.Handler) http.Handler {
	t.Helper()
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	return New(Deps{
		Translator: tr,
		Version:    "test",
		StartedAt:  time.Now(),
		UIHandler:  ui,
		Sessions:   sessions,
		Users:      users,
	})
}

func okHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
}

// [AC-S1e7eeb-2-2] 未認証で /ui/admin にアクセスすると /login にリダイレクト.
func TestAuth_UnauthRedirectsToLogin(t *testing.T) {
	sessions := &fakeSessions{sessions: map[string]session.Session{}}
	users := &fakeUsers{
		byID:  map[string]user.User{},
		count: map[user.Role]int{user.RoleAdmin: 1}, // admin exists, but not us
	}
	h := newGateway(t, sessions, users, okHandler("admin-page"))
	req := httptest.NewRequest(http.MethodGet, "/ui/admin/dashboard", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, want /login", loc)
	}
}

// [AC-S1e7eeb-2-3] user ロールでは /ui/admin に 403、/ui には 200.
func TestAuth_UserRoleGetsForbiddenOnAdmin(t *testing.T) {
	sessions := &fakeSessions{
		sessions: map[string]session.Session{
			"tok": {ID: "tok", UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	users := &fakeUsers{
		byID: map[string]user.User{
			"u1": {ID: "u1", Username: "alice", Role: user.RoleUser},
		},
		count: map[user.Role]int{user.RoleAdmin: 1},
	}
	h := newGateway(t, sessions, users, okHandler("page"))

	t.Run("admin -> 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ui/admin/dashboard", nil)
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "tok"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})
	t.Run("/ui -> 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ui", nil)
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "tok"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

func TestAuth_AdminGetsAllowedEverywhere(t *testing.T) {
	sessions := &fakeSessions{
		sessions: map[string]session.Session{
			"tok": {ID: "tok", UserID: "a1", ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	users := &fakeUsers{
		byID: map[string]user.User{
			"a1": {ID: "a1", Username: "root", Role: user.RoleAdmin},
		},
		count: map[user.Role]int{user.RoleAdmin: 1},
	}
	h := newGateway(t, sessions, users, okHandler("ok"))
	for _, p := range []string{"/ui", "/ui/admin/dashboard"} {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "tok"})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
		})
	}
}

// [AC-S1e7eeb-3-1] admin が未作成の状態で / にアクセスすると /setup にリダイレクト.
func TestAuth_NoAdmin_RootRedirectsToSetup(t *testing.T) {
	sessions := &fakeSessions{sessions: map[string]session.Session{}}
	users := &fakeUsers{
		byID:  map[string]user.User{},
		count: map[user.Role]int{user.RoleAdmin: 0}, // no admin yet
	}
	h := newGateway(t, sessions, users, okHandler("ok"))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/setup" {
		t.Fatalf("Location = %q, want /setup", loc)
	}
}

func TestAuth_Healthz_Unauthenticated(t *testing.T) {
	// /healthz must be reachable without a session — load balancers and
	// orchestrators rely on it.
	sessions := &fakeSessions{sessions: map[string]session.Session{}}
	users := &fakeUsers{count: map[user.Role]int{user.RoleAdmin: 1}}
	h := newGateway(t, sessions, users, okHandler("ok"))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAuth_DBErrorReturns500(t *testing.T) {
	sessions := &fakeSessions{sessions: map[string]session.Session{
		"tok": {ID: "tok", UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
	}}
	users := &fakeUsers{err: errors.New("disk full")}
	h := newGateway(t, sessions, users, okHandler("ok"))
	req := httptest.NewRequest(http.MethodGet, "/ui/admin/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "tok"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// [AC-S413bd5-1-4] Pending user gets 302 to /ui/pending-approval for any
// /ui/* path other than /ui/pending-approval itself. The pending-approval
// page handler is added in Story 2; Story 1 only tests that the redirect
// fires correctly.
func TestRequireRole_PendingUserRedirects(t *testing.T) {
	sessions := &fakeSessions{
		sessions: map[string]session.Session{
			"tok": {ID: "tok", UserID: "u-pend", ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	users := &fakeUsers{
		byID: map[string]user.User{
			"u-pend": {ID: "u-pend", Username: "pending-alice", Role: user.RolePending},
		},
		count: map[user.Role]int{user.RoleAdmin: 1},
	}
	h := newGateway(t, sessions, users, okHandler("page"))

	// Paths that must redirect a pending user to /ui/pending-approval.
	redirectCases := []string{
		"/ui",
		"/ui/admin/dashboard",
	}
	for _, path := range redirectCases {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "tok"})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302 (pending redirect) for path %s", rec.Code, path)
			}
			if loc := rec.Header().Get("Location"); loc != "/ui/pending-approval" {
				t.Fatalf("Location = %q, want /ui/pending-approval for path %s", loc, path)
			}
		})
	}
}

func TestAuth_DisabledUserTreatedAsUnauth(t *testing.T) {
	sessions := &fakeSessions{
		sessions: map[string]session.Session{
			"tok": {ID: "tok", UserID: "u1", ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	users := &fakeUsers{
		byID: map[string]user.User{
			"u1": {ID: "u1", Username: "alice", Role: user.RoleAdmin, Disabled: true},
		},
		count: map[user.Role]int{user.RoleAdmin: 1},
	}
	h := newGateway(t, sessions, users, okHandler("ok"))
	req := httptest.NewRequest(http.MethodGet, "/ui/admin/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "tok"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (login)", rec.Code)
	}
}
