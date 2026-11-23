-- Phase 3: which machine a plan entry's source lives on.
ALTER TABLE plan_entries ADD COLUMN source_peer TEXT NOT NULL DEFAULT 'local';
