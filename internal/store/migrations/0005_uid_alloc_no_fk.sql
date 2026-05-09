-- Drop the FK on uid_alloc.user_id -> users(id). The vault (priority #1
-- SSOT) must round-trip independently of structural state — a backup
-- restored into a fresh state.db that has not yet replayed user creates
-- from config.json was failing with FOREIGN KEY violation. Discovered
-- during VM verification.

CREATE TABLE IF NOT EXISTS uid_alloc_new (
    user_id      TEXT PRIMARY KEY,
    uid          INTEGER NOT NULL UNIQUE,
    allocated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO uid_alloc_new (user_id, uid, allocated_at)
    SELECT user_id, uid, allocated_at FROM uid_alloc;

DROP TABLE uid_alloc;
ALTER TABLE uid_alloc_new RENAME TO uid_alloc;
