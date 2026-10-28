"""Ferry's ops: plans, offloads, receipts, verification, proxies and Resolve projects.

A plan is built from the folder in front of us and kept as an event, so the offload that
follows reads what was decided rather than the card. The three jobs (``copy``, ``proxy``,
``resolve``) report through the transfer and receipt ops here, which is how a file that
landed becomes a row the Ingest view and ``ferry.receipt.get`` can show.
"""

from __future__ import annotations

import json
import re
from collections.abc import Callable
from hashlib import sha256
from pathlib import Path
from typing import Annotated, Any, Literal

from park.kernel import ids
from park.kernel.log import utc_now
from park.ops import Conflict, Invalid, NotFound, OpContext, op
from park.ops import resolve as resolve_op

from . import config as ferry_config
from . import layout, records
from .copy import hash_file
from .probe import scan

SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
RECEIPT_STATUS = Literal["ok", "partial", "failed"]
TRANSFER_STATUS = {"ok": "copied", "partial": "partial", "failed": "failed"}
MAX_WINDOW = 500


# -- shared helpers ----------------------------------------------------------------


def _op(name: str, **kwargs: Any) -> Callable[[Callable[..., Any]], Callable[..., Any]]:
    """Register one of ferry's ops. ``plugin`` is set here so ``op.list`` can filter."""
    return op(name, plugin="ferry", **kwargs)


def _now() -> str:
    return utc_now()


def _config(path: str | None) -> ferry_config.Config:
    try:
        return ferry_config.load(path)
    except ferry_config.ConfigError as exc:
        raise Invalid(str(exc)) from exc


def _folder(value: str, where: str) -> Path:
    path = Path(value).expanduser()
    if not path.exists():
        raise NotFound(f"{where} is not there: {path}")
    if not path.is_dir():
        raise Invalid(f"{where} is not a folder: {path}")
    return path


def _window(limit: int, offset: int) -> tuple[int, int]:
    if limit < 1 or limit > MAX_WINDOW:
        raise Invalid(f"limit must be between 1 and {MAX_WINDOW}")
    if offset < 0:
        raise Invalid("offset must be >= 0")
    return limit, offset


def _plan_row(ctx: OpContext, plan: str, *, items: bool = False) -> dict:
    row = ctx.one("SELECT * FROM ferry_plans WHERE id=?", (plan,))
    if row is None:
        raise NotFound(f"no such plan: {plan}")
    if items:
        row["items"] = ctx.all("SELECT * FROM ferry_plan_items WHERE plan=? ORDER BY rel", (plan,))
    return row


def _transfer_row(ctx: OpContext, transfer: str) -> dict:
    row = ctx.one("SELECT * FROM ferry_transfers WHERE id=?", (transfer,))
    if row is None:
        raise NotFound(f"no such transfer: {transfer}")
    return row


def _latest_receipt(ctx: OpContext, transfer: str) -> dict:
    row = ctx.one(
        "SELECT * FROM ferry_receipts WHERE transfer=? ORDER BY created DESC, id DESC LIMIT 1",
        (transfer,),
    )
    if row is None:
        raise NotFound(f"transfer {transfer} has no receipt yet")
    return records.receipt_out(row)


def _submit(ctx: OpContext, job: str, params: dict, machine: str | None) -> tuple[str | None, bool]:
    """Queue one of this plugin's jobs, when park's machines layer is loaded.

    Returns ``(job id, queued)``. A home without the machines ops records the work
    without a queue, which is the state before park-node exists.
    """
    try:
        resolve_op("machines.job.submit")
    except NotFound:
        return None, False
    result = ctx.call(
        "machines.job.submit",
        {"plugin": "ferry", "name": job, "params": params, "machine": machine},
    )
    job_id = None
    if isinstance(result, dict):
        job_id = result.get("id") or result.get("job")
    return job_id, True


def _file_rows(files: list[dict] | None) -> list[dict]:
    out: list[dict] = []
    for item in files or []:
        if not isinstance(item, dict):
            raise Invalid("each file must be an object")
        digest = str(item.get("sha256") or "").strip().lower()
        if not SHA256_RE.match(digest):
            raise Invalid(f"file {item.get('rel')!r} needs a 64 character sha256")
        size = item.get("size", 0)
        if isinstance(size, bool) or not isinstance(size, int) or size < 0:
            raise Invalid(f"file {item.get('rel')!r} needs a non-negative size")
        out.append(
            {
                "rel": str(item.get("rel") or ""),
                "source": str(item.get("source") or ""),
                "dest": str(item.get("dest") or ""),
                "sha256": digest,
                "size": size,
                "verified": bool(item.get("verified", True)),
                "skipped": bool(item.get("skipped", False)),
            }
        )
    return out


