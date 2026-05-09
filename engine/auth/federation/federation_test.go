package federation

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/kuraos-org/kura/engine/auth/oidc"
	"github.com/kuraos-org/kura/engine/auth/session"

	_ "modernc.org/sqlite"
)

// memCreds is the federation manager's view of a credential vault. The
// federation Manager doesn't actually store anything via the credential
// path — it's just present to satisfy the constructor — but we still
// supply a working impl for completeness.
type memCreds struct {
	mu sync.Mutex
	kv map[string]string
}

func newMemCreds() *memCreds { return &memCreds{kv: map[string]string{}} }

func (m *memCreds) LookupCredential(_ context.Context, k, ok, oid string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, present := m.kv[k+"|"+ok+"|"+oid]
	if !present {
		return "", sql.ErrNoRows
	}
	return v, nil
}

func (m *memCreds) SetCredential(_ context.Context, k, ok, oid, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kv[k+"|"+ok+"|"+oid] = v
	return nil
}

// fakeProvisioner implements UserProvisioner; records calls so tests
// can assert AutoProvision behavior.
type fakeProvisioner struct {
	mu       sync.Mutex
	created  []string
	failNext error
}

func (f *fakeProvisioner) CreateFromFederation(_ context.Context, username, _, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return "", err
	}
	id := "u-" + username
	f.created = append(f.created, id)
	return id, nil
}

// mockIdP is a minimal OAuth2/OIDC provider for tests. It serves the
// discovery doc, accepts an authorize redirect, and exchanges the code
// for an id_token + access_token whose claims hard-code the supplied
// subject + email.
type mockIdP struct {
	mu        sync.Mutex
	server    *httptest.Server
	subject   string
	email     string
	codeIssued string
}

func newMockIdP(t *testing.T, subject, email string) *mockIdP {
	mux := http.NewServeMux()
	idp := &mockIdP{subject: subject, email: email}
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 base,
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
			"userinfo_endpoint":      base + "/userinfo",
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		// Issue a code and redirect.
		idp.mu.Lock()
		idp.codeIssued = "fake-code-" + idp.subject
		code := idp.codeIssued
		idp.mu.Unlock()
		redir := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		u, _ := url.Parse(redir)
		q := u.Query()
		q.Set("code", code)
		q.Set("state", state)
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		// Issue id_token + access_token.
		idToken := makeFakeIDToken(idp.subject, idp.email)
		writeJSON(w, map[string]any{
			"access_token": "access-" + idp.subject,
			"id_token":     idToken,
			"token_type":   "Bearer",
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"sub": idp.subject, "email": idp.email})
	})
	idp.server = httptest.NewServer(mux)
	return idp
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

