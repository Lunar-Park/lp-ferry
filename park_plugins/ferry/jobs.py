"""Ferry's jobs: copy a planned offload, send between machines, encode proxies, build a
Resolve project.

Copy and send are thin: the local Go engine (``ferry daemon``) plans, copies, verifies and
moves bytes between machines; these handlers relay its progress and record its receipt
in Park. Proxies and Resolve projects are still done here.

Each handler runs against a :class:`park.machines.contract.JobContext`, on whichever node
has the source mounted. Park is reached only through ``job.call`` and
``job.register_output``, so the same code runs under park-node and under the in-process
``LocalJobContext`` the tests use.
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
from pathlib import Path
from typing import TYPE_CHECKING, Any

from . import config, engine, layout
from .probe import kind_for, scan

if TYPE_CHECKING:  # park is not importable in a plain interpreter
    from park.machines.contract import CallContext, JobContext

PRORES_PROFILES = {
    "prores422proxy": 0,
    "prores422lt": 1,
    "prores422": 2,
    "prores422hq": 3,
    "prores4444": 4,
    "prores4444xq": 5,
}
PROGRESS_STEPS = 20


class JobInputError(ValueError):
    """A job was queued without the parameters it needs."""


# -- copy: the Go engine plans, copies and verifies; Park records and registers ----------


def _engine_error(exc: Exception) -> str:
    return str(exc) or exc.__class__.__name__


def plan(call: CallContext) -> dict:
    """Plan on the node selected to run the eventual copy, with that node's engine."""
    params = call.params
    settings = config.load(params.get("config_path"))
    roots = {
        field: Path(params[field]).expanduser().resolve(strict=True)
        for field in ("source", "destination")
    }
    src, dest = roots.values()
    if src.is_relative_to(dest) or dest.is_relative_to(src):
        raise ValueError("source and destination must be separate folders")
    try:
        result = engine.build(
            str(src),
            str(dest),
            template=settings.template,
            on_conflict=settings.on_conflict,
            label=params.get("label"),
        )
    except engine.EngineError as exc:
        raise ValueError(_engine_error(exc)) from exc
    result["mounts"] = {field: layout.mount_signature(root) for field, root in roots.items()}
    return {**result, "machine": call.machine}


def engine_call(call: CallContext) -> dict:
    """This node's engine, for the hub: its peer id, and pairing with another node."""
    action = call.params.get("action")
    try:
        if action == "info":
            return engine.info()
        if action == "pair_start":
            return {"code": engine.pair_start()}
        if action == "pair_complete":
            peer = engine.pair_complete(str(call.params.get("code") or ""))
            return {"peer_id": peer["id"]}
    except engine.EngineError as exc:
        raise ValueError(_engine_error(exc)) from exc
    raise ValueError(f"unknown engine action {action!r}")


