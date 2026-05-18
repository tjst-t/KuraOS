package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ScheduleRow is one row from backup_schedules.
type ScheduleRow struct {
	ID         string
	Name       string
	CronExpr   string
	Datasets   []string // newline-split from DB
	RetHourly  int
	RetDaily   int
	RetMonthly int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// BackendRow is one row from backup_backends.
type BackendRow struct {
	ID              string
	Name            string
	Kind            string // "zfs_send"|"restic"|"rclone"
	CredentialState string // "set"|"unset"
	ConfigJSON      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Store manages backup schedules and backend configs in SQLite.
// Credentials live in the vault; this store carries structural state only
// (DESIGN_PRINCIPLES priority #1).
type Store struct {
	db *sql.DB
}

// NewStore wraps db.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// ---------- Schedule CRUD ----------

// ListSchedules returns all backup schedule rows in insertion order.
func (s *Store) ListSchedules(ctx context.Context) ([]ScheduleRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, cron_expr, datasets, ret_hourly, ret_daily, ret_monthly,
		       created_at, updated_at
		FROM backup_schedules ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("backup store list schedules: %w", err)
	}
	defer rows.Close()
	return scanScheduleRows(rows)
}

// GetSchedule returns a single schedule by ID.
func (s *Store) GetSchedule(ctx context.Context, id string) (ScheduleRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, cron_expr, datasets, ret_hourly, ret_daily, ret_monthly,
		       created_at, updated_at
		FROM backup_schedules WHERE id = ?`, id)
	return scanOneScheduleRow(row)
}

// UpsertSchedule inserts or replaces a schedule row.
func (s *Store) UpsertSchedule(ctx context.Context, r ScheduleRow) error {
	datasets := strings.Join(r.Datasets, "\n")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO backup_schedules
		    (id, name, cron_expr, datasets, ret_hourly, ret_daily, ret_monthly,
		     created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		    name=excluded.name, cron_expr=excluded.cron_expr, datasets=excluded.datasets,
		    ret_hourly=excluded.ret_hourly, ret_daily=excluded.ret_daily,
		    ret_monthly=excluded.ret_monthly, updated_at=excluded.updated_at`,
		r.ID, r.Name, r.CronExpr, datasets,
		r.RetHourly, r.RetDaily, r.RetMonthly, now, now)
	if err != nil {
		return fmt.Errorf("backup store upsert schedule: %w", err)
	}
	return nil
}

// DeleteSchedule removes a schedule by ID.
func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM backup_schedules WHERE id = ?`, id); err != nil {
		return fmt.Errorf("backup store delete schedule: %w", err)
	}
	return nil
}

// ---------- Backend CRUD ----------

// ListBackends returns all backend rows in insertion order.
func (s *Store) ListBackends(ctx context.Context) ([]BackendRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, kind, credential_state, config_json, created_at, updated_at
		FROM backup_backends ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("backup store list backends: %w", err)
	}
	defer rows.Close()
	return scanBackendRows(rows)
}

// GetBackend returns a single backend by ID.
func (s *Store) GetBackend(ctx context.Context, id string) (BackendRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, kind, credential_state, config_json, created_at, updated_at
		FROM backup_backends WHERE id = ?`, id)
	return scanOneBackendRow(row)
}

// UpsertBackend inserts or replaces a backend row. Credentials are NOT stored
// here — they go to the vault. ConfigJSON carries non-secret settings only.
func (s *Store) UpsertBackend(ctx context.Context, r BackendRow) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO backup_backends
		    (id, name, kind, credential_state, config_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		    name=excluded.name, kind=excluded.kind,
		    credential_state=excluded.credential_state,
		    config_json=excluded.config_json,
		    updated_at=excluded.updated_at`,
		r.ID, r.Name, r.Kind, r.CredentialState, r.ConfigJSON, now, now)
	if err != nil {
		return fmt.Errorf("backup store upsert backend: %w", err)
	}
	return nil
}

// DeleteBackend removes a backend by ID.
func (s *Store) DeleteBackend(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM backup_backends WHERE id = ?`, id); err != nil {
		return fmt.Errorf("backup store delete backend: %w", err)
	}
	return nil
}

