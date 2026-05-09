package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kuraos-org/kura/engine/system"
	"github.com/kuraos-org/kura/internal/config"
	"golang.org/x/term"
)

// backupCmd implements `kura backup -o <file> [--encrypt-passphrase | --passphrase-stdin]`.
//
// Output layout (case X, see DESIGN_PRINCIPLES priority #11):
//
//	tarball.tar.gz
//	  ├─ manifest.json     (schema version, created_at, host hint)
//	  ├─ config.json       (plaintext — declarative state only)
//	  └─ secrets.kura.age  (age-encrypted vault — credentials, uid/gid map)
//
// The vault is ALWAYS age-encrypted. There is no plaintext-vault mode —
// case X requires it for "one user, one password" mental model + safe
// transport.
func backupCmd(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("o", "", "output tarball path (required)")
	encryptPrompt := fs.Bool("encrypt-passphrase", false, "prompt /dev/tty for vault passphrase (interactive)")
	encryptStdin := fs.Bool("passphrase-stdin", false, "read vault passphrase from stdin (one line)")
	recipient := fs.Bool("recipient", false, "recipient (X25519) mode — DEFERRED to v1.x")
	hostHint := fs.String("host-hint", "", "optional label for the manifest (e.g. hostname)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *recipient {
		return errors.New("kura backup: recipient mode is deferred to v1.x; use --encrypt-passphrase")
	}
	if *out == "" {
		return errors.New("kura backup: -o <file> is required")
	}
	if !*encryptPrompt && !*encryptStdin {
		return errors.New("kura backup: vault encryption is mandatory; pass --encrypt-passphrase (interactive) or --passphrase-stdin")
	}
	if *encryptPrompt && *encryptStdin {
		return errors.New("kura backup: pick one of --encrypt-passphrase / --passphrase-stdin")
	}

	pw, err := readPassphrase(*encryptStdin, "vault passphrase: ")
	if err != nil {
		return err
	}
	if len(pw) < 8 {
		return errors.New("kura backup: passphrase must be at least 8 characters")
	}

	ctx := context.Background()
	h, err := openSystemEngine(ctx)
	if err != nil {
		return err
	}
	defer h.Close()

	cfg := config.New()
	cfgRaw, err := config.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("kura backup: marshal config: %w", err)
	}

	tmp := *out + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("kura backup: open %q: %w", tmp, err)
	}
	defer func() { _ = os.Remove(tmp) }()

	werr := system.WriteBackup(ctx, f, h.store.DB(), system.BackupOptions{
		ConfigJSON: cfgRaw,
		Passphrase: pw,
		HostHint:   *hostHint,
	})
	if cerr := f.Close(); cerr != nil && werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("kura backup: %w", werr)
	}
	if err := os.Rename(tmp, *out); err != nil {
		return fmt.Errorf("kura backup: rename: %w", err)
	}
	fmt.Printf("backup written: %s\n", *out)
	return nil
}

// restoreCmd implements `kura restore <file>` — reads the tarball, prompts
// for the passphrase, decrypts the vault, and applies credentials +
// uid/gid allocations into state.db. config.json is printed to stdout for
// the operator to review and feed into `kura config apply`.
func restoreCmd(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	stdinPass := fs.Bool("passphrase-stdin", false, "read passphrase from stdin (one line)")
	configOut := fs.String("config-out", "", "write extracted config.json to this path (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("kura restore: usage: kura restore [--passphrase-stdin] [--config-out <path>] <file>")
	}

	pw, err := readPassphrase(*stdinPass, "vault passphrase: ")
	if err != nil {
		return err
	}

	src, err := os.Open(rest[0])
	if err != nil {
		return fmt.Errorf("kura restore: open: %w", err)
	}
	defer src.Close()

	ctx := context.Background()
	res, err := system.ReadBackup(ctx, src, pw)
	if err != nil {
		return fmt.Errorf("kura restore: %w", err)
	}

	h, err := openSystemEngine(ctx)
	if err != nil {
		return err
	}
	defer h.Close()

	if err := system.ApplyVaultRestore(ctx, h.store.DB(), res.Vault); err != nil {
		return fmt.Errorf("kura restore: apply vault: %w", err)
	}

	if *configOut == "" {
		_, _ = io.Copy(os.Stdout, strings.NewReader(string(res.ConfigJSON)))
	} else {
		if err := os.WriteFile(*configOut, res.ConfigJSON, 0o600); err != nil {
			return fmt.Errorf("kura restore: write config-out: %w", err)
		}
		fmt.Printf("config.json written: %s\n", *configOut)
	}
	fmt.Printf("vault restored: %d credentials, %d uid allocs, %d gid allocs\n",
		len(res.Vault.Credentials), len(res.Vault.UIDAllocs), len(res.Vault.GIDAllocs))
	return nil
}

// readPassphrase mirrors readPassword in user.go but on /dev/tty (no echo)
// and with the prompt customizable.
func readPassphrase(fromStdin bool, prompt string) (string, error) {
	if fromStdin {
		s := bufio.NewScanner(os.Stdin)
		s.Buffer(make([]byte, 0, 4096), 4096)
		if !s.Scan() {
			if err := s.Err(); err != nil {
				return "", fmt.Errorf("read stdin: %w", err)
			}
			return "", nil
		}
		return strings.TrimRight(s.Text(), "\r\n"), nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("open /dev/tty: %w (use --passphrase-stdin to skip)", err)
	}
	defer tty.Close()
	fmt.Fprint(tty, prompt)
	pw, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	if err != nil {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	return string(pw), nil
}
