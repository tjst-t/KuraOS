// Package session issues and validates browser session cookies backed by the
// SQLite sessions table.
//
// Sessions are opaque random tokens (32 bytes, base64url) — not JWTs. VISION
// "tech_constraints_phase_1" calls for session cookies on UI / JWT on API; this
// package owns the UI half. Server-side opaque tokens make logout actually
// invalidate (impossible with stateless JWT) and avoid the JWT key-rotation
// surface area entirely. JWT for the /api surface lands in a later sprint.
package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// CookieName is the canonical cookie key. Must match what middleware reads
// and what /logout clears.
const CookieName = "kura_session"

// DefaultTTL is the session lifetime. Phase-1 NAS: 7 days strikes a balance
// between not asking again every browser restart and bounded blast radius if
// a cookie leaks. Refresh-on-activity lands in a later sprint.
const DefaultTTL = 7 * 24 * time.Hour

// ErrNotFound is returned by Lookup when a token has no live row, including
// when the row exists but expired or was revoked.
var ErrNotFound = errors.New("session: not found")

// Session is the in-memory representation of a sessions row. Most callers
// only care about UserID; the rest is for /ui/admin/sessions audit views.
type Session struct {
	ID         string
	UserID     string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	UserAgent  string
	RemoteAddr string
}

// Store backs the package's exported functions. Tests inject a custom now()
// so expiry boundaries can be exercised without time.Sleep.
type Store struct {
	db  *sql.DB
	now func() time.Time
	ttl time.Duration
	rng func([]byte) (int, error)
}

// NewStore returns a Store with default TTL and crypto/rand for token bytes.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now, ttl: DefaultTTL, rng: rand.Read}
}

// WithTTL returns a copy of s with a different TTL. Useful in tests.
func (s *Store) WithTTL(ttl time.Duration) *Store {
	c := *s
	c.ttl = ttl
	return &c
}

// WithClock returns a copy of s with the supplied clock. Tests use this to
// freeze "now" so expiry assertions are deterministic.
func (s *Store) WithClock(now func() time.Time) *Store {
	c := *s
	c.now = now
	return &c
}

// Issue creates a new session for userID and returns the opaque token plus
// the row. The token is the cookie value; it is *not* persisted directly —
// only the token (which equals the row id) is what we look up by.
func (s *Store) Issue(ctx context.Context, userID, userAgent, remoteAddr string) (Session, error) {
	if userID == "" {
		return Session{}, errors.New("session: empty user id")
	}
	tok, err := s.randomToken()
	if err != nil {
		return Session{}, err
	}
	now := s.now().UTC()
	expires := now.Add(s.ttl)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, issued_at, expires_at, revoked_at, user_agent, remote_addr)
		VALUES (?, ?, ?, ?, NULL, ?, ?)
	`, tok, userID, now.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano), userAgent, remoteAddr)
	if err != nil {
		return Session{}, fmt.Errorf("session: insert: %w", err)
	}
	return Session{
		ID:         tok,
		UserID:     userID,
		IssuedAt:   now,
		ExpiresAt:  expires,
		UserAgent:  userAgent,
		RemoteAddr: remoteAddr,
	}, nil
}

// Lookup resolves a cookie token to a live Session. Expired or revoked rows
// return ErrNotFound — the caller should treat them indistinguishably from
// "no row" (DESIGN_PRINCIPLES priority #5: 信頼性 — never serve a request
// with an invalid session).
func (s *Store) Lookup(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, issued_at, expires_at, user_agent, remote_addr
		FROM sessions
		WHERE id = ? AND revoked_at IS NULL
	`, token)
	var (
		sess            Session
		issued, expires string
	)
	err := row.Scan(&sess.ID, &sess.UserID, &issued, &expires, &sess.UserAgent, &sess.RemoteAddr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("session: scan: %w", err)
	}
	if t, perr := time.Parse(time.RFC3339Nano, issued); perr == nil {
		sess.IssuedAt = t
	}
	if t, perr := time.Parse(time.RFC3339Nano, expires); perr == nil {
		sess.ExpiresAt = t
	}
	if !sess.ExpiresAt.IsZero() && s.now().UTC().After(sess.ExpiresAt) {
		return Session{}, ErrNotFound
	}
	return sess, nil
}

// Revoke marks the session row revoked. Subsequent Lookup calls return
// ErrNotFound so the cookie is dead immediately, even if the user's browser
// keeps presenting it.
func (s *Store) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL
	`, s.now().UTC().Format(time.RFC3339Nano), token)
	if err != nil {
		return fmt.Errorf("session: revoke: %w", err)
	}
	return nil
}

func (s *Store) randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := s.rng(b); err != nil {
		return "", fmt.Errorf("session: read rng: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// TTL returns the configured session lifetime so HTTP handlers can set
// matching Max-Age on the cookie.
func (s *Store) TTL() time.Duration { return s.ttl }
