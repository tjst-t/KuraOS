package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
)

// fakeSetupUserStore is a tiny in-memory User store for setup wizard tests.
// CountByRole tracks how many of each role exist; CreateLocalUser appends
// and bumps the count so AC-S1e7eeb-3-2 (no double-admin) is exercisable.
type fakeSetupUserStore struct {
	mu        sync.Mutex
	users     []user.User
	createErr error
}

func (f *fakeSetupUserStore) CountByRole(_ context.Context, role user.Role) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, u := range f.users {
		if u.Role == role {
			n++
		}
	}
	return n, nil
}

func (f *fakeSetupUserStore) CreateLocalUser(_ context.Context, username, displayName, password string, role user.Role) (user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return user.User{}, f.createErr
	}
	for _, u := range f.users {
		if u.Username == username {
			return user.User{}, user.ErrUsernameTaken
		}
	}
	u := user.User{
		ID:          "id-" + username,
		Username:    username,
		DisplayName: displayName,
		Role:        role,
	}
	f.users = append(f.users, u)
	return u, nil
}

func newSetupServer(t *testing.T, users SetupUserStore, sessions AuthSessionStore) *httptest.Server {
	t.Helper()
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.SetupHandler(SetupDeps{Users: users, Sessions: sessions}))
	t.Cleanup(srv.Close)
	return srv
}

// [AC-S1e7eeb-3-1] /setup renders Step 1 admin form on a fresh DB.
func TestSetup_GETRendersAdminForm(t *testing.T) {
	users := &fakeSetupUserStore{}
	srv := newSetupServer(t, users, &fakeAuthSessionStore{})
	resp, err := http.Get(srv.URL + "/setup")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	for _, want := range []string{
		`data-testid="setup-form"`,
		`name="username"`,
		`name="password"`,
		`name="password_confirm"`,
		`action="/setup/admin"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestSetup_POSTCreatesAdminAndIssuesSession(t *testing.T) {
	users := &fakeSetupUserStore{}
	sessions := &fakeAuthSessionStore{}
	srv := newSetupServer(t, users, sessions)

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(srv.URL+"/setup/admin", url.Values{
		"username":         {"root"},
		"display_name":     {"Root Admin"},
		"password":         {"longenoughpw"},
		"password_confirm": {"longenoughpw"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/ui/admin/dashboard" {
		t.Fatalf("Location = %q, want /ui/admin/dashboard", loc)
	}
	if n, _ := users.CountByRole(context.Background(), user.RoleAdmin); n != 1 {
		t.Fatalf("admin count = %d, want 1", n)
	}
	var hasCookie bool
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName && c.Value != "" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Errorf("session cookie not issued after admin creation")
	}
}

// [AC-S1e7eeb-3-2] After an admin exists, POST /setup/admin redirects to /login (no double-admin).
func TestSetup_POSTRefusesWhenAdminAlreadyExists(t *testing.T) {
	users := &fakeSetupUserStore{users: []user.User{{ID: "x", Username: "existing", Role: user.RoleAdmin}}}
	srv := newSetupServer(t, users, &fakeAuthSessionStore{})
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(srv.URL+"/setup/admin", url.Values{
		"username":         {"another"},
		"password":         {"longenoughpw"},
		"password_confirm": {"longenoughpw"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, want /login", loc)
	}
	// And no second admin was actually created.
	if n, _ := users.CountByRole(context.Background(), user.RoleAdmin); n != 1 {
		t.Errorf("admin count = %d, want 1 (no double-admin)", n)
	}
}

func TestSetup_POSTValidationErrors(t *testing.T) {
	cases := []struct {
		name   string
		form   url.Values
		wantID i18n.MessageID
	}{
		{
			name: "username invalid characters",
			form: url.Values{
				"username":         {"has spaces"},
				"password":         {"longenoughpw"},
				"password_confirm": {"longenoughpw"},
			},
			wantID: i18n.MsgSetupAdminUsernameRule,
		},
		{
			name: "password too short",
			form: url.Values{
				"username":         {"alice"},
				"password":         {"short"},
				"password_confirm": {"short"},
			},
			wantID: i18n.MsgSetupAdminPasswordShort,
		},
		{
			name: "password mismatch",
			form: url.Values{
				"username":         {"alice"},
				"password":         {"longenoughpw"},
				"password_confirm": {"differentpw1"},
			},
			wantID: i18n.MsgSetupAdminPasswordMatch,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			users := &fakeSetupUserStore{}
			srv := newSetupServer(t, users, &fakeAuthSessionStore{})
			r := newTestRenderer(t)
			resp, err := http.PostForm(srv.URL+"/setup/admin", c.form)
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			body := readBody(t, resp)
			wantMsg := r.tr.T(c.wantID)
			if !strings.Contains(body, wantMsg) {
				t.Errorf("body missing translated msg %q", wantMsg)
			}
			if n, _ := users.CountByRole(context.Background(), user.RoleAdmin); n != 0 {
				t.Errorf("user was created on validation failure; want 0, got %d", n)
			}
		})
	}
}

func TestSetup_GETMethodNotAllowed(t *testing.T) {
	srv := newSetupServer(t, &fakeSetupUserStore{}, &fakeAuthSessionStore{})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/setup", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /setup: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}