def _digest(files: list[dict]) -> str:
    body = [[item["rel"], item["sha256"]] for item in sorted(files, key=lambda one: one["rel"])]
    return sha256(json.dumps(body, separators=(",", ":")).encode("utf-8")).hexdigest()


# -- plans -------------------------------------------------------------------------


@_op(
    "ferry.plan",
    summary="Probe a source folder and build the transfer plan.",
    effect="external",
    returns="{id, label, source, destination, files, copies, skips, bytes, fingerprint, items}",
)
def ferry_plan(
    ctx: OpContext,
    source: Annotated[str, "Folder to plan from, such as a mounted card"],
    destination: Annotated[str, "Folder the files land in"],
    label: Annotated[str | None, "Name for the plan; the source folder's name when absent"] = None,
    config_path: Annotated[str | None, "ferry.toml to read"] = None,
    probe: Annotated[bool, "Read media metadata with ffprobe when it is installed"] = True,
) -> dict:
    """Reads the source outside the write lock, then records the plan as one event."""
    settings = _config(config_path)
    root = _folder(source, "source")
    plan = layout.build(root, destination, settings, label=label, read_probe=probe)
    if not plan["files"]:
        raise Invalid(f"no files to plan under {root}")
    plan_id = ids.new_id()
    created = _now()
    ctx.write("ferry.plan.created", {"id": plan_id, "created": created, **plan})
    return {"id": plan_id, "created": created, **plan}


@_op("ferry.plan.get", summary="One plan with every file in it.")
def ferry_plan_get(ctx: OpContext, id: Annotated[str, "Plan id"]) -> dict:
    """The items are in ``rel`` order, so two reads of the same plan line up."""
    row = _plan_row(ctx, id, items=True)
    return records.plan_out(row, row["items"])


@_op("ferry.plan.list", summary="Plans, newest first.")
def ferry_plan_list(
    ctx: OpContext,
    source: Annotated[str | None, "Only plans from this source folder"] = None,
    limit: Annotated[int, "Maximum plans to return"] = 50,
    offset: Annotated[int, "Plans to skip"] = 0,
) -> list[dict]:
    """A plan row carries counts; ``ferry.plan.get`` adds the files."""
    limit, offset = _window(limit, offset)
    sql = "SELECT * FROM ferry_plans"
    params: list = []
    if source is not None:
        sql += " WHERE source=?"
        params.append(str(Path(source).expanduser()))
    sql += " ORDER BY created DESC, id DESC LIMIT ? OFFSET ?"
    params.extend([limit, offset])
    return [records.plan_out(row) for row in ctx.all(sql, tuple(params))]


@_op(
    "ferry.source.list", summary="Source folders ferry has planned from, and whether they are here."
)
def ferry_source_list(ctx: OpContext) -> list[dict]:
    """One row per source: how many plans, how many files, and if the folder is mounted."""
    rows = ctx.all(
        "SELECT source, COUNT(*) AS plans, SUM(files) AS files, SUM(bytes) AS bytes,"
        " MAX(created) AS last_plan FROM ferry_plans GROUP BY source ORDER BY last_plan DESC"
    )
    out = []
    for row in rows:
        path = Path(row["source"])
        out.append(
            {
                "source": row["source"],
                "label": path.name,
                "plans": row["plans"],
                "files": row["files"] or 0,
                "bytes": row["bytes"] or 0,
                "last_plan": row["last_plan"],
                "mounted": path.is_dir(),
            }
        )
    return out


# -- transfers ---------------------------------------------------------------------


@_op(
    "ferry.offload",
    summary="Queue a plan's copy as a job.",
    effect="write",
    returns="{plan, transfer, job, machine, queued, files, bytes}",
)
def ferry_offload(
    ctx: OpContext,
    plan: Annotated[str, "Plan id from ferry.plan"],
    machine: Annotated[str | None, "Node that should run the copy"] = None,
) -> dict:
    """Records the transfer first, then hands the job to the queue.

    ``queued`` says whether park's machines layer took the job. Without it the
    transfer is recorded and the copy job runs from wherever the source is mounted.
    """
    row = _plan_row(ctx, plan, items=True)
    transfer = ids.new_id()
    job_id, queued = _submit(ctx, "copy", {"plan": plan, "transfer": transfer}, machine)
    ctx.write(
        "ferry.transfer.queued",
        {
            "id": transfer,
            "plan": plan,
            "job": job_id,
            "machine": machine,
            "total": row["copies"],
            "queued": _now(),
        },
    )
    return {
        "plan": plan,
        "transfer": transfer,
        "job": job_id,
        "machine": machine,
        "queued": queued,
        "files": row["copies"],
        "bytes": row["bytes"],
    }


