package oidc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SessionResolver is the gateway-side principal lookup we need at
// /authorize. The OIDC OP only proceeds when the operator already has a
// live KuraOS session.
type SessionResolver interface {
	ResolveSession(ctx context.Context, cookie string) (userID string, ok bool, err error)
}

// UserInfoLookup returns the userinfo claims for a userID. Implemented
// by engine/user adapter in cmd/kura.
type UserInfoLookup interface {
	GetClaims(ctx context.Context, userID string) (map[string]any, error)
}

// UserRoleLookup resolves the role string ("admin", "user", "pending", …) for
// a userID. Used at /authorize to gate pending users before an auth-code is
// issued. Optional — if nil, no role check is performed.
type UserRoleLookup interface {
	LookupRole(ctx context.Context, userID string) (string, error)
}

// SessionCookieName is the cookie key the OP reads to identify the
// operator. Matches engine/auth/session.CookieName but redeclared here so
// engine/auth/oidc does not import engine/auth/session (avoids a cycle
// when session later imports oidc for federation linking).
const SessionCookieName = "kura_session"

// Provider is the wired OP. Built from the storage + signing key + the
// integration shims.
type Provider struct {
	Issuer     string
	Storage    *Storage
	SigningKey *SigningKey
	Sessions   SessionResolver
	Users      UserInfoLookup
	// RoleLookup is optional. When set, /authorize rejects users whose role
	// is "pending" — they must be promoted by an admin before they may
	// obtain OIDC tokens.
	RoleLookup UserRoleLookup
	// SecretLookup translates a client_id into the vault-stored secret.
	// MUST be set in production: nil makes /token reject every confidential
	// client with invalid_client. Tests with public-only clients can leave
	// it nil.
	SecretLookup func(ctx context.Context, clientID string) (string, error)

	// accessTokens maps opaque access tokens issued at /token to
	// (user_id, expires_at) tuples. In-memory: tokens never outlive the
	// process — RPs that need long-lived sessions use refresh_tokens
	// (not implemented in v1, backlog).
	accessTokens *accessTokenStore
}

// New returns a Provider. issuer is the public base URL ("https://nas.local").
func New(issuer string, st *Storage, key *SigningKey, sess SessionResolver, users UserInfoLookup) *Provider {
	return &Provider{
		Issuer:       strings.TrimRight(issuer, "/"),
		Storage:      st,
		SigningKey:   key,
		Sessions:     sess,
		Users:        users,
		accessTokens: newAccessTokenStore(),
	}
}

// Routes returns the /oidc/* mux. The gateway mounts it under /oidc/.
func (p *Provider) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/oidc/.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("/oidc/authorize", p.handleAuthorize)
	mux.HandleFunc("/oidc/token", p.handleToken)
	mux.HandleFunc("/oidc/userinfo", p.handleUserinfo)
	mux.HandleFunc("/oidc/jwks.json", p.handleJWKS)
	mux.HandleFunc("/oidc/end_session", p.handleEndSession)
	return mux
}

// --- Discovery -------------------------------------------------------------

func (p *Provider) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	doc := map[string]any{
		"issuer":                                p.Issuer,
		"authorization_endpoint":                p.Issuer + "/oidc/authorize",
		"token_endpoint":                        p.Issuer + "/oidc/token",
		"userinfo_endpoint":                     p.Issuer + "/oidc/userinfo",
		"jwks_uri":                              p.Issuer + "/oidc/jwks.json",
		"end_session_endpoint":                  p.Issuer + "/oidc/end_session",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{SigningAlg},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"plain", "S256"},
		"claims_supported": []string{
			"sub", "iss", "aud", "exp", "iat", "auth_time", "nonce",
			"email", "email_verified", "name", "preferred_username",
		},
	}
	writeJSON(w, http.StatusOK, doc)
}

// --- Authorize -------------------------------------------------------------

