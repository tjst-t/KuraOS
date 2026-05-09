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
// CreateUser / UpdateUser / DeleteUser are also implemented in-memory
// so the engine/system orchestration tests can exercise the full chain
// (Sfix001 high-level methods) without booting engine/user.
type fakeUserSource struct {
	users   []SourceUser
	groups  []SourceGroup
	members map[string][]string
}

func (f *fakeUserSource) List(_ context.Context) ([]SourceUser, error) {
	return append([]SourceUser(nil), f.users...), nil
}

func (f *fakeUserSource) Create(_ context.Context, in SourceCreateUser) (SourceUser, error) {
	id := fmt.Sprintf("u-%s-%04d", in.Username, len(f.users)+1)
	u := SourceUser{ID: id, Username: in.Username, DisplayName: in.DisplayName, Role: in.Role}
	f.users = append(f.users, u)
	return u, nil
}

func (f *fakeUserSource) Update(_ context.Context, userID, displayName, role string) error {
	for i := range f.users {
		if f.users[i].ID == userID {
			f.users[i].DisplayName = displayName
			f.users[i].Role = role
			return nil
		}
	}
	return ErrUserNotFound
}

func (f *fakeUserSource) Delete(_ context.Context, userID string) error {
	for i := range f.users {
		if f.users[i].ID == userID {
			f.users = append(f.users[:i], f.users[i+1:]...)
			return nil
		}
	}
	return ErrUserNotFound
}

func (f *fakeUserSource) ListGroups(_ context.Context) ([]SourceGroup, error) {
	return append([]SourceGroup(nil), f.groups...), nil
}

func (f *fakeUserSource) CreateGroup(_ context.Context, name, description string) (SourceGroup, error) {
	id := fmt.Sprintf("g-%s-%04d", name, len(f.groups)+1)
	g := SourceGroup{ID: id, Name: name, Description: description}
	f.groups = append(f.groups, g)
	return g, nil
}

func (f *fakeUserSource) DeleteGroup(_ context.Context, groupID string) error {
	for i := range f.groups {
		if f.groups[i].ID == groupID {
			f.groups = append(f.groups[:i], f.groups[i+1:]...)
			delete(f.members, groupID)
			return nil
		}
	}
	return ErrUserNotFound
}

func (f *fakeUserSource) SetGroupMembers(_ context.Context, groupID string, userIDs []string) error {
	if f.members == nil {
		f.members = map[string][]string{}
	}
	f.members[groupID] = append([]string(nil), userIDs...)
	return nil
}

func (f *fakeUserSource) MembersOfGroup(_ context.Context, groupID string) ([]string, error) {
	return append([]string(nil), f.members[groupID]...), nil
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
