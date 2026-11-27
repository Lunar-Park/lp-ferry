"""Ferry's ops: plans, offloads, sends, receipts, verification, proxies and Resolve projects.

Planning, copying, machine-to-machine transport and re-verification are done by the local
Go engine (``ferry daemon``; see ``engine``). These ops keep Park's side: the events, the
index the Ingest view reads, project filing and the job queue. A plan is the engine's plan,
recorded as an event under the engine's id and fingerprint, so the offload that follows
starts exactly what was reviewed. The three jobs (``copy``, ``proxy``,
``resolve``) report through the transfer and receipt ops here, which is how a file that
landed becomes a row the Ingest view and ``ferry.receipt.get`` can show.
"""

from __future__ import annotations

import json
import posixpath
import re
from collections.abc import Callable
from hashlib import sha256
from pathlib import Path
from typing import Annotated, Any, Literal

from park.kernel import ids
from park.kernel.log import utc_now
from park.ops import Conflict, Forbidden, Invalid, NotFound, OpContext, Unavailable, op
from park.ops import resolve as resolve_op
from park.sdk import is_admin, job_worker

from . import config as ferry_config
from . import engine, layout, records
from .probe import scan

SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
RECEIPT_STATUS = Literal["ok", "partial", "failed"]
TRANSFER_STATUS = {"ok": "copied", "partial": "partial", "failed": "failed"}
MAX_WINDOW = 500
TRANSFER_READ = (
    "SELECT f.*, j.status AS job_status, j.error AS job_error"
    " FROM ferry_transfers AS f LEFT JOIN jobs AS j ON j.id=f.job"
)
SOURCE_ROLE = "source"


# -- shared helpers ----------------------------------------------------------------


def _op(name: str, **kwargs: Any) -> Callable[[Callable[..., Any]], Callable[..., Any]]:
    """Register one of ferry's ops. ``plugin`` is set here so ``op.list`` can filter."""
    return op(name, plugin="ferry", **kwargs)


def _worker_only(ctx: OpContext, job: str | None, machine: str | None = None, *, what: str) -> None:
    """Authorize a report that only the job running a transfer should make.

    Three callers may: the node surface (park dispatched the call to that node, which is how a
    job reaches these ops), a reviewer (D from the CLI or an app), and the machine actually
    holding the job — which is what ``job_worker`` proves, from the queue rather than from
    anything the caller says about itself. An agent's session is none of those, and used to be
    able to report progress on, or close, a transfer it never ran.
    """
    if ctx.surface == "node" or is_admin(ctx):
        return
    if not job:
        raise Forbidden(f"{what} has to name the job that ran it")
    job_worker(ctx, job, machine=machine)


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
    row = ctx.one(f"{TRANSFER_READ} WHERE f.id=?", (transfer,))
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
    return job_id, bool(job_id)


def _request_clearance(
    ctx: OpContext,
    *,
    op: str,
    transfer: str,
    reason: str,
    preview: dict | None = None,
    job: str | None = None,
) -> dict | None:
    """Ask Park for a human decision about a transfer, when this Park has clearances.

    Clearances arrived with Park's Phase 1, so this is optional end to end: an older hub has
    no ``machines.clearance.request`` and the transfer is queued or reported exactly as it
    was before. ``job`` links the clearance to the job it pauses, which Park only does for a
    *queued* job; a job that is already running cannot be paused, so the clearance is
    recorded against the transfer alone and the caller says so.
    """
    try:
        resolve_op("machines.clearance.request")
    except NotFound:
        return None
    body = {
        "op": op,
        "reason": reason,
        # `paused` says whether Park stopped the job behind this decision. A person reading
        # the card can tell "nothing is moving" from "it is still running".
        "preview": {"transfer": transfer, "paused": False, **(preview or {})},
    }
    if job:
        try:
            return ctx.call(
                "machines.clearance.request",
                {**body, "job": job, "preview": {**body["preview"], "job": job, "paused": True}},
            )
        except (Conflict, Forbidden, NotFound):
            # The job is running, or another actor's: record the decision without pausing it.
            pass
    try:
        return ctx.call("machines.clearance.request", body)
    except (Conflict, Forbidden, NotFound, Invalid):
        return None


