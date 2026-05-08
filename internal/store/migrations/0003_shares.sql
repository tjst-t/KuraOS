-- Share engine tables (Sd64f38).
--
-- shares is the persistent state for SMB / NFS exports. The runtime templates
-- in /etc/samba/conf.d/kura.conf and /etc/exports.d/kura.exports are
-- regenerated from this table on every Apply — DESIGN_PRINCIPLES priority #1
-- (SSOT) means SQLite + config.json is authoritative, the conf files are
-- regenerable artifacts.
--
-- protocol: 'smb' | 'nfs' | 'both'.
-- preset:   'general' | 'media' | 'time_machine' | 'database'. Each preset
--           bundles fixed SMB performance / compatibility options applied at
--           template render time (see engine/share/presets.go).
-- access_mode: 'read_only' | 'read_write' — coarse default the per-principal
--              ACL inherits when not overridden.

CREATE TABLE IF NOT EXISTS shares (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    path         TEXT NOT NULL,
    protocol     TEXT NOT NULL CHECK (protocol IN ('smb','nfs','both')),
    preset       TEXT NOT NULL CHECK (preset IN ('general','media','time_machine','database')),
    access_mode  TEXT NOT NULL CHECK (access_mode IN ('read_only','read_write')),
    description  TEXT NOT NULL DEFAULT '',
    disabled     INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- share_acl is the per-principal access list. principal_kind = 'user' | 'group'.
-- mode = 'rw' | 'r' | 'none'; 'none' explicitly blocks (overrides default).
CREATE TABLE IF NOT EXISTS share_acl (
    share_id       TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
    principal_kind TEXT NOT NULL CHECK (principal_kind IN ('user','group')),
    principal_name TEXT NOT NULL,
    mode           TEXT NOT NULL CHECK (mode IN ('rw','r','none')),
    PRIMARY KEY (share_id, principal_kind, principal_name)
);

CREATE INDEX IF NOT EXISTS share_acl_by_share ON share_acl(share_id);
