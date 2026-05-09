-- Installed app instances. config.json is the SSOT (apps.installed[]) but the
-- daemon needs a queryable runtime cache so the UI can render "what's running
-- right now" without re-deriving from config.json on every request.
--
-- setup_json / settings_json store JSON-encoded string maps (snake_case keys
-- per CLAUDE.md). Secret settings are NEVER stored here — they live in the
-- credentials vault. The map only carries non-secret values.
--
-- state semantics:
--   installing    = install in progress (rolled back on crash via reconcile)
--   running       = healthchecks passed, gateway routing active
--   stopped       = container stopped by operator (reachable via UI)
--   failed        = install/update failed and was rolled back; row retained
--                   so the UI can show the error history
--   uninstalling  = transient state during uninstall; row deleted on success
CREATE TABLE IF NOT EXISTS app_installs (
    app_id        TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    version       TEXT NOT NULL,
    registry      TEXT NOT NULL,
    state         TEXT NOT NULL,
    setup_json    TEXT NOT NULL DEFAULT '{}',
    settings_json TEXT NOT NULL DEFAULT '{}',
    installed_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS app_installs_name_idx ON app_installs (name);

-- Backup policy registration. backup:true datasets in a manifest get a row
-- per dataset so the snapshot scheduler (engine/backup, future Sprint) can
-- pick them up without re-parsing every install's manifest.
CREATE TABLE IF NOT EXISTS app_backup_policy (
    app_id       TEXT NOT NULL,
    dataset_name TEXT NOT NULL,
    dataset_path TEXT NOT NULL,
    strategy     TEXT NOT NULL DEFAULT 'daily',
    PRIMARY KEY (app_id, dataset_name)
);
