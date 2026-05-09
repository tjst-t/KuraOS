package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/kuraos-org/kura/engine/system"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/internal/cmdexec"
	"github.com/kuraos-org/kura/internal/store"
)

// systemCmd dispatches `kura system <subcommand>`.
//
//	reconcile   — re-project SQLite users/groups onto /etc/passwd, /etc/group,
//	              /etc/samba/smb.conf include, and chown every share path.
//	              Idempotent. Soft-fails individual steps so partial drift
//	              still gets corrected.
func systemCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("kura system: subcommand required (reconcile)")
	}
	switch args[0] {
	case "reconcile":
		return systemReconcile(args[1:], os.Stdout)
	default:
		return fmt.Errorf("kura system: unknown subcommand %q (try: reconcile)", args[0])
	}
}

func systemReconcile(args []string, out io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("kura system reconcile: unexpected arguments %v", args)
	}
	ctx := context.Background()
	sysEng, err := openSystemEngine(ctx)
	if err != nil {
		return err
	}
	defer sysEng.Close()
	if err := sysEng.eng.Reconcile(ctx); err != nil {
		return fmt.Errorf("kura system reconcile: %w", err)
	}
	fmt.Fprintln(out, "system reconcile: ok")
	return nil
}

// systemHandle bundles the engine and the underlying store so CLI commands
// can defer Close cleanly.
type systemHandle struct {
	eng   system.Engine
	store *store.Store
	users *user.Store
}

func (h *systemHandle) Close() {
	if h == nil || h.store == nil {
		return
	}
	_ = h.store.Close()
}

// openSystemEngine opens the state DB and constructs the engines/system
// stack used by all `kura system` / `kura user` / `kura share` /
// `kura backup` / `kura restore` subcommands. KURA_STATE_DB env var picks
// the SQLite path; default mirrors the daemon's path.
func openSystemEngine(ctx context.Context) (*systemHandle, error) {
	dbPath := os.Getenv("KURA_STATE_DB")
	if dbPath == "" {
		// Match the daemon's default search order: env var first, then the
		// canonical /var/lib/kura/state.db. CLI invocations on dev VMs that
		// run the daemon with KURA_STATE_DB pointed elsewhere must export
		// the same env var so the CLI sees the same rows.
		dbPath = "/var/lib/kura/state.db"
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("kura: open state %q: %w", dbPath, err)
	}
	hasher := user.NewHasher()
	users := user.NewStore(st.DB(), hasher)
	// KURA_SYSTEM_ROOT lets test runs target /tmp instead of "/" so the
	// engine's /etc/passwd writes don't need root. Production unsets the
	// var (RealFS defaults to "/").
	rootDir := os.Getenv("KURA_SYSTEM_ROOT")
	if rootDir == "" {
		rootDir = "/"
	}
	eng, err := system.New(system.Options{
		DB:               st.DB(),
		FS:               system.NewRealFS(rootDir),
		Exec:             cmdexec.NewReal(),
		Hasher:           system.NewHasherAdapter(hasher),
		Users:            system.NewUserStoreAdapter(users),
		OnPasswordChange: system.LegacyAuthMethodMirror(st.DB()),
	})
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("kura: init system engine: %w", err)
	}
	return &systemHandle{eng: eng, store: st, users: users}, nil
}
