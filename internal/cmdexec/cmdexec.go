// Package cmdexec is the seam between Go code and external CLI tools.
//
// Every engine that shells out (engine/storage's zpool/zfs/lsblk/smartctl
// today, engine/share's smbd / engine/network's ip later) calls through an
// Executor instead of os/exec.Command directly. That keeps the production
// path a single line — exec.CommandContext + CombinedOutput — while letting
// tests inject a Fake that returns canned stdout/stderr from testdata.
//
// DESIGN_PRINCIPLES priority #9 (テスタビリティ) calls this out by name:
// "副作用は interface の裏に隠す". CLAUDE.md repeats it for ZFS/Docker/SMB.
//
// Why is this in internal/ rather than engine/storage/cmdexec? The same
// abstraction is reused by engine/share, engine/network/acme, etc. Putting
// it under internal/ makes that intent explicit and avoids the awkward case
// where engine/share has to import engine/storage to get an Executor.
package cmdexec

import (
	"context"
	"fmt"
	"os/exec"
)

// Executor runs an external command and returns the captured output. The
// stderr is returned even on success so the caller can surface warnings; the
// underlying *exec.ExitError is wrapped in the returned error so callers can
// errors.As to it when they need the exit code.
type Executor interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// Real is the production Executor that shells out via os/exec. It captures
// stdout and stderr separately so callers can distinguish "the tool said this
// went fine but printed a warning" from "the tool failed and the reason is on
// stderr". Context cancellation propagates to the spawned process via
// exec.CommandContext.
type Real struct{}

// NewReal returns a production Executor. It carries no state — the type is a
// struct rather than a bare function so it satisfies the Executor interface
// without callers having to wrap it.
func NewReal() Real { return Real{} }

// Run executes name with args under ctx and returns the captured streams.
// The error wraps the underlying *exec.ExitError so callers may use errors.As
// to inspect ProcessState if they care about the exit code.
func (Real) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.Output()
	var stderr []byte
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = exitErr.Stderr
		return stdout, stderr, fmt.Errorf("cmdexec: %s %v: %w", name, args, err)
	}
	if err != nil {
		return stdout, stderr, fmt.Errorf("cmdexec: %s %v: %w", name, args, err)
	}
	return stdout, stderr, nil
}
