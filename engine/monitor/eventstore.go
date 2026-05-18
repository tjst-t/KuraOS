package monitor

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// EventStore persists Event rows to the event_log SQLite table. It implements
// the persist func signature expected by NewEventBus.
type EventStore struct {
	db *sql.DB
}

// NewEventStore wraps db for event_log operations.
func NewEventStore(db *sql.DB) *EventStore {
	return &EventStore{db: db}
}

// Persist writes ev to the event_log table. Called synchronously from Publish;
// errors are silently dropped to keep the publish path non-blocking. If the
// caller needs to know about DB failures it should wrap or replace this method.
func (s *EventStore) Persist(ev Event) {
	// Intentionally ignores error — a DB write failure must not crash the
	// event bus or block the publish caller (DESIGN_PRINCIPLES #5: reliability
	// > features; monitoring must not destabilise the main serving path).
	_, _ = s.db.ExecContext(context.Background(), `
		INSERT INTO event_log (occurred_at, severity, category, source, title, detail, metric_name, metric_value)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.Timestamp.UTC().Format(time.RFC3339Nano),
		string(ev.Severity),
		ev.Category,
		ev.Source,
		ev.Title,
		ev.Detail,
		ev.MetricName,
		ev.MetricValue,
	)
}

// QueryResult is one row from event_log.
type QueryResult struct {
	ID          int64
	OccurredAt  time.Time
	Severity    Severity
	Category    string
	Source      string
	Title       string
	Detail      string
	MetricName  string
	MetricValue float64
}

// List returns the most recent limit events, optionally filtered by severity
// and/or category. Empty strings for severity/category mean "all".
func (s *EventStore) List(ctx context.Context, severity, category string, limit int) ([]QueryResult, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id, occurred_at, severity, category, source, title, detail, metric_name, metric_value
	          FROM event_log WHERE 1=1`
	var args []any
	if severity != "" {
		query += " AND severity = ?"
		args = append(args, severity)
	}
	if category != "" {
		query += " AND category = ?"
		args = append(args, category)
	}
	query += " ORDER BY occurred_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("event_log list: %w", err)
	}
	defer rows.Close()

	var out []QueryResult
	for rows.Next() {
		var r QueryResult
		var ts string
		if err := rows.Scan(&r.ID, &ts, &r.Severity, &r.Category, &r.Source,
			&r.Title, &r.Detail, &r.MetricName, &r.MetricValue); err != nil {
			return nil, fmt.Errorf("event_log scan: %w", err)
		}
		r.OccurredAt, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, r)
	}
	return out, rows.Err()
}
