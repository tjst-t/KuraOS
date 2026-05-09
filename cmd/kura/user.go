package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kuraos-org/kura/engine/system"
	"golang.org/x/term"
)

// userCmd dispatches `kura user <subcommand>`.
//
//	set-password <username> [--from-stdin]   Re-set the password for an
//	    existing user. Without --from-stdin, the new password is read
//	    from /dev/tty (no echo). With --from-stdin, the first line on
//	    stdin is consumed (suitable for scripted set-up).
func userCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("kura user: subcommand required (set-password)")
	}
	switch args[0] {
	case "set-password":
		return userSetPassword(args[1:], os.Stdin, os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("kura user: unknown subcommand %q (try: set-password)", args[0])
	}
}

func userSetPassword(args []string, in io.Reader, out, errw io.Writer) error {
	fromStdin := false
	var positional []string
	for _, a := range args {
		if a == "--from-stdin" {
			fromStdin = true
			continue
		}
		positional = append(positional, a)
	}
	if len(positional) != 1 {
		return fmt.Errorf("kura user set-password: usage: kura user set-password <username> [--from-stdin]")
	}
	username := system.SanitizeUsername(positional[0])

	ctx := context.Background()
	h, err := openSystemEngine(ctx)
	if err != nil {
		return err
	}
	defer h.Close()

	u, err := h.users.GetByUsername(ctx, username)
	if err != nil {
		return fmt.Errorf("kura user set-password: %w", err)
	}

	pw, err := readPassword(fromStdin, in, out)
	if err != nil {
		return err
	}
	if pw == "" {
		return fmt.Errorf("kura user set-password: empty password")
	}

	pt := system.NewPlaintextPassword(pw)
	if err := h.eng.SetUserPassword(ctx, u.ID, pt); err != nil {
		return fmt.Errorf("kura user set-password: %w", err)
	}
	fmt.Fprintf(out, "user %s: password updated (argon2id + NT-hash mirrored to vault)\n", username)
	return nil
}

// readPassword reads a password either non-interactively from stdin (one
// line, trimmed) or interactively from /dev/tty with no echo. The /dev/tty
// path is used for `kura user set-password admin` invoked from an
// administrator's shell; the stdin path supports automation.
func readPassword(fromStdin bool, in io.Reader, prompt io.Writer) (string, error) {
	if fromStdin {
		s := bufio.NewScanner(in)
		s.Buffer(make([]byte, 0, 1024), 1024)
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
		return "", fmt.Errorf("open /dev/tty: %w (use --from-stdin to skip)", err)
	}
	defer tty.Close()
	fmt.Fprint(prompt, "new password: ")
	pw, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(prompt)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(pw), nil
}
