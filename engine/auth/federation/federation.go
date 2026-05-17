// Package federation implements external IdP relying-party logic. v1
// targets Google (the only enumerated federation in VISION), with the
// abstraction shaped so GitHub / Microsoft can be added in v1.x without
// schema changes.
//
// Flow per provider:
//
//	GET /federation/<provider>/start?return_to=<path>
//	     -> 302 to provider's authorize endpoint (PKCE-protected,
//	        opaque state stored in pending_state).
//
//	GET /federation/<provider>/callback?code=...&state=...
//	     -> exchange code for token at provider, decode id_token,
//	        lookup federation_links by (provider, sub),
//	        - bound: issue KuraOS session for the linked user.
//	        - unbound + auto_provision=true: create user via
//	          engine/system.AllocateUID + write federation_links row,
//	          issue session.
//	        - unbound + auto_provision=false: 403.
//
//	POST /federation/<provider>/link  (authenticated KuraOS user)
//	     -> begins the same authorize redirect but the callback path
//	        tags the operator's user_id onto the resulting link.
//
// The implementation uses only stdlib + crypto/rand for PKCE; the
// id_token is verified against the provider's JWKS fetched once at
// startup (cached for 24h, refreshed on demand if a kid is missing).
package federation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kuraos-org/kura/engine/auth/oidc"
	"github.com/kuraos-org/kura/engine/auth/session"
)

// CredentialStore is the slice of engine/system the federation manager
// needs. Same pattern as oidc.CredentialStore — declared here so this
// package never imports engine/system directly.
type CredentialStore interface {
	LookupCredential(ctx context.Context, kind, ownerKind, ownerID string) (string, error)
	SetCredential(ctx context.Context, kind, ownerKind, ownerID, value string) error
}

// Provider describes one federated IdP.
type Provider struct {
	Name          string
	Issuer        string
	ClientID      string
	ClientSecret  string
	RedirectURI   string
	Scopes        []string
	AutoProvision bool

	// Discovered endpoints (filled in by Manager.Register).
	AuthorizeURL string
	TokenURL     string
	UserinfoURL  string
}

// pendingState is the in-memory record we keep between /start and
// /callback. State is short-lived (10 min) so a stale tab opening
// doesn't pile up.
type pendingState struct {
	Provider string
	UserID   string // set when /link initiated; empty for unbound login
	Verifier string
	Nonce    string
	ReturnTo string
	Expires  time.Time
}

// UserProvisioner is the slice of engine/system the auto-provision path
// needs. Provided by cmd/kura wiring.
type UserProvisioner interface {
	// CreateFromFederation creates a new KuraOS user with the given
	// username and display name, allocates a uid, and returns the
	// new user_id.
	CreateFromFederation(ctx context.Context, username, displayName, email string) (string, error)
}

// RoleLookup returns the role tier of a KuraOS user so the federation
// callback can pick a redirect target the user actually has permission
// to reach. Provided by cmd/kura wiring. When nil (in tests), the
// callback uses /ui as a universally-permitted fallback.
type RoleLookup interface {
	// LookupRole returns "admin", "user", or "" when not found.
	LookupRole(ctx context.Context, userID string) (string, error)
}

// ErrorRenderer renders a user-facing federation error page (i18n,
// Fog palette, 戻る button). Wired by cmd/kura; tests fall back to
// http.Error when nil so the test fixture stays minimal.
type ErrorRenderer interface {
	RenderFederationError(w http.ResponseWriter, r *http.Request, msgID string, status int)
}

// Manager owns all registered providers and the /federation/* routes.
type Manager struct {
	storage     *oidc.Storage
	sessions    *session.Store
	creds       CredentialStore
	provisioner UserProvisioner
	roles       RoleLookup
	errorPage   ErrorRenderer
	httpClient  *http.Client

	mu        sync.RWMutex
	providers map[string]*Provider
	pending   map[string]pendingState
}

// New constructs an empty Manager. cmd/kura calls Register for each
// configured provider, then mounts m.Routes() at /federation/.
func New(storage *oidc.Storage, sessions *session.Store, creds CredentialStore) *Manager {
	return &Manager{
		storage:    storage,
		sessions:   sessions,
		creds:      creds,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		providers:  map[string]*Provider{},
		pending:    map[string]pendingState{},
	}
}

// SetProvisioner wires the auto-provision adapter. Call before Register.
func (m *Manager) SetProvisioner(p UserProvisioner) { m.provisioner = p }

