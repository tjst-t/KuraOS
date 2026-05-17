// federation_wiring_test.go verifies the cmd/kura wiring for federation
// auto-provisioning. Uses a minimal in-memory DB + stub system.Engine
// adapter so no ZFS / smbpasswd / OS state is needed.
package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/kuraos-org/kura/engine/system"
	"github.com/kuraos-org/kura/engine/user"

	_ "modernc.org/sqlite"
)

// minimalEngine is a stub of system.Engine that records the CreateUser call.
// All other Engine methods panic immediately — any accidental call shows up
// in the test output with a clear message.
type minimalEngine struct {
	createCalled []system.CreateUserInput
	createResult string
	createErr    error
}

func (e *minimalEngine) CreateUser(_ context.Context, in system.CreateUserInput) (string, error) {
	e.createCalled = append(e.createCalled, in)
	return e.createResult, e.createErr
}

// Unneeded Engine methods: panic on call.
func (e *minimalEngine) AllocateUID(context.Context, string) (int, error) { panic("not expected") }
func (e *minimalEngine) AllocateGID(context.Context, string) (int, error) { panic("not expected") }
func (e *minimalEngine) LookupUID(context.Context, string) (int, error)   { panic("not expected") }
func (e *minimalEngine) SetUserPassword(_ context.Context, _ string, _ system.PlaintextPassword) error {
	panic("not expected")
}
func (e *minimalEngine) Reconcile(context.Context) error { panic("not expected") }
func (e *minimalEngine) ApplyShareOwnership(_ context.Context, _ system.ShareTarget) error {
	panic("not expected")
}
func (e *minimalEngine) LookupCredential(_ context.Context, _ system.CredentialKind, _ system.CredentialOwnerKind, _ string) (system.Credential, error) {
	panic("not expected")
}
func (e *minimalEngine) SetCredential(_ context.Context, _ system.Credential) error {
	panic("not expected")
}
func (e *minimalEngine) UpdateUser(_ context.Context, _, _, _ string) error { panic("not expected") }
func (e *minimalEngine) DeleteUser(_ context.Context, _ string) error       { panic("not expected") }
func (e *minimalEngine) CreateGroup(_ context.Context, _, _ string) (string, error) {
	panic("not expected")
}
func (e *minimalEngine) DeleteGroup(_ context.Context, _ string) error { panic("not expected") }
func (e *minimalEngine) SetGroupMembers(_ context.Context, _ string, _ []string) error {
	panic("not expected")
}
func (e *minimalEngine) PromoteFromPending(_ context.Context, _, _ string) (string, error) {
	panic("not expected")
}

func newFedWiringTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE users (
			id TEXT PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'user',
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);
		CREATE TABLE auth_methods (
			user_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			credential TEXT NOT NULL,
			PRIMARY KEY (user_id, kind)
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}
	return db
}

// [AC-S413bd5-2-2] TestFedProvisioner_CreateFromFederation_RolePending asserts
// that auto-provisioning a new federated user sets role=pending, not role=user.
func TestFedProvisioner_CreateFromFederation_RolePending(t *testing.T) {
	db := newFedWiringTestDB(t)
	hasher := user.NewHasher()
	store := user.NewStore(db, hasher)
	eng := &minimalEngine{createResult: "new-user-id"}

	p := &fedProvisioner{
		db:    db,
		sys:   eng,
		users: store,
	}

	id, err := p.CreateFromFederation(context.Background(), "newuser", "New User", "new@example.com")
	if err != nil {
		t.Fatalf("CreateFromFederation: %v", err)
	}
	if id != "new-user-id" {
		t.Fatalf("id = %q, want new-user-id", id)
	}
	if len(eng.createCalled) != 1 {
		t.Fatalf("CreateUser called %d times, want 1", len(eng.createCalled))
	}
	got := eng.createCalled[0]
	if got.Role != string(user.RolePending) {
		t.Fatalf("CreateUser.Role = %q, want %q", got.Role, string(user.RolePending))
	}
}

// TestFedProvisioner_CreateFromFederation_DeduplicatesExisting verifies that
// when a user with the same username already exists, CreateFromFederation
// returns the existing ID without calling engine.CreateUser.
func TestFedProvisioner_CreateFromFederation_DeduplicatesExisting(t *testing.T) {
	db := newFedWiringTestDB(t)
	// Seed an existing user directly in the DB.
	_, err := db.Exec(`INSERT INTO users (id, username, display_name, role) VALUES ('existing-id', 'alice', 'Alice', 'user')`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	hasher := user.NewHasher()
	store := user.NewStore(db, hasher)
	eng := &minimalEngine{createResult: "should-not-be-returned"}

	p := &fedProvisioner{
		db:    db,
		sys:   eng,
		users: store,
	}

	id, err := p.CreateFromFederation(context.Background(), "alice", "Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "existing-id" {
		t.Fatalf("id = %q, want existing-id (de-dup path)", id)
	}
	if len(eng.createCalled) != 0 {
		t.Fatalf("CreateUser must not be called for de-dup; called %d times", len(eng.createCalled))
	}
}
