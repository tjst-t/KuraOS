package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kuraos-org/kura/internal/store"
)

func newTestSessionStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// Seed a user row so the FK on sessions.user_id is satisfiable. The
	// foreign-key check is enforced via the _pragma in store.Open.
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO users (id, username, role, created_at, updated_at)
		VALUES ('u1', 'alice', 'admin',
		        strftime('%Y-%m-%dT%H:%M:%fZ','now'),
		        strftime('%Y-%m-%dT%H:%M:%fZ','now'))`)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return NewStore(st.DB())
}

func TestSession_IssueAndLookup(t *testing.T) {
	s := newTestSessionStore(t)
	ctx := context.Background()
	sess, err := s.Issue(ctx, "u1", "ua", "1.2.3.4")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if sess.ID == "" {
		t.Fatalf("Issue returned empty token")
	}
	got, err := s.Lookup(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.UserID != "u1" {
		t.Fatalf("UserID = %q", got.UserID)
	}
}

func TestSession_LookupExpired(t *testing.T) {
	s := newTestSessionStore(t)
	frozen := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	s = s.WithTTL(1 * time.Hour).WithClock(func() time.Time { return frozen })
	sess, err := s.Issue(context.Background(), "u1", "", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Move clock past expiry — Lookup must refuse.
	s = s.WithClock(func() time.Time { return frozen.Add(2 * time.Hour) })
	if _, err := s.Lookup(context.Background(), sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired Lookup err = %v, want ErrNotFound", err)
	}
}

func TestSession_RevokeMakesLookupFail(t *testing.T) {
	s := newTestSessionStore(t)
	ctx := context.Background()
	sess, err := s.Issue(ctx, "u1", "", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Revoke(ctx, sess.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := s.Lookup(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup after revoke = %v, want ErrNotFound", err)
	}
}

func TestSession_LookupUnknownToken(t *testing.T) {
	s := newTestSessionStore(t)
	if _, err := s.Lookup(context.Background(), "no-such-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := s.Lookup(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty err = %v, want ErrNotFound", err)
	}
}
