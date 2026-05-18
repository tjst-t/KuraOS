-- Migration 0012: backup schedules + backend configs (Se1e7a6).
--
-- backup_schedules stores cron-driven snapshot schedule configs.
-- backup_backends stores offsite backend configs.
-- Both tables carry only structural/declarative state; credentials live in
-- the vault (DESIGN_PRINCIPLES priority #1 — no secrets in SQLite).

CREATE TABLE IF NOT EXISTS backup_schedules (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    cron_expr  TEXT    NOT NULL,
    datasets   TEXT    NOT NULL DEFAULT '',   -- newline-separated list
    ret_hourly INTEGER NOT NULL DEFAULT 0,
    ret_daily  INTEGER NOT NULL DEFAULT 0,
    ret_monthly INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS backup_backends (
    id               TEXT    PRIMARY KEY,
    name             TEXT    NOT NULL,
    kind             TEXT    NOT NULL,          -- zfs_send | restic | rclone
    credential_state TEXT    NOT NULL DEFAULT 'unset',
    config_json      TEXT    NOT NULL DEFAULT '{}',
    created_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