func (p *Provider) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	// Required params per OIDC core.
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	responseType := q.Get("response_type")
	scope := q.Get("scope")
	state := q.Get("state")
	nonce := q.Get("nonce")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")

	if clientID == "" || redirectURI == "" || responseType == "" {
		http.Error(w, "missing required parameter", http.StatusBadRequest)
		return
	}
	if responseType != "code" {
		http.Error(w, "unsupported response_type (only 'code')", http.StatusBadRequest)
		return
	}

	client, err := p.Storage.LookupClient(r.Context(), clientID)
	if err != nil {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if !redirectURIRegistered(client.RedirectURIs, redirectURI) {
		http.Error(w, "redirect_uri not registered", http.StatusBadRequest)
		return
	}

	// Resolve the operator. Without a live KuraOS session we 302 to /login
	// preserving the current /oidc/authorize URL so the post-login redirect
	// brings the operator back to consent.
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || p.Sessions == nil {
		redirectToLogin(w, r)
		return
	}
	userID, ok, err := p.Sessions.ResolveSession(r.Context(), cookie.Value)
	if err != nil {
		http.Error(w, "session lookup failed", http.StatusInternalServerError)
		return
	}
	if !ok {
		redirectToLogin(w, r)
		return
	}

	// Pending users must not obtain OIDC tokens — they are inert until an
	// admin promotes them. Redirect to /login with error=access_denied.
	if p.RoleLookup != nil {
		role, roleErr := p.RoleLookup.LookupRole(r.Context(), userID)
		if roleErr == nil && role == "pending" {
			http.Redirect(w, r, "/login?error=access_denied", http.StatusFound)
			return
		}
	}

	// Auto-approve consent for trusted clients (auth.mode=oidc apps the
	// operator just installed). Multi-tenant deployments would render a
	// consent screen here; the v1 NAS doesn't.
	code, err := p.Storage.IssueAuthCode(r.Context(), AuthCode{
		ClientID:            clientID,
		UserID:              userID,
		RedirectURI:         redirectURI,
		Scope:               scope,
		Nonce:               nonce,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
	})
	if err != nil {
		http.Error(w, "issue code: "+err.Error(), http.StatusInternalServerError)
		return
	}

	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect_uri: "+err.Error(), http.StatusBadRequest)
		return
	}
	qq := target.Query()
	qq.Set("code", code.Code)
	if state != "" {
		qq.Set("state", state)
	}
	target.RawQuery = qq.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	loginURL := "/login?return_to=" + url.QueryEscape(r.URL.RequestURI())
	http.Redirect(w, r, loginURL, http.StatusFound)
}

