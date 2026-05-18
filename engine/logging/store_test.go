package logging_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuraos-org/kura/engine/logging"
)

// [AC-Sf92666-3-1] Log entries are written to JSONL and readable via Query.
func TestStore_WriteAndQuery(t *testing.T) {
	dir := t.TempDir()
	s := logging.NewStore(dir, 10)
	ctx := context.Background()

	entries := []logging.Entry{
		{Level: "info", Source: "kuraos", Message: "started"},
		{Level: "warn", Source: "kuraos", Message: "slow query"},
		{Level: "error", Source: "docker:nginx", Message: "connection refused"},
	}
	for _, e := range entries {
		if err := s.Write(ctx, e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	// [AC-Sf92666-3-1] Query all entries.
	got, err := s.Query(ctx, logging.Filter{}, 100)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != len(entries) {
		t.Errorf("expected %d entries, got %d", len(entries), len(got))
	}
}

// [AC-Sf92666-3-1] Filter by Source narrows results.
func TestStore_FilterBySource(t *testing.T) {
	dir := t.TempDir()
	s := logging.NewStore(dir, 10)
	ctx := context.Background()

	_ = s.Write(ctx, logging.Entry{Level: "info", Source: "kuraos", Message: "a"})
	_ = s.Write(ctx, logging.Entry{Level: "info", Source: "docker:nginx", Message: "b"})
	_ = s.Write(ctx, logging.Entry{Level: "info", Source: "kuraos", Message: "c"})

	got, err := s.Query(ctx, logging.Filter{Source: "kuraos"}, 100)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 kuraos entries, got %d", len(got))
	}
}

// [AC-Sf92666-3-1] Filter by Level narrows results.
func TestStore_FilterByLevel(t *testing.T) {
	dir := t.TempDir()
	s := logging.NewStore(dir, 10)
	ctx := context.Background()

	_ = s.Write(ctx, logging.Entry{Level: "info", Source: "kuraos", Message: "ok"})
	_ = s.Write(ctx, logging.Entry{Level: "error", Source: "kuraos", Message: "fail"})

	got, err := s.Query(ctx, logging.Filter{Level: "error"}, 100)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].Level != "error" {
		t.Errorf("expected 1 error entry, got %v", got)
	}
}

// [AC-Sf92666-3-1] Filter by text search (substring on Message).
func TestStore_FilterBySearch(t *testing.T) {
	dir := t.TempDir()
	s := logging.NewStore(dir, 10)
	ctx := context.Background()

	_ = s.Write(ctx, logging.Entry{Level: "info", Source: "kuraos", Message: "disk usage 80%"})
	_ = s.Write(ctx, logging.Entry{Level: "info", Source: "kuraos", Message: "cpu usage 20%"})

	got, err := s.Query(ctx, logging.Filter{Search: "disk"}, 100)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Message, "disk") {
		t.Errorf("expected 1 disk entry, got %v", got)
	}
}

// [AC-Sf92666-3-1] Retention removes oldest files when exceeded.
func TestStore_RetentionPrune(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Pre-create 2 old .jsonl files.
	for _, date := range []string{"2024-01-01", "2024-01-02"} {
		path := filepath.Join(dir, date+".jsonl")
		if err := writeJSONL(path, logging.Entry{Level: "info", Source: "kuraos", Message: "old"}); err != nil {
			t.Fatalf("write old file: %v", err)
		}
	}

	s := logging.NewStore(dir, 2) // retain only 2 files (including today's)
	if err := s.Write(ctx, logging.Entry{Level: "info", Source: "kuraos", Message: "new"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// We should have at most 2 files: today's + the newest old one.
	files := listJSONLFiles(dir)
	if len(files) > 2 {
		t.Errorf("expected at most 2 JSONL files, got %d: %v", len(files), files)
	}
}

// [AC-Sf92666-3-2] Subscribe receives entries written after subscription.
func TestStore_Subscribe(t *testing.T) {
	dir := t.TempDir()
	s := logging.NewStore(dir, 10)
	ctx := context.Background()

	ch, unsub := s.Subscribe()
	defer unsub()

	want := "live entry"
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = s.Write(ctx, logging.Entry{Level: "info", Source: "kuraos", Message: want})
	}()

	select {
	case e := <-ch:
		if e.Message != want {
			t.Errorf("expected %q, got %q", want, e.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE entry")
	}
}

// helpers

func writeJSONL(path string, e logging.Entry) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(e)
}

func listJSONLFiles(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, e.Name())
		}
	}
	return files
}