@_op("ferry.transfer.get", summary="One transfer with its progress.")
def ferry_transfer_get(ctx: OpContext, id: Annotated[str, "Transfer id"]) -> dict:
    """A transfer moves queued, running, then copied, partial or failed."""
    return records.transfer_out(_transfer_row(ctx, id))


@_op("ferry.transfer.list", summary="Transfers, newest first.")
def ferry_transfer_list(
    ctx: OpContext,
    plan: Annotated[str | None, "Only transfers of this plan"] = None,
    status: Annotated[
        Literal["queued", "running", "copied", "partial", "failed"] | None, "Transfer status"
    ] = None,
    limit: Annotated[int, "Maximum transfers to return"] = 50,
    offset: Annotated[int, "Transfers to skip"] = 0,
) -> list[dict]:
    """The Ingest view reads this for its progress list."""
    limit, offset = _window(limit, offset)
    sql = "SELECT * FROM ferry_transfers WHERE 1=1"
    params: list = []
    if plan is not None:
        sql += " AND plan=?"
        params.append(plan)
    if status is not None:
        sql += " AND status=?"
        params.append(status)
    sql += " ORDER BY COALESCE(queued, started, ended) DESC, id DESC LIMIT ? OFFSET ?"
    params.extend([limit, offset])
    return [records.transfer_out(row) for row in ctx.all(sql, tuple(params))]


@_op("ferry.transfer.start", summary="Mark a queued transfer as running.", effect="write")
def ferry_transfer_start(
    ctx: OpContext,
    transfer: Annotated[str, "Transfer id"],
    job: Annotated[str | None, "Job that is running the transfer"] = None,
    machine: Annotated[str | None, "Node running it"] = None,
) -> dict:
    """The copy job calls this once, before its first file."""
    _transfer_row(ctx, transfer)
    ctx.write(
        "ferry.transfer.started",
        {"id": transfer, "job": job, "machine": machine, "started": _now()},
    )
    return records.transfer_out(_transfer_row(ctx, transfer))


@_op("ferry.transfer.progress", summary="Record how far a transfer has got.", effect="write")
def ferry_transfer_progress(
    ctx: OpContext,
    transfer: Annotated[str, "Transfer id"],
    done: Annotated[int, "Files finished so far"],
    total: Annotated[int, "Files in the transfer"],
    note: Annotated[str, "What it is doing now"] = "",
) -> dict:
    """A long copy calls this in steps of a few percent, not once per file."""
    if done < 0 or total < 0 or done > total:
        raise Invalid("done must be between 0 and total")
    _transfer_row(ctx, transfer)
    fraction = 0.0 if total == 0 else round(done / total, 4)
    ctx.write(
        "ferry.transfer.progress",
        {
            "id": transfer,
            "done": done,
            "total": total,
            "fraction": fraction,
            "note": note,
        },
    )
    return records.transfer_out(_transfer_row(ctx, transfer))


# -- receipts ----------------------------------------------------------------------


@_op(
    "ferry.receipt.record",
    summary="Record what a job did: the files, their hashes and anything that failed.",
    effect="write",
    returns="{id, kind, status, transfer, files, bytes, digest, errors, created}",
)
def ferry_receipt_record(
    ctx: OpContext,
    kind: Annotated[Literal["copy", "proxy", "resolve"], "What the job was"],
    status: Annotated[RECEIPT_STATUS, "ok, partial or failed"],
    transfer: Annotated[str | None, "Transfer this receipt closes"] = None,
    plan: Annotated[str | None, "Plan the work came from"] = None,
    job: Annotated[str | None, "Job that ran it"] = None,
    machine: Annotated[str | None, "Node that ran it"] = None,
    files: Annotated[list[dict] | None, "Files: [{rel, source, dest, sha256, size}]"] = None,
    errors: Annotated[list[str] | None, "What went wrong, one line each"] = None,
    total_bytes: Annotated[int, "Bytes written"] = 0,
    detail: Annotated[dict | None, "Anything else worth keeping with the receipt"] = None,
) -> dict:
    """A receipt is written once per job. Closing a transfer writes both events together."""
    rows = _file_rows(files)
    if transfer is not None:
        current = _transfer_row(ctx, transfer)
        if current["ended"]:
            raise Conflict(f"transfer {transfer} already ended as {current['status']}")
    if kind == "copy" and transfer is None:
        raise Invalid("a copy receipt has to name the transfer it closes")
    if plan is not None:
        _plan_row(ctx, plan)

    receipt_id = ids.new_id()
    created = _now()
    digest = _digest(rows)
    problems = [str(message) for message in (errors or [])]
    if transfer is not None:
        ctx.write(
            "ferry.transfer.finished",
            {
                "id": transfer,
                "status": TRANSFER_STATUS[status],
                "files": len(rows),
                "verified": sum(1 for row in rows if row["verified"]),
                "skipped": sum(1 for row in rows if row["skipped"]),
                "failed": len(problems),
                "bytes": total_bytes,
                "errors": problems,
                "digest": digest,
                "ended": created,
            },
        )
    receipt = {
        "id": receipt_id,
        "kind": kind,
        "status": status,
        "transfer": transfer,
        "plan": plan,
        "job": job,
        "machine": machine,
        "files": rows,
        "bytes": total_bytes,
        "digest": digest,
        "errors": problems,
        "detail": detail or {},
        "created": created,
    }
    ctx.write("ferry.receipt.recorded", receipt)
    return receipt