def _finish(
    job: JobContext,
    plan: dict,
    transfer: str,
    state: dict | None,
    errors: list[str],
    *,
    canceled: bool,
    register_role: str,
) -> dict:
    """Turn the engine's receipt into Park's: register every landed file with Park, keep
    only what both hashes agree on as verified, and close the transfer."""
    from park.node.client import NodeError
    from park.ops import ParkError

    files: list[dict] = []
    written = 0
    detail: dict[str, Any] = {
        "label": plan.get("label"),
        "destination": plan.get("destination"),
        "canceled": canceled,
    }
    if state is not None:
        body = engine.receipt(state["transfer"]["id"])
        detail["engine"] = {
            "transfer": body["transfer_id"],
            "receipt": body["receipt_id"],
            "plan": body["plan_id"],
            "status": body["status"],
            "digest": body["receipt_digest"],
        }
        detail["engine_receipt"] = body
        items = {item["dest"]: item for item in plan.get("items") or []}
        root = plan.get("destination") or ""
        for f in body.get("files") or []:
            dest = root.rstrip("/") + "/" + f["dest_rel_path"]
            item = items.get(dest) or {}
            rel = item.get("rel") or f["dest_rel_path"]
            if f["action"] in ("copied", "skipped_identical"):
                if f.get("checksum_algo") != "sha256":
                    errors.append(
                        f"{rel}: verified with {f.get('checksum_algo')}; Park registers sha256"
                    )
                    continue
                if register_role:
                    try:
                        registered = job.register_output(Path(dest), role=register_role)
                    except (NodeError, ParkError, OSError) as exc:
                        errors.append(f"{rel}: Park did not confirm registration: {exc}")
                        continue
                    if registered != f["dest_hash"]:
                        errors.append(
                            f"{rel}: verified as {f['dest_hash']} but Park registered {registered}"
                        )
                        continue
                skipped = f["action"] == "skipped_identical"
                files.append(
                    {
                        "rel": rel,
                        "source": f["source_path"],
                        "dest": dest,
                        "sha256": f["dest_hash"],
                        "size": f["size"],
                        "kind": item.get("kind") or kind_for(Path(rel)),
                        "verified": True,
                        "skipped": skipped,
                    }
                )
                written += 0 if skipped else f["size"]
            elif f["action"] in ("failed", "canceled", "not_run"):
                errors.append(f"{rel}: {f.get('reason') or f['action']}")
        if body["status"] in ("needs_attention",) and not errors:
            errors.append(
                "the engine needs attention: "
                + "; ".join(body.get("findings") or ["see the receipt"])
            )
    status = _status(files, errors)
    receipt = job.call(
        "ferry.receipt.record",
        {
            "kind": "copy",
            "status": status,
            "transfer": transfer,
            "plan": plan["id"],
            "job": job.job_id,
            "machine": job.machine,
            "files": files,
            "errors": errors,
            "total_bytes": written,
            "detail": detail,
        },
    )
    filing = None
    if plan.get("project") and files:
        try:
            filed = job.call("ferry.receipt.file", {"id": receipt["id"]})
            filing = {"attached": len(filed["attached"]), "pending": len(filed["pending"])}
            if filed["errors"]:
                filing["errors"] = filed["errors"]
        except (NodeError, ParkError) as exc:
            filing = {"error": str(exc)}
            job.log(f"project filing unconfirmed for receipt {receipt['id']}: {exc}")
    job.log(f"transfer {transfer} {status}: {len(files)} files, {len(errors)} errors")
    return {
        "transfer": transfer,
        "plan": plan["id"],
        "status": status,
        "files": len(files),
        "failed": len(errors),
        "bytes": written,
        "receipt": receipt["id"],
        **({"filing": filing} if filing is not None else {}),
    }


def _execute(
    job: JobContext, plan: dict, transfer: str, *, register_role: str = "transfer"
) -> dict:
    """Run a plan's engine transfer, relaying progress into Park, then record the receipt."""
    from park.machines.contract import JobCancelled

    total = plan["copies"] if plan.get("copies") else len(plan.get("items") or [])
    total_bytes = max(
        1, sum(item["size"] for item in plan.get("items") or [] if item["action"] == "copy")
    )
    last = [-1]

    errors: list[str] = []

    def on_progress(p: dict) -> None:
        from park.node.client import NodeError
        from park.ops import ParkError

        fraction = float(p.get("overall_fraction") or 0)
        step = int(fraction * PROGRESS_STEPS)
        if step == last[0] or last[0] == -2:
            return
        last[0] = step
        done = min(total, round(fraction * total))
        job.progress(fraction, f"{p.get('bytes_verified', 0)} of {total_bytes} bytes verified")
        try:
            job.call(
                "ferry.transfer.progress",
                {
                    "transfer": transfer,
                    "done": done,
                    "total": total,
                    "note": f"{p.get('bytes_verified', 0)} of {total_bytes} bytes verified, ETA {p.get('eta_seconds')}s",
                },
            )
        except (NodeError, ParkError) as exc:
            # The copy itself goes on; the receipt says Park missed the progress.
            errors.append(f"Park did not confirm transfer progress: {exc}")
            last[0] = -2

    state = None
    try:
        layout.validate_mounts(plan)
        state = engine.run(
            plan["id"], plan["fingerprint"], on_progress=on_progress, cancelled=job.cancelled
        )
    except engine.EngineError as exc:
        errors.append(_engine_error(exc))
    except (OSError, ValueError) as exc:
        errors.append(str(exc))
    canceled = bool(state and state["transfer"]["status"] == "canceled")
    if canceled:
        errors.append("Copy canceled before the remaining files were verified")
    result = _finish(
        job, plan, transfer, state, errors, canceled=canceled, register_role=register_role
    )
    if canceled:
        raise JobCancelled("copy canceled after recording verified work")
    return result


