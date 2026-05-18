// Package fileapi implements the KuraOS File API — 8 HTTP endpoints for
// native apps to access user files via scoped, HMAC-authenticated requests.
//
// Architecture:
//   - FileBackend interface abstracts the filesystem so tests use MemBackend
//   - osBackend wraps the real os package for production
//   - TokenValidator interface abstracts HMAC verification
//   - ScopeStore interface abstracts the per-app scope DB lookup
//
// Path traversal hardening (DESIGN_PRINCIPLES priority #5):
//   - Every path goes through safeJoin which filepath.Clean + HasPrefix
//   - Symlinks that escape the share root are rejected
//   - Filenames with ".." segments after Clean are rejected
package fileapi

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileInfo describes a single filesystem entry. Mirrors the fields returned
// by the list endpoint so handlers don't leak os.FileInfo to templates.
type FileInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"mod_time"`
	Mode    string    `json:"mode"`
}

// FileBackend is the abstract filesystem the File API operates on.
// Production uses osBackend; tests use MemBackend.
//
// All paths are already sanitised by safeJoin before being passed to
// backend methods — backends may trust they are within the share root.
type FileBackend interface {
	// Stat returns info about the entry at path.
	Stat(ctx context.Context, path string) (FileInfo, error)
	// ReadDir returns the immediate children of the directory at path.
	ReadDir(ctx context.Context, path string) ([]FileInfo, error)
	// Open opens the file at path for reading. Caller must close the returned
	// ReadSeekCloser. The second return value is the file's size (needed by
	// http.ServeContent).
	Open(ctx context.Context, path string) (io.ReadSeekCloser, FileInfo, error)
	// Create creates or overwrites the file at path with the provided content.
	Create(ctx context.Context, path string, content io.Reader) error
	// Mkdir creates the directory at path (and any missing parents).
	Mkdir(ctx context.Context, path string) error
	// Remove removes the file or empty directory at path.
	Remove(ctx context.Context, path string) error
	// RemoveAll recursively removes path.
	RemoveAll(ctx context.Context, path string) error
	// Rename renames (moves) oldpath to newpath.
	Rename(ctx context.Context, oldpath, newpath string) error
	// Copy copies the file at src to dst (within the same backend).
	Copy(ctx context.Context, src, dst string) error
}

// osBackend implements FileBackend using the real os package.
type osBackend struct{}

// NewOSBackend returns a FileBackend backed by the real filesystem.
func NewOSBackend() FileBackend {
	return &osBackend{}
}

func (b *osBackend) Stat(_ context.Context, path string) (FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return FileInfo{}, err
	}
	return fileInfoFromOS(info), nil
}

func (b *osBackend) ReadDir(_ context.Context, path string) ([]FileInfo, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, fileInfoFromOS(info))
	}
	return out, nil
}

func (b *osBackend) Open(_ context.Context, path string) (io.ReadSeekCloser, FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, FileInfo{}, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, FileInfo{}, err
	}
	return f, fileInfoFromOS(info), nil
}

func (b *osBackend) Create(_ context.Context, path string, content io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, content)
	return err
}

func (b *osBackend) Mkdir(_ context.Context, path string) error {
	return os.MkdirAll(path, 0o755)
}

func (b *osBackend) Remove(_ context.Context, path string) error {
	return os.Remove(path)
}

func (b *osBackend) RemoveAll(_ context.Context, path string) error {
	return os.RemoveAll(path)
}

func (b *osBackend) Rename(_ context.Context, oldpath, newpath string) error {
	if err := os.MkdirAll(filepath.Dir(newpath), 0o755); err != nil {
		return err
	}
	return os.Rename(oldpath, newpath)
}

func (b *osBackend) Copy(_ context.Context, src, dst string) error {
	srcF, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcF.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	dstF, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstF.Close()
	_, err = io.Copy(dstF, srcF)
	return err
}

// fileInfoFromOS converts an os.FileInfo to our FileInfo.
func fileInfoFromOS(info fs.FileInfo) FileInfo {
	return FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime(),
		Mode:    info.Mode().String(),
	}
}

// safeJoin joins root and the user-supplied relPath, ensuring the result
// stays within root. It rejects:
//   - paths that contain ".." segments after Clean
//   - paths that escape root (symlinks can still escape — use safeJoinReal for production)
//   - empty paths
//
// Returns the clean absolute path or an error.
func safeJoin(root, relPath string) (string, error) {
	if strings.Contains(relPath, "..") {
		return "", &traversalError{path: relPath, reason: "contains '..' segment"}
	}
	cleaned := filepath.Join(root, filepath.Clean("/"+relPath))
	// Ensure the cleaned path starts with root followed by a separator or
	// exactly equals root. This prevents path tricks that survive Clean.
	if cleaned != root && !strings.HasPrefix(cleaned, root+string(os.PathSeparator)) {
		return "", &traversalError{path: relPath, reason: "escapes share root"}
	}
	return cleaned, nil
}

// safeJoinReal is like safeJoin but additionally calls filepath.EvalSymlinks
// on the result to reject symlinks that resolve outside root.
// This is the production variant; tests use MemBackend which doesn't have symlinks.
func safeJoinReal(root, relPath string) (string, error) {
	joined, err := safeJoin(root, relPath)
	if err != nil {
		return "", err
	}
	// If the path doesn't exist yet (for writes), check the parent.
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		// Path doesn't exist yet. Check the parent for symlink escapes.
		resolved, err = filepath.EvalSymlinks(filepath.Dir(joined))
		if err != nil {
			// Parent also doesn't exist — let the backend report the error.
			return joined, nil
		}
		resolved = filepath.Join(resolved, filepath.Base(joined))
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return "", &traversalError{path: relPath, reason: "symlink escapes share root"}
	}
	return resolved, nil
}

// traversalError is returned when a path traversal attempt is detected.
type traversalError struct {
	path   string
	reason string
}

func (e *traversalError) Error() string {
	return "fileapi: path traversal detected: " + e.path + ": " + e.reason
}
