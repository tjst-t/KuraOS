package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db   *sql.DB
	path string
}

// Open ensures the parent directory exists, opens the SQLite file at path
// (creating it if absent), and applies any pending migrations. It returns the
// ready-to-use Store. The caller must Close.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("store: empty path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: mkdir %q: %w", dir, err)
		}
	}
	// _pragma options keep behavior predictable: WAL for concurrent readers,
	// foreign_keys on so future migrations can rely on referential integrity.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: ping %q: %w", path, err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Path() string { return s.path }

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// migrate applies any embedded migration files whose numeric version is greater
// than the most recently applied one recorded in schema_version.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
        CREATE TABLE IF NOT EXISTS schema_version (
            version    INTEGER PRIMARY KEY,
            applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
        )`); err != nil {
		return fmt.Errorf("store: ensure schema_version: %w", err)
	}

	pending, err := loadMigrations()
	if err != nil {
		return err
	}

	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return err
	}

	for _, m := range pending {
		if applied[m.version] {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("store: apply migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_version`)
	if err != nil {
		return nil, fmt.Errorf("store: query schema_version: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scan schema_version: %w", err)
		}
		out[v] = true
	}
	return out, rows.Err()
}

func (s *Store) applyMigration(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, m.version); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	return tx.Commit()
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations dir: %w", err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		ver, err := parseVersion(e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: migration %q: %w", e.Name(), err)
		}
		raw, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read %q: %w", e.Name(), err)
		}
		out = append(out, migration{version: ver, name: e.Name(), sql: string(raw)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// parseVersion extracts the leading integer from "0001_init.sql"-style names.
// Filenames must start with digits; an integer prefix lets us guarantee
// deterministic ordering across operating systems regardless of locale collation.
func parseVersion(name string) (int, error) {
	cut := 0
	for cut < len(name) && name[cut] >= '0' && name[cut] <= '9' {
		cut++
	}
	if cut == 0 {
		return 0, fmt.Errorf("filename %q: expected leading digits", name)
	}
	var v int
	if _, err := fmt.Sscanf(name[:cut], "%d", &v); err != nil {
		return 0, fmt.Errorf("filename %q: parse version: %w", name, err)
	}
	return v, nil
}
