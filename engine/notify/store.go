package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ChannelRow is one row from notification_channels.
type ChannelRow struct {
	ID              string
	Name            string
	Kind            string
	Enabled         bool
	SeverityFilter  []string // split from comma-separated DB field
	CategoryFilter  []string // empty = all categories
	CredentialState string
	ConfigJSON      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Store manages notification_channels in SQLite.
type Store struct {
	db *sql.DB
}

// NewStore wraps db.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// List returns all notification channels in insertion order.
func (s *Store) List(ctx context.Context) ([]ChannelRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, kind, enabled, severity_filter, category_filter,
		       credential_state, config_json, created_at, updated_at
		FROM notification_channels ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("notify store list: %w", err)
	}
	defer rows.Close()
	return scanChannelRows(rows)
}

// Get returns a single channel by ID.
func (s *Store) Get(ctx context.Context, id string) (ChannelRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, kind, enabled, severity_filter, category_filter,
		       credential_state, config_json, created_at, updated_at
		FROM notification_channels WHERE id = ?`, id)
	rows, err := scanOneChannelRow(row)
	if err != nil {
		return ChannelRow{}, fmt.Errorf("notify store get %q: %w", id, err)
	}
	return rows, nil
}

// Upsert inserts or replaces a channel row (keyed by id).
func (s *Store) Upsert(ctx context.Context, ch ChannelRow) error {
	sevFilter := strings.Join(ch.SeverityFilter, ",")
	catFilter := strings.Join(ch.CategoryFilter, ",")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_channels
		    (id, name, kind, enabled, severity_filter, category_filter,
		     credential_state, config_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		    name=excluded.name, kind=excluded.kind, enabled=excluded.enabled,
		    severity_filter=excluded.severity_filter,
		    category_filter=excluded.category_filter,
		    credential_state=excluded.credential_state,
		    config_json=excluded.config_json,
		    updated_at=excluded.updated_at`,
		ch.ID, ch.Name, ch.Kind, boolToInt(ch.Enabled),
		sevFilter, catFilter, ch.CredentialState, ch.ConfigJSON, now, now)
	if err != nil {
		return fmt.Errorf("notify store upsert: %w", err)
	}
	return nil
}

// Delete removes a channel by ID. Not found is not an error.
func (s *Store) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM notification_channels WHERE id = ?`, id); err != nil {
		return fmt.Errorf("notify store delete %q: %w", id, err)
	}
	return nil
}

// SetCredentialState updates only the credential_state field for a channel.
func (s *Store) SetCredentialState(ctx context.Context, id, state string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE notification_channels SET credential_state=?, updated_at=? WHERE id=?`,
		state, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func scanChannelRows(rows *sql.Rows) ([]ChannelRow, error) {
	var out []ChannelRow
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanOneChannelRow(row *sql.Row) (ChannelRow, error) {
	var (
		id, name, kind, sevFilter, catFilter, credState, cfgJSON, createdAt, updatedAt string
		enabled                                                                          int
	)
	if err := row.Scan(&id, &name, &kind, &enabled, &sevFilter, &catFilter,
		&credState, &cfgJSON, &createdAt, &updatedAt); err != nil {
		return ChannelRow{}, err
	}
	return buildRow(id, name, kind, enabled, sevFilter, catFilter, credState, cfgJSON, createdAt, updatedAt), nil
}

func scanRow(rows *sql.Rows) (ChannelRow, error) {
	var (
		id, name, kind, sevFilter, catFilter, credState, cfgJSON, createdAt, updatedAt string
		enabled                                                                          int
	)
	if err := rows.Scan(&id, &name, &kind, &enabled, &sevFilter, &catFilter,
		&credState, &cfgJSON, &createdAt, &updatedAt); err != nil {
		return ChannelRow{}, err
	}
	return buildRow(id, name, kind, enabled, sevFilter, catFilter, credState, cfgJSON, createdAt, updatedAt), nil
}

func buildRow(id, name, kind string, enabled int, sevFilter, catFilter, credState, cfgJSON, createdAt, updatedAt string) ChannelRow {
	r := ChannelRow{
		ID:              id,
		Name:            name,
		Kind:            kind,
		Enabled:         enabled != 0,
		CredentialState: credState,
		ConfigJSON:      cfgJSON,
	}
	if sevFilter != "" {
		r.SeverityFilter = strings.Split(sevFilter, ",")
	}
	if catFilter != "" {
		r.CategoryFilter = strings.Split(catFilter, ",")
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return r
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// BuildChannel constructs the appropriate Channel implementation from a
// ChannelRow. The secret (smtp password, ntfy token, etc.) must be injected
// into configJSON by the caller before calling BuildChannel — the store does
// not have access to the vault. Returns an error for unknown kinds.
func BuildChannel(row ChannelRow) (Channel, error) {
	switch row.Kind {
	case "ntfy":
		var cfg NtfyConfig
		if err := json.Unmarshal([]byte(row.ConfigJSON), &cfg); err != nil {
			return nil, fmt.Errorf("notify: unmarshal ntfy config: %w", err)
		}
		return NewNtfyChannel(cfg), nil
	case "webhook":
		var cfg WebhookConfig
		if err := json.Unmarshal([]byte(row.ConfigJSON), &cfg); err != nil {
			return nil, fmt.Errorf("notify: unmarshal webhook config: %w", err)
		}
		return NewWebhookChannel(cfg), nil
	case "smtp":
		var cfg SMTPConfig
		if err := json.Unmarshal([]byte(row.ConfigJSON), &cfg); err != nil {
			return nil, fmt.Errorf("notify: unmarshal smtp config: %w", err)
		}
		return NewSMTPChannel(cfg), nil
	case "line_notify":
		var cfg LineNotifyConfig
		if err := json.Unmarshal([]byte(row.ConfigJSON), &cfg); err != nil {
			return nil, fmt.Errorf("notify: unmarshal line_notify config: %w", err)
		}
		return NewLineNotifyChannel(cfg), nil
	case "gotify":
		var cfg GotifyConfig
		if err := json.Unmarshal([]byte(row.ConfigJSON), &cfg); err != nil {
			return nil, fmt.Errorf("notify: unmarshal gotify config: %w", err)
		}
		return NewGotifyChannel(cfg), nil
	default:
		return nil, fmt.Errorf("notify: unknown channel kind %q", row.Kind)
	}
}
