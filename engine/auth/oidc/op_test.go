package oidc

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// memCredStore is a tiny in-memory implementation of CredentialStore so
// the signer can exercise its persist + reload path without booting the
// full engine/system stack.
type memCredStore struct {
	mu  sync.Mutex
	kv  map[string]string
}

func newMemCreds() *memCredStore { return &memCredStore{kv: map[string]string{}} }

func (m *memCredStore) key(kind, ownerKind, ownerID string) string {
	return kind + "|" + ownerKind + "|" + ownerID
}

func (m *memCredStore) LookupCredential(_ context.Context, kind, ownerKind, ownerID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.kv[m.key(kind, ownerKind, ownerID)]; ok && v != "" {
		return v, nil
	}
	return "", sql.ErrNoRows
}

func (m *memCredStore) SetCredential(_ context.Context, kind, ownerKind, ownerID, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kv[m.key(kind, ownerKind, ownerID)] = value
	return nil
}

// fixedSession is a SessionResolver that returns a hard-coded user.
type fixedSession struct {
	cookie, userID string
	ok             bool
}

func (f fixedSession) ResolveSession(_ context.Context, cookie string) (string, bool, error) {
	if cookie != f.cookie {
		return "", false, nil
	}
	return f.userID, f.ok, nil
}

// staticUserClaims stubs the userinfo claims for a user.
type staticUserClaims struct{ claims map[string]any }

func (s staticUserClaims) GetClaims(_ context.Context, _ string) (map[string]any, error) {
	return s.claims, nil
}

