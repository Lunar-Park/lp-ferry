-- Ferry v2 core schema. Forward-only; never edit a shipped migration.

CREATE TABLE peers (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    pubkey_b64      TEXT NOT NULL,
    addresses_json  TEXT NOT NULL DEFAULT '[]',
    last_seen       TEXT,
    last_transport  TEXT,
    paired_at       TEXT NOT NULL,
    revoked         INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE presets (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    archived    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE preset_revisions (
    id                 INTEGER PRIMARY KEY,
    preset_id          TEXT NOT NULL REFERENCES presets(id),
    revision           INTEGER NOT NULL,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    rules_json         TEXT NOT NULL,
    groups_json        TEXT NOT NULL DEFAULT '[]',
    exclusions_json    TEXT NOT NULL DEFAULT '[]',
    fallback_template  TEXT NOT NULL,
    conflict_policy    TEXT NOT NULL,
    content_hash       TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    UNIQUE (preset_id, revision)
);

CREATE TABLE saved_destinations (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    peer_id              TEXT NOT NULL DEFAULT 'local',
    location_kind        TEXT NOT NULL,
    root_path            TEXT NOT NULL,
    rel_subpath          TEXT NOT NULL DEFAULT '',
    identity_kind        TEXT NOT NULL,
    identity_value       TEXT NOT NULL,
    identity_confidence  TEXT NOT NULL,
    pinned_preset_id     TEXT REFERENCES presets(id),
    pinned_preset_rev    INTEGER,
    default_conflict     TEXT NOT NULL DEFAULT 'needs_review',
    checksum_algo        TEXT NOT NULL DEFAULT 'sha256',
    reserve_bytes        INTEGER NOT NULL DEFAULT 0,
    archived             INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);

CREATE TABLE camera_bindings (
    id            INTEGER PRIMARY KEY,
    make          TEXT NOT NULL,
    model         TEXT NOT NULL,
    serial        TEXT NOT NULL,
    default_role  TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (make, model, serial)
);

CREATE TABLE inventories (
    id                TEXT PRIMARY KEY,
    peer_id           TEXT NOT NULL DEFAULT 'local',
    source_path       TEXT NOT NULL,
    source_label      TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL,
    file_count        INTEGER NOT NULL DEFAULT 0,
    total_bytes       INTEGER NOT NULL DEFAULT 0,
    error_count       INTEGER NOT NULL DEFAULT 0,
    camera_hint_json  TEXT,
    manifest_hash     TEXT,
    created_at        TEXT NOT NULL,
    completed_at      TEXT
);

CREATE TABLE inventory_entries (
    id             INTEGER PRIMARY KEY,
    inventory_id   TEXT NOT NULL REFERENCES inventories(id),
    rel_path       TEXT NOT NULL,
    entry_type     TEXT NOT NULL,
    size           INTEGER NOT NULL DEFAULT 0,
    mtime_ns       INTEGER NOT NULL DEFAULT 0,
    scan_status    TEXT NOT NULL,
    scan_error     TEXT,
    metadata_json  TEXT
);
CREATE INDEX inventory_entries_inv ON inventory_entries(inventory_id);

CREATE TABLE plans (
    id                  TEXT PRIMARY KEY,
    fingerprint         TEXT NOT NULL,
    status              TEXT NOT NULL,
    actor               TEXT NOT NULL,
    checksum_algo       TEXT NOT NULL DEFAULT 'sha256',
    preset_id           TEXT,
    preset_rev          INTEGER,
    preset_hash         TEXT,
    camera_roles_json   TEXT,
    created_at          TEXT NOT NULL,
    approved_at         TEXT,
    invalidated_reason  TEXT
);

CREATE TABLE plan_destinations (
    id                      TEXT PRIMARY KEY,
    plan_id                 TEXT NOT NULL REFERENCES plans(id),
    destination_id          TEXT,
    label                   TEXT NOT NULL DEFAULT '',
    peer_id                 TEXT NOT NULL DEFAULT 'local',
    root_path               TEXT NOT NULL,
    identity_snapshot_json  TEXT,
    storage_class           TEXT NOT NULL DEFAULT 'unknown',
    publish_strategy        TEXT,
    free_bytes              INTEGER,
    required_bytes          INTEGER NOT NULL DEFAULT 0,
    reserve_bytes           INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX plan_destinations_plan ON plan_destinations(plan_id);

CREATE TABLE plan_entries (
    id                  INTEGER PRIMARY KEY,
    plan_id             TEXT NOT NULL REFERENCES plans(id),
    plan_dest_id        TEXT NOT NULL REFERENCES plan_destinations(id),
    inventory_id        TEXT REFERENCES inventories(id),
    inventory_entry_id  INTEGER REFERENCES inventory_entries(id),
    source_root         TEXT NOT NULL,
    source_rel          TEXT NOT NULL,
    source_path         TEXT NOT NULL,
    dest_rel_path       TEXT NOT NULL,
    action              TEXT NOT NULL,
    matched_rule        TEXT,
    group_id            TEXT,
    conflict_kind       TEXT,
    exclusion_reason    TEXT,
    excluded_by_user    INTEGER NOT NULL DEFAULT 0,
    camera_role         TEXT,
    size                INTEGER NOT NULL DEFAULT 0,
    mtime_ns            INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX plan_entries_plan ON plan_entries(plan_id);

CREATE TABLE transfers (
    id             TEXT PRIMARY KEY,
    plan_id        TEXT NOT NULL REFERENCES plans(id),
    status         TEXT NOT NULL,
    actor          TEXT NOT NULL,
    checksum_algo  TEXT NOT NULL,
    started_at     TEXT,
    ended_at       TEXT,
    canceled       INTEGER NOT NULL DEFAULT 0,
    error_summary  TEXT
);

CREATE TABLE transfer_targets (
    id                TEXT PRIMARY KEY,
    transfer_id       TEXT NOT NULL REFERENCES transfers(id),
    plan_dest_id      TEXT NOT NULL REFERENCES plan_destinations(id),
    label             TEXT NOT NULL DEFAULT '',
    peer_id           TEXT NOT NULL DEFAULT 'local',
    root_path         TEXT NOT NULL,
    root_dev          INTEGER,
    root_ino          INTEGER,
    transport_kind    TEXT NOT NULL DEFAULT 'local',
    peer_addr         TEXT,
    publish_strategy  TEXT,
    readback_mode     TEXT,
    status            TEXT NOT NULL,
    bytes_written     INTEGER NOT NULL DEFAULT 0,
    bytes_verified    INTEGER NOT NULL DEFAULT 0,
    files_done        INTEGER NOT NULL DEFAULT 0,
    files_failed      INTEGER NOT NULL DEFAULT 0,
    ema_write_bps     REAL NOT NULL DEFAULT 0,
    ema_verify_bps    REAL NOT NULL DEFAULT 0,
    error             TEXT,
    started_at        TEXT,
    ended_at          TEXT
);
CREATE INDEX transfer_targets_transfer ON transfer_targets(transfer_id);

CREATE TABLE transfer_items (
    id                  INTEGER PRIMARY KEY,
    transfer_target_id  TEXT NOT NULL REFERENCES transfer_targets(id),
    plan_entry_id       INTEGER NOT NULL REFERENCES plan_entries(id),
    status              TEXT NOT NULL,
    part_path           TEXT,
    final_path          TEXT,
    reservation_ino     INTEGER,
    bytes_copied        INTEGER NOT NULL DEFAULT 0,
    source_hash         TEXT,
    dest_hash           TEXT,
    checksum_algo       TEXT,
    readback_mode       TEXT,
    publish_strategy    TEXT,
    mtime_preserved     INTEGER,
    error               TEXT,
    warning             TEXT,
    attempts            INTEGER NOT NULL DEFAULT 0,
    updated_at          TEXT NOT NULL,
    UNIQUE (transfer_target_id, plan_entry_id)
);
CREATE INDEX transfer_items_target ON transfer_items(transfer_target_id);

CREATE TABLE path_reservations (
    dest_key       TEXT NOT NULL,
    dest_rel_path  TEXT NOT NULL,
    transfer_id    TEXT NOT NULL REFERENCES transfers(id),
    created_at     TEXT NOT NULL,
    PRIMARY KEY (dest_key, dest_rel_path)
);
CREATE INDEX path_reservations_transfer ON path_reservations(transfer_id);

CREATE TABLE receipts (
    id             TEXT PRIMARY KEY,
    transfer_id    TEXT NOT NULL REFERENCES transfers(id),
    plan_id        TEXT NOT NULL REFERENCES plans(id),
    status         TEXT NOT NULL,
    actor          TEXT NOT NULL,
    digest         TEXT NOT NULL,
    body_json      TEXT NOT NULL,
    exported_path  TEXT,
    export_error   TEXT,
    created_at     TEXT NOT NULL
);

CREATE INDEX receipts_transfer ON receipts(transfer_id);

CREATE TABLE verifications (
    id             INTEGER PRIMARY KEY,
    receipt_id     TEXT NOT NULL,
    transfer_id    TEXT NOT NULL,
    peer_id        TEXT NOT NULL DEFAULT 'local',
    folder         TEXT NOT NULL,
    checked        INTEGER NOT NULL,
    missing        INTEGER NOT NULL,
    modified       INTEGER NOT NULL,
    added          INTEGER NOT NULL,
    unreadable     INTEGER NOT NULL,
    clean          INTEGER NOT NULL,
    readback_mode  TEXT NOT NULL,
    detail_json    TEXT NOT NULL,
    created_at     TEXT NOT NULL
);
