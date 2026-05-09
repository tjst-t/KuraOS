package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/kuraos-org/kura/engine/share"
	"github.com/kuraos-org/kura/engine/system"
	"github.com/kuraos-org/kura/internal/cmdexec"
)

// shareCmd dispatches `kura share <subcommand>`.
//
//	reconcile-perms   chown / chmod every share path so KuraOS users can
//	                  write to it. Idempotent — share_perm_state cached.
func shareCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("kura share: subcommand required (reconcile-perms)")
	}
	switch args[0] {
	case "reconcile-perms":
		return shareReconcilePerms(args[1:], os.Stdout)
	default:
		return fmt.Errorf("kura share: unknown subcommand %q (try: reconcile-perms)", args[0])
	}
}

func shareReconcilePerms(args []string, out io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("kura share reconcile-perms: unexpected arguments %v", args)
	}
	ctx := context.Background()
	h, err := openSystemEngine(ctx)
	if err != nil {
		return err
	}
	defer h.Close()

	shareStore := share.NewStore(h.store.DB())
	mgr := share.NewManager(shareStore, cmdexec.NewReal(), share.Options{
		Ownership: system.NewShareOwnershipAdapter(h.eng),
	})
	// Apply regenerates the smb.conf / exports fragments from current
	// SQLite state — picks up template changes (e.g. obey-pam toggle).
	if err := mgr.Apply(ctx); err != nil {
		// Soft-fail Apply on dev boxes without smbd; perms still get
		// reconciled below.
		fmt.Fprintf(out, "warning: share Apply failed (continuing with perms): %v\n", err)
	}
	if err := mgr.ReconcilePerms(ctx); err != nil {
		return fmt.Errorf("kura share reconcile-perms: %w", err)
	}
	fmt.Fprintln(out, "share reconcile-perms: ok")
	return nil
}
