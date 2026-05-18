package fileapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MemBackend is an in-memory FileBackend for testing. It stores file contents
// in a map keyed by absolute path. Thread-safe.
type MemBackend struct {
	mu    sync.RWMutex
	files map[string]*memFile // keyed by clean absolute path
}

type memFile struct {
	name    string
	content []byte
	isDir   bool
	modTime time.Time
}

// NewMemBackend returns an empty MemBackend. Callers can seed it via WriteFile.
func NewMemBackend() *MemBackend {
	m := &MemBackend{files: make(map[string]*memFile)}
	// Always create the root entry.
	m.files["/"] = &memFile{name: "/", isDir: true, modTime: time.Now()}
	return m
}

// WriteFile seeds the backend with a file at path with the given content.
func (m *MemBackend) WriteFile(path string, content []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	// Ensure parents exist.
	for p := filepath.Dir(path); p != "/" && p != "."; p = filepath.Dir(p) {
		if _, ok := m.files[p]; !ok {
			m.files[p] = &memFile{name: filepath.Base(p), isDir: true, modTime: time.Now()}
		}
	}
	m.files[path] = &memFile{name: filepath.Base(path), content: append([]byte{}, content...), modTime: time.Now()}
}

// WriteDir seeds a directory at path.
func (m *MemBackend) WriteDir(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	m.files[path] = &memFile{name: filepath.Base(path), isDir: true, modTime: time.Now()}
}

// FileContent returns the raw bytes of the file at path (for assertions).
func (m *MemBackend) FileContent(path string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.files[filepath.Clean(path)]
	if !ok || f.isDir {
		return nil, false
	}
	return append([]byte{}, f.content...), true
}

// Exists returns true if the path exists in the backend.
func (m *MemBackend) Exists(path string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.files[filepath.Clean(path)]
	return ok
}

func (m *MemBackend) Stat(_ context.Context, path string) (FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.files[filepath.Clean(path)]
	if !ok {
		return FileInfo{}, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
	}
	return memFileInfo(f), nil
}

func (m *MemBackend) ReadDir(_ context.Context, path string) ([]FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	clean := filepath.Clean(path)
	dir, ok := m.files[clean]
	if !ok {
		return nil, &os.PathError{Op: "readdir", Path: path, Err: os.ErrNotExist}
	}
	if !dir.isDir {
		return nil, fmt.Errorf("fileapi mem: %q is not a directory", path)
	}
	var out []FileInfo
	for p, f := range m.files {
		if filepath.Dir(p) == clean && p != clean {
			out = append(out, memFileInfo(f))
		}
	}
	return out, nil
}

func (m *MemBackend) Open(_ context.Context, path string) (io.ReadSeekCloser, FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.files[filepath.Clean(path)]
	if !ok {
		return nil, FileInfo{}, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	if f.isDir {
		return nil, FileInfo{}, fmt.Errorf("fileapi mem: %q is a directory", path)
	}
	return openMemReadSeekCloser(f.content), memFileInfo(f), nil
}

// nopReadSeekCloser wraps bytes.Reader to satisfy io.ReadSeekCloser.
// bytes.Reader already implements Read and Seek; we add a no-op Close.
type nopReadSeekCloser struct {
	*bytes.Reader
}

func (nopReadSeekCloser) Close() error { return nil }

// Verify the interface at compile time.
var _ io.ReadSeekCloser = nopReadSeekCloser{}

func (m *MemBackend) Create(_ context.Context, path string, content io.Reader) error {
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	clean := filepath.Clean(path)
	// Ensure parent dir exists.
	parent := filepath.Dir(clean)
	if _, ok := m.files[parent]; !ok {
		return &os.PathError{Op: "create", Path: path, Err: os.ErrNotExist}
	}
	m.files[clean] = &memFile{name: filepath.Base(clean), content: data, modTime: time.Now()}
	return nil
}

func (m *MemBackend) Mkdir(_ context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	clean := filepath.Clean(path)
	// Ensure parents exist (MkdirAll semantics).
	parts := strings.Split(clean, "/")
	accumulated := ""
	for _, part := range parts {
		if part == "" {
			accumulated = "/"
			continue
		}
		if accumulated == "/" {
			accumulated = "/" + part
		} else {
			accumulated = accumulated + "/" + part
		}
		if _, ok := m.files[accumulated]; !ok {
			m.files[accumulated] = &memFile{name: part, isDir: true, modTime: time.Now()}
		}
	}
	return nil
}

func (m *MemBackend) Remove(_ context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	clean := filepath.Clean(path)
	f, ok := m.files[clean]
	if !ok {
		return &os.PathError{Op: "remove", Path: path, Err: os.ErrNotExist}
	}
	if f.isDir {
		// Check no children.
		for p := range m.files {
			if filepath.Dir(p) == clean && p != clean {
				return fmt.Errorf("fileapi mem: directory not empty: %q", path)
			}
		}
	}
	delete(m.files, clean)
	return nil
}

func (m *MemBackend) RemoveAll(_ context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	clean := filepath.Clean(path)
	prefix := clean + "/"
	for p := range m.files {
		if p == clean || strings.HasPrefix(p, prefix) {
			delete(m.files, p)
		}
	}
	return nil
}

func (m *MemBackend) Rename(_ context.Context, oldpath, newpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldClean := filepath.Clean(oldpath)
	newClean := filepath.Clean(newpath)
	f, ok := m.files[oldClean]
	if !ok {
		return &os.PathError{Op: "rename", Path: oldpath, Err: os.ErrNotExist}
	}
	// Ensure destination parent exists.
	newParent := filepath.Dir(newClean)
	if _, ok := m.files[newParent]; !ok {
		return &os.PathError{Op: "rename", Path: newpath, Err: errors.New("parent directory does not exist")}
	}
	// Move all children if directory.
	oldPrefix := oldClean + "/"
	for p, child := range m.files {
		if strings.HasPrefix(p, oldPrefix) {
			rel := strings.TrimPrefix(p, oldClean)
			m.files[newClean+rel] = child
			delete(m.files, p)
		}
	}
	// Move the entry itself.
	f.name = filepath.Base(newClean)
	m.files[newClean] = f
	delete(m.files, oldClean)
	return nil
}

func (m *MemBackend) Copy(_ context.Context, src, dst string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	srcClean := filepath.Clean(src)
	dstClean := filepath.Clean(dst)
	f, ok := m.files[srcClean]
	if !ok {
		return &os.PathError{Op: "copy", Path: src, Err: os.ErrNotExist}
	}
	if f.isDir {
		return fmt.Errorf("fileapi mem: copy: %q is a directory", src)
	}
	dstParent := filepath.Dir(dstClean)
	if _, ok := m.files[dstParent]; !ok {
		return &os.PathError{Op: "copy", Path: dst, Err: errors.New("destination parent does not exist")}
	}
	m.files[dstClean] = &memFile{
		name:    filepath.Base(dstClean),
		content: append([]byte{}, f.content...),
		modTime: time.Now(),
	}
	return nil
}

func memFileInfo(f *memFile) FileInfo {
	return FileInfo{
		Name:    f.name,
		Size:    int64(len(f.content)),
		IsDir:   f.isDir,
		ModTime: f.modTime,
		Mode:    "-rw-r--r--",
	}
}

// openMemReadSeekCloser returns an io.ReadSeekCloser backed by a bytes.Reader.
func openMemReadSeekCloser(content []byte) io.ReadSeekCloser {
	// Make a copy so concurrent tests don't share the underlying slice.
	return &nopReadSeekCloser{bytes.NewReader(append([]byte{}, content...))}
}