def _agent(actor: str) -> bool:
    return str(actor or "").startswith("agent:")


def _needs_clearance(ctx: OpContext, actor: str, size: int) -> bool:
    """Whether an agent's transfer has to wait for a person before it is queued."""
    if not _agent(actor):
        return False
    try:
        limit = _config(None).auto_approve_max_bytes
    except Invalid:
        limit = 0
    return size > limit


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
                "kind": str(item.get("kind") or ""),
                "verified": bool(item.get("verified", True)),
                "skipped": bool(item.get("skipped", False)),
            }
        )
    return out


def _digest(files: list[dict]) -> str:
    body = [[item["rel"], item["sha256"]] for item in sorted(files, key=lambda one: one["rel"])]
    return sha256(json.dumps(body, separators=(",", ":")).encode("utf-8")).hexdigest()


# -- plans -------------------------------------------------------------------------


def _engine_plan(source: str, destination: str, **kwargs: Any) -> dict:
    """``engine.build`` with its failures as Park errors."""
    try:
        return engine.build(source, destination, **kwargs)
    except engine.PlanError as exc:
        raise Invalid(str(exc)) from exc
    except engine.EngineUnavailableError as exc:
        raise Unavailable(str(exc)) from exc
    except engine.EngineError as exc:
        if exc.code == engine.NOT_FOUND:
            raise NotFound(str(exc)) from exc
        raise Invalid(str(exc)) from exc


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
    machine: Annotated[str | None, "Worker that can see both folders"] = None,
    project: Annotated[str | None, "Project id or slug"] = None,
) -> dict:
    """Reads the source outside the write lock, then records the plan as one event."""
    if project and not machine:
        raise Invalid("choose the worker that owns the source and destination")
    scope = ctx.call("project.get", {"project": project}) if project else None
    if machine:
        owner = ctx.call("machines.get", {"machine": machine})["id"]
        plan = ctx.node_call(
            "ferry.plan",
            {
                "source": source,
                "destination": destination,
                "label": label,
                "config_path": config_path,
                "probe": probe,
            },
            machine=owner,
        )
        if plan.get("machine") != owner or not plan.get("mounts"):
            raise Conflict("the selected worker did not confirm both folders")
    else:
        settings = _config(config_path)
        root = _folder(source, "source")
        target = Path(destination).expanduser()
        if root.resolve() == target.resolve() or target.resolve().is_relative_to(root.resolve()):
            raise Invalid("the destination must not be inside the source")
        # Park offloads name their destination explicitly; the folder is made here, empty,
        # so the engine can bind and probe it before anything is approved.
        target.mkdir(parents=True, exist_ok=True)
        plan = _engine_plan(
            str(root),
            str(target),
            template=settings.template,
            on_conflict=settings.on_conflict,
            label=label,
        )
    if not plan["files"]:
        raise Invalid(f"no files to plan under {plan['source']}")
    created = _now()
    result = {
        "created": created,
        **plan,
        "project": scope["id"] if scope else None,
        "project_slug": scope["slug"] if scope else None,
    }
    ctx.write("ferry.plan.created", result, project=result["project"])
    return result


@_op("ferry.plan.get", summary="One plan with every file in it.")
def ferry_plan_get(
    ctx: OpContext,
    id: Annotated[str, "Plan id"],
    items_limit: Annotated[
        int | None, "Return at most this many files; every file when absent"
    ] = None,
) -> dict:
    """The items are in ``rel`` order, so two reads of the same plan line up.

    ``files`` is always the plan's full count, so a view that asked for a page of the
    items can say how many it left out.
    """
    if items_limit is not None and items_limit < 1:
        raise Invalid("items_limit must be at least 1")
    row = _plan_row(ctx, id)
    sql = "SELECT * FROM ferry_plan_items WHERE plan=? ORDER BY rel"
    params: tuple = (id,)
    if items_limit is not None:
        sql += " LIMIT ?"
        params = (id, items_limit)
    return records.plan_out(row, ctx.all(sql, params))


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