def _guard(job: JobContext) -> tuple[str, str, dict, dict] | dict:
    plan_id = str(job.params.get("plan") or "")
    transfer = str(job.params.get("transfer") or "")
    if not plan_id or not transfer:
        raise JobInputError("the copy job needs a plan and a transfer")
    plan = job.call("ferry.plan.get", {"id": plan_id})
    owner = plan.get("machine")
    if owner and (owner != job.machine or owner != job.params.get("machine")):
        raise JobInputError(f"plan belongs to {owner}; refusing copy on {job.machine}")
    current = job.call("ferry.transfer.get", {"id": transfer})
    if not owner and current["job"]:
        raise JobInputError("legacy transfer has no verified worker; plan it again before copying")
    if current["plan"] != plan_id or (owner and current["job"] != job.job_id):
        raise JobInputError("transfer does not belong to this job and plan")
    if current["ended"]:
        receipt = job.call("ferry.receipt.list", {"transfer": transfer, "limit": 1})[0]
        return {
            "transfer": transfer,
            "plan": plan_id,
            "status": receipt["status"],
            "files": receipt["files"],
            "failed": len(receipt["errors"]),
            "bytes": receipt["bytes"],
            "receipt": receipt["id"],
        }
    return plan_id, transfer, plan, current


def copy(job: JobContext) -> dict:
    """Run a planned offload with the engine and record what it proved.

    The engine copies to a job-owned temp, fsyncs, reads it back from disk with the cache
    bypassed, compares hashes and publishes without replacing anything. A file counts as
    copied only when that verification passed and Park registered the same hash; anything
    else is reported in the receipt, and the transfer ends partial or failed rather than ok.
    """
    guarded = _guard(job)
    if isinstance(guarded, dict):
        return guarded
    _, transfer, plan, _ = guarded
    job.call(
        "ferry.transfer.start", {"transfer": transfer, "job": job.job_id, "machine": job.machine}
    )
    return _execute(job, plan, transfer)


# -- send: the destination pulls straight from the source machine's engine ---------------


def _fail_send(job: JobContext, transfer: str, errors: list[str]) -> dict:
    """End a send that never got as far as a plan, so the transfer says why instead of waiting."""
    from park.node.client import NodeError
    from park.ops import ParkError

    try:
        job.call("ferry.send.fail", {"transfer": transfer, "errors": errors})
    except (NodeError, ParkError) as exc:
        job.log(f"could not record the failure of transfer {transfer}: {exc}")
    for line in errors:
        job.log(f"send failed: {line}")
    return {
        "transfer": transfer,
        "status": "failed",
        "files": 0,
        "failed": len(errors),
        "errors": errors,
    }


