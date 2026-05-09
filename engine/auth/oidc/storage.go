// Package oidc implements the in-house OIDC OpenID Provider that legacy
// (forward_auth) and native (oidc) KuraOS apps target for SSO.
//
// Why in-house instead of zitadel/oidc/v3 (named in VISION):
//
//   - DESIGN_PRINCIPLES priority #6 puts a hard limit on dragging in
//     external libraries that are not the differentiator. zitadel/oidc/v3's
//     transitive tree (chi router, go-jose, schema, x/text/language, rs/cors)
//     more than doubles the kura binary.
//   - The S65b510 sprint already set the precedent (LocalVerifier swap of
//     cosign keyless) of pragmatically substituting a 600-LoC in-house
//     surface for a heavyweight external library when the AC contract is
//     small.
//   - Every endpoint we need (.well-known, authorize, token, userinfo, jwks,
//     end_session) is well-specified by OpenID Connect Core 1.0 — no novel
//     protocol behaviour to reimplement.
//
// Backlog: replace with zitadel/oidc/v3 in v1.x when the registry demands
// device flow, JAR/PAR, or richer client_credentials patterns.
package oidc

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AuthCodeTTL is the lifetime of an authorization code from issue to
// /token consumption. The OIDC spec recommends "short" — 60 seconds is
// what zitadel and dex default to.
const AuthCodeTTL = 60 * time.Second

// AccessTokenTTL is the OAuth2 access token lifetime. KuraOS apps live
// behind the gateway and can refresh trivially via session, so 1h is
// safe.
const AccessTokenTTL = time.Hour

// IDTokenTTL is the OIDC ID token lifetime. Matches access token by
// default; individual apps can re-validate via /userinfo.
const IDTokenTTL = time.Hour

// Errors callers want to detect specifically.
var (
	ErrClientNotFound     = errors.New("oidc: client not found")
	ErrAuthCodeNotFound   = errors.New("oidc: auth code not found")
	ErrAuthCodeConsumed   = errors.New("oidc: auth code already consumed")
	ErrAuthCodeExpired    = errors.New("oidc: auth code expired")
	ErrInvalidRedirectURI = errors.New("oidc: redirect_uri not registered")
	ErrInvalidClientAuth  = errors.New("oidc: client authentication failed")
	ErrInvalidPKCE        = errors.New("oidc: PKCE verification failed")
)

// Client is the registered relying party. Sensitive fields (client_secret)
// live in the credential vault, never in this struct or the table.
type Client struct {
	ClientID         string
	Name             string
	RedirectURIs     []string
	GrantTypes       []string
	ResponseTypes    []string
	Scopes           []string
	TokenEndpointAuth string
	AppID            string // empty when not tied to an app install
	CreatedAt        time.Time
}

// AuthCode is the in-memory representation of an issued authorization
// code awaiting /token exchange.
type AuthCode struct {
	Code                string
	ClientID            string
	UserID              string
	RedirectURI         string
	Scope               string
	Nonce               string
	CodeChallenge       string
	CodeChallengeMethod string
	IssuedAt            time.Time
	ExpiresAt           time.Time
	ConsumedAt          *time.Time
}

// Storage is the SQLite-backed state owner. Constructed once per process
// from cmd/kura main.
type Storage struct {
	db  *sql.DB
	now func() time.Time
}

// NewStorage returns a Storage over the supplied DB handle. db is the
// shared *sql.DB owned by internal/store.Store.
func NewStorage(db *sql.DB) *Storage {
	return &Storage{db: db, now: time.Now}
}

// WithClock returns a copy of s with a custom clock. Tests use this to
// freeze "now" so expiry assertions are deterministic.
func (s *Storage) WithClock(now func() time.Time) *Storage {
	c := *s
	c.now = now
	return &c
}