def _sources(ctx: OpContext, project: str | None = None) -> list[dict]:
    where, params = (" WHERE project=?", (project,)) if project else ("", ())
    rows = ctx.all(
        "SELECT source, machine, COUNT(*) AS plans, SUM(files) AS files, SUM(bytes) AS bytes,"
        f" MAX(created) AS last_plan FROM ferry_plans{where}"
        " GROUP BY source, machine ORDER BY last_plan DESC",
        params,
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
                "machine": row["machine"],
                "mounted": None if row["machine"] else path.is_dir(),
            }
        )
    return out


@_op(
    "ferry.source.list", summary="Source folders ferry has planned from, and whether they are here."
)
def ferry_source_list(ctx: OpContext) -> list[dict]:
    """One row per source: how many plans, how many files, and if the folder is mounted."""
    return _sources(ctx)


# -- the Ingest view's one read ------------------------------------------------------

INGEST_STATE = {
    "queued": "queued",
    "running": "running",
    "copied": "done",
    "partial": "partial",
    "failed": "failed",
}
RECEIPT_STATE = {"ok": "done", "partial": "partial", "failed": "failed"}


def _in(column: str, values: list[str]) -> tuple[str, tuple]:
    return f"{column} IN ({', '.join('?' for _ in values)})", tuple(values)


def _offload_row(
    plan: dict, transfer: dict | None, receipt: dict | None, attempts: int, mounted: bool | None
) -> dict:
    """One offload: the plan, the newest transfer that ran it and the receipt it left."""
    path = Path(plan["source"])
    destination = Path(plan["destination"])
    row = {
        "kind": "offload",
        "id": plan["id"],
        "plan": plan["id"],
        "project": plan["project"],
        "project_slug": plan["project_slug"],
        "owner": plan["machine"],
        "label": plan["label"],
        "card": path.name or plan["source"],
        "source": plan["source"],
        "mounted": mounted,
        "destination": plan["destination"],
        "destination_name": destination.name or plan["destination"],
        "on_conflict": plan["on_conflict"],
        "files": plan["files"],
        "copies": plan["copies"],
        "skips": plan["skips"],
        "bytes": plan["bytes"],
        "created": plan["created"],
        "state": "planned",
        "transfer": None,
        "attempts": attempts,
        "receipt": receipt["id"] if receipt else None,
        "digest": (receipt or {}).get("digest") or None,
        "fraction": None,
        "done": 0,
        "total": plan["copies"],
        "failed": 0,
        "verified": 0,
        "skipped": 0,
        "errors": [],
        "note": None,
        "machine": plan["machine"],
        "job": None,
        "job_status": None,
        "queued": None,
        "started": None,
        "ended": None,
        "when": plan["created"],
    }
    if transfer is not None:
        state = INGEST_STATE.get(transfer["status"], "queued")
        errors = transfer["errors"]
        if not receipt and transfer["job_status"] in ("done", "failed", "canceled"):
            state = "canceled" if transfer["job_status"] == "canceled" else "failed"
            errors = errors or [
                transfer["job_error"] or f"Copy job {state} before it wrote a receipt"
            ]
        row.update(
            {
                "state": state,
                "transfer": transfer["id"],
                "fraction": transfer["fraction"],
                "done": transfer["done"],
                "total": transfer["total"] or plan["copies"],
                "failed": transfer["failed"],
                "verified": transfer["verified"],
                "skipped": transfer["skipped"],
                "errors": errors,
                "note": transfer["note"],
                "machine": transfer["machine"],
                "job": transfer["job"],
                "job_status": transfer["job_status"],
                "queued": transfer["queued"],
                "started": transfer["started"],
                "ended": transfer["ended"],
                "when": transfer["ended"]
                or transfer["started"]
                or transfer["queued"]
                or plan["created"],
            }
        )
        if transfer["status"] in ("copied", "partial", "failed"):
            row["bytes"] = transfer["bytes"]
    return row


