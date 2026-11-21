
CREATE TABLE IF NOT EXISTS ferry_plans (
    id TEXT PRIMARY KEY,
    label TEXT NOT NULL,
    source TEXT NOT NULL,
    destination TEXT NOT NULL,
    template TEXT NOT NULL,
    on_conflict TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    files INTEGER NOT NULL DEFAULT 0,
    copies INTEGER NOT NULL DEFAULT 0,
    skips INTEGER NOT NULL DEFAULT 0,
    bytes INTEGER NOT NULL DEFAULT 0,
    by_kind TEXT NOT NULL DEFAULT '{}',
    created TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ferry_plans_by_source ON ferry_plans (source, id);
CREATE TABLE IF NOT EXISTS ferry_plan_items (
    plan TEXT NOT NULL,
    rel TEXT NOT NULL,
    source TEXT NOT NULL,
    dest TEXT NOT NULL,
    size INTEGER NOT NULL,
    mtime REAL,
    kind TEXT NOT NULL,
    action TEXT NOT NULL,
    probe TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (plan, rel)
);