// RegisterClient upserts a client row. Returns the inserted Client (with
// CreatedAt populated) or any underlying SQL error. Callers must store
// the secret separately via engine/system.SetCredential so this method
// stays oblivious to the credential vault.
func (s *Storage) RegisterClient(ctx context.Context, c Client) (Client, error) {
	if c.ClientID == "" {
		return Client{}, errors.New("oidc: client_id is required")
	}
	if len(c.RedirectURIs) == 0 {
		return Client{}, errors.New("oidc: at least one redirect_uri is required")
	}
	if len(c.GrantTypes) == 0 {
		c.GrantTypes = []string{"authorization_code"}
	}
	if len(c.ResponseTypes) == 0 {
		c.ResponseTypes = []string{"code"}
	}
	if len(c.Scopes) == 0 {
		c.Scopes = []string{"openid", "profile", "email"}
	}
	if c.TokenEndpointAuth == "" {
		c.TokenEndpointAuth = "client_secret_basic"
	}
	redirectsJSON, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return Client{}, fmt.Errorf("oidc: marshal redirects: %w", err)
	}
	grantsJSON, _ := json.Marshal(c.GrantTypes)
	respJSON, _ := json.Marshal(c.ResponseTypes)
	scopesJSON, _ := json.Marshal(c.Scopes)
	if c.CreatedAt.IsZero() {
		c.CreatedAt = s.now().UTC()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO oidc_clients
		    (client_id, name, redirect_uris, grant_types, response_types, scopes,
		     token_endpoint_auth, app_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(client_id) DO UPDATE SET
		    name = excluded.name,
		    redirect_uris = excluded.redirect_uris,
		    grant_types = excluded.grant_types,
		    response_types = excluded.response_types,
		    scopes = excluded.scopes,
		    token_endpoint_auth = excluded.token_endpoint_auth,
		    app_id = excluded.app_id
	`, c.ClientID, c.Name, string(redirectsJSON), string(grantsJSON),
		string(respJSON), string(scopesJSON), c.TokenEndpointAuth, c.AppID,
		c.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return Client{}, fmt.Errorf("oidc: register client: %w", err)
	}
	return c, nil
}

// LookupClient returns the registered client for clientID.
func (s *Storage) LookupClient(ctx context.Context, clientID string) (Client, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT client_id, name, redirect_uris, grant_types, response_types, scopes,
		       token_endpoint_auth, app_id, created_at
		FROM oidc_clients WHERE client_id = ?
	`, clientID)
	var c Client
	var redirects, grants, resp, scopes, createdAt, appID string
	err := row.Scan(&c.ClientID, &c.Name, &redirects, &grants, &resp, &scopes,
		&c.TokenEndpointAuth, &appID, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Client{}, ErrClientNotFound
		}
		return Client{}, fmt.Errorf("oidc: lookup client: %w", err)
	}
	c.AppID = appID
	_ = json.Unmarshal([]byte(redirects), &c.RedirectURIs)
	_ = json.Unmarshal([]byte(grants), &c.GrantTypes)
	_ = json.Unmarshal([]byte(resp), &c.ResponseTypes)
	_ = json.Unmarshal([]byte(scopes), &c.Scopes)
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		c.CreatedAt = t
	}
	return c, nil
}

// ListClients returns every registered client ordered by created_at.
// Used by the Users UI / OIDC clients tab.
func (s *Storage) ListClients(ctx context.Context) ([]Client, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT client_id, name, redirect_uris, grant_types, response_types, scopes,
		       token_endpoint_auth, app_id, created_at
		FROM oidc_clients ORDER BY created_at, client_id
	`)
	if err != nil {
		return nil, fmt.Errorf("oidc: list clients: %w", err)
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		var c Client
		var redirects, grants, resp, scopes, createdAt, appID string
		if err := rows.Scan(&c.ClientID, &c.Name, &redirects, &grants, &resp, &scopes,
			&c.TokenEndpointAuth, &appID, &createdAt); err != nil {
			return nil, fmt.Errorf("oidc: scan client: %w", err)
		}
		c.AppID = appID
		_ = json.Unmarshal([]byte(redirects), &c.RedirectURIs)
		_ = json.Unmarshal([]byte(grants), &c.GrantTypes)
		_ = json.Unmarshal([]byte(resp), &c.ResponseTypes)
		_ = json.Unmarshal([]byte(scopes), &c.Scopes)
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			c.CreatedAt = t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteClient removes a client by ID. Tolerates missing rows.
func (s *Storage) DeleteClient(ctx context.Context, clientID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oidc_clients WHERE client_id = ?`, clientID)
	if err != nil {
		return fmt.Errorf("oidc: delete client: %w", err)
	}
	return nil
}