@_op(
    "ferry.ingest.list",
    summary="What the Ingest view shows, in one read: the cards and one row per offload.",
    returns="{sources, rows, more}",
)
def ferry_ingest_list(
    ctx: OpContext,
    limit: Annotated[int, "Maximum rows to return"] = 100,
    project: Annotated[str | None, "Only this project's plans and transfers, by id or slug"] = None,
) -> dict:
    """``rows`` is newest first. An offload row joins a plan with the newest transfer that
    ran it and the receipt that transfer left, so the view needs no read per row; a proxy or
    Resolve receipt is a row of its own, because nothing else says it happened. ``more`` is
    true when older rows were left out."""
    limit, _ = _window(limit, 0)
    project_id = ctx.call("project.get", {"project": project})["id"] if project else None
    sources = _sources(ctx, project_id)
    mounted = {(item["source"], item["machine"]): item["mounted"] for item in sources}
    where, args = (" WHERE project=?", (project_id,)) if project_id else ("", ())

    plans = [
        records.plan_out(row)
        for row in ctx.all(
            f"SELECT * FROM ferry_plans{where} ORDER BY created DESC, id DESC LIMIT ?",
            (*args, limit + 1),
        )
    ]
    more = len(plans) > limit
    plans = plans[:limit]

    transfers: dict[str, list[dict]] = {}
    receipts: dict[str, dict] = {}
    if plans:
        clause, params = _in("f.plan", [plan["id"] for plan in plans])
        for row in ctx.all(f"{TRANSFER_READ} WHERE {clause}", params):
            transfers.setdefault(row["plan"], []).append(records.transfer_out(row))
        ids_ = [item["id"] for group in transfers.values() for item in group]
        if ids_:
            clause, params = _in("transfer", ids_)
            for row in ctx.all(
                f"SELECT * FROM ferry_receipts WHERE {clause} ORDER BY created, id", params
            ):
                receipts[row["transfer"]] = records.receipt_summary(row)

    rows = []
    for plan in plans:
        ran = sorted(
            transfers.get(plan["id"], []),
            key=lambda item: (item["queued"] or item["started"] or item["ended"] or "", item["id"]),
        )
        newest = ran[-1] if ran else None
        receipt = receipts.get(newest["id"]) if newest else None
        here = mounted.get((plan["source"], plan["machine"]))
        rows.append(_offload_row(plan, newest, receipt, len(ran), here))

    extra = (
        []
        if project_id
        else ctx.all(
            "SELECT * FROM ferry_receipts WHERE kind != 'copy' ORDER BY created DESC, id DESC LIMIT ?",
            (limit,),
        )
    )
    for row in extra:
        summary = records.receipt_summary(row)
        detail = (records.receipt_out(row).get("detail")) or {}
        request = detail.get("request")
        source = ""
        if request:
            table = (
                "ferry_proxy_requests" if summary["kind"] == "proxy" else "ferry_resolve_requests"
            )
            found = ctx.one(f"SELECT source FROM {table} WHERE id=?", (request,))
            source = (found or {}).get("source") or ""
        card = Path(source).name if source else ""
        word = "Proxies" if summary["kind"] == "proxy" else "Resolve project"
        rows.append(
            {
                "kind": summary["kind"],
                "id": summary["id"],
                "plan": summary["plan"],
                "label": f"{word} for {card}" if card else word,
                "card": card,
                "source": source,
                "mounted": mounted.get((source, None), False),
                "destination": "",
                "destination_name": detail.get("name") or "",
                "files": summary["files"],
                "bytes": summary["bytes"],
                "state": RECEIPT_STATE.get(summary["status"], "done"),
                "receipt": summary["id"],
                "digest": summary["digest"] or None,
                "errors": summary["errors"],
                "failed": len(summary["errors"]),
                "machine": summary["machine"],
                "ended": summary["created"],
                "created": summary["created"],
                "when": summary["created"],
            }
        )
    rows.sort(key=lambda item: (item["when"] or "", item["id"]), reverse=True)
    return {"sources": sources, "rows": rows[:limit], "more": more or len(rows) > limit}


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
    """Queue one live transfer per owned plan and return the exact job it uses."""
    row = _plan_row(ctx, plan)
    owner = row.get("machine")
    if not owner:
        try:
            resolve_op("machines.job.submit")
        except NotFound:
            pass  # Historical in-process copy, with no distributed queue.
        else:
            raise Invalid("plan again on the worker that owns both folders before queueing a copy")
    if owner and machine and ctx.call("machines.get", {"machine": machine})["id"] != owner:
        raise Conflict(f"plan belongs to {owner}; plan again to choose another worker")
    if owner:
        machine = owner
        current = ctx.one(
            f"{TRANSFER_READ} WHERE f.plan=? ORDER BY f.id DESC LIMIT 1",
            (plan,),
        )
        if (
            current
            and current["job"]
            and (
                current["status"] == "copied"
                or (
                    current["status"] in ("queued", "running")
                    and current["job_status"] not in ("done", "failed", "canceled")
                )
            )
        ):
            return {
                "plan": plan,
                "transfer": current["id"],
                "job": current["job"],
                "machine": owner,
                "queued": True,
                "files": row["files"],
                "bytes": row["bytes"],
            }
    transfer = ids.new_id()
    params = {"plan": plan, "transfer": transfer}
    if owner:
        params["machine"] = owner
    job_id, queued = _submit(ctx, "copy", params, machine)
    if owner and not queued:
        raise Unavailable("Park did not accept the copy job")
    total = row["files"] if owner else row["copies"]
    clearance = None
    if queued and _needs_clearance(ctx, ctx.actor, int(row["bytes"] or 0)):
        # An agent's copy over the auto-approve limit waits for a person: Park pauses the
        # queued job, so no node picks it up until the clearance is granted.
        clearance = _request_clearance(
            ctx,
            op="ferry.offload",
            transfer=transfer,
            reason=f"{ctx.actor} asked to copy {row['bytes']} bytes; over the auto-approve limit",
            preview={"plan": plan, "machine": machine, "files": total, "bytes": row["bytes"]},
            job=job_id,
        )
    ctx.write(
        "ferry.transfer.queued",
        {
            "id": transfer,
            "plan": plan,
            "job": job_id,
            "machine": machine,
            "total": total,
            "queued": _now(),
            "clearance": (clearance or {}).get("id"),
        },
    )
    return {
        "plan": plan,
        "transfer": transfer,
        "job": job_id,
        "machine": machine,
        "queued": queued,
        "files": total,
        "bytes": row["bytes"],
        **({"clearance": clearance["id"]} if clearance else {}),
    }


