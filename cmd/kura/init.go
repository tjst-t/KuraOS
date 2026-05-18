package main

// initCmd implements `kura init` — the headless first-run setup (AC-S99702c-2-2).
//
// Usage:
//
//	kura init --admin-user=<name> --admin-password=<pw> [--display-name=<dn>]
//
// It creates the first admin user in KURA_STATE_DB and exits. The
// follow-up wizard (steps 2-5) can then be accessed via the web UI.
// On a fresh install without KURA_PORT available, this subcommand lets
// CI / unattended setups seed the admin credentials before launching
// the daemon.
//
// DESIGN_PRINCIPLES priority #1 (SSOT): kura init does NOT touch
// storage or shares — those belong to config.json apply. It only
// seeds auth state (user row + session capability).

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/internal/store"
)

func initCmd(args []string) error {
	fs := flag.NewFlagSet("kura init", flag.ContinueOnError)
	adminUser := fs.String("admin-user", "", "username for the first admin account (required)")
	adminPW := fs.String("admin-password", "", "password for the first admin account (required, min 12 chars)")
	displayName := fs.String("display-name", "", "display name for the admin (optional, defaults to username)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *adminUser == "" || *adminPW == "" {
		return errors.New("kura init: --admin-user and --admin-password are required")
	}
	if len(*adminPW) < 12 {
		return errors.New("kura init: --admin-password must be at least 12 characters")
	}
	dn := *displayName
	if dn == "" {
		dn = *adminUser
	}

	dbPath := os.Getenv("KURA_STATE_DB")
	if dbPath == "" {
		dbPath = "/var/lib/kura/state.db"
	}

	ctx := context.Background()

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("kura init: open state db: %w", err)
	}
	defer st.Close()

	hasher := user.NewHasher()
	users := user.NewStore(st.DB(), hasher)

	// Guard: if any admin already exists, refuse to overwrite.
	existing, err := users.CountByRole(ctx, user.RoleAdmin)
	if err != nil {
		return fmt.Errorf("kura init: check existing admins: %w", err)
	}
	if existing > 0 {
		return fmt.Errorf("kura init: admin user already exists (use `kura user` to manage accounts)")
	}

	if _, err := users.CreateLocalUser(ctx, *adminUser, dn, *adminPW, user.RoleAdmin); err != nil {
		return fmt.Errorf("kura init: create admin: %w", err)
	}

	// Verify sessions table exists (no-op if already migrated by daemon).
	_ = session.NewStore(st.DB())

	fmt.Fprintf(os.Stdout, "kura init: admin user %q created. Start the daemon and open the web UI to complete setup.\n", *adminUser)
	return nil
}
