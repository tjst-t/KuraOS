package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kuraos-org/kura/engine/auth/session"
	"github.com/kuraos-org/kura/engine/user"
	"github.com/kuraos-org/kura/i18n"
	"github.com/kuraos-org/kura/internal/gateway"
	"github.com/kuraos-org/kura/internal/store"
	"github.com/kuraos-org/kura/internal/ui"
)

// Version is overridable at link time: -ldflags "-X main.Version=v0.1.0".
var Version = "dev"

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		// developer-facing error message stays English (DESIGN_PRINCIPLES coding_conventions)
		log.Fatalf("kura: %v", err)
	}
}

// dispatch routes the top-level subcommand. With no args, kura falls through
// to the long-running server (`run`). Subcommands like `kura config export`
// short-circuit before binding a port so they're safe to run from cron jobs
// and CI without colliding with a live instance.
func dispatch(args []string) error {
	if len(args) == 0 {
		return run()
	}
	switch args[0] {
	case "config":
		return configCmd(args[1:])
	case "version", "--version", "-v":
		fmt.Println(Version)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: config | version)", args[0])
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tr, err := i18n.New()
	if err != nil {
		return fmt.Errorf("init i18n: %w", err)
	}

	port := os.Getenv("KURA_PORT")
	if port == "" {
		// portman wraps `make serve` and is supposed to inject KURA_PORT.
		// Refuse to invent a default — DESIGN_PRINCIPLES forbids hardcoded
		// magic ports and CLAUDE.md explicitly bans port hardcoding.
		return errors.New("env KURA_PORT not set (expected portman to inject it)")
	}

	dbPath := os.Getenv("KURA_STATE_DB")
	if dbPath == "" {
		dbPath = "/var/lib/kura/state.db"
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	uiRenderer, err := ui.New(tr, Version)
	if err != nil {
		return fmt.Errorf("init ui: %w", err)
	}

	users := user.NewStore(st.DB(), nil)
	sessions := session.NewStore(st.DB())
	secureCookies := os.Getenv("KURA_SECURE_COOKIES") == "1"

	authH := uiRenderer.AuthHandler(ui.AuthDeps{
		Users:         users,
		Sessions:      sessions,
		SecureCookies: secureCookies,
	})
	setupH := uiRenderer.SetupHandler(ui.SetupDeps{
		Users:         users,
		Sessions:      sessions,
		SecureCookies: secureCookies,
	})

	startedAt := time.Now().UTC()
	handler := gateway.New(gateway.Deps{
		Translator:   tr,
		Version:      Version,
		StartedAt:    startedAt,
		UIHandler:    uiRenderer.Routes(),
		AuthHandler:  authH,
		SetupHandler: setupH,
		Sessions:     sessions,
		Users:        users,
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Println(tr.T(i18n.MsgSystemStartup, port))

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		log.Println(tr.T(i18n.MsgSystemShutdown))
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
}