# -- send: one machine's disk to another's, directly ---------------------------------


def _absolute(value: str, where: str) -> str:
    text = str(value or "").strip()
    if not text or not posixpath.isabs(text):
        raise Invalid(f"{where} must be an absolute path")
    return posixpath.normpath(text)


def _engine_on(ctx: OpContext, machine: str, params: dict) -> dict:
    return ctx.node_call("ferry.engine", params, machine=machine)


@_op(
    "ferry.send",
    summary="Send a folder from one machine's disk to another's, directly, verified, with a receipt.",
    effect="write",
    returns="{transfer, job, source_machine, destination_machine, queued}",
)
def ferry_send(
    ctx: OpContext,
    source_machine: Annotated[str, "Machine that holds the folder"],
    source: Annotated[str, "Absolute path of the folder on that machine"],
    destination_machine: Annotated[str, "Machine the files should land on"],
    destination: Annotated[str, "Absolute path of the folder they land in, on that machine"],
    label: Annotated[str | None, "Name for the send; the source folder's name when absent"] = None,
    project: Annotated[str | None, "Project id or slug"] = None,
) -> dict:
    """Queues one job, on the destination machine, which pulls every file straight from the
    source machine's ferry engine and verifies it there. The two engines are paired here the
    first time (each pins the other's key); afterwards every connection is mutual TLS on
    those keys. No byte passes through the hub, nothing is staged on a third machine, and the
    source is never changed. The source machine serves only folders under its ``[[roots]]``."""
    root, target = _absolute(source, "source"), _absolute(destination, "destination")
    origin = ctx.call("machines.get", {"machine": source_machine})
    landing = ctx.call("machines.get", {"machine": destination_machine})
    if origin["id"] == landing["id"]:
        raise Invalid("source and destination are the same machine; plan an offload instead")
    scope = ctx.call("project.get", {"project": project}) if project else None
    src = _engine_on(ctx, origin["id"], {"action": "info"})
    dst = _engine_on(ctx, landing["id"], {"action": "info"})
    if src["peer_id"] not in dst["paired"] or dst["peer_id"] not in src["paired"]:
        code = _engine_on(ctx, origin["id"], {"action": "pair_start"})["code"]
        _engine_on(ctx, landing["id"], {"action": "pair_complete", "code": code})
    transfer = ids.new_id()
    job_id, queued = _submit(
        ctx,
        "send",
        {
            "transfer": transfer,
            "source": root,
            "destination": target,
            "source_peer": src["peer_id"],
            "label": label or posixpath.basename(root),
            "project": scope["id"] if scope else None,
        },
        landing["id"],
    )
    if not queued:
        raise Unavailable("Park did not accept the send job")
    clearance = None
    if _needs_clearance(ctx, ctx.actor, 0):
        # A send's size is only known once its plan exists, so an agent's send waits for a
        # person before a node is asked to plan it at all.
        clearance = _request_clearance(
            ctx,
            op="ferry.send",
            transfer=transfer,
            reason=f"{ctx.actor} asked to send {root} to {landing['name']}",
            preview={"source": root, "destination": target, "machine": landing["id"]},
            job=job_id,
        )
    ctx.write(
        "ferry.transfer.queued",
        {
            "id": transfer,
            "plan": "",
            "job": job_id,
            "machine": landing["id"],
            "total": 0,
            "queued": _now(),
            "clearance": (clearance or {}).get("id"),
        },
    )
    return {
        "transfer": transfer,
        "job": job_id,
        "source_machine": origin["id"],
        "destination_machine": landing["id"],
        "queued": True,
        **({"clearance": clearance["id"]} if clearance else {}),
    }


