"""Ferry's three jobs: copy a planned offload, encode proxies, build a Resolve project.

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

from . import layout
from .copy import CopyError, copy_verified
from .probe import scan

if TYPE_CHECKING:  # park is not importable in a plain interpreter
    from park.machines.contract import JobContext

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


# -- copy --------------------------------------------------------------------------


def copy(job: JobContext) -> dict:
    """Copy every item in a plan, verify each destination, and register it.

    A file is counted as copied only when the copy verified and park registered the same
    hash this handler did. A mismatch is reported in the receipt instead of being claimed
    as a file that landed, and the transfer ends partial or failed rather than ok.
    """
    plan_id = str(job.params.get("plan") or "")
    transfer = str(job.params.get("transfer") or "")
    if not plan_id or not transfer:
        raise JobInputError("the copy job needs a plan and a transfer")
    plan = job.call("ferry.plan.get", {"id": plan_id})
    items = plan.get("items") or []
    job.call(
        "ferry.transfer.start",
        {"transfer": transfer, "job": job.job_id, "machine": job.machine},
    )

    files: list[dict] = []
    errors: list[str] = []
    written = 0
    total = len(items)
    every = max(1, total // PROGRESS_STEPS)
    for index, item in enumerate(items, start=1):
        source = Path(item["source"])
        dest = Path(item["dest"])
        try:
            if not source.is_file():
                raise CopyError(f"source is not mounted: {source}")
            skipped = item["action"] == layout.SKIP and dest.is_file()
            if skipped:
                digest = job.register_output(dest, role="transfer")
                size = dest.stat().st_size
            else:
                digest, size = copy_verified(source, dest)
                registered = job.register_output(dest, role="transfer")
                if registered != digest:
                    raise CopyError(f"{dest} verified as {digest} but park registered {registered}")
                written += size
            files.append(
                {
                    "rel": item["rel"],
                    "source": str(source),
                    "dest": str(dest),
                    "sha256": digest,
                    "size": size,
                    "verified": not skipped,
                    "skipped": skipped,
                }
            )
        except (CopyError, OSError) as exc:
            errors.append(f"{item['rel']}: {exc}")
            job.log(f"failed: {item['rel']}: {exc}")
        if index % every == 0 or index == total:
            job.progress(index / total if total else 1.0, f"{len(files)} of {total} files")
            job.call(
                "ferry.transfer.progress",
                {
                    "transfer": transfer,
                    "done": index,
                    "total": total,
                    "note": f"{len(files)} copied, {len(errors)} failed",
                },
            )

    status = _status(files, errors)
    receipt = job.call(
        "ferry.receipt.record",
        {
            "kind": "copy",
            "status": status,
            "transfer": transfer,
            "plan": plan_id,
            "job": job.job_id,
            "machine": job.machine,
            "files": files,
            "errors": errors,
            "total_bytes": written,
            "detail": {"label": plan.get("label"), "destination": plan.get("destination")},
        },
    )
    job.log(f"transfer {transfer} {status}: {len(files)} files, {len(errors)} errors")
    return {
        "transfer": transfer,
        "plan": plan_id,
        "status": status,
        "files": len(files),
        "failed": len(errors),
        "bytes": written,
        "receipt": receipt["id"],
    }


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
                raise CopyError(f"source is not there: {source}")
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
        except (CopyError, OSError, ProxyError) as exc:
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
    "find_ffmpeg",
    "find_resolve",
    "proxy",
    "resolve",
]
