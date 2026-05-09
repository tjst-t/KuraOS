-- Generic key/value cache the App engine uses for small persisted runtime
-- settings (trusted registry list, port-range overrides, ...). config.json
-- is still the SSOT — kura_kv is just a fast cache the daemon writes through
-- on every config apply.
CREATE TABLE IF NOT EXISTS kura_kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