@_op(
    "ferry.send.plan",
    summary="Record the plan a send's destination machine built with its engine.",
    effect="write",
    returns="the recorded plan",
)
def ferry_send_plan(
    ctx: OpContext,
    transfer: Annotated[str, "Transfer id from ferry.send"],
    plan: Annotated[dict, "The engine's plan, from the send job"],
    machine: Annotated[str, "Machine that planned it and will land it"],
    job: Annotated[str | None, "The send job"] = None,
    project: Annotated[str | None, "Project id or slug"] = None,
) -> dict:
    """Called by the send job once the destination's engine has planned the pull. The plan
    belongs to the destination machine, so the ownership checks every transfer has apply."""
    _worker_only(ctx, job, machine, what="recording a send's plan")
    current = _transfer_row(ctx, transfer)
    if current["ended"] or current["plan"]:
        raise Conflict(f"transfer {transfer} is not waiting for a plan")
    landing = ctx.call("machines.get", {"machine": machine})["id"]
    if current["machine"] != landing or current["job"] != job:
        raise Conflict("the transfer was queued for a different machine or job")
    for field in ("id", "fingerprint", "source", "destination", "items"):
        if field not in plan:
            raise Invalid(f"the plan is missing {field}")
    scope = ctx.call("project.get", {"project": project}) if project else None
    record = {
        **plan,
        "created": _now(),
        "machine": landing,
        "mounts": None,
        "project": scope["id"] if scope else None,
        "project_slug": scope["slug"] if scope else None,
    }
    ctx.write("ferry.plan.created", record, project=record["project"])
    ctx.write(
        "ferry.transfer.queued",
        {
            "id": transfer,
            "plan": plan["id"],
            "job": job,
            "machine": landing,
            "total": plan.get("copies", 0),
            "queued": _now(),
        },
    )
    return record


@_op(
    "ferry.send.fail",
    summary="End a send that could not be planned, with the reason.",
    effect="write",
    returns="{transfer}",
)
def ferry_send_fail(
    ctx: OpContext,
    transfer: Annotated[str, "Transfer id from ferry.send"],
    errors: Annotated[list[str], "What went wrong, one line each"],
) -> dict:
    """Called by the send job when it cannot reach a plan. A send that already has a plan ends
    through its receipt instead."""
    current = _transfer_row(ctx, transfer)
    _worker_only(ctx, current["job"], what="ending a send")
    if current["ended"] or current["plan"]:
        raise Conflict(f"transfer {transfer} is not waiting for a plan")
    problems = [str(line) for line in errors] or ["the send failed"]
    ctx.write(
        "ferry.transfer.finished",
        {
            "id": transfer,
            "status": TRANSFER_STATUS["failed"],
            "files": 0,
            "verified": 0,
            "skipped": 0,
            "failed": len(problems),
            "bytes": 0,
            "errors": problems,
            "digest": None,
            "ended": _now(),
        },
    )
    return {"transfer": transfer}


