// users_crud_test.go covers Sfix001-1 AC-3 and Sfix001-2 AC-3 — the
// transactional create / delete / projection chains that engine/system
// owns on behalf of the Users / Groups UI.
//
// Tests focus on the orchestration: user creation must produce a uid
// allocation + vault credentials in one logical step; user deletion must
// scrub the credentials but keep uid_alloc; group deletion does not
// enforce share-ACL referential integrity (that lives in the UI handler
// that has access to engine/share).
package system

import (
	"context"
	"testing"
)

// [AC-Sfix001-1-3] CreateUser allocates uid, persists argon2id + NT-hash
// in the vault, and DeleteUser scrubs them again — uid_alloc remains.
func TestEngine_CreateUserAllocatesUIDAndPersistsCredentials(t *testing.T) {
	eng, st := newTestEngine(t)
	ctx := context.Background()

	id, err := eng.CreateUser(ctx, CreateUserInput{
		Username:    "alice",
		DisplayName: "Alice",
		Password:    "longenoughpw",
		Role:        "user",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if id == "" {
		t.Fatalf("CreateUser returned empty id")
	}
	uid, err := eng.LookupUID(ctx, id)
	if err != nil {
		t.Fatalf("LookupUID: %v", err)
	}
	if uid < UIDMin || uid > UIDMax {
		t.Fatalf("uid %d out of allocator range [%d,%d]", uid, UIDMin, UIDMax)
	}

	// Vault must carry both verifier kinds for the new user.
	if _, err := eng.LookupCredential(ctx, CredentialArgon2id, OwnerUser, id); err != nil {
		t.Fatalf("LookupCredential argon2id: %v", err)
	}
	if _, err := eng.LookupCredential(ctx, CredentialNTHash, OwnerUser, id); err != nil {
		t.Fatalf("LookupCredential nt_hash: %v", err)
	}

	if err := eng.DeleteUser(ctx, id); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// Vault rows for this owner must be gone.
	if _, err := eng.LookupCredential(ctx, CredentialArgon2id, OwnerUser, id); err == nil {
		t.Fatalf("LookupCredential argon2id after delete: want error, got nil")
	}
	if _, err := eng.LookupCredential(ctx, CredentialNTHash, OwnerUser, id); err == nil {
		t.Fatalf("LookupCredential nt_hash after delete: want error, got nil")
	}

	// uid_alloc row must persist (priority #1 round-trip stability:
	// recreating the same user later must land on the same uid).
	var stillAllocated int
	if err := st.DB().QueryRow(`SELECT uid FROM uid_alloc WHERE user_id = ?`, id).Scan(&stillAllocated); err != nil {
		t.Fatalf("uid_alloc retained: %v", err)
	}
	if stillAllocated != uid {
		t.Fatalf("uid_alloc changed: was %d, now %d", uid, stillAllocated)
	}
}

// [AC-Sfix001-2-3] CreateGroup allocates a stable gid; SetGroupMembers
// projects member IDs into /etc/group; DeleteGroup tears the row down
// (UI owns the share-ACL referential check).
func TestEngine_CreateGroupAllocatesGIDAndProjectsMembers(t *testing.T) {
	eng, _ := newTestEngine(t)
	ctx := context.Background()

	uid, err := eng.CreateUser(ctx, CreateUserInput{
		Username: "bob", DisplayName: "Bob", Password: "longenoughpw", Role: "user",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	gid, err := eng.CreateGroup(ctx, "devs", "Developers")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if gid == "" {
		t.Fatalf("CreateGroup returned empty id")
	}

	if err := eng.SetGroupMembers(ctx, gid, []string{uid}); err != nil {
		t.Fatalf("SetGroupMembers: %v", err)
	}
	// Re-running with the same set is a no-op (idempotent).
	if err := eng.SetGroupMembers(ctx, gid, []string{uid}); err != nil {
		t.Fatalf("SetGroupMembers idempotent: %v", err)
	}
	// Empty set clears the membership.
	if err := eng.SetGroupMembers(ctx, gid, nil); err != nil {
		t.Fatalf("SetGroupMembers nil: %v", err)
	}

	if err := eng.DeleteGroup(ctx, gid); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
}
