package system

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/kuraos-org/kura/internal/store"
)

// LegacyAuthMethodMirror must self-heal when the auth_methods row is missing.
// Without this, a CLI `kura user set-password admin` for a user whose
// auth_methods row was lost (backup recovery, future migration) would silently
// 0-rows-UPDATE and report success, leaving the next login still failing.
// Surfaced 2026-05-17 during S413bd5 sprint verify when admin/password kept
// returning 401 after a CLI password reset reported success.
func TestLegacyAuthMethodMirror_InsertsWhenMissing(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const userID = "u-admin"
	if _, err := st.DB().ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, role, disabled, created_at, updated_at)
		VALUES (?, 'admin', '', 'admin', 0, '', '')
	`, userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	mirror := LegacyAuthMethodMirror(st.DB())
	if err := mirror(ctx, userID, "argon2id$encoded$fake"); err != nil {
		t.Fatalf("mirror with no existing row: %v", err)
	}

	var secret string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT secret FROM auth_methods WHERE user_id = ? AND method = 'password'`,
		userID,
	).Scan(&secret); err != nil {
		if err == sql.ErrNoRows {
			t.Fatalf("auth_methods row not created; mirror silently dropped the password")
		}
		t.Fatalf("query auth_methods: %v", err)
	}
	if secret != "argon2id$encoded$fake" {
		t.Fatalf("auth_methods.secret = %q, want %q", secret, "argon2id$encoded$fake")
	}

	if err := mirror(ctx, userID, "argon2id$rotated$fake"); err != nil {
		t.Fatalf("mirror on existing row: %v", err)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT secret FROM auth_methods WHERE user_id = ? AND method = 'password'`,
		userID,
	).Scan(&secret); err != nil {
		t.Fatalf("re-query auth_methods: %v", err)
	}
	if secret != "argon2id$rotated$fake" {
		t.Fatalf("after rotation auth_methods.secret = %q, want %q", secret, "argon2id$rotated$fake")
	}

	var rowCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM auth_methods WHERE user_id = ? AND method = 'password'`,
		userID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count auth_methods: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("auth_methods rows for admin = %d, want 1 (rotation must not duplicate)", rowCount)
	}
}
