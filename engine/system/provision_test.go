package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/internal/cmdexec"
	"github.com/kuraos-org/kura/internal/store"
)

// [AC-Ssys001-3-1] Reconcile writes the include line into smb.conf when
// missing, and is idempotent (re-running does not modify mtime / content).
func TestReconcile_EnsuresSmbInclude(t *testing.T) {
	ctx := context.Background()
	eng, root := newReconcileFixture(t, []SourceUser{
		{ID: "u-alice", Username: "alice", DisplayName: "Alice"},
	})

	// Pre-seed an existing smb.conf with no include line.
	smbPath := filepath.Join(root, "etc/samba/smb.conf")
	mustMkdirAll(t, filepath.Dir(smbPath))
	mustWrite(t, smbPath, "[global]\n    workgroup = WORKGROUP\n")

	if err := eng.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := mustRead(t, smbPath)
	if !strings.Contains(got, "include = /etc/samba/conf.d/kura.conf") {
		t.Fatalf("smb.conf missing include line:\n%s", got)
	}

	// Idempotent re-run: file content should be unchanged.
	wantContents := mustRead(t, smbPath)
	if err := eng.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile (re-run): %v", err)
	}
	if got := mustRead(t, smbPath); got != wantContents {
		t.Fatalf("smb.conf changed on idempotent re-run\nbefore:\n%s\nafter:\n%s", wantContents, got)
	}
}

// TestSmbIncludeIdempotent: ensureSmbInclude returns changed=false when
// the include is already present.
func TestSmbIncludeIdempotent(t *testing.T) {
	root := t.TempDir()
	fs := NewRealFS(root)
	smbPath := filepath.Join(root, "etc/samba/smb.conf")
	mustMkdirAll(t, filepath.Dir(smbPath))
	mustWrite(t, smbPath, "[global]\n    include = /etc/samba/conf.d/kura.conf\n")

	changed, err := ensureSmbInclude(fs)
	if err != nil {
		t.Fatalf("ensureSmbInclude: %v", err)
	}
	if changed {
		t.Fatalf("expected idempotent no-op, got changed=true")
	}
}

// [AC-Ssys001-3-2] Drift recovery: the engine re-projects users whenever
// SQLite has rows that aren't in /etc/passwd's managed block.
func TestStartupReconciliation_ProjectsAllUsers(t *testing.T) {
	ctx := context.Background()
	eng, root := newReconcileFixture(t, []SourceUser{
		{ID: "u-alice", Username: "alice", DisplayName: "Alice"},
		{ID: "u-bob", Username: "bob", DisplayName: "Bob"},
	})

	if err := eng.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	passwd := mustRead(t, filepath.Join(root, "etc/passwd"))
	for _, u := range []string{"alice", "bob"} {
		if !strings.Contains(passwd, "\n"+u+":x:") && !strings.HasPrefix(passwd, u+":x:") {
			t.Fatalf("/etc/passwd missing user %q:\n%s", u, passwd)
		}
	}
	if !strings.Contains(passwd, BeginMarker) {
		t.Fatalf("/etc/passwd missing managed block marker")
	}
	groupFile := mustRead(t, filepath.Join(root, "etc/group"))
	if !strings.Contains(groupFile, PrimaryGroupName+":x:") {
		t.Fatalf("/etc/group missing kura-users group:\n%s", groupFile)
	}
}

// [AC-Ssys001-3-1] Managed block round-trip: existing distro lines are
// preserved verbatim outside the markers.
func TestMergeManagedBlock_PreservesExisting(t *testing.T) {
	original := []byte("root:x:0:0:root:/root:/bin/bash\ndaemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin\n")
	newBlock := BeginMarker + "\nalice:x:30001:30000:Alice:/var/empty:/usr/sbin/nologin\n" + EndMarker + "\n"
	got := mergeManagedBlock(original, newBlock, []string{"alice"})
	gotStr := string(got)
	if !strings.Contains(gotStr, "root:x:0:0:") {
		t.Fatalf("root entry not preserved:\n%s", gotStr)
	}
	if !strings.Contains(gotStr, "daemon:x:1:1:") {
		t.Fatalf("daemon entry not preserved:\n%s", gotStr)
	}
	if !strings.Contains(gotStr, "alice:x:30001:") {
		t.Fatalf("managed alice entry missing:\n%s", gotStr)
	}
}

// [AC-Ssys001-3-1] Re-run replaces only the managed block, removing
// stale entries and updating present ones.
func TestMergeManagedBlock_ReplacesStaleManagedLines(t *testing.T) {
	original := []byte(strings.Join([]string{
		"root:x:0:0:root:/root:/bin/bash",
		BeginMarker,
		"alice:x:30001:30000:OLD:/old:/old",
		"removed-user:x:30002:30000:::",
		EndMarker,
		"distro:x:1000:1000:distro:/home/distro:/bin/bash",
	}, "\n") + "\n")
	newBlock := BeginMarker + "\nalice:x:30001:30000:Alice:/var/empty:/usr/sbin/nologin\n" + EndMarker + "\n"
	got := string(mergeManagedBlock(original, newBlock, []string{"alice"}))
	if strings.Contains(got, "OLD") {
		t.Fatalf("old block not replaced:\n%s", got)
	}
	if strings.Contains(got, "removed-user") {
		t.Fatalf("removed user still present:\n%s", got)
	}
	if !strings.Contains(got, "Alice") {
		t.Fatalf("new block missing Alice:\n%s", got)
	}
	if !strings.Contains(got, "distro:x:1000:") {
		t.Fatalf("distro user dropped:\n%s", got)
	}
}

// newReconcileFixture wires an Engine onto a temp rootfs and a stub
// UserSource holding the supplied users. Returns the rootfs path so tests
// can read back the projected files.
func newReconcileFixture(t *testing.T, users []SourceUser) (Engine, string) {
	t.Helper()
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, u := range users {
		if _, err := st.DB().Exec(`
			INSERT INTO users (id, username, display_name, role, disabled, created_at, updated_at)
			VALUES (?, ?, ?, 'user', 0, '', '')
		`, u.ID, u.Username, u.DisplayName); err != nil {
			t.Fatalf("seed user %q: %v", u.Username, err)
		}
	}
	eng, err := New(Options{
		DB:     st.DB(),
		FS:     NewRealFS(root),
		Exec:   newPermissiveExec(),
		Hasher: fakeHasher{},
		Users:  &fakeUserSource{users: users},
	})
	if err != nil {
		t.Fatalf("system.New: %v", err)
	}
	return eng, root
}

// Test helpers for filesystem manipulation.
func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(b)
}

// _ keeps cmdexec import alive under future refactors.
var _ = cmdexec.NewFake