@_op(
    "ferry.clearance",
    summary="Ask Park for a human decision about a transfer that needs one.",
    effect="write",
    returns="{requested, clearance}",
)
def ferry_clearance(
    ctx: OpContext,
    transfer: str,
    reason: str,
    op: Annotated[str, "Operation being cleared"] = "ferry.send",
    preview: Annotated[dict | None, "What the reviewer should see"] = None,
) -> dict:
    """The stable way a job asks for a clearance, wherever it runs.

    A job runs on a node and reaches Park through ``job.call``, so it calls *this* op (which
    every Ferry knows) rather than ``machines.clearance.request`` (which a hub older than
    Phase 1 does not have). When Park has no clearances this answers ``requested: false`` and
    the transfer is reported exactly as before.
    """
    clearance = _request_clearance(ctx, op=op, transfer=transfer, reason=reason, preview=preview)
    return {"requested": clearance is not None, "clearance": (clearance or {}).get("id")}


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
    sql = f"{TRANSFER_READ} WHERE 1=1"
    params: list = []
    if plan is not None:
        sql += " AND f.plan=?"
        params.append(plan)
    if status is not None:
        sql += " AND f.status=?"
        params.append(status)
    sql += " ORDER BY COALESCE(f.queued, f.started, f.ended) DESC, f.id DESC LIMIT ? OFFSET ?"
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
    _worker_only(ctx, job, machine, what="starting a transfer")
    current = _transfer_row(ctx, transfer)
    if current["ended"]:
        raise Conflict(f"transfer {transfer} already ended as {current['status']}")
    owner = _plan_row(ctx, current["plan"]).get("machine")
    if owner and (owner != machine or current["job"] != job):
        raise Conflict("the transfer belongs to a different worker or job")
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
    detail: Annotated[
        dict | None,
        "Live detail: bytes written and verified, rate, eta_s, current_file, phase, transport",
    ] = None,
) -> dict:
    """A long copy calls this about once a second, not once per file.

    ``detail`` is optional and carried on the event, which is what a live view streams (the
    Ingest view follows ``ferry.transfer.progress``); a caller that does not send it, or a
    hub older than this op, is no worse off than before."""
    if done < 0 or total < 0 or done > total:
        raise Invalid("done must be between 0 and total")
    current = _transfer_row(ctx, transfer)
    _worker_only(ctx, current["job"], what="reporting transfer progress")
    fraction = 0.0 if total == 0 else round(done / total, 4)
    event = {
        "id": transfer,
        "done": done,
        "total": total,
        "fraction": fraction,
        "note": note,
    }
    if detail:
        event["detail"] = {str(key): value for key, value in detail.items()}
    ctx.write("ferry.transfer.progress", event)
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
    _worker_only(ctx, job, machine, what="recording a receipt")
    rows = _file_rows(files)
    if transfer is not None:
        current = _transfer_row(ctx, transfer)
        if current["ended"]:
            raise Conflict(f"transfer {transfer} already ended as {current['status']}")
        planned = _plan_row(ctx, current["plan"])
        owner = planned.get("machine")
        if owner and (owner != machine or current["job"] != job or planned["id"] != plan):
            raise Conflict("the receipt belongs to a different worker, job or plan")
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
    receipt = records.receipt_out(row)
    if receipt.get("kind") == "copy" and receipt.get("plan"):
        plan = _plan_row(ctx, receipt["plan"])
        if plan.get("project"):
            receipt["filing"] = _filing(ctx, plan, receipt)
    return receipt


def _verified_hashes(receipt: dict) -> list[str]:
    return sorted({item["sha256"] for item in receipt["files"] if item["verified"]})


