// Package logging implements JSONL log ingestion and storage for KuraOS.
//
// Responsibilities:
//   - Ingest log entries from kuraos-internal, Docker containers, and journald
//   - Write them to daily-rotated JSONL files in /var/log/kuraos/
//   - Enforce retention by count (delete oldest files beyond the limit)
//   - Expose an SSE stream for live log viewing in the UI
//
// DESIGN_PRINCIPLES priority #9: LogWriter interface lets tests use a mem store.
// DESIGN_PRINCIPLES priority #1: retention config goes in config.json logging section.
package logging

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one log line stored in the JSONL file.
type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`  // "info" | "warn" | "error" | "debug"
	Source    string    `json:"source"` // "kuraos" | "docker:<name>" | "journald"
	Message   string    `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Filter selects log entries for display / streaming.
type Filter struct {
	Source string // empty = all
	Level  string // empty = all
	Search string // empty = all; substring match on Message
}

// LogWriter is the interface for writing log entries. Tests can substitute
// a MemWriter to avoid filesystem I/O.
type LogWriter interface {
	Write(ctx context.Context, e Entry) error
}

// LogReader is the interface for reading log entries from the store.
type LogReader interface {
	// Query returns entries matching f, newest first, limited to limit.
	Query(ctx context.Context, f Filter, limit int) ([]Entry, error)
}

// Store writes JSONL entries to daily-rotated files and reads them back.
// [AC-Sf92666-3-1]
type Store struct {
	mu         sync.Mutex
	dir        string
	retention  int // max number of daily files to keep
	sub        *subscriber
}

// subscriber holds SSE live-stream channels.
type subscriber struct {
	mu  sync.Mutex
	chs []chan Entry
}

func (s *subscriber) broadcast(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.chs {
		select {
		case ch <- e:
		default: // skip slow consumers to avoid blocking the writer
		}
	}
}

func (s *subscriber) subscribe() (ch chan Entry, unsub func()) {
	ch = make(chan Entry, 64)
	s.mu.Lock()
	s.chs = append(s.chs, ch)
	s.mu.Unlock()
	unsub = func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		newChs := s.chs[:0]
		for _, c := range s.chs {
			if c != ch {
				newChs = append(newChs, c)
			}
		}
		s.chs = newChs
		close(ch)
	}
	return ch, unsub
}

// NewStore creates a JSONL log store writing to dir.
// retention is the max number of daily JSONL files to keep; 0 means unlimited.
// [AC-Sf92666-3-1]
func NewStore(dir string, retention int) *Store {
	return &Store{dir: dir, retention: retention, sub: &subscriber{}}
}

// Write appends e to today's JSONL file and broadcasts to SSE subscribers.
// [AC-Sf92666-3-1]
func (s *Store) Write(ctx context.Context, e Entry) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("logging: marshal entry: %w", err)
	}
	line = append(line, '\n')

	s.mu.Lock()
	err = s.appendToday(line)
	s.mu.Unlock()
	if err != nil {
		return err
	}

	// Broadcast to SSE subscribers (non-blocking; held subscribers drain async).
	s.sub.broadcast(e)
	return nil
}

// appendToday writes line to the current day's file, creating it if needed.
func (s *Store) appendToday(line []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("logging: mkdir %s: %w", s.dir, err)
	}
	path := s.todayPath()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("logging: open %s: %w", path, err)
	}
	_, err = f.Write(line)
	f.Close()
	if err != nil {
		return fmt.Errorf("logging: write %s: %w", path, err)
	}

	// Enforce retention after writing (cheap — only list dir once per Write).
	if s.retention > 0 {
		_ = s.pruneOldFiles()
	}
	return nil
}

func (s *Store) todayPath() string {
	return filepath.Join(s.dir, time.Now().UTC().Format("2006-01-02")+".jsonl")
}

// pruneOldFiles deletes the oldest JSONL files beyond the retention limit.
// [AC-Sf92666-3-1]
func (s *Store) pruneOldFiles() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files) // ascending = oldest first
	for len(files) > s.retention {
		old := filepath.Join(s.dir, files[0])
		os.Remove(old)
		files = files[1:]
	}
	return nil
}

// Query reads entries from disk matching f, newest first, up to limit.
// [AC-Sf92666-3-1]
func (s *Store) Query(ctx context.Context, f Filter, limit int) ([]Entry, error) {
	files, err := s.listFiles()
	if err != nil {
		return nil, err
	}
	// Read newest files first.
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i]
	}

	var result []Entry
	for _, fname := range files {
		if limit > 0 && len(result) >= limit {
			break
		}
		entries, err := s.readFile(ctx, fname, f)
		if err != nil {
			continue // skip unreadable files
		}
		// Newest-first within each file.
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
		result = append(result, entries...)
	}

	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *Store) listFiles() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("logging: list %s: %w", s.dir, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, filepath.Join(s.dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func (s *Store) readFile(_ context.Context, path string, f Filter) ([]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []Entry
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue // skip malformed lines
		}
		if matchesFilter(e, f) {
			entries = append(entries, e)
		}
	}
	return entries, sc.Err()
}

func matchesFilter(e Entry, f Filter) bool {
	if f.Source != "" && e.Source != f.Source {
		return false
	}
	if f.Level != "" && e.Level != f.Level {
		return false
	}
	if f.Search != "" && !strings.Contains(e.Message, f.Search) {
		return false
	}
	return true
}

// Subscribe returns a channel of live log entries and an unsubscribe function.
// The channel is closed when unsubscribe is called.
// [AC-Sf92666-3-2]
func (s *Store) Subscribe() (ch <-chan Entry, unsub func()) {
	c, u := s.sub.subscribe()
	return c, u
}

// Ingest is a convenience method to write an entry from internal kuraos code.
func (s *Store) Ingest(level, source, message string, fields map[string]any) {
	e := Entry{
		Timestamp: time.Now().UTC(),
		Level:     level,
		Source:    source,
		Message:   message,
		Fields:    fields,
	}
	_ = s.Write(context.Background(), e)
}

// MemWriter is an in-memory LogWriter for tests.
type MemWriter struct {
	mu      sync.Mutex
	entries []Entry
}

// Write records the entry in memory.
func (m *MemWriter) Write(_ context.Context, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
	return nil
}

// Entries returns a copy of all recorded entries.
func (m *MemWriter) Entries() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, len(m.entries))
	copy(out, m.entries)
	return out
}

// WriteTo pipes entries matching f to w, newest first, from r.
// Used to seed a streaming response with history before going live.
func WriteTo(ctx context.Context, r LogReader, f Filter, limit int, w io.Writer) error {
	entries, err := r.Query(ctx, f, limit)
	if err != nil {
		return fmt.Errorf("logging: query for WriteTo: %w", err)
	}
	enc := json.NewEncoder(w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}
