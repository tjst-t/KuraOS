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

	"github.com/kuraos-org/kura/i18n"
	"github.com/kuraos-org/kura/internal/gateway"
	"github.com/kuraos-org/kura/internal/store"
)

// Version is overridable at link time: -ldflags "-X main.Version=v0.1.0".
var Version = "dev"

func main() {
	if err := run(); err != nil {
		// developer-facing error message stays English (DESIGN_PRINCIPLES coding_conventions)
		log.Fatalf("kura: %v", err)
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

	startedAt := time.Now().UTC()
	handler := gateway.New(gateway.Deps{
		Translator: tr,
		Version:    Version,
		StartedAt:  startedAt,
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