def send(job: JobContext) -> dict:
    """On the destination machine: plan against the source machine's engine, then pull.

    The two engines are paired (mutual TLS on pinned keys). The source machine only serves
    files under its configured ``[[roots]]``, through a grant scoped to this transfer; the
    bytes go straight from its disk to this one. Every file is verified here with this
    machine's physical readback, published without replacing anything, and registered with
    Park on this machine.
    """
    transfer = str(job.params.get("transfer") or "")
    source = str(job.params.get("source") or "")
    destination = str(job.params.get("destination") or "")
    source_peer = str(job.params.get("source_peer") or "")
    if not transfer or not source or not destination or not source_peer:
        raise JobInputError(
            "the send job needs a transfer, a source peer, a source and a destination"
        )
    target = Path(destination).expanduser()
    if not target.is_dir():
        return _fail_send(
            job, transfer, [f"destination is not a folder on {job.machine}: {target}"]
        )
    try:
        built = engine.build(
            source,
            str(target.resolve()),
            template=engine.DEFAULT_TEMPLATE,
            on_conflict="skip",
            label=job.params.get("label"),
            source_peer=source_peer,
        )
    except (engine.EngineError, ValueError) as exc:
        return _fail_send(job, transfer, [_engine_error(exc)])
    if not built["files"]:
        return _fail_send(job, transfer, [f"no files to send under {source}"])
    plan = job.call(
        "ferry.send.plan",
        {
            "transfer": transfer,
            "plan": built,
            "machine": job.machine,
            "job": job.job_id,
            "project": job.params.get("project"),
        },
    )
    job.call(
        "ferry.transfer.start", {"transfer": transfer, "job": job.job_id, "machine": job.machine}
    )
    return _execute(job, plan, transfer)


# -- proxies -----------------------------------------------------------------------


def proxy(job: JobContext) -> dict:
    """Encode one proxy per requested item with ffmpeg, and register each result."""
    items = job.params.get("items") or []
    if not items:
        raise JobInputError("the proxy job needs items")
    codec = str(job.params.get("codec") or "ProRes422Proxy")
    height = int(job.params.get("target_height") or 1080)
    binary = find_ffmpeg(job.params.get("ffmpeg_path"))
    job.log(f"ffmpeg: {binary}")

    files: list[dict] = []
    errors: list[str] = []
    written = 0
    total = len(items)
    for index, item in enumerate(items, start=1):
        source = Path(item["source"])
        output = Path(item["proxy"])
        try:
            if not source.is_file():
                raise ProxyError(f"source is not there: {source}")
            encode(binary, source, output, codec=codec, target_height=height)
            digest = job.register_output(output, role="proxy")
            size = output.stat().st_size
            written += size
            files.append(
                {
                    "rel": item.get("rel") or source.name,
                    "source": str(source),
                    "dest": str(output),
                    "sha256": digest,
                    "size": size,
                    "verified": True,
                    "skipped": False,
                }
            )
        except (OSError, ProxyError) as exc:
            errors.append(f"{item.get('rel') or source.name}: {exc}")
            job.log(f"failed: {source.name}: {exc}")
        job.progress(index / total, f"{len(files)} of {total} proxies")

    status = _status(files, errors)
    receipt = job.call(
        "ferry.receipt.record",
        {
            "kind": "proxy",
            "status": status,
            "job": job.job_id,
            "machine": job.machine,
            "files": files,
            "errors": errors,
            "total_bytes": written,
            "detail": {
                "request": job.params.get("request"),
                "codec": codec,
                "target_height": height,
                "backend": "ffmpeg",
            },
        },
    )
    return {
        "request": job.params.get("request"),
        "status": status,
        "proxies": len(files),
        "failed": len(errors),
        "bytes": written,
        "receipt": receipt["id"],
    }


class ProxyError(Exception):
    """ffmpeg is missing, or it refused an encode."""


def find_ffmpeg(preferred: str | None = None) -> str:
    """The ffmpeg binary: the configured path when it exists, else ffmpeg on PATH."""
    if preferred:
        candidate = Path(preferred).expanduser()
        if candidate.is_file():
            return str(candidate)
        raise ProxyError(f"no ffmpeg at the configured path: {candidate}")
    found = shutil.which("ffmpeg")
    if not found:
        raise ProxyError("ffmpeg is not installed on this machine")
    return found


