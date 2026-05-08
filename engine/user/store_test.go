package user

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kuraos-org/kura/internal/store"
)

// fakeHasher is a deterministic, fast Hasher used by store tests so the
// argon2id cost params don't slow the unit suite down.
type fakeHasher struct{}

func (fakeHasher) Hash(pw string) (string, error)          { return "fake$" + pw, nil }
func (fakeHasher) Verify(pw, encoded string) (bool, error) { return encoded == "fake$"+pw, nil }

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewStore(st.DB(), fakeHasher{})
}

// [AC-S1e7eeb-1-1] User / Group / AuthMethod テーブルが migration で作られ、
// admin/user ロールが扱える.
func TestStore_CreateLocalUser_AdminAndUserRoles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name string
		role Role
	}{
		{"admin", RoleAdmin},
		{"user", RoleUser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, err := s.CreateLocalUser(ctx, c.name, c.name+" display", "pw-"+c.name, c.role)
			if err != nil {
				t.Fatalf("CreateLocalUser: %v", err)
			}
			if u.Role != c.role {
				t.Fatalf("role = %q, want %q", u.Role, c.role)
			}
			if u.Username != c.name {
				t.Fatalf("username = %q, want %q", u.Username, c.name)
			}
			if u.ID == "" {
				t.Fatalf("ID is empty")
			}
		})
	}
}

func TestStore_CreateLocalUser_RejectsInvalidRole(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateLocalUser(context.Background(), "x", "", "pw", Role("root")); err == nil {
		t.Fatalf("expected error for invalid role")
	}
}

func TestStore_CreateLocalUser_DuplicateUsernameRejected(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateLocalUser(ctx, "alice", "", "pw", RoleUser); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := s.CreateLocalUser(ctx, "ALICE", "", "pw", RoleUser)
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("err = %v, want ErrUsernameTaken (case-insensitive uniqueness)", err)
	}
}

func TestStore_GetByUsername_NotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetByUsername(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestStore_VerifyPassword(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateLocalUser(ctx, "bob", "Bob", "secret", RoleAdmin); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("correct password", func(t *testing.T) {
		u, ok, err := s.VerifyPassword(ctx, "bob", "secret")
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if !ok || u.Username != "bob" {
			t.Fatalf("expected ok user, got ok=%v u=%+v", ok, u)
		}
	})
	t.Run("wrong password", func(t *testing.T) {
		_, ok, err := s.VerifyPassword(ctx, "bob", "nope")
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if ok {
			t.Fatalf("expected ok=false for wrong password")
		}
	})
	t.Run("unknown user", func(t *testing.T) {
		_, ok, err := s.VerifyPassword(ctx, "ghost", "anything")
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if ok {
			t.Fatalf("expected ok=false for unknown user")
		}
	})
}

func TestStore_CountByRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	n, err := s.CountByRole(ctx, RoleAdmin)
	if err != nil || n != 0 {
		t.Fatalf("initial admin count = %d err=%v", n, err)
	}
	if _, err := s.CreateLocalUser(ctx, "root", "", "pw", RoleAdmin); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CreateLocalUser(ctx, "alice", "", "pw", RoleUser); err != nil {
		t.Fatalf("create: %v", err)
	}
	n, err = s.CountByRole(ctx, RoleAdmin)
	if err != nil {
		t.Fatalf("CountByRole: %v", err)
	}
	if n != 1 {
		t.Fatalf("admin count = %d, want 1", n)
	}
}