// newTestProvider stands up an in-memory SQLite + signer + provider.
func newTestProvider(t *testing.T) (*Provider, *Storage, func()) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE oidc_clients (
		    client_id TEXT PRIMARY KEY, name TEXT, redirect_uris TEXT, grant_types TEXT,
		    response_types TEXT, scopes TEXT, token_endpoint_auth TEXT, app_id TEXT, created_at TEXT);
		CREATE TABLE oidc_auth_codes (
		    code TEXT PRIMARY KEY, client_id TEXT, user_id TEXT, redirect_uri TEXT, scope TEXT,
		    nonce TEXT, code_challenge TEXT, code_challenge_method TEXT,
		    issued_at TEXT, expires_at TEXT, consumed_at TEXT);
		CREATE TABLE federation_links (
		    provider TEXT, subject TEXT, user_id TEXT, email TEXT, linked_at TEXT,
		    PRIMARY KEY (provider, subject));
	`); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	st := NewStorage(db)
	creds := newMemCreds()
	key, err := EnsureSigningKey(context.Background(), creds)
	if err != nil {
		t.Fatalf("ensure key: %v", err)
	}
	op := New("https://nas.test", st, key,
		fixedSession{cookie: "sess-admin", userID: "user-1", ok: true},
		staticUserClaims{claims: map[string]any{
			"preferred_username": "admin",
			"email":              "admin@kuraos.test",
			"email_verified":     true,
			"name":               "Administrator",
		}})
	op.SecretLookup = func(_ context.Context, clientID string) (string, error) {
		v, ok := creds.kv["oidc_client_secret|app|"+clientID]
		if !ok {
			return "", sql.ErrNoRows
		}
		return v, nil
	}
	cleanup := func() { _ = db.Close() }
	// register a baseline confidential client so each test does not need
	// to re-register.
	if _, err := st.RegisterClient(context.Background(), Client{
		ClientID:     "rp-1",
		Name:         "Test RP",
		RedirectURIs: []string{"https://rp.test/callback"},
		AppID:        "rp-1",
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	_ = creds.SetCredential(context.Background(), "oidc_client_secret", "app", "rp-1", "secret-rp-1")
	return op, st, cleanup
}

// AC-S822961-1-1: /oidc/.well-known/openid-configuration returns 200 with
// issuer matching the configured KuraOS host.
func TestDiscoveryEndpoint_AC_S822961_1_1(t *testing.T) {
	op, _, cleanup := newTestProvider(t)
	defer cleanup()
	srv := httptest.NewServer(op.Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/oidc/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("GET discovery: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var doc map[string]any
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := doc["issuer"]; got != "https://nas.test" {
		t.Fatalf("issuer = %v, want https://nas.test", got)
	}
	for _, want := range []string{
		"authorization_endpoint", "token_endpoint", "userinfo_endpoint",
		"jwks_uri", "end_session_endpoint",
	} {
		if v, ok := doc[want].(string); !ok || v == "" {
			t.Fatalf("%s missing or empty: %v", want, doc[want])
		}
	}
	if rt, _ := doc["response_types_supported"].([]any); len(rt) == 0 {
		t.Fatalf("response_types_supported empty")
	}
}

// AC-S822961-1-2 (engine half): authorization_code flow completes — the
// /authorize -> /token -> id_token verify path. The full HTTP-level e2e
// against a real RP fixture lives in tests/e2e/oidc-flow.e2e.spec.ts;
// this test exercises the same wire contract using net/http directly so
// regressions in /authorize or /token break Go-level CI immediately.
func TestAuthorizationCodeFlow_AC_S822961_1_2(t *testing.T) {
	op, _, cleanup := newTestProvider(t)
	defer cleanup()
	srv := httptest.NewServer(op.Routes())
	defer srv.Close()

	// Step 1: GET /authorize with a session cookie. The server should 302
	// to the redirect_uri carrying a `code` query param.
	authURL := srv.URL + "/oidc/authorize?" + url.Values{
		"client_id":     {"rp-1"},
		"redirect_uri":  {"https://rp.test/callback"},
		"response_type": {"code"},
		"scope":         {"openid profile email"},
		"state":         {"opaque-state-xyz"},
		"nonce":         {"opaque-nonce-abc"},
	}.Encode()
	req, _ := http.NewRequest("GET", authURL, nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "sess-admin"})

	noRedirect := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("GET /authorize: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302; body looks like: %v", res.StatusCode, res.Header)
	}
	loc, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %s", loc)
	}
	if got := loc.Query().Get("state"); got != "opaque-state-xyz" {
		t.Fatalf("state = %q, want opaque-state-xyz", got)
	}

	// Step 2: POST /token with the code + Basic auth.
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {"https://rp.test/callback"},
	}
	tokReq, _ := http.NewRequest("POST", srv.URL+"/oidc/token", strings.NewReader(form.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokReq.SetBasicAuth("rp-1", "secret-rp-1")
	tokRes, err := http.DefaultClient.Do(tokReq)
	if err != nil {
		t.Fatalf("POST /token: %v", err)
	}
	defer tokRes.Body.Close()
	if tokRes.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d, want 200", tokRes.StatusCode)
	}
	var tok tokenResponse
	if err := json.NewDecoder(tokRes.Body).Decode(&tok); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tok.IDToken == "" {
		t.Fatalf("id_token empty")
	}
	if tok.AccessToken == "" {
		t.Fatalf("access_token empty")
	}
	if tok.TokenType != "Bearer" {
		t.Fatalf("token_type = %q, want Bearer", tok.TokenType)
	}

	// Step 3: verify the id_token signature + aud + nonce.
	claims, err := op.SigningKey.VerifyIDToken(tok.IDToken)
	if err != nil {
		t.Fatalf("verify id_token: %v", err)
	}
	if claims["iss"] != "https://nas.test" {
		t.Fatalf("iss = %v, want https://nas.test", claims["iss"])
	}
	if claims["sub"] != "user-1" {
		t.Fatalf("sub = %v, want user-1", claims["sub"])
	}
	if claims["aud"] != "rp-1" {
		t.Fatalf("aud = %v, want rp-1", claims["aud"])
	}
	if claims["nonce"] != "opaque-nonce-abc" {
		t.Fatalf("nonce = %v, want opaque-nonce-abc", claims["nonce"])
	}
	if claims["email"] != "admin@kuraos.test" {
		t.Fatalf("email = %v, want admin@kuraos.test", claims["email"])
	}

	// Step 4: /userinfo with the access token.
	uiReq, _ := http.NewRequest("GET", srv.URL+"/oidc/userinfo", nil)
	uiReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uiRes, err := http.DefaultClient.Do(uiReq)
	if err != nil {
		t.Fatalf("GET /userinfo: %v", err)
	}
	defer uiRes.Body.Close()
	if uiRes.StatusCode != http.StatusOK {
		t.Fatalf("userinfo status = %d, want 200", uiRes.StatusCode)
	}
	var ui map[string]any
	if err := json.NewDecoder(uiRes.Body).Decode(&ui); err != nil {
		t.Fatalf("decode userinfo: %v", err)
	}
	if ui["sub"] != "user-1" {
		t.Fatalf("userinfo sub = %v, want user-1", ui["sub"])
	}

	// Step 5: replay attempt — re-using the same code MUST 400.
	tokReq2, _ := http.NewRequest("POST", srv.URL+"/oidc/token", strings.NewReader(form.Encode()))
	tokReq2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokReq2.SetBasicAuth("rp-1", "secret-rp-1")
	tokRes2, _ := http.DefaultClient.Do(tokReq2)
	tokRes2.Body.Close()
	if tokRes2.StatusCode == http.StatusOK {
		t.Fatalf("code replay should fail; got 200")
	}
}

// Authorize without a session cookie redirects to /login.
func TestAuthorize_NoSessionRedirectsToLogin(t *testing.T) {
	op, _, cleanup := newTestProvider(t)
	defer cleanup()
	srv := httptest.NewServer(op.Routes())
	defer srv.Close()

	noRedirect := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := noRedirect.Get(srv.URL + "/oidc/authorize?client_id=rp-1&redirect_uri=https://rp.test/callback&response_type=code")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("Location = %q, want /login...", loc)
	}
}

// JWKS endpoint returns the active signing key.
func TestJWKS(t *testing.T) {
	op, _, cleanup := newTestProvider(t)
	defer cleanup()
	srv := httptest.NewServer(op.Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/oidc/jwks.json")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var doc struct{ Keys []map[string]any }
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(doc.Keys))
	}
	if doc.Keys[0]["kty"] != "RSA" {
		t.Fatalf("kty = %v, want RSA", doc.Keys[0]["kty"])
	}
}

// Auth code expiry: a code older than AuthCodeTTL must fail with
// invalid_grant.
func TestAuthCode_Expired(t *testing.T) {
	op, st, cleanup := newTestProvider(t)
	defer cleanup()
	now := time.Now().UTC()
	old := now.Add(-2 * AuthCodeTTL)
	st = st.WithClock(func() time.Time { return old })
	op.Storage = st
	code, err := st.IssueAuthCode(context.Background(), AuthCode{
		ClientID:    "rp-1",
		UserID:      "user-1",
		RedirectURI: "https://rp.test/callback",
		Scope:       "openid",
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Now consume from the future.
	st2 := NewStorage(st.db).WithClock(func() time.Time { return now })
	_, err = st2.ConsumeAuthCode(context.Background(), code.Code)
	if err != ErrAuthCodeExpired {
		t.Fatalf("err = %v, want ErrAuthCodeExpired", err)
	}
}