def _filing(ctx: OpContext, plan: dict, receipt: dict) -> dict:
    verified = _verified_hashes(receipt)
    attached = {
        row["sha256"]
        for row in ctx.all(
            "SELECT sha256 FROM project_assets WHERE project=? AND role=?",
            (plan["project"], SOURCE_ROLE),
        )
    }
    return {
        "project": plan["project"],
        "project_slug": plan["project_slug"],
        "role": SOURCE_ROLE,
        "attached": [sha for sha in verified if sha in attached],
        "pending": [sha for sha in verified if sha not in attached],
    }


@_op(
    "ferry.receipt.file",
    summary="File the receipt's verified media as project sources.",
    effect="write",
)
def ferry_receipt_file(ctx: OpContext, id: Annotated[str, "Copy receipt id"]) -> dict:
    """File the verified hashes in their originating project. Safe to repair or repeat."""
    receipt = ferry_receipt_get(ctx, id)
    if "filing" not in receipt:
        raise Invalid("the copy receipt does not have an originating project")
    filing = receipt["filing"]
    errors = []
    for digest in filing["pending"]:
        try:
            ctx.call(
                "project.attach_asset",
                {
                    "project": filing["project"],
                    "sha256": digest,
                    "role": SOURCE_ROLE,
                },
            )
        except NotFound as exc:
            errors.append({"sha256": digest, "message": str(exc)})
    return {**_filing(ctx, _plan_row(ctx, receipt["plan"]), receipt), "errors": errors}


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


def _legacy_verify(receipt: dict) -> dict:
    """Receipts recorded before the engine carry only paths and hashes: re-hash them through
    the page cache (no physical readback), in the engine's report shape."""
    missing, modified, unreadable = [], [], []
    checked = 0
    for row in receipt.get("files") or []:
        path = Path(row["dest"])
        checked += 1
        if not path.is_file():
            missing.append(row["dest"])
            continue
        digest = sha256()
        try:
            with path.open("rb") as fh:
                for chunk in iter(lambda: fh.read(1 << 20), b""):
                    digest.update(chunk)
        except OSError as exc:
            unreadable.append({"path": row["dest"], "error": str(exc)})
            continue
        if digest.hexdigest() != row["sha256"]:
            modified.append(row["dest"])
    target = {
        "target_id": "legacy",
        "root": "",
        "missing": missing,
        "modified": modified,
        "unreadable": unreadable,
    }
    return {"checked": checked, "targets": [target], "readback_mode": "page_cache"}


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
    rel_of = {row["dest"]: row["rel"] or row["dest"] for row in receipt.get("files") or []}
    expected = {row["dest"]: row for row in receipt.get("files") or []}
    body = (receipt.get("detail") or {}).get("engine_receipt")
    if not body:
        report = _legacy_verify(receipt)
    else:
        try:
            report = engine.verify_receipt(body)
        except engine.EngineError as exc:
            raise Unavailable(str(exc)) from exc
    roots = {t["target_id"]: t["root"] for t in report.get("targets") or []}

    def rels(key: str) -> list[str]:
        out = []
        for target in report.get("targets") or []:
            for item in target.get(key) or []:
                rel = item["path"] if isinstance(item, dict) else item
                root = roots.get(target["target_id"], "")
                dest = root.rstrip("/") + "/" + rel if root else rel
                out.append(rel_of.get(dest, rel))
        return out

    missing = rels("missing")
    modified = rels("modified")
    unreadable = rels("unreadable")
    checked = report.get("checked", 0) - len(missing)

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
    items = layout.proxy_items(root, destination)
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
    "ferry_clearance",
    "ferry_ingest_list",
    "ferry_offload",
    "ferry_plan",
    "ferry_plan_get",
    "ferry_plan_list",
    "ferry_proxies",
    "ferry_receipt_get",
    "ferry_receipt_list",
    "ferry_receipt_record",
    "ferry_resolve_project",
    "ferry_send",
    "ferry_send_fail",
    "ferry_send_plan",
    "ferry_source_list",
    "ferry_transfer_get",
    "ferry_transfer_list",
    "ferry_transfer_progress",
    "ferry_transfer_start",
    "ferry_verify",
]
