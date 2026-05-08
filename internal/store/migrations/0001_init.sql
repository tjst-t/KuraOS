-- Bootstrap migration. Engines (storage, share, app, ...) own their own tables
-- in later sprints; this only establishes a key/value scratch space and the
-- schema_version registry that future migrations append to.

CREATE TABLE IF NOT EXISTS kv (
    k TEXT PRIMARY KEY,
    v TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