@_op("ferry.receipt.get", summary="One receipt, in full.")
def ferry_receipt_get(ctx: OpContext, id: Annotated[str, "Receipt id"]) -> dict:
    """The receipt carries every file, its hash and where it landed."""
    row = ctx.one("SELECT * FROM ferry_receipts WHERE id=?", (id,))
    if row is None:
        raise NotFound(f"no such receipt: {id}")
    return records.receipt_out(row)


@_op("ferry.receipt.list", summary="Receipts, newest first.")
def ferry_receipt_list(
    ctx: OpContext,
    kind: Annotated[
        Literal["copy", "proxy", "resolve"] | None, "Only receipts of this kind"
    ] = None,
    transfer: Annotated[str | None, "Only the receipt for this transfer"] = None,
    status: Annotated[RECEIPT_STATUS | None, "Only receipts in this state"] = None,
    limit: Annotated[int, "Maximum receipts to return"] = 50,
    offset: Annotated[int, "Receipts to skip"] = 0,
) -> list[dict]:
    """Summaries only. ``ferry.receipt.get`` carries the files."""
    limit, offset = _window(limit, offset)
    sql = "SELECT * FROM ferry_receipts WHERE 1=1"
    params: list = []
    for column, value in (("kind", kind), ("transfer", transfer), ("status", status)):
        if value is not None:
            sql += f" AND {column}=?"
            params.append(value)
    sql += " ORDER BY created DESC, id DESC LIMIT ? OFFSET ?"
    params.extend([limit, offset])
    return [records.receipt_summary(row) for row in ctx.all(sql, tuple(params))]


# -- verification ------------------------------------------------------------------


@_op(
    "ferry.verify",
    summary="Hash a transfer's destinations again and compare them with its receipt.",
    effect="external",
)
def ferry_verify(
    ctx: OpContext,
    transfer: Annotated[str, "Transfer whose receipt is the baseline"],
    folder: Annotated[str | None, "Also look for files the receipt does not know"] = None,
) -> dict:
    """Reads the destination outside the write lock, then records one report.

    A file that is gone counts as missing, a file whose bytes differ as modified, and,
    when ``folder`` is given, a file the receipt never mentioned as added.
    """
    _transfer_row(ctx, transfer)
    receipt = _latest_receipt(ctx, transfer)
    expected = {row["dest"]: row for row in receipt.get("files") or []}
    missing: list[str] = []
    modified: list[str] = []
    unreadable: list[str] = []
    checked = 0
    for dest, row in expected.items():
        path = Path(dest)
        if not path.is_file():
            missing.append(row["rel"] or dest)
            continue
        checked += 1
        try:
            landed = hash_file(path)
        except OSError:
            unreadable.append(row["rel"] or dest)
            continue
        if landed != row["sha256"]:
            modified.append(row["rel"] or dest)

    added: list[str] = []
    if folder is not None:
        root = _folder(folder, "folder")
        known = {Path(dest).resolve() for dest in expected}
        for found in scan(root):
            if found.path.resolve() not in known:
                added.append(found.rel)

    clean = not missing and not modified and not added and not unreadable
    report = {
        "id": ids.new_id(),
        "transfer": transfer,
        "plan": receipt.get("plan"),
        "folder": str(Path(folder).expanduser()) if folder is not None else "",
        "checked": checked,
        "missing": sorted(missing),
        "modified": sorted(modified),
        "added": sorted(added),
        "unreadable": sorted(unreadable),
        "clean": clean,
        "created": _now(),
    }
    ctx.write("ferry.verify.recorded", report)
    return report


