package oidc

import (
	"sync"
	"time"
)

// accessTokenStore is an in-memory bearer token table. Tokens issued at
// /token live here until /userinfo consumes them or the process restarts.
//
// In-memory because: (a) access tokens are short-lived (1h), (b) RPs are
// expected to re-auth via the code flow on restart, (c) keeping the
// SQLite write rate down matters more than surviving a restart.
//
// Refresh tokens (which would need persistence) are intentionally not
// implemented in v1 — RPs that want long sessions ride the operator's
// KuraOS session cookie path.
type accessTokenStore struct {
	mu     sync.RWMutex
	tokens map[string]accessTokenEntry
	now    func() time.Time
}

type accessTokenEntry struct {
	UserID    string
	ExpiresAt time.Time
}

func newAccessTokenStore() *accessTokenStore {
	return &accessTokenStore{tokens: map[string]accessTokenEntry{}, now: time.Now}
}

// put stores token -> userID with the given expiry.
func (s *accessTokenStore) put(token, userID string, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = accessTokenEntry{UserID: userID, ExpiresAt: expiresAt}
}

// get returns (userID, true) iff the token exists and has not expired.
func (s *accessTokenStore) get(token string) (string, bool) {
	s.mu.RLock()
	entry, ok := s.tokens[token]
	s.mu.RUnlock()
	if !ok {
		return "", false
	}
	if !entry.ExpiresAt.IsZero() && s.now().UTC().After(entry.ExpiresAt) {
		s.mu.Lock()
		delete(s.tokens, token)
		s.mu.Unlock()
		return "", false
	}
	return entry.UserID, true
}

// purge drops every expired entry. Cheap to call from a periodic job.
func (s *accessTokenStore) purge() {
	cutoff := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, e := range s.tokens {
		if !e.ExpiresAt.IsZero() && cutoff.After(e.ExpiresAt) {
			delete(s.tokens, tok)
		}
	}
}
