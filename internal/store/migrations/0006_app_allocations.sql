-- App allocations: deterministic mapping of (app instance, container,
-- manifest_port) -> host_port. Per DESIGN_PRINCIPLES priority #1 the SQLite
-- table is a runtime cache: the canonical record is config.json's
-- apps[].port_assignments, but allocations must survive restarts so the
-- compose YAML keeps emitting the same host port across reboots.
--
-- app_id is the install identifier (e.g. 'immich.0001') the App engine
-- assigns at install time. container is the manifest service name
-- ('server', 'db'), manifest_port the in-container port the manifest
-- declared, host_port the bound external port. AC-S1bccf5-3-2 checks
-- that the same (app_id, container, manifest_port) tuple keeps returning
-- the same host_port.
CREATE TABLE IF NOT EXISTS app_port_reservations (
    app_id        TEXT NOT NULL,
    container     TEXT NOT NULL,
    manifest_port INTEGER NOT NULL,
    host_port     INTEGER NOT NULL,
    allocated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (app_id, container, manifest_port)
);

-- host_port must be globally unique to prevent two installs binding the
-- same external port. Allocator picks the next free in a configurable
-- range; the unique index is the durable guard.
CREATE UNIQUE INDEX IF NOT EXISTS app_port_reservations_host_port_uidx
    ON app_port_reservations (host_port);

-- Dataset plan caches the planner output so subsequent installs / config
-- applies emit the same paths even if the pool topology changed (operator
-- gets an explicit migration error rather than silent dataset move).
CREATE TABLE IF NOT EXISTS app_dataset_plan (
    app_id         TEXT NOT NULL,
    dataset_name   TEXT NOT NULL,
    pool_hint      TEXT NOT NULL,
    chosen_pool    TEXT NOT NULL,
    dataset_path   TEXT NOT NULL,
    quota          TEXT,
    backup         INTEGER NOT NULL DEFAULT 0,
    planned_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (app_id, dataset_name)
);
