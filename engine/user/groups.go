package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrGroupNameTaken is returned by CreateGroup on a UNIQUE collision so the
// UI can surface a translated message instead of a generic 500.
var ErrGroupNameTaken = errors.New("user: group name already taken")

// ErrGroupNotFound is returned by Group / membership lookups when the row
// is missing. Caller should errors.Is to map to the i18n message.
var ErrGroupNotFound = errors.New("user: group not found")

// CreateGroup inserts a new group row. Name is normalised to lowercase to
// match the projected /etc/group entry (Linux groupnames are case-sensitive
// in theory but case-insensitive in every NSS backend that matters).
func (s *Store) CreateGroup(ctx context.Context, name, description string) (Group, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return Group{}, errors.New("user: group name is empty")
	}
	if !validGroupName(name) {
		return Group{}, fmt.Errorf("user: invalid group name %q", name)
	}
	id := newID()
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO groups (id, name, description, created_at)
		VALUES (?, ?, ?, ?)
	`, id, name, description, now)
	if err != nil {
		if isUniqueViolation(err) {
			return Group{}, ErrGroupNameTaken
		}
		return Group{}, fmt.Errorf("user: insert group: %w", err)
	}
	return s.GetGroupByID(ctx, id)
}

// DeleteGroup removes the group row. ON DELETE CASCADE drops the
// user_groups link rows. The caller (engine/system) is responsible for
// rejecting the call if the group is still referenced by a Share ACL —
// this method enforces nothing about share consumers.
func (s *Store) DeleteGroup(ctx context.Context, groupID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, groupID)
	if err != nil {
		return fmt.Errorf("user: delete group: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrGroupNotFound
	}
	return nil
}

// GetGroupByID returns the row by primary key.
func (s *Store) GetGroupByID(ctx context.Context, id string) (Group, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, created_at FROM groups WHERE id = ?
	`, id)
	return scanGroup(row)
}

// GetGroupByName returns the row by lowercased name.
func (s *Store) GetGroupByName(ctx context.Context, name string) (Group, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, created_at FROM groups WHERE name = ?
	`, name)
	return scanGroup(row)
}

// ListGroups enumerates every group, ordered by name. Used by the Users
// page (Groups tab + Share ACL picker) and by engine/system Reconcile.
func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, created_at FROM groups ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("user: list groups: %w", err)
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		var created string
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &created); err != nil {
			return nil, fmt.Errorf("user: scan group: %w", err)
		}
		if t, perr := time.Parse(time.RFC3339Nano, created); perr == nil {
			g.CreatedAt = t
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetGroupMembers replaces the membership list for groupID with userIDs in
// a single transaction. Order does not matter; duplicates are silently
// dropped by the PRIMARY KEY.
func (s *Store) SetGroupMembers(ctx context.Context, groupID string, userIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("user: begin: %w", err)
	}
	defer tx.Rollback()

	// Confirm the group exists so we don't silently no-op when the caller
	// passes an unknown ID.
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE id = ?`, groupID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrGroupNotFound
		}
		return fmt.Errorf("user: lookup group: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_groups WHERE group_id = ?`, groupID); err != nil {
		return fmt.Errorf("user: clear group members: %w", err)
	}
	for _, uid := range userIDs {
		if uid == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO user_groups (user_id, group_id) VALUES (?, ?)
		`, uid, groupID); err != nil {
			return fmt.Errorf("user: insert group member: %w", err)
		}
	}
	return tx.Commit()
}

// GroupsForUser returns the group rows the user belongs to.
func (s *Store) GroupsForUser(ctx context.Context, userID string) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.name, g.description, g.created_at
		FROM groups g
		JOIN user_groups ug ON ug.group_id = g.id
		WHERE ug.user_id = ?
		ORDER BY g.name
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("user: groups for user: %w", err)
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		var created string
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &created); err != nil {
			return nil, fmt.Errorf("user: scan: %w", err)
		}
		if t, perr := time.Parse(time.RFC3339Nano, created); perr == nil {
			g.CreatedAt = t
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// MembersOfGroup returns the user IDs in groupID. Used by engine/system to
// project secondary group membership into /etc/group.
func (s *Store) MembersOfGroup(ctx context.Context, groupID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id FROM user_groups WHERE group_id = ?
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("user: members of group: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("user: scan: %w", err)
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

func scanGroup(row *sql.Row) (Group, error) {
	var g Group
	var created string
	err := row.Scan(&g.ID, &g.Name, &g.Description, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, ErrGroupNotFound
		}
		return Group{}, fmt.Errorf("user: scan group: %w", err)
	}
	if t, perr := time.Parse(time.RFC3339Nano, created); perr == nil {
		g.CreatedAt = t
	}
	return g, nil
}

// validGroupName allows lowercase ASCII letters, digits, hyphen, underscore.
// Mirrors the smbd / Linux groupname rules so the projected entry in
// /etc/group is always valid.
func validGroupName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}