// ---------- Scan helpers ----------

func scanScheduleRows(rows *sql.Rows) ([]ScheduleRow, error) {
	var out []ScheduleRow
	for rows.Next() {
		r, err := scanOneScheduleRowFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanOneScheduleRow(row *sql.Row) (ScheduleRow, error) {
	var (
		id, name, cronExpr, datasets, createdAt, updatedAt string
		retH, retD, retM                                   int
	)
	if err := row.Scan(&id, &name, &cronExpr, &datasets, &retH, &retD, &retM, &createdAt, &updatedAt); err != nil {
		return ScheduleRow{}, fmt.Errorf("backup store scan schedule: %w", err)
	}
	return buildScheduleRow(id, name, cronExpr, datasets, retH, retD, retM, createdAt, updatedAt), nil
}

func scanOneScheduleRowFromRows(rows *sql.Rows) (ScheduleRow, error) {
	var (
		id, name, cronExpr, datasets, createdAt, updatedAt string
		retH, retD, retM                                   int
	)
	if err := rows.Scan(&id, &name, &cronExpr, &datasets, &retH, &retD, &retM, &createdAt, &updatedAt); err != nil {
		return ScheduleRow{}, fmt.Errorf("backup store scan schedule row: %w", err)
	}
	return buildScheduleRow(id, name, cronExpr, datasets, retH, retD, retM, createdAt, updatedAt), nil
}

func buildScheduleRow(id, name, cronExpr, datasets string, retH, retD, retM int, createdAt, updatedAt string) ScheduleRow {
	var dsList []string
	for _, ds := range strings.Split(datasets, "\n") {
		ds = strings.TrimSpace(ds)
		if ds != "" {
			dsList = append(dsList, ds)
		}
	}
	ca, _ := time.Parse(time.RFC3339Nano, createdAt)
	ua, _ := time.Parse(time.RFC3339Nano, updatedAt)
	return ScheduleRow{
		ID: id, Name: name, CronExpr: cronExpr, Datasets: dsList,
		RetHourly: retH, RetDaily: retD, RetMonthly: retM,
		CreatedAt: ca, UpdatedAt: ua,
	}
}

func scanBackendRows(rows *sql.Rows) ([]BackendRow, error) {
	var out []BackendRow
	for rows.Next() {
		r, err := scanOneBackendRowFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanOneBackendRow(row *sql.Row) (BackendRow, error) {
	var id, name, kind, credState, cfgJSON, createdAt, updatedAt string
	if err := row.Scan(&id, &name, &kind, &credState, &cfgJSON, &createdAt, &updatedAt); err != nil {
		return BackendRow{}, fmt.Errorf("backup store scan backend: %w", err)
	}
	return buildBackendRow(id, name, kind, credState, cfgJSON, createdAt, updatedAt), nil
}

func scanOneBackendRowFromRows(rows *sql.Rows) (BackendRow, error) {
	var id, name, kind, credState, cfgJSON, createdAt, updatedAt string
	if err := rows.Scan(&id, &name, &kind, &credState, &cfgJSON, &createdAt, &updatedAt); err != nil {
		return BackendRow{}, fmt.Errorf("backup store scan backend row: %w", err)
	}
	return buildBackendRow(id, name, kind, credState, cfgJSON, createdAt, updatedAt), nil
}

func buildBackendRow(id, name, kind, credState, cfgJSON, createdAt, updatedAt string) BackendRow {
	ca, _ := time.Parse(time.RFC3339Nano, createdAt)
	ua, _ := time.Parse(time.RFC3339Nano, updatedAt)
	return BackendRow{
		ID: id, Name: name, Kind: kind,
		CredentialState: credState, ConfigJSON: cfgJSON,
		CreatedAt: ca, UpdatedAt: ua,
	}
}

// ConfigAsMap unmarshals a BackendRow's ConfigJSON into a map.
func (r BackendRow) ConfigAsMap() map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(r.ConfigJSON), &m)
	if m == nil {
		m = make(map[string]any)
	}
	return m
}