// IssueAuthCode generates a one-shot authorization code and persists it.
// The returned code value is what gets sent to the RP via 302; the RP
// later POSTs it to /token to redeem.
func (s *Storage) IssueAuthCode(ctx context.Context, code AuthCode) (AuthCode, error) {
	if code.Code == "" {
		code.Code = newOpaqueToken(24)
	}
	now := s.now().UTC()
	if code.IssuedAt.IsZero() {
		code.IssuedAt = now
	}
	if code.ExpiresAt.IsZero() {
		code.ExpiresAt = now.Add(AuthCodeTTL)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO oidc_auth_codes
		    (code, client_id, user_id, redirect_uri, scope, nonce,
		     code_challenge, code_challenge_method, issued_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, code.Code, code.ClientID, code.UserID, code.RedirectURI, code.Scope,
		code.Nonce, code.CodeChallenge, code.CodeChallengeMethod,
		code.IssuedAt.Format(time.RFC3339Nano),
		code.ExpiresAt.Format(time.RFC3339Nano))
	if err != nil {
		return AuthCode{}, fmt.Errorf("oidc: issue code: %w", err)
	}
	return code, nil
}

// ConsumeAuthCode atomically retrieves an unconsumed code and marks it
// consumed. Returns ErrAuthCodeNotFound / ErrAuthCodeExpired /
// ErrAuthCodeConsumed for the obvious failure modes.
func (s *Storage) ConsumeAuthCode(ctx context.Context, code string) (AuthCode, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AuthCode{}, fmt.Errorf("oidc: begin: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT code, client_id, user_id, redirect_uri, scope, nonce,
		       code_challenge, code_challenge_method, issued_at, expires_at, consumed_at
		FROM oidc_auth_codes WHERE code = ?
	`, code)
	var ac AuthCode
	var issued, expires string
	var consumed sql.NullString
	err = row.Scan(&ac.Code, &ac.ClientID, &ac.UserID, &ac.RedirectURI, &ac.Scope,
		&ac.Nonce, &ac.CodeChallenge, &ac.CodeChallengeMethod,
		&issued, &expires, &consumed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthCode{}, ErrAuthCodeNotFound
		}
		return AuthCode{}, fmt.Errorf("oidc: scan code: %w", err)
	}
	if consumed.Valid && consumed.String != "" {
		return AuthCode{}, ErrAuthCodeConsumed
	}
	if t, err := time.Parse(time.RFC3339Nano, issued); err == nil {
		ac.IssuedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, expires); err == nil {
		ac.ExpiresAt = t
	}
	now := s.now().UTC()
	if !ac.ExpiresAt.IsZero() && now.After(ac.ExpiresAt) {
		return AuthCode{}, ErrAuthCodeExpired
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE oidc_auth_codes SET consumed_at = ? WHERE code = ?
	`, now.Format(time.RFC3339Nano), code); err != nil {
		return AuthCode{}, fmt.Errorf("oidc: mark consumed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AuthCode{}, fmt.Errorf("oidc: commit: %w", err)
	}
	consumedAt := now
	ac.ConsumedAt = &consumedAt
	return ac, nil
}

// PurgeExpired drops auth codes that have been expired for more than 5
// minutes (keep a small grace window so idempotent retries surface as
// ErrAuthCodeConsumed instead of ErrAuthCodeNotFound).
func (s *Storage) PurgeExpired(ctx context.Context) error {
	cutoff := s.now().UTC().Add(-5 * time.Minute).Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `DELETE FROM oidc_auth_codes WHERE expires_at < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("oidc: purge expired: %w", err)
	}
	return nil
}

// FederationLink represents one row of federation_links.
type FederationLink struct {
	Provider string
	Subject  string
	UserID   string
	Email    string
	LinkedAt time.Time
}

// LinkFederation upserts the (provider, subject) -> user_id mapping. Used
// by the federation callback after a successful external IdP login.
func (s *Storage) LinkFederation(ctx context.Context, link FederationLink) error {
	if link.Provider == "" || link.Subject == "" || link.UserID == "" {
		return errors.New("oidc: link requires provider, subject, user_id")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO federation_links (provider, subject, user_id, email)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(provider, subject) DO UPDATE SET
		    user_id = excluded.user_id,
		    email = excluded.email
	`, link.Provider, link.Subject, link.UserID, link.Email)
	if err != nil {
		return fmt.Errorf("oidc: link federation: %w", err)
	}
	return nil
}

// LookupFederationByUser returns the link for (provider, user_id). Used
// by the Users UI to render "Google: alice@gmail.com" badges.
func (s *Storage) LookupFederationByUser(ctx context.Context, provider, userID string) (FederationLink, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT provider, subject, user_id, email, linked_at
		FROM federation_links WHERE provider = ? AND user_id = ?
	`, provider, userID)
	var l FederationLink
	var linkedAt string
	err := row.Scan(&l.Provider, &l.Subject, &l.UserID, &l.Email, &linkedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FederationLink{}, sql.ErrNoRows
		}
		return FederationLink{}, fmt.Errorf("oidc: lookup federation: %w", err)
	}
	if t, err := time.Parse(time.RFC3339Nano, linkedAt); err == nil {
		l.LinkedAt = t
	}
	return l, nil
}

// LookupFederationBySubject resolves (provider, subject) to the linked
// user. Used by the federation callback to find the KuraOS user that
// previously linked this external account.
func (s *Storage) LookupFederationBySubject(ctx context.Context, provider, subject string) (FederationLink, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT provider, subject, user_id, email, linked_at
		FROM federation_links WHERE provider = ? AND subject = ?
	`, provider, subject)
	var l FederationLink
	var linkedAt string
	err := row.Scan(&l.Provider, &l.Subject, &l.UserID, &l.Email, &linkedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FederationLink{}, sql.ErrNoRows
		}
		return FederationLink{}, fmt.Errorf("oidc: lookup federation by subject: %w", err)
	}
	if t, err := time.Parse(time.RFC3339Nano, linkedAt); err == nil {
		l.LinkedAt = t
	}
	return l, nil
}