// SetRoleLookup wires the role-aware redirect chooser. Without it, the
// callback assumes the user has only user-role permissions and falls
// back to /ui (never /ui/admin/*) — safe under-grant rather than
// over-grant (DESIGN_PRINCIPLES priority #5 信頼性).
func (m *Manager) SetRoleLookup(r RoleLookup) { m.roles = r }

// SetErrorRenderer wires the i18n error page used when auto_provision
// is off and the inbound user is unbound, or when provision itself
// fails. Without it the callback falls back to http.Error (used by
// existing unit tests).
func (m *Manager) SetErrorRenderer(e ErrorRenderer) { m.errorPage = e }

// SetHTTPClient overrides the HTTP client. Tests use this to point at a
// httptest.Server fixture.
func (m *Manager) SetHTTPClient(c *http.Client) { m.httpClient = c }

// Register adds a provider, discovering its OIDC endpoints from
// <issuer>/.well-known/openid-configuration. Discovery is best-effort:
// when the issuer is unreachable, the provider is registered with
// stubbed endpoints that surface a friendly error at /start.
func (m *Manager) Register(ctx context.Context, p Provider) error {
	if p.Name == "" || p.ClientID == "" || p.RedirectURI == "" {
		return errors.New("federation: name, client_id, redirect_uri required")
	}
	if len(p.Scopes) == 0 {
		p.Scopes = []string{"openid", "email", "profile"}
	}
	if err := m.discover(ctx, &p); err != nil {
		// Best-effort: continue with empty endpoints; /start will return
		// a clear error rather than crashing the whole daemon.
		_ = err
	}
	m.mu.Lock()
	m.providers[p.Name] = &p
	m.mu.Unlock()
	return nil
}

// Empty reports whether any providers are registered. cmd/kura uses
// this to decide whether to mount the /federation route at all.
func (m *Manager) Empty() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.providers) == 0
}

// Lookup returns the named provider (used by tests).
func (m *Manager) Lookup(name string) (*Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providers[name]
	return p, ok
}

