"""Ferry's tables and the appliers that project its events into them.

An offload leaves a trail: the plan, the transfer that ran it, the receipt the job
wrote and the verification that followed. Each of those is an event, and each applier
here is a pure function of the event and the current index, so an index rebuilt from
the log holds the same rows.
"""

from __future__ import annotations

import json
from typing import Any

from park.kernel.index import applier, schema

schema(
    "ferry.plans",
    """
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
""",
)

schema(
    "ferry.transfers",
    """
CREATE TABLE IF NOT EXISTS ferry_transfers (
    id TEXT PRIMARY KEY,
    plan TEXT NOT NULL DEFAULT '',
    job TEXT,
    machine TEXT,
    status TEXT NOT NULL DEFAULT 'queued',
    total INTEGER NOT NULL DEFAULT 0,
    done INTEGER NOT NULL DEFAULT 0,
    fraction REAL NOT NULL DEFAULT 0,
    files INTEGER NOT NULL DEFAULT 0,
    verified INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0,
    bytes INTEGER NOT NULL DEFAULT 0,
    errors TEXT NOT NULL DEFAULT '[]',
    digest TEXT,
    note TEXT,
    queued TEXT,
    started TEXT,
    ended TEXT
);
CREATE INDEX IF NOT EXISTS ferry_transfers_by_plan ON ferry_transfers (plan, id);
CREATE INDEX IF NOT EXISTS ferry_transfers_by_status ON ferry_transfers (status, id);
""",
)

schema(
    "ferry.receipts",
    """
CREATE TABLE IF NOT EXISTS ferry_receipts (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    transfer TEXT,
    plan TEXT,
    job TEXT,
    machine TEXT,
    files INTEGER NOT NULL DEFAULT 0,
    bytes INTEGER NOT NULL DEFAULT 0,
    digest TEXT NOT NULL DEFAULT '',
    errors TEXT NOT NULL DEFAULT '[]',
    body TEXT NOT NULL DEFAULT '{}',
    created TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ferry_receipts_by_transfer ON ferry_receipts (transfer, id);
CREATE INDEX IF NOT EXISTS ferry_receipts_by_kind ON ferry_receipts (kind, id);
""",
)

schema(
    "ferry.verifications",
    """
CREATE TABLE IF NOT EXISTS ferry_verifications (
    id TEXT PRIMARY KEY,
    transfer TEXT,
    plan TEXT,
    folder TEXT NOT NULL DEFAULT '',
    checked INTEGER NOT NULL DEFAULT 0,
    missing INTEGER NOT NULL DEFAULT 0,
    modified INTEGER NOT NULL DEFAULT 0,
    added INTEGER NOT NULL DEFAULT 0,
    clean INTEGER NOT NULL DEFAULT 1,
    detail TEXT NOT NULL DEFAULT '[]',
    created TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ferry_verifications_by_transfer ON ferry_verifications (transfer, id);
""",
)

schema(
    "ferry.requests",
    """
CREATE TABLE IF NOT EXISTS ferry_proxy_requests (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    destination TEXT NOT NULL DEFAULT '',
    codec TEXT NOT NULL,
    target_height INTEGER NOT NULL,
    files INTEGER NOT NULL DEFAULT 0,
    job TEXT,
    items TEXT NOT NULL DEFAULT '[]',
    created TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ferry_resolve_requests (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    name TEXT NOT NULL,
    resolution TEXT NOT NULL,
    frame_rate TEXT NOT NULL,
    color_space TEXT NOT NULL,
    manifest TEXT NOT NULL DEFAULT '',
    bins TEXT NOT NULL DEFAULT '[]',
    job TEXT,
    created TEXT NOT NULL
);
""",
)


# -- appliers ----------------------------------------------------------------------


