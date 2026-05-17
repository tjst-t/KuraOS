-- Extend the role CHECK constraint to include 'pending' (S413bd5).
--
-- SQLite does not support ALTER COLUMN, so we recreate the users table with
-- the updated constraint using the standard rename-create-copy-drop sequence.
-- All FK-referencing tables (auth_methods, user_groups, sessions, uid_alloc)
-- use ON DELETE CASCADE or were migrated to no-FK in 0005, so the recreate is
-- safe without disabling foreign_keys — we only rename/drop the old table
-- after copying all rows.

PRAGMA foreign_keys = OFF;

CREATE TABLE users_new (
    id           TEXT PRIMARY KEY,
    username     TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    role         TEXT NOT NULL CHECK (role IN ('admin','user','pending')),
    disabled     INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

INSERT INTO users_new SELECT * FROM users;

DROP TABLE users;

ALTER TABLE users_new RENAME TO users;

PRAGMA foreign_keys = ON;