def encode(
    binary: str,
    source: Path,
    output: Path,
    *,
    codec: str,
    target_height: int,
) -> Path:
    """Encode one proxy: ProRes at the target height, any audio kept, CFR output."""
    profile = PRORES_PROFILES.get(codec.lower())
    if profile is None:
        raise ProxyError(f"unsupported proxy codec {codec!r}; the ffmpeg path encodes ProRes")
    output.parent.mkdir(parents=True, exist_ok=True)
    command = [
        binary,
        "-y",
        "-i",
        str(source),
        "-vf",
        f"scale=-2:{target_height}",
        "-c:v",
        "prores_ks",
        "-profile:v",
        str(profile),
        "-map",
        "0:v:0",
        "-map",
        "0:a?",
        "-c:a",
        "pcm_s16le",
        "-fps_mode",
        "cfr",
        str(output),
    ]
    done = subprocess.run(command, capture_output=True, text=True, check=False)
    if done.returncode != 0:
        tail = (done.stderr or "").strip().splitlines()
        raise ProxyError(tail[-1] if tail else f"ffmpeg exited {done.returncode}")
    if not output.is_file():
        raise ProxyError(f"ffmpeg wrote no file to {output}")
    return output


# -- the Resolve project -----------------------------------------------------------


def resolve(job: JobContext) -> dict:
    """Build the project manifest and, when Resolve is installed, the project itself.

    The manifest is always written and always registered: it is the deliverable when
    Resolve is not there, and the record of what the project was asked to be when it is.
    """
    source = Path(str(job.params.get("source") or "")).expanduser()
    if not source.is_dir():
        raise JobInputError(f"source folder is not there: {source}")
    name = str(job.params.get("name") or source.name)
    manifest_path = Path(str(job.params.get("manifest") or source / f"{name}.resolve.json"))
    settings = {
        "resolution": str(job.params.get("resolution") or "1080"),
        "frame_rate": str(job.params.get("frame_rate") or "24"),
        "color_space": str(job.params.get("color_space") or "Rec.709"),
    }
    manifest = build_manifest(source, name, settings)
    manifest_path.parent.mkdir(parents=True, exist_ok=True)
    manifest_path.write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    digest = job.register_output(manifest_path, role="manifest")
    files = [
        {
            "rel": manifest_path.name,
            "source": str(source),
            "dest": str(manifest_path),
            "sha256": digest,
            "size": manifest_path.stat().st_size,
            "verified": True,
            "skipped": False,
        }
    ]

    backend = "manifest"
    note = "Resolve is not installed on this machine; the manifest is the deliverable"
    project_path = ""
    app = find_resolve()
    if app is not None:
        try:
            project_path = create_project(app, manifest, manifest_path.parent)
            backend = "resolve"
            note = "" if project_path else "Resolve created the project; it reported no file path"
            if project_path:
                digest = job.register_output(Path(project_path), role="project")
                files.append(
                    {
                        "rel": Path(project_path).name,
                        "source": str(source),
                        "dest": project_path,
                        "sha256": digest,
                        "size": Path(project_path).stat().st_size,
                        "verified": True,
                        "skipped": False,
                    }
                )
        except (ResolveError, OSError) as exc:
            note = f"Resolve was there but refused the project: {exc}"

    receipt = job.call(
        "ferry.receipt.record",
        {
            "kind": "resolve",
            "status": "ok",
            "job": job.job_id,
            "machine": job.machine,
            "files": files,
            "errors": [],
            "total_bytes": sum(item["size"] for item in files),
            "detail": {
                "request": job.params.get("request"),
                "name": name,
                "settings": settings,
                "bins": sorted(manifest["bins"]),
                "backend": backend,
                "note": note,
                "project": project_path,
            },
        },
    )
    return {
        "request": job.params.get("request"),
        "name": name,
        "manifest": str(manifest_path),
        "project": project_path,
        "backend": backend,
        "bins": len(manifest["bins"]),
        "receipt": receipt["id"],
        "note": note,
    }


class ResolveError(Exception):
    """Resolve's scripting API refused a call."""