# -- proxies -----------------------------------------------------------------------


@_op(
    "ferry.proxies",
    summary="Queue proxy encodes for the video in a source.",
    effect="write",
    returns="{id, source, destination, codec, target_height, items, job, queued}",
)
def ferry_proxies(
    ctx: OpContext,
    source: Annotated[str, "Folder holding the media"],
    destination: Annotated[str, "Folder the proxies land in"],
    codec: Annotated[str | None, "Proxy codec; the config's proxy_codec when absent"] = None,
    target_height: Annotated[
        int | None, "Proxy height; the config's proxy_height when absent"
    ] = None,
    machine: Annotated[str | None, "Node that should run the encodes"] = None,
    config_path: Annotated[str | None, "ferry.toml to read"] = None,
) -> dict:
    """The request and its files are recorded before the job is queued."""
    settings = _config(config_path)
    root = _folder(source, "source")
    chosen_codec = codec or settings.proxy_codec
    height = target_height or settings.proxy_height
    plan = layout.build(root, destination, settings)
    items = [
        {"rel": item["rel"], "source": item["source"], "proxy": item["dest"]}
        for item in layout.video_items(plan["items"])
    ]
    if not items:
        raise Invalid(f"no video to proxy under {root}")
    request = ids.new_id()
    params = {
        "request": request,
        "items": items,
        "codec": chosen_codec,
        "target_height": height,
        "ffmpeg_path": settings.ffmpeg_path,
    }
    job_id, queued = _submit(ctx, "proxy", params, machine)
    record = {
        "id": request,
        "source": str(root),
        "destination": str(Path(destination).expanduser()),
        "codec": chosen_codec,
        "target_height": height,
        "items": items,
        "job": job_id,
        "created": _now(),
    }
    ctx.write("ferry.proxies.requested", record)
    return {**record, "queued": queued}


# -- the Resolve project -----------------------------------------------------------


@_op(
    "ferry.resolve_project",
    summary="Request a Resolve project for a folder: bins, settings and a job.",
    effect="write",
    returns="{id, source, name, resolution, frame_rate, color_space, manifest, bins, job}",
)
def ferry_resolve_project(
    ctx: OpContext,
    source: Annotated[str, "Folder of media the project is built from"],
    destination: Annotated[str, "Folder the project file and manifest go in"],
    name: Annotated[str | None, "Project name; the source folder's name when absent"] = None,
    resolution: Annotated[Literal["720", "1080", "4K"], "Timeline resolution"] = "1080",
    frame_rate: Annotated[
        Literal["23.976", "24", "25", "29.97", "30", "50", "59.94", "60"], "Timeline frame rate"
    ] = "24",
    color_space: Annotated[str, "Timeline colour space"] = "Rec.709",
    machine: Annotated[str | None, "Node with Resolve, when one should build it"] = None,
) -> dict:
    """Records the request; the ``resolve`` job writes the manifest and, if Resolve is
    installed, creates the project. Without Resolve the manifest is the deliverable."""
    root = _folder(source, "source")
    project_name = name or root.name
    manifest = Path(destination).expanduser() / f"{project_name}.resolve.json"
    request = ids.new_id()
    params = {
        "request": request,
        "source": str(root),
        "name": project_name,
        "manifest": str(manifest),
        "resolution": resolution,
        "frame_rate": frame_rate,
        "color_space": color_space,
    }
    job_id, queued = _submit(ctx, "resolve", params, machine)
    record = {
        "id": request,
        "source": str(root),
        "name": project_name,
        "resolution": resolution,
        "frame_rate": frame_rate,
        "color_space": color_space,
        "manifest": str(manifest),
        "bins": layout.bin_names(root),
        "job": job_id,
        "created": _now(),
    }
    ctx.write("ferry.resolve.requested", record)
    return {**record, "queued": queued}


__all__ = [
    "ferry_offload",
    "ferry_plan",
    "ferry_plan_get",
    "ferry_plan_list",
    "ferry_proxies",
    "ferry_receipt_get",
    "ferry_receipt_list",
    "ferry_receipt_record",
    "ferry_resolve_project",
    "ferry_source_list",
    "ferry_transfer_get",
    "ferry_transfer_list",
    "ferry_transfer_progress",
    "ferry_transfer_start",
    "ferry_verify",
]
