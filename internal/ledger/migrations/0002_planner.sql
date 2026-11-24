-- Phase 2: planner, presets, invalidation and skip-identical evidence.

ALTER TABLE plans ADD COLUMN request_json TEXT;
ALTER TABLE plans ADD COLUMN findings_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE plans ADD COLUMN derived_from TEXT;
ALTER TABLE plans ADD COLUMN superseded_by TEXT;

ALTER TABLE plan_destinations ADD COLUMN preset_id TEXT;
ALTER TABLE plan_destinations ADD COLUMN preset_rev INTEGER;
ALTER TABLE plan_destinations ADD COLUMN preset_hash TEXT;
ALTER TABLE plan_destinations ADD COLUMN conflict_policy TEXT;

ALTER TABLE plan_entries ADD COLUMN entry_kind TEXT NOT NULL DEFAULT 'file';
ALTER TABLE plan_entries ADD COLUMN expected_hash TEXT;
ALTER TABLE plan_entries ADD COLUMN renamed_from TEXT;
ALTER TABLE plan_entries ADD COLUMN warning TEXT;
ALTER TABLE plan_entries ADD COLUMN source_label TEXT;
ALTER TABLE plan_entries ADD COLUMN metadata_json TEXT;

-- Which camera bindings a plan's routing depended on, so editing one
-- invalidates exactly the approvals it affects.
CREATE TABLE plan_camera_keys (
    plan_id     TEXT NOT NULL REFERENCES plans(id),
    camera_key  TEXT NOT NULL,
    PRIMARY KEY (plan_id, camera_key)
);
CREATE INDEX plan_camera_keys_key ON plan_camera_keys(camera_key);

CREATE INDEX plan_entries_dest ON plan_entries(plan_dest_id, id);
CREATE INDEX plan_destinations_dest ON plan_destinations(destination_id);
CREATE INDEX plan_destinations_preset ON plan_destinations(preset_id);