// --- Token -----------------------------------------------------------------

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	IDToken     string `json:"id_token,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

func (p *Provider) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "parse form")
		return
	}
	grantType := r.PostForm.Get("grant_type")
	if grantType != "authorization_code" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code")
		return
	}
	codeStr := r.PostForm.Get("code")
	redirectURI := r.PostForm.Get("redirect_uri")
	codeVerifier := r.PostForm.Get("code_verifier")

	clientID, clientSecret := extractClientCredentials(r)
	if clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "missing client_id")
		return
	}
	client, err := p.Storage.LookupClient(r.Context(), clientID)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}

	// Verify the secret if the client is confidential.
	if client.TokenEndpointAuth != "none" {
		expected, err := p.lookupClientSecret(r.Context(), clientID)
		if err != nil {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "missing secret")
			return
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(clientSecret)) != 1 {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client auth failed")
			return
		}
	}

	code, err := p.Storage.ConsumeAuthCode(r.Context(), codeStr)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		return
	}
	if code.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code/client mismatch")
		return
	}
	if code.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}
	if code.CodeChallenge != "" {
		if err := verifyPKCE(code.CodeChallenge, code.CodeChallengeMethod, codeVerifier); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE failed")
			return
		}
	}

	// Build claims.
	now := time.Now().UTC()
	claims := map[string]any{
		"iss":       p.Issuer,
		"sub":       code.UserID,
		"aud":       client.ClientID,
		"exp":       now.Add(IDTokenTTL).Unix(),
		"iat":       now.Unix(),
		"auth_time": now.Unix(),
	}
	if code.Nonce != "" {
		claims["nonce"] = code.Nonce
	}
	// Pull profile / email claims from the user lookup so the id_token
	// carries them (RPs can avoid /userinfo for the common case).
	if p.Users != nil {
		if extra, err := p.Users.GetClaims(r.Context(), code.UserID); err == nil {
			for k, v := range extra {
				claims[k] = v
			}
		}
	}
	idToken, err := p.SigningKey.SignIDToken(claims)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "sign id_token")
		return
	}

	access := newOpaqueToken(32)
	p.accessTokens.put(access, code.UserID, now.Add(AccessTokenTTL))

	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken: access,
		TokenType:   "Bearer",
		ExpiresIn:   int(AccessTokenTTL.Seconds()),
		IDToken:     idToken,
		Scope:       code.Scope,
	})
}

// extractClientCredentials returns (client_id, client_secret) reading
// from Basic auth first, then form body.
func extractClientCredentials(r *http.Request) (string, string) {
	if id, secret, ok := r.BasicAuth(); ok {
		return id, secret
	}
	return r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
}

// lookupClientSecret defers to the provider's vault adapter via the
// SecretLookup hook field. Defined as a method so /token can call it
// without engine/auth/oidc importing engine/system directly.
func (p *Provider) lookupClientSecret(ctx context.Context, clientID string) (string, error) {
	if p.SecretLookup == nil {
		return "", errors.New("oidc: secret lookup not wired")
	}
	return p.SecretLookup(ctx, clientID)
}

// --- Userinfo --------------------------------------------------------------

func (p *Provider) handleUserinfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		w.Header().Set("WWW-Authenticate", `Bearer realm="kuraos"`)
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	token := strings.TrimSpace(auth[len("Bearer "):])
	userID, ok := p.accessTokens.get(token)
	if !ok {
		http.Error(w, "invalid_token", http.StatusUnauthorized)
		return
	}
	claims := map[string]any{"sub": userID}
	if p.Users != nil {
		if extra, err := p.Users.GetClaims(r.Context(), userID); err == nil {
			for k, v := range extra {
				claims[k] = v
			}
		}
	}
	writeJSON(w, http.StatusOK, claims)
}

// --- JWKS / End session ----------------------------------------------------

func (p *Provider) handleJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, p.SigningKey.JWKS())
}

func (p *Provider) handleEndSession(w http.ResponseWriter, r *http.Request) {
	postLogout := r.URL.Query().Get("post_logout_redirect_uri")
	if postLogout == "" {
		postLogout = "/"
	}
	// Clear session cookie (client-side only; the actual session row is
	// dropped by /logout).
	http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, postLogout, http.StatusFound)
}

// --- Helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func redirectURIRegistered(registered []string, candidate string) bool {
	for _, r := range registered {
		if r == candidate {
			return true
		}
	}
	return false
}

// verifyPKCE accepts both "plain" and "S256" methods, defaulting to
// plain when no method was supplied.
func verifyPKCE(challenge, method, verifier string) error {
	if verifier == "" {
		return ErrInvalidPKCE
	}
	switch method {
	case "", "plain":
		if subtle.ConstantTimeCompare([]byte(challenge), []byte(verifier)) != 1 {
			return ErrInvalidPKCE
		}
		return nil
	case "S256":
		hash := sha256.Sum256([]byte(verifier))
		if subtle.ConstantTimeCompare([]byte(challenge), []byte(base64.RawURLEncoding.EncodeToString(hash[:]))) != 1 {
			return ErrInvalidPKCE
		}
		return nil
	default:
		return fmt.Errorf("oidc: unsupported PKCE method %q", method)
	}
}
