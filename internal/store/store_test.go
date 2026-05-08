package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpen_CreatesDBAndAppliesMigrations(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "state.db")

	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if got := s.Path(); got != dbPath {
		t.Fatalf("Path() = %q, want %q", got, dbPath)
	}

	tables := mustQueryTables(t, s)
	for _, want := range []string{"schema_version", "kv"} {
		if !tables[want] {
			t.Fatalf("table %q not present after migrate; got %v", want, tables)
		}
	}

	var v int
	if err := s.DB().QueryRowContext(ctx, `SELECT version FROM schema_version ORDER BY version DESC LIMIT 1`).Scan(&v); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if v < 1 {
		t.Fatalf("schema_version min = %d, want >= 1", v)
	}
}

func TestOpen_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	ctx := context.Background()
	s1, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	var n int
	if err := s2.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
		t.Fatalf("count schema_version: %v", err)
	}
	if n == 0 {
		t.Fatalf("schema_version count = 0, want > 0 after re-open")
	}
}

func TestOpen_EmptyPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("Open(\"\") error = nil, want error")
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{"basic", "0001_init.sql", 1, false},
		{"larger", "0042_add_users.sql", 42, false},
		{"no_prefix", "init.sql", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseVersion(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func mustQueryTables(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.DB().QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[n] = true
	}
	return out
}
