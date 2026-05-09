package system

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// [AC-Ssys001-4-1] ApplyShareOwnership chmod's to mode 2775 (setgid +
// rwx group) and records the applied state in share_perm_state.
func TestApplyShareOwnership_ChownChmodAndRecord(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sharePath := filepath.Join(root, "tank/photos")
	mustMkdirAll(t, sharePath)

	eng, _ := newReconcileFixture(t, []SourceUser{
		{ID: "u-alice", Username: "alice"},
	})
	// The fixture's FS is rooted at its own tempdir; replace with one
	// rooted at our root so the chmod targets a path that exists.
	svc := eng.(*service)
	svc.fs = NewRealFS(root)

	if err := svc.db.QueryRowContext(ctx, "SELECT 1").Scan(new(int)); err != nil {
		t.Fatalf("db sanity: %v", err)
	}

	target := ShareTarget{ID: "share-001", Name: "photos", Path: "/tank/photos"}
	if err := eng.ApplyShareOwnership(ctx, target); err != nil {
		t.Fatalf("ApplyShareOwnership: %v", err)
	}

	st, err := os.Stat(sharePath)
	if err != nil {
		t.Fatalf("stat share path: %v", err)
	}
	mode := st.Mode().Perm()
	if mode != 0o775 {
		t.Fatalf("expected base perm bits 0775, got %#o", mode)
	}
	if st.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("expected setgid bit, got mode %v", st.Mode())
	}

	// share_perm_state row exists with the recorded uid/gid/mode.
	var row struct {
		path string
		uid  int
		gid  int
		mode int
	}
	if err := svc.db.QueryRowContext(ctx,
		`SELECT path, owner_uid, owner_gid, mode FROM share_perm_state WHERE share_id = ?`,
		"share-001",
	).Scan(&row.path, &row.uid, &row.gid, &row.mode); err != nil {
		t.Fatalf("read share_perm_state: %v", err)
	}
	if row.path != "/tank/photos" {
		t.Fatalf("recorded path %q", row.path)
	}
	if row.uid != 0 {
		t.Fatalf("recorded uid %d (expected 0=root)", row.uid)
	}
	if row.mode != int(os.FileMode(0o2775)) {
		t.Fatalf("recorded mode %#o (expected 02775)", row.mode)
	}
}
