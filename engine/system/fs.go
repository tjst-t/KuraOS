package system

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// FileSystem is the seam between engine/system and the underlying disk.
// Production wires RealFS rooted at /. Tests wire RealFS rooted at
// t.TempDir() so the same code that mutates /etc/passwd in production
// mutates ${TempDir}/etc/passwd in tests — no special "mock paths" branch
// in the engine code.
//
// DESIGN_PRINCIPLES priority #9.
type FileSystem interface {
	// ReadFile reads the file at path. ENOENT must be reported via
	// errors.Is(err, fs.ErrNotExist).
	ReadFile(path string) ([]byte, error)

	// WriteAtomic writes data to path via temp+rename so concurrent
	// readers always see a complete file. mkdir -p the parent dir.
	WriteAtomic(path string, data []byte, mode os.FileMode) error

	// Chown sets uid/gid of path. Returns nil on a kernel that does not
	// support uid/gid changes (e.g. Windows). Production callers always
	// wrap a real Linux fs.
	Chown(path string, uid, gid int) error

	// Chmod sets the mode bits (including setgid for shared dirs).
	Chmod(path string, mode os.FileMode) error

	// Stat exposes fs.FileInfo so callers can detect drift before a
	// chown/chmod (and skip the syscall when nothing changed).
	Stat(path string) (os.FileInfo, error)
}

// RealFS is the production FileSystem rooted at root. Pass "/" in main(),
// pass t.TempDir() in tests.
type RealFS struct {
	root string
}

// NewRealFS returns a RealFS rooted at root. An empty root defaults to "/".
func NewRealFS(root string) *RealFS {
	if root == "" {
		root = "/"
	}
	return &RealFS{root: root}
}

// resolve expands path relative to the FS root. Path must start with "/"
// because production callers always pass absolute system paths.
func (r *RealFS) resolve(path string) (string, error) {
	if path == "" || path[0] != '/' {
		return "", fmt.Errorf("system fs: path %q must be absolute", path)
	}
	if r.root == "/" {
		return path, nil
	}
	return filepath.Join(r.root, path), nil
}

func (r *RealFS) ReadFile(path string) ([]byte, error) {
	full, err := r.resolve(path)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// WriteAtomic uses temp+rename to keep readers happy and to make a partial
// write disappear on crash. Permissions are applied to the temp file
// before rename so the visible file always has the right mode.
func (r *RealFS) WriteAtomic(path string, data []byte, mode os.FileMode) error {
	full, err := r.resolve(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("system fs: mkdir %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".kura-*.tmp")
	if err != nil {
		return fmt.Errorf("system fs: create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("system fs: write temp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("system fs: chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("system fs: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("system fs: close temp: %w", err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		cleanup()
		return fmt.Errorf("system fs: rename %q -> %q: %w", tmpName, full, err)
	}
	return nil
}

// Chown sets uid/gid of path. When the calling process is not root the
// kernel returns EPERM — non-fatal in dev / test runs (we still want
// downstream chmod + share_perm_state recording to happen). Production
// VM runs as root so the EPERM branch never fires there.
func (r *RealFS) Chown(path string, uid, gid int) error {
	full, err := r.resolve(path)
	if err != nil {
		return err
	}
	if err := os.Chown(full, uid, gid); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			// Soft-fail under unprivileged dev runs; the rest of the
			// reconcile pipeline still records the intended ownership
			// in share_perm_state so a later root-privileged run can
			// converge.
			return nil
		}
		return fmt.Errorf("system fs: chown %q: %w", full, err)
	}
	return nil
}

// Chmod sets the mode bits including setuid/setgid/sticky. Go's
// os.Chmod() drops those high bits on POSIX systems before invoking the
// kernel, so we translate to the os.FileMode "Mode*" sentinel bits the
// runtime understands. The runtime then preserves them through to chmod(2).
func (r *RealFS) Chmod(path string, mode os.FileMode) error {
	full, err := r.resolve(path)
	if err != nil {
		return err
	}
	// Translate the raw u/g/s/sticky bits encoded as the upper octal
	// digit (e.g. 02775 -> 0775 + ModeSetgid) into FileMode flags.
	withFlags := mode.Perm()
	if mode&0o4000 != 0 {
		withFlags |= os.ModeSetuid
	}
	if mode&0o2000 != 0 {
		withFlags |= os.ModeSetgid
	}
	if mode&0o1000 != 0 {
		withFlags |= os.ModeSticky
	}
	if err := os.Chmod(full, withFlags); err != nil {
		return fmt.Errorf("system fs: chmod %q: %w", full, err)
	}
	return nil
}

func (r *RealFS) Stat(path string) (os.FileInfo, error) {
	full, err := r.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.Stat(full)
}