def build_manifest(source: Path, name: str, settings: dict, proxy_dir: Path | None = None) -> dict:
    """The project description: bins mirroring the folders, and a timeline of clips."""
    source = Path(source)
    bins: dict[str, list[str]] = {bin_name: [] for bin_name in layout.bin_names(source)}
    clips: list[dict] = []
    for found in scan(source):
        parent = Path(found.rel).parent.as_posix()
        bin_name = source.name if parent in ("", ".") else (Path(source.name) / parent).as_posix()
        bins.setdefault(bin_name, []).append(str(found.path))
        if proxy_dir is not None:
            proxy = Path(proxy_dir) / found.rel
            if proxy.is_file():
                clips.append({"source": str(found.path), "proxy": str(proxy)})
    return {
        "name": name,
        "settings": settings,
        "source_folder": str(source),
        "proxy_dir": str(proxy_dir) if proxy_dir is not None else None,
        "bins": bins,
        "timeline": {"name": name, "clips": clips},
    }


def find_resolve(resolve_path: str | None = None) -> Any:
    """Resolve's scripting app, or ``None`` when the module is not installed."""
    if resolve_path:
        modules = Path(resolve_path).expanduser() / "Developer" / "Scripting" / "Modules"
        if modules.is_dir() and str(modules) not in sys.path:
            sys.path.insert(0, str(modules))
    try:
        import DaVinciResolveScript as script  # type: ignore[import-not-found]  # noqa: N813
    except ImportError:
        return None
    try:
        return script.scriptapp("Resolve")
    except Exception:
        return None


def create_project(app: Any, manifest: dict, output_dir: Path) -> str:
    """Create the project in Resolve, with a bin per folder and an empty timeline.

    Returns the project file Resolve saved when it landed beside the manifest, and an
    empty string when Resolve kept it in its own library. Raises ``ResolveError`` when
    an API call fails; the caller keeps the manifest either way.
    """
    manager = app.GetProjectManager()
    if manager is None:
        raise ResolveError("GetProjectManager returned None")
    output_dir.mkdir(parents=True, exist_ok=True)
    project = manager.CreateProject(manifest["name"], str(output_dir))
    if project is None:
        raise ResolveError(f"CreateProject failed for {manifest['name']!r}")
    settings = manifest["settings"]
    for key, value in (
        ("timelineResolutionWidth", settings["resolution"]),
        ("timelineResolutionHeight", settings["resolution"]),
        ("timelineFrameRate", settings["frame_rate"]),
        ("colorScienceMode", settings["color_space"]),
    ):
        try:
            project.SetSetting(key, value)
        except Exception as exc:
            raise ResolveError(f"SetSetting({key}) failed: {exc}") from exc
    pool = manager.GetMediaPool()
    if pool is None:
        raise ResolveError("GetMediaPool returned None")
    root = pool.GetRootFolder()
    for bin_name in sorted(manifest["bins"]):
        if "/" not in bin_name:
            continue
        try:
            pool.AddSubFolder(root, bin_name.split("/", 1)[1])
        except Exception as exc:
            raise ResolveError(f"AddSubFolder({bin_name}) failed: {exc}") from exc
    try:
        pool.CreateTimeline(manifest["timeline"]["name"])
    except Exception as exc:
        raise ResolveError(f"CreateTimeline failed: {exc}") from exc
    try:
        manager.SaveProject()
    except Exception as exc:
        raise ResolveError(f"SaveProject failed: {exc}") from exc
    for candidate in (output_dir / f"{manifest['name']}.drp", output_dir / "project.drp"):
        if candidate.is_file():
            return str(candidate)
    return ""


# -- shared ------------------------------------------------------------------------


def _status(files: list[dict], errors: list[str]) -> str:
    if not errors:
        return "ok"
    return "partial" if files else "failed"


__all__ = [
    "JobInputError",
    "ProxyError",
    "ResolveError",
    "build_manifest",
    "copy",
    "create_project",
    "encode",
    "engine_call",
    "find_ffmpeg",
    "find_resolve",
    "proxy",
    "resolve",
    "send",
]
