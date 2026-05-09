package system

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kuraos-org/kura/internal/cmdexec"
	"github.com/kuraos-org/kura/internal/store"
)

// fakeHasher is a deterministic stub Hasher used by tests so we don't pay
// the argon2id cost on every assertion. Same idea as fastHasher in
// tests/acceptance.
type fakeHasher struct{}

func (fakeHasher) Hash(pw string) (string, error) { return "fake$" + pw, nil }

// permissiveExec is an Executor variant that swallows every call and
// returns success. engine/system shells out to pdbedit / chown; tests
// without a real samba install need this.
type permissiveExec struct{ calls []cmdexec.FakeCall }

func newPermissiveExec() *permissiveExec { return &permissiveExec{} }

func (p *permissiveExec) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	p.calls = append(p.calls, cmdexec.FakeCall{Name: name, Args: append([]string(nil), args...)})
	return nil, nil, nil
}

// fakeUserSource lets tests stage a fixed user list for Reconcile.
type fakeUserSource struct{ users []SourceUser }

func (f *fakeUserSource) List(_ context.Context) ([]SourceUser, error) {
	return append([]SourceUser(nil), f.users...), nil
}

// newTestEngine returns an Engine plus the underlying *store.Store so the
// caller can re-open the same DB to test cross-restart behaviour.
func newTestEngine(t *testing.T) (Engine, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := seedUsersTable(st.DB()); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	eng, err := New(Options{
		DB:     st.DB(),
		FS:     NewRealFS(filepath.Join(t.TempDir(), "rootfs")),
		Exec:   newPermissiveExec(),
		Hasher: fakeHasher{},
		Users:  &fakeUserSource{},
	})
	if err != nil {
		t.Fatalf("system.New: %v", err)
	}
	return eng, st
}

// newEngineOnStore re-opens an Engine on an existing store (simulates a
// kura restart against the same state.db).
func newEngineOnStore(t *testing.T, st *store.Store) Engine {
	t.Helper()
	eng, err := New(Options{
		DB:     st.DB(),
		FS:     NewRealFS(filepath.Join(t.TempDir(), "rootfs")),
		Exec:   newPermissiveExec(),
		Hasher: fakeHasher{},
		Users:  &fakeUserSource{},
	})
	if err != nil {
		t.Fatalf("system.New: %v", err)
	}
	return eng
}

// seedUsersTable inserts the synthetic user rows referenced by allocator
// tests. The uid_alloc table has a FK to users(id), so we need at least
// the rows to exist before AllocateUID can succeed.
func seedUsersTable(db *sql.DB) error {
	for i := 0; i < 250; i++ {
		_, err := db.Exec(`
			INSERT INTO users (id, username, display_name, role, disabled, created_at, updated_at)
			VALUES (?, ?, ?, 'user', 0, '', '')
			ON CONFLICT (id) DO NOTHING
		`, fmt.Sprintf("u-%05d", i), fmt.Sprintf("u%05d", i), "")
		if err != nil {
			return err
		}
	}
	for _, uid := range []string{"u-alice", "u-alice-0001", "u-bob-0002", "u-carol-0003"} {
		_, err := db.Exec(`
			INSERT INTO users (id, username, display_name, role, disabled, created_at, updated_at)
			VALUES (?, ?, ?, 'user', 0, '', '')
			ON CONFLICT (id) DO NOTHING
		`, uid, uid, "")
		if err != nil {
			return err
		}
	}
	return nil
}