// ListFederationsForUser returns every link for a given user. Used by
// the Users UI to display all bound external IdPs.
func (s *Storage) ListFederationsForUser(ctx context.Context, userID string) ([]FederationLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, subject, user_id, email, linked_at
		FROM federation_links WHERE user_id = ? ORDER BY provider
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("oidc: list federations: %w", err)
	}
	defer rows.Close()
	var out []FederationLink
	for rows.Next() {
		var l FederationLink
		var linkedAt string
		if err := rows.Scan(&l.Provider, &l.Subject, &l.UserID, &l.Email, &linkedAt); err != nil {
			return nil, fmt.Errorf("oidc: scan federation: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, linkedAt); err == nil {
			l.LinkedAt = t
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// UnlinkFederation drops a (provider, user_id) link. Used by the Users
// UI "解除" button.
func (s *Storage) UnlinkFederation(ctx context.Context, provider, userID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM federation_links WHERE provider = ? AND user_id = ?
	`, provider, userID)
	if err != nil {
		return fmt.Errorf("oidc: unlink: %w", err)
	}
	return nil
}

// newOpaqueToken returns a base64url-encoded random byte string of length
// nBytes. Used for auth codes and access tokens.
func newOpaqueToken(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic("oidc: rand.Read: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewClientSecret returns a 32-byte hex random secret. Used by the app
// installer when registering a client_id automatically.
func NewClientSecret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("oidc: rand.Read: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// SplitScope splits an OAuth2 scope string ("openid profile email") into
// its tokens. Tolerates extra whitespace and empty entries.
func SplitScope(s string) []string {
	parts := strings.Fields(s)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
