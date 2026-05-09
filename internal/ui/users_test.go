package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/i18n"
)

// fakeUsersLister returns a single admin row for the Users page test.
type fakeUsersLister struct{ rows []UsersUserRow }

func (f *fakeUsersLister) List(_ context.Context) ([]UsersUserRow, error) {
	return f.rows, nil
}

type fakeFedLister struct{ rows []UsersFederationRow }

func (f *fakeFedLister) ListFor(_ context.Context, _ string) ([]UsersFederationRow, error) {
	return f.rows, nil
}

type fakeOIDCLister struct{ rows []UsersOIDCClient }

func (f *fakeOIDCLister) ListClients(_ context.Context) ([]UsersOIDCClient, error) {
	return f.rows, nil
}

type fakeProvLister struct{ rows []UsersProvider }

func (f *fakeProvLister) ListProviders(_ context.Context) ([]UsersProvider, error) {
	return f.rows, nil
}

// AC-S822961-3-1 surface: the Users 画面 must expose a "Google を紐付け"
// button per row and the Auth tab must render a Google provider card.
func TestUsersPage_LinkGoogleButton_AC_S822961_3_1(t *testing.T) {
	tr, _ := i18n.New()
	r, err := New(tr, "test")
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	r.SetUsersHandler(UsersDeps{
		Users: &fakeUsersLister{rows: []UsersUserRow{
			{UserID: "u-1", Username: "admin", Role: "admin", Methods: []string{"local"}},
		}},
		Federations: &fakeFedLister{},
		OIDCClients: &fakeOIDCLister{},
		Providers: &fakeProvLister{rows: []UsersProvider{
			{Name: "google", Enabled: true, ClientID: "test-client", AutoProvision: false},
		}},
	})
	srv := httptest.NewServer(r.usersHandler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/users?tab=users")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body := readResp(resp)
	for _, want := range []string{
		`data-testid="users-tab-users"`,
		`data-testid="users-tab-auth"`,
		`data-testid="users-tab-oidc"`,
		`data-testid="users-link-google-btn"`,
		"admin",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("users tab body missing %q", want)
		}
	}

	// Auth tab.
	resp2, err := http.Get(srv.URL + "/ui/admin/users?tab=auth")
	if err != nil {
		t.Fatalf("get auth: %v", err)
	}
	defer resp2.Body.Close()
	body2 := readResp(resp2)
	if !strings.Contains(body2, `data-testid="provider-google"`) {
		t.Fatalf("auth tab body missing provider-google")
	}
	if !strings.Contains(body2, "test-client") {
		t.Fatalf("auth tab body missing client_id")
	}
}

// OIDC clients tab renders auto-registered apps.
func TestUsersPage_OIDCClientsTab(t *testing.T) {
	tr, _ := i18n.New()
	r, _ := New(tr, "test")
	r.SetUsersHandler(UsersDeps{
		OIDCClients: &fakeOIDCLister{rows: []UsersOIDCClient{
			{ClientID: "app-immich.123", Name: "immich", RedirectURIs: []string{"http://nas.local/apps/immich/oidc/callback"}},
		}},
	})
	srv := httptest.NewServer(r.usersHandler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/users?tab=oidc")
	if err != nil {
		t.Fatalf("get oidc: %v", err)
	}
	defer resp.Body.Close()
	body := readResp(resp)
	if !strings.Contains(body, "app-immich.123") {
		t.Fatalf("oidc tab missing client_id")
	}
	if !strings.Contains(body, "/apps/immich/oidc/callback") {
		t.Fatalf("oidc tab missing redirect URI")
	}
}
