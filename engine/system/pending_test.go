// pending_test.go covers AC-S413bd5-1-2 and AC-S413bd5-1-3:
//   - A user with role=pending is NOT projected into /etc/passwd or tdbsam
//     (but still gets a uid_alloc row).
//   - PromoteFromPending changes the role, sets credentials, and causes
//     Reconcile to project the user into /etc/passwd.
package system

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// [AC-S413bd5-1-2] Pending user: uid_alloc row exists, but /etc/passwd
// and pdbedit are NOT touched.
func TestReconcile_PendingUserNotProjected(t *testing.T) {
	ctx := context.Background()
	eng, root := newReconcileFixture(t, []SourceUser{
		{ID: "u-bob", Username: "bob", DisplayName: "Bob", Role: "user"},
		{ID: "u-pending", Username: "pending-alice", DisplayName: "Alice P.", Role: "pending"},
	})

	if err := eng.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// bob must be projected; pending-alice must not.
	passwd := mustRead(t, filepath.Join(root, "etc/passwd"))
	if !strings.Contains(passwd, "bob:x:") {
		t.Fatalf("/etc/passwd missing bob:\n%s", passwd)
	}
	if strings.Contains(passwd, "pending-alice:") {
		t.Fatalf("/etc/passwd must NOT contain pending-alice:\n%s", passwd)
	}

	// uid_alloc for the pending user must exist.
	uid, err := eng.LookupUID(ctx, "u-pending")
	if err != nil {
		t.Fatalf("LookupUID for pending user: %v (uid_alloc row required by AC-S413bd5-1-2)", err)
	}
	if uid < UIDMin || uid > UIDMax {
		t.Fatalf("uid %d out of expected range", uid)
	}
}

// [AC-S413bd5-1-2] Pending user is excluded from kura-users /etc/group members.
func TestReconcile_PendingUserNotInGroup(t *testing.T) {
	ctx := context.Background()
	eng, root := newReconcileFixture(t, []SourceUser{
		{ID: "u-carol", Username: "carol", Role: "user"},
		{ID: "u-pend2", Username: "pending-dave", Role: "pending"},
	})

	if err := eng.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	groupFile := mustRead(t, filepath.Join(root, "etc/group"))
	if !strings.Contains(groupFile, "carol") {
		t.Fatalf("/etc/group missing carol:\n%s", groupFile)
	}
	if strings.Contains(groupFile, "pending-dave") {
		t.Fatalf("/etc/group must NOT contain pending-dave:\n%s", groupFile)
	}
}

// [AC-S413bd5-1-3] PromoteFromPending sets role, credentials, and projects
// the user into /etc/passwd.
func TestPromoteFromPending(t *testing.T) {
	ctx := context.Background()

	// Create an engine with a fakeUserSource that starts with one pending user.
	eng, root := newReconcileFixture(t, []SourceUser{
		{ID: "u-prom", Username: "eva", DisplayName: "Eva", Role: "pending"},
	})

	// Confirm she is not yet in /etc/passwd.
	if err := eng.Reconcile(ctx); err != nil {
		t.Fatalf("pre-promote Reconcile: %v", err)
	}
	passwd := mustRead(t, filepath.Join(root, "etc/passwd"))
	if strings.Contains(passwd, "eva:x:") {
		t.Fatalf("/etc/passwd must NOT contain eva before promotion:\n%s", passwd)
	}

	// Promote.
	plaintext, err := eng.PromoteFromPending(ctx, "u-prom", "user")
	if err != nil {
		t.Fatalf("PromoteFromPending: %v", err)
	}
	if plaintext == "" {
		t.Fatalf("PromoteFromPending returned empty plaintext password")
	}
	if len(plaintext) < 20 {
		t.Fatalf("generated password too short (%d chars): %q", len(plaintext), plaintext)
	}

	// After promotion /etc/passwd must include eva.
	passwd = mustRead(t, filepath.Join(root, "etc/passwd"))
	if !strings.Contains(passwd, "eva:x:") {
		t.Fatalf("/etc/passwd must contain eva after promotion:\n%s", passwd)
	}
}

// [AC-S413bd5-1-3] PromoteFromPending returns error when user is not pending.
func TestPromoteFromPending_NonPendingRejected(t *testing.T) {
	ctx := context.Background()
	eng, _ := newReconcileFixture(t, []SourceUser{
		{ID: "u-admin", Username: "root", Role: "admin"},
	})

	_, err := eng.PromoteFromPending(ctx, "u-admin", "user")
	if err == nil {
		t.Fatalf("PromoteFromPending on non-pending user should return error")
	}
}
