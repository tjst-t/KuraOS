package share

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store is the SQLite-backed persistence for shares + share_acl. It exposes
// only the primitives the Engine needs — list / get / create / delete — so the
// engine layer carries the policy (validation, conflict detection, template
// regeneration).
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore returns a Store bound to db. Tests inject a fixed clock via
// SetNow; production leaves it on time.Now.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

// SetNow overrides the clock — only used by tests for deterministic timestamps.
func (s *Store) SetNow(now func() time.Time) { s.now = now }

// Create inserts a new share and its ACL entries in a single transaction.
// Returns ErrNameTaken on UNIQUE constraint violation so the engine can
// translate it before reaching the UI.
func (s *Store) Create(ctx context.Context, sh Share) (Share, error) {
	if sh.ID == "" {
		sh.ID = newID()
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Share{}, fmt.Errorf("share store: begin: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO shares (id, name, path, protocol, preset, access_mode, description, disabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, sh.ID, sh.Name, sh.Path, string(sh.Protocol), string(sh.Preset), string(sh.AccessMode), sh.Description, boolToInt(sh.Disabled), now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return Share{}, ErrNameTaken
		}
		return Share{}, fmt.Errorf("share store: insert share: %w", err)
	}
	for _, a := range sh.ACL {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO share_acl (share_id, principal_kind, principal_name, mode)
			VALUES (?, ?, ?, ?)
		`, sh.ID, string(a.Kind), a.Name, string(a.Mode)); err != nil {
			return Share{}, fmt.Errorf("share store: insert acl: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Share{}, fmt.Errorf("share store: commit: %w", err)
	}
	return s.Get(ctx, sh.ID)
}

// Get fetches a single share + ACL by ID. ErrShareNotFound is returned for
// missing rows.
func (s *Store) Get(ctx context.Context, id string) (Share, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, path, protocol, preset, access_mode, description, disabled, created_at, updated_at
		FROM shares WHERE id = ?
	`, id)
	sh, err := scanShare(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Share{}, ErrShareNotFound
		}
		return Share{}, fmt.Errorf("share store: get: %w", err)
	}
	acl, err := s.loadACL(ctx, id)
	if err != nil {
		return Share{}, err
	}
	sh.ACL = acl
	return sh, nil
}

// List returns every share, ordered by name for stable rendering. ACL is
// populated per row — N+1 by design (n tiny, simpler than a JOIN that
// reshapes into nested slices).
func (s *Store) List(ctx context.Context) ([]Share, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, path, protocol, preset, access_mode, description, disabled, created_at, updated_at
		FROM shares ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("share store: list: %w", err)
	}
	defer rows.Close()
	var out []Share
	for rows.Next() {
		sh, err := scanShare(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("share store: scan: %w", err)
		}
		out = append(out, sh)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("share store: rows: %w", err)
	}
	for i := range out {
		acl, err := s.loadACL(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].ACL = acl
	}
	return out, nil
}

// Delete removes a share. The ON DELETE CASCADE on share_acl cleans up the
// ACL rows automatically. Returns ErrShareNotFound when no row was deleted.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM shares WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("share store: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("share store: rows affected: %w", err)
	}
	if n == 0 {
		return ErrShareNotFound
	}
	return nil
}

func (s *Store) loadACL(ctx context.Context, shareID string) ([]ACLEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT principal_kind, principal_name, mode
		FROM share_acl WHERE share_id = ?
		ORDER BY principal_kind, principal_name
	`, shareID)
	if err != nil {
		return nil, fmt.Errorf("share store: acl query: %w", err)
	}
	defer rows.Close()
	var out []ACLEntry
	for rows.Next() {
		var kind, name, mode string
		if err := rows.Scan(&kind, &name, &mode); err != nil {
			return nil, fmt.Errorf("share store: acl scan: %w", err)
		}
		out = append(out, ACLEntry{
			Kind: PrincipalKind(kind),
			Name: name,
			Mode: ACLMode(mode),
		})
	}
	return out, rows.Err()
}

type rowScanner func(...any) error

func scanShare(scan rowScanner) (Share, error) {
	var sh Share
	var protocol, preset, accessMode, created, updated string
	var disabled int
	if err := scan(&sh.ID, &sh.Name, &sh.Path, &protocol, &preset, &accessMode, &sh.Description, &disabled, &created, &updated); err != nil {
		return Share{}, err
	}
	sh.Protocol = Protocol(protocol)
	sh.Preset = Preset(preset)
	sh.AccessMode = AccessMode(accessMode)
	sh.Disabled = disabled != 0
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		sh.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
		sh.UpdatedAt = t
	}
	return sh, nil
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("share: rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "constraint failed")
}