def _dumps(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def _upsert(conn, table: str, values: dict, key: tuple[str, ...] = ("id",)) -> None:
    """Insert, or overwrite the columns this event carries."""
    columns = list(values)
    marks = ", ".join("?" for _ in columns)
    assignments = ", ".join(f"{name}=excluded.{name}" for name in columns if name not in key)
    sql = f"INSERT INTO {table} ({', '.join(columns)}) VALUES ({marks})"
    sql += f" ON CONFLICT({', '.join(key)})"
    sql += f" DO UPDATE SET {assignments}" if assignments else " DO NOTHING"
    conn.execute(sql, tuple(values[name] for name in columns))


@applier("ferry.plan.created")
def _apply_plan_created(conn, event: dict) -> None:
    data = event["data"]
    _upsert(
        conn,
        "ferry_plans",
        {
            "id": data["id"],
            "label": data["label"],
            "source": data["source"],
            "destination": data["destination"],
            "template": data["template"],
            "on_conflict": data["on_conflict"],
            "fingerprint": data["fingerprint"],
            "files": data["files"],
            "copies": data["copies"],
            "skips": data["skips"],
            "bytes": data["bytes"],
            "by_kind": _dumps(data.get("by_kind") or {}),
            "created": data["created"],
        },
    )
    for item in data.get("items") or []:
        _upsert(
            conn,
            "ferry_plan_items",
            {
                "plan": data["id"],
                "rel": item["rel"],
                "source": item["source"],
                "dest": item["dest"],
                "size": item["size"],
                "mtime": item.get("mtime"),
                "kind": item["kind"],
                "action": item["action"],
                "probe": _dumps(item.get("probe") or {}),
            },
            key=("plan", "rel"),
        )


@applier("ferry.transfer.queued")
def _apply_transfer_queued(conn, event: dict) -> None:
    data = event["data"]
    _upsert(
        conn,
        "ferry_transfers",
        {
            "id": data["id"],
            "plan": data["plan"],
            "job": data.get("job"),
            "machine": data.get("machine"),
            "status": "queued",
            "total": data.get("total", 0),
            "queued": data["queued"],
        },
    )


@applier("ferry.transfer.started")
def _apply_transfer_started(conn, event: dict) -> None:
    data = event["data"]
    _upsert(
        conn,
        "ferry_transfers",
        {
            "id": data["id"],
            "status": "running",
            "job": data.get("job"),
            "machine": data.get("machine"),
            "started": data["started"],
        },
    )


@applier("ferry.transfer.progress")
def _apply_transfer_progress(conn, event: dict) -> None:
    data = event["data"]
    _upsert(
        conn,
        "ferry_transfers",
        {
            "id": data["id"],
            "status": "running",
            "done": data["done"],
            "total": data["total"],
            "fraction": data["fraction"],
            "note": data.get("note") or None,
        },
    )


@applier("ferry.transfer.finished")
def _apply_transfer_finished(conn, event: dict) -> None:
    data = event["data"]
    _upsert(
        conn,
        "ferry_transfers",
        {
            "id": data["id"],
            "status": data["status"],
            "files": data["files"],
            "verified": data["verified"],
            "skipped": data["skipped"],
            "failed": data["failed"],
            "bytes": data["bytes"],
            "errors": _dumps(data.get("errors") or []),
            "digest": data.get("digest"),
            "ended": data["ended"],
        },
    )


@applier("ferry.receipt.recorded")
def _apply_receipt_recorded(conn, event: dict) -> None:
    data = event["data"]
    _upsert(
        conn,
        "ferry_receipts",
        {
            "id": data["id"],
            "kind": data["kind"],
            "status": data["status"],
            "transfer": data.get("transfer"),
            "plan": data.get("plan"),
            "job": data.get("job"),
            "machine": data.get("machine"),
            "files": len(data.get("files") or []),
            "bytes": data.get("bytes", 0),
            "digest": data.get("digest") or "",
            "errors": _dumps(data.get("errors") or []),
            "body": _dumps(data),
            "created": data["created"],
        },
    )


@applier("ferry.verify.recorded")
def _apply_verify_recorded(conn, event: dict) -> None:
    data = event["data"]
    _upsert(
        conn,
        "ferry_verifications",
        {
            "id": data["id"],
            "transfer": data.get("transfer"),
            "plan": data.get("plan"),
            "folder": data.get("folder") or "",
            "checked": data["checked"],
            "missing": len(data.get("missing") or []),
            "modified": len(data.get("modified") or []),
            "added": len(data.get("added") or []),
            "clean": 1 if data["clean"] else 0,
            "detail": _dumps(data),
            "created": data["created"],
        },
    )


@applier("ferry.proxies.requested")
def _apply_proxies_requested(conn, event: dict) -> None:
    data = event["data"]
    _upsert(
        conn,
        "ferry_proxy_requests",
        {
            "id": data["id"],
            "source": data["source"],
            "destination": data.get("destination") or "",
            "codec": data["codec"],
            "target_height": data["target_height"],
            "files": len(data.get("items") or []),
            "job": data.get("job"),
            "items": _dumps(data.get("items") or []),
            "created": data["created"],
        },
    )


@applier("ferry.resolve.requested")
def _apply_resolve_requested(conn, event: dict) -> None:
    data = event["data"]
    _upsert(
        conn,
        "ferry_resolve_requests",
        {
            "id": data["id"],
            "source": data["source"],
            "name": data["name"],
            "resolution": data["resolution"],
            "frame_rate": data["frame_rate"],
            "color_space": data["color_space"],
            "manifest": data.get("manifest") or "",
            "bins": _dumps(data.get("bins") or []),
            "job": data.get("job"),
            "created": data["created"],
        },
    )


# -- reading rows back -------------------------------------------------------------


def _loads(text: Any, default: Any) -> Any:
    if text is None or text == "":
        return default
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return default


def plan_out(row: dict, items: list[dict] | None = None) -> dict:
    out = {
        "id": row["id"],
        "label": row["label"],
        "source": row["source"],
        "destination": row["destination"],
        "template": row["template"],
        "on_conflict": row["on_conflict"],
        "fingerprint": row["fingerprint"],
        "files": row["files"],
        "copies": row["copies"],
        "skips": row["skips"],
        "bytes": row["bytes"],
        "by_kind": _loads(row.get("by_kind"), {}),
        "created": row["created"],
    }
    if items is not None:
        out["items"] = [item_out(item) for item in items]
    return out


def item_out(row: dict) -> dict:
    return {
        "rel": row["rel"],
        "source": row["source"],
        "dest": row["dest"],
        "size": row["size"],
        "mtime": row["mtime"],
        "kind": row["kind"],
        "action": row["action"],
        "probe": _loads(row.get("probe"), {}),
    }


def transfer_out(row: dict) -> dict:
    return {
        "id": row["id"],
        "plan": row["plan"],
        "job": row["job"],
        "machine": row["machine"],
        "status": row["status"],
        "total": row["total"],
        "done": row["done"],
        "fraction": row["fraction"],
        "files": row["files"],
        "verified": row["verified"],
        "skipped": row["skipped"],
        "failed": row["failed"],
        "bytes": row["bytes"],
        "errors": _loads(row.get("errors"), []),
        "digest": row["digest"],
        "note": row["note"],
        "queued": row["queued"],
        "started": row["started"],
        "ended": row["ended"],
    }


def receipt_out(row: dict) -> dict:
    return _loads(row.get("body"), {}) or {
        "id": row["id"],
        "kind": row["kind"],
        "status": row["status"],
        "created": row["created"],
    }


def receipt_summary(row: dict) -> dict:
    return {
        "id": row["id"],
        "kind": row["kind"],
        "status": row["status"],
        "transfer": row["transfer"],
        "plan": row["plan"],
        "job": row["job"],
        "machine": row["machine"],
        "files": row["files"],
        "bytes": row["bytes"],
        "digest": row["digest"],
        "errors": _loads(row.get("errors"), []),
        "created": row["created"],
    }


def verification_out(row: dict) -> dict:
    detail = _loads(row.get("detail"), {})
    return {
        "id": row["id"],
        "transfer": row["transfer"],
        "plan": row["plan"],
        "folder": row["folder"],
        "checked": row["checked"],
        "missing": detail.get("missing", []),
        "modified": detail.get("modified", []),
        "added": detail.get("added", []),
        "unreadable": detail.get("unreadable", []),
        "clean": bool(row["clean"]),
        "created": row["created"],
    }


__all__ = [
    "item_out",
    "plan_out",
    "receipt_out",
    "receipt_summary",
    "transfer_out",
    "verification_out",
]