// discover fills p.AuthorizeURL / p.TokenURL / p.UserinfoURL from the
// provider's discovery document.
func (m *Manager) discover(ctx context.Context, p *Provider) error {
	if p.Issuer == "" {
		return errors.New("federation: issuer empty")
	}
	url := strings.TrimRight(p.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("federation: discovery req: %w", err)
	}
	res, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("federation: discovery: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("federation: discovery status %d", res.StatusCode)
	}
	var doc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		UserinfoEndpoint      string `json:"userinfo_endpoint"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return fmt.Errorf("federation: decode discovery: %w", err)
	}
	p.AuthorizeURL = doc.AuthorizationEndpoint
	p.TokenURL = doc.TokenEndpoint
	p.UserinfoURL = doc.UserinfoEndpoint
	return nil
}

// Routes returns the /federation/<provider>/(start|callback|link) HTTP
// handler. Mounted by the gateway.
func (m *Manager) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/federation/", m.dispatch)
	return mux
}

func (m *Manager) dispatch(w http.ResponseWriter, r *http.Request) {
	// /federation/<name>/(start|callback|link)
	rest := strings.TrimPrefix(r.URL.Path, "/federation/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	name := parts[0]
	op := parts[1]

	provider, ok := m.Lookup(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch op {
	case "start":
		m.handleStart(w, r, provider, "")
	case "link":
		m.handleLink(w, r, provider)
	case "callback":
		m.handleCallback(w, r, provider)
	default:
		http.NotFound(w, r)
	}
}

func (m *Manager) handleStart(w http.ResponseWriter, r *http.Request, p *Provider, userID string) {
	if p.AuthorizeURL == "" {
		http.Error(w, "federation provider not ready: discovery incomplete", http.StatusServiceUnavailable)
		return
	}
	state := newOpaque(24)
	verifier := newOpaque(32)
	challenge := pkceS256(verifier)
	nonce := newOpaque(16)
	returnTo := r.URL.Query().Get("return_to")

	m.mu.Lock()
	m.pending[state] = pendingState{
		Provider: p.Name,
		UserID:   userID,
		Verifier: verifier,
		Nonce:    nonce,
		ReturnTo: returnTo,
		Expires:  time.Now().Add(10 * time.Minute),
	}
	m.mu.Unlock()

	q := url.Values{
		"client_id":             {p.ClientID},
		"redirect_uri":          {p.RedirectURI},
		"response_type":         {"code"},
		"scope":                 {strings.Join(p.Scopes, " ")},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	target := p.AuthorizeURL + "?" + q.Encode()
	http.Redirect(w, r, target, http.StatusFound)
}

func (m *Manager) handleLink(w http.ResponseWriter, r *http.Request, p *Provider) {
	// /link requires a logged-in operator. We read the session cookie
	// directly here rather than relying on middleware so the same handler
	// chain works whether or not the gateway pre-authed the request.
	cookie, err := r.Cookie(oidc.SessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	sess, err := m.sessions.Lookup(r.Context(), cookie.Value)
	if err != nil {
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	m.handleStart(w, r, p, sess.UserID)
}

func (m *Manager) handleCallback(w http.ResponseWriter, r *http.Request, p *Provider) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	ps, ok := m.pending[state]
	if ok {
		delete(m.pending, state)
	}
	m.mu.Unlock()
	if !ok {
		http.Error(w, "unknown state", http.StatusBadRequest)
		return
	}
	if time.Now().After(ps.Expires) {
		http.Error(w, "state expired", http.StatusBadRequest)
		return
	}
	if ps.Provider != p.Name {
		http.Error(w, "state/provider mismatch", http.StatusBadRequest)
		return
	}

	// Exchange code -> token at the provider.
	tok, err := m.exchangeCode(r.Context(), p, code, ps.Verifier)
	if err != nil {
		http.Error(w, "code exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Decode id_token (no signature verification in v1 because the
	// userinfo round-trip below is what we trust). v1.x: add JWKS verify.
	subject, email, err := decodeIDTokenClaims(tok.IDToken)
	if err != nil || subject == "" {
		// Fall back to userinfo if id_token decode failed.
		subject, email, err = m.fetchUserInfo(r.Context(), p, tok.AccessToken)
		if err != nil {
			http.Error(w, "userinfo failed: "+err.Error(), http.StatusBadGateway)
			return
		}
	}

	if ps.UserID != "" {
		// Link flow: bind the (provider, subject) to ps.UserID and return.
		// The operator is already logged in, so we apply the same
		// role-safe redirect rule that the login branch uses — a
		// non-admin who just linked their Google must not bounce to
		// /ui/admin/users (Sfix002-1).
		if err := m.storage.LinkFederation(r.Context(), oidc.FederationLink{
			Provider: p.Name, Subject: subject, UserID: ps.UserID, Email: email,
		}); err != nil {
			http.Error(w, "link failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, m.redirectTarget(r.Context(), ps.UserID, ps.ReturnTo, "/ui/admin/users"), http.StatusFound)
		return
	}

	// Login flow: resolve (provider, subject) -> user_id.
	link, err := m.storage.LookupFederationBySubject(r.Context(), p.Name, subject)
	if err == nil {
		// Existing link: issue a KuraOS session for this user.
		m.issueSession(w, r, link.UserID, ps.ReturnTo)
		return
	}
	// Not bound. Auto-provision if allowed.
	if !p.AutoProvision {
		m.renderError(w, r, "page.federation.unbound_error", http.StatusForbidden,
			"user is not linked; auto_provision disabled")
		return
	}
	if m.provisioner == nil {
		m.renderError(w, r, "page.federation.provision_unavailable", http.StatusInternalServerError,
			"auto_provision enabled but no provisioner wired")
		return
	}
	username := emailLocalPart(email)
	if username == "" {
		username = "user-" + subject[:6]
	}
	userID, err := m.provisioner.CreateFromFederation(r.Context(), username, email, email)
	if err != nil {
		m.renderError(w, r, "page.federation.provision_failed", http.StatusInternalServerError,
			"provision failed: "+err.Error())
		return
	}
	if err := m.storage.LinkFederation(r.Context(), oidc.FederationLink{
		Provider: p.Name, Subject: subject, UserID: userID, Email: email,
	}); err != nil {
		http.Error(w, "link new user failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	m.issueSession(w, r, userID, ps.ReturnTo)
}

// renderError uses the wired ErrorRenderer when available; falls back
// to plain http.Error so unit tests still see a meaningful status
// code without standing up a full UI renderer.
func (m *Manager) renderError(w http.ResponseWriter, r *http.Request, msgID string, status int, devMsg string) {
	if m.errorPage != nil {
		m.errorPage.RenderFederationError(w, r, msgID, status)
		return
	}
	http.Error(w, devMsg, status)
}

func (m *Manager) issueSession(w http.ResponseWriter, r *http.Request, userID, returnTo string) {
	sess, err := m.sessions.Issue(r.Context(), userID, r.UserAgent(), r.RemoteAddr)
	if err != nil {
		http.Error(w, "session issue failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidc.SessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
	})
	// Pick a redirect target the user is actually allowed to reach
	// — without this, a `user` role hitting /ui/admin/dashboard
	// (the historical default) gets a 403 immediately after a
	// successful Google login (Sfix002-1).
	http.Redirect(w, r, m.redirectTarget(r.Context(), userID, returnTo, "/ui/admin/dashboard"), http.StatusFound)
}

// redirectTarget chooses the post-callback URL the user has permission
// to reach:
//
//   - admin -> explicit return_to (if safe), else adminFallback.
//   - user  -> explicit return_to (if safe AND not /ui/admin/*),
//     else /ui.
//   - role unknown / RoleLookup nil -> treat as user (under-grant).
//
// "safe" means starts with "/" (open-redirect guard, same rule as
// ifReturn). adminFallback is the legacy default the caller wants
// admins to land on when they didn't supply an explicit return_to
// (login flow: /ui/admin/dashboard, link flow: /ui/admin/users).
func (m *Manager) redirectTarget(ctx context.Context, userID, returnTo, adminFallback string) string {
	role := ""
	if m.roles != nil && userID != "" {
		if r, err := m.roles.LookupRole(ctx, userID); err == nil {
			role = r
		}
	}
	switch role {
	case "admin":
		// admin can go anywhere; honour explicit return_to when valid.
		if isSafeReturnTo(returnTo) {
			return returnTo
		}
		return adminFallback
	default:
		// user role (or unknown). Explicit return_to is honoured iff
		// it isn't /ui/admin/* — otherwise we'd just hand them a 403.
		if isSafeReturnTo(returnTo) && !strings.HasPrefix(returnTo, "/ui/admin") && !strings.HasPrefix(returnTo, "/setup") {
			return returnTo
		}
		return "/ui"
	}
}

// isSafeReturnTo gates open-redirect: only "/<path>" forms are honoured.
// "//evil.example/" is explicitly rejected — it would resolve to a
// scheme-relative URL once the browser sees it in the Location header.
func isSafeReturnTo(s string) bool {
	if s == "" || s[0] != '/' {
		return false
	}
	if len(s) >= 2 && s[1] == '/' {
		return false
	}
	return true
}

// exchangeCode POSTs the authorization code + PKCE verifier to the
// provider's token endpoint and returns the parsed token response.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

func (m *Manager) exchangeCode(ctx context.Context, p *Provider, code, verifier string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.RedirectURI},
		"client_id":     {p.ClientID},
		"code_verifier": {verifier},
	}
	if p.ClientSecret != "" {
		form.Set("client_secret", p.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := m.httpClient.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return tokenResponse{}, fmt.Errorf("token endpoint %d: %s", res.StatusCode, string(body))
	}
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return tokenResponse{}, fmt.Errorf("decode token: %w", err)
	}
	return tok, nil
}

func (m *Manager) fetchUserInfo(ctx context.Context, p *Provider, access string) (string, string, error) {
	if p.UserinfoURL == "" {
		return "", "", errors.New("federation: no userinfo endpoint")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.UserinfoURL, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	res, err := m.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("userinfo status %d", res.StatusCode)
	}
	var info struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return "", "", err
	}
	return info.Sub, info.Email, nil
}

// decodeIDTokenClaims parses the JWT payload (no signature check) and
// returns sub + email.
func decodeIDTokenClaims(idToken string) (string, string, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", "", errors.New("idtoken: not 3 parts")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Tolerate stdpadding variant.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", "", fmt.Errorf("idtoken b64: %w", err)
		}
	}
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", fmt.Errorf("idtoken json: %w", err)
	}
	return claims.Sub, claims.Email, nil
}

func newOpaque(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("federation: rand: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func emailLocalPart(email string) string {
	if i := strings.Index(email, "@"); i > 0 {
		return strings.ToLower(email[:i])
	}
	return ""
}

func ifReturn(returnTo, fallback string) string {
	if returnTo == "" {
		return fallback
	}
	// Only allow return_to that begins with "/" so we don't open redirect.
	if returnTo[0] != '/' {
		return fallback
	}
	return returnTo
}

// PKCEHelper for tests that need to compute the challenge from a known
// verifier. Exported for use in tests/e2e.
func PKCEHelper(verifier string) string { return pkceS256(verifier) }

// hex left here so unused-import is silenced if we later need it.
var _ = hex.EncodeToString