// makeFakeIDToken builds a JWT-shaped string with no signature. The
// federation manager v1 doesn't verify the signature (relies on
// userinfo round-trip), so this is enough to exercise the parse path.
func makeFakeIDToken(sub, email string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := map[string]string{"sub": sub, "email": email}
	pb, _ := json.Marshal(payload)
	return header + "." + base64.RawURLEncoding.EncodeToString(pb) + "." + ""
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE oidc_clients (client_id TEXT PRIMARY KEY, name TEXT, redirect_uris TEXT,
		   grant_types TEXT, response_types TEXT, scopes TEXT, token_endpoint_auth TEXT,
		   app_id TEXT, created_at TEXT);
		CREATE TABLE oidc_auth_codes (code TEXT PRIMARY KEY, client_id TEXT, user_id TEXT,
		   redirect_uri TEXT, scope TEXT, nonce TEXT, code_challenge TEXT,
		   code_challenge_method TEXT, issued_at TEXT, expires_at TEXT, consumed_at TEXT);
		CREATE TABLE federation_links (provider TEXT, subject TEXT, user_id TEXT, email TEXT,
		   linked_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		   PRIMARY KEY (provider, subject));
		CREATE TABLE sessions (id TEXT PRIMARY KEY, user_id TEXT, issued_at TEXT,
		   expires_at TEXT, revoked_at TEXT, user_agent TEXT, remote_addr TEXT);
	`); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db
}

// AC-S822961-3-1: A user with a Google link can log in via Google.
func TestFederation_BoundUserCanLogIn_AC_S822961_3_1(t *testing.T) {
	db := newTestDB(t)
	idp := newMockIdP(t, "google-subject-123", "alice@example.com")
	defer idp.server.Close()

	storage := oidc.NewStorage(db)
	sessions := session.NewStore(db)
	mgr := New(storage, sessions, newMemCreds())
	mgr.SetHTTPClient(idp.server.Client())

	// Pre-existing KuraOS user pre-linked to Google.
	const userID = "user-alice"
	if err := storage.LinkFederation(context.Background(), oidc.FederationLink{
		Provider: "google", Subject: "google-subject-123", UserID: userID, Email: "alice@example.com",
	}); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	// Wire the front-end of the federation flow: the federation
	// callback URL must point at our test gateway.
	gateway := httptest.NewServer(mgr.Routes())
	defer gateway.Close()

	if err := mgr.Register(context.Background(), Provider{
		Name:        "google",
		Issuer:      idp.server.URL,
		ClientID:    "test-client",
		RedirectURI: gateway.URL + "/federation/google/callback",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Step 1: GET /federation/google/start
	res, err := client.Get(gateway.URL + "/federation/google/start")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("start status = %d, want 302", res.StatusCode)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	if !strings.Contains(loc.String(), idp.server.URL) {
		t.Fatalf("redirect = %s, expected to point at IdP", loc)
	}

	// Step 2: follow the IdP authorize redirect (mock IdP issues a code).
	res2, err := client.Get(loc.String())
	if err != nil {
		t.Fatalf("idp: %v", err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusFound {
		t.Fatalf("idp status = %d, want 302", res2.StatusCode)
	}
	cb, _ := url.Parse(res2.Header.Get("Location"))
	if !strings.HasPrefix(cb.String(), gateway.URL) {
		t.Fatalf("callback = %s, expected gateway prefix", cb)
	}

	// Step 3: hit our /federation/google/callback with the code.
	res3, err := client.Get(cb.String())
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	res3.Body.Close()
	if res3.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", res3.StatusCode)
	}

	// Step 4: cookie jar should now have a kura_session cookie.
	u, _ := url.Parse(gateway.URL + "/")
	cookies := jar.Cookies(u)
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == oidc.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("kura_session cookie missing")
	}

	// Step 5: that cookie resolves to the seeded user.
	sess, err := sessions.Lookup(context.Background(), sessionCookie.Value)
	if err != nil {
		t.Fatalf("session lookup: %v", err)
	}
	if sess.UserID != userID {
		t.Fatalf("session.UserID = %q, want %q", sess.UserID, userID)
	}
}

// AC-S822961-3-2: auto_provision=false rejects an unbound user.
func TestFederation_UnboundUserRejected_AC_S822961_3_2(t *testing.T) {
	db := newTestDB(t)
	idp := newMockIdP(t, "google-stranger-999", "stranger@example.com")
	defer idp.server.Close()

	storage := oidc.NewStorage(db)
	sessions := session.NewStore(db)
	mgr := New(storage, sessions, newMemCreds())
	mgr.SetHTTPClient(idp.server.Client())
	prov := &fakeProvisioner{}
	mgr.SetProvisioner(prov)

	gateway := httptest.NewServer(mgr.Routes())
	defer gateway.Close()

	if err := mgr.Register(context.Background(), Provider{
		Name:          "google",
		Issuer:        idp.server.URL,
		ClientID:      "test-client",
		RedirectURI:   gateway.URL + "/federation/google/callback",
		AutoProvision: false, // explicit
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// /start -> idp /authorize -> back to /callback.
	res, _ := client.Get(gateway.URL + "/federation/google/start")
	res.Body.Close()
	loc, _ := url.Parse(res.Header.Get("Location"))
	res2, _ := client.Get(loc.String())
	res2.Body.Close()
	cb, _ := url.Parse(res2.Header.Get("Location"))

	res3, err := client.Get(cb.String())
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer res3.Body.Close()
	if res3.StatusCode != http.StatusForbidden {
		t.Fatalf("callback status = %d, want 403 (auto_provision off)", res3.StatusCode)
	}
	if len(prov.created) != 0 {
		t.Fatalf("provisioner should not have been called: %v", prov.created)
	}
	// No session was issued.
	cookies := jar.Cookies(cb)
	for _, c := range cookies {
		if c.Name == oidc.SessionCookieName {
			t.Fatalf("kura_session cookie should not be set: %v", c)
		}
	}
}

// AC-S822961-3-2 (positive variant): auto_provision=true creates the
// user on first login.
func TestFederation_AutoProvision_CreatesUser(t *testing.T) {
	db := newTestDB(t)
	idp := newMockIdP(t, "google-newcomer-456", "newcomer@example.com")
	defer idp.server.Close()

	storage := oidc.NewStorage(db)
	sessions := session.NewStore(db)
	mgr := New(storage, sessions, newMemCreds())
	mgr.SetHTTPClient(idp.server.Client())
	prov := &fakeProvisioner{}
	mgr.SetProvisioner(prov)

	gateway := httptest.NewServer(mgr.Routes())
	defer gateway.Close()

	_ = mgr.Register(context.Background(), Provider{
		Name: "google", Issuer: idp.server.URL, ClientID: "c",
		RedirectURI: gateway.URL + "/federation/google/callback",
		AutoProvision: true,
	})

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, _ := client.Get(gateway.URL + "/federation/google/start")
	res.Body.Close()
	loc, _ := url.Parse(res.Header.Get("Location"))
	res2, _ := client.Get(loc.String())
	res2.Body.Close()
	cb, _ := url.Parse(res2.Header.Get("Location"))
	res3, _ := client.Get(cb.String())
	res3.Body.Close()
	if res3.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", res3.StatusCode)
	}
	if len(prov.created) != 1 {
		t.Fatalf("provisioner created %d users, want 1", len(prov.created))
	}
	link, err := storage.LookupFederationBySubject(context.Background(), "google", "google-newcomer-456")
	if err != nil {
		t.Fatalf("link not stored: %v", err)
	}
	if link.UserID != prov.created[0] {
		t.Fatalf("link UserID = %q, want %q", link.UserID, prov.created[0])
	}
}

// PKCE helper sanity check (used by tests reproducing the verifier).
func TestPKCEHelper(t *testing.T) {
	got := PKCEHelper("verifier-1")
	if len(got) == 0 {
		t.Fatalf("empty challenge")
	}
}

// silence import linters during incremental edits
var _ = rand.Read
