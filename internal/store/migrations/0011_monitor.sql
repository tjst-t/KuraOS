-- event_log stores every Event published to the EventBus so the Settings
-- page can show the event history. All events are written here regardless
-- of whether a notification channel matches, so admins can see the full
-- timeline even without configuring notifications.
--
-- notification_channels is the SSOT persistence for Story 3: each row
-- mirrors one entry from config.json `notifications.channels[]`. Secrets
-- (smtp password, ntfy token, etc.) live in the vault — this table only
-- carries the structural config.
CREATE TABLE IF NOT EXISTS event_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    severity    TEXT    NOT NULL,  -- 'info' | 'warning' | 'critical' | 'ok'
    category    TEXT    NOT NULL,  -- e.g. 'storage', 'apps', 'backup'
    source      TEXT    NOT NULL,  -- engine name
    title       TEXT    NOT NULL,
    detail      TEXT    NOT NULL DEFAULT '',
    metric_name TEXT    NOT NULL DEFAULT '',
    metric_value REAL   NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS event_log_occurred_at ON event_log (occurred_at DESC);
CREATE INDEX IF NOT EXISTS event_log_severity     ON event_log (severity);
CREATE INDEX IF NOT EXISTS event_log_category     ON event_log (category);

-- notification_channels holds the structural config for each notification
-- channel. Credentials are stored in the vault (kv table under key
-- 'notify.<id>.secret') — only credential_state ('set'|'unset') is here.
CREATE TABLE IF NOT EXISTS notification_channels (
    id                  TEXT    PRIMARY KEY,
    name                TEXT    NOT NULL,
    kind                TEXT    NOT NULL,  -- 'ntfy'|'webhook'|'smtp'|'line_notify'|'gotify'
    enabled             INTEGER NOT NULL DEFAULT 1,
    severity_filter     TEXT    NOT NULL DEFAULT 'info,warning,critical,ok',
    category_filter     TEXT    NOT NULL DEFAULT '',  -- empty = all categories
    credential_state    TEXT    NOT NULL DEFAULT 'unset',
    -- kind-specific config fields (JSON blob keeps the schema generic)
    config_json         TEXT    NOT NULL DEFAULT '{}',
    created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
