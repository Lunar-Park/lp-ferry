"""Turning a scanned source into a transfer plan: where each file lands and what to do.

The plan is the durable answer to "what will the offload do". It names every file, its
destination, the size, the probed metadata when there is any, and whether the copy will
run or be skipped because the destination already holds that file. Everything here is a
pure function of the source folder, the destination root and the config.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from datetime import UTC, datetime
from hashlib import sha256
from pathlib import Path

from .config import Config
from .probe import ProbeError, find_ffprobe, is_system_artifact, probe_file, scan

CODEC_FAMILIES = {
    "prores": "prores",
    "h264": "h264",
    "avc": "h264",
    "avc1": "h264",
    "hevc": "h265",
    "h265": "h265",
    "hvc1": "h265",
    "hev1": "h265",
    "mpeg2video": "mpeg",
    "mpeg4": "mpeg",
    "mjpeg": "mpeg",
    "vp9": "modern",
    "av1": "modern",
    "theora": "modern",
    "dnxhd": "dnx",
    "dnxhr": "dnx",
    "rawvideo": "raw",
    "pcm_s16le": "audio",
    "pcm_s24le": "audio",
    "pcm_s32le": "audio",
    "pcm_f32le": "audio",
}

PROBED_KINDS = ("video", "audio")

COPY = "copy"
SKIP = "skip"


@dataclass(frozen=True)
class Item:
    """One file in a plan."""

    rel: str
    source: str
    dest: str
    size: int
    mtime: float
    kind: str
    action: str = COPY
    probe: dict = field(default_factory=dict)

    def to_dict(self) -> dict:
        return {
            "rel": self.rel,
            "source": self.source,
            "dest": self.dest,
            "size": self.size,
            "mtime": self.mtime,
            "kind": self.kind,
            "action": self.action,
            "probe": self.probe,
        }


def codec_family(probe: dict) -> str:
    """A coarse codec bucket, ``unknown`` when the file was not probed."""
    codec = probe.get("codec") if probe else None
    if not codec:
        return "unknown"
    return CODEC_FAMILIES.get(str(codec).lower(), str(codec).lower())


def resolution_bucket(probe: dict) -> str:
    """A coarse resolution bucket from the probed height."""
    height = probe.get("height") if probe else None
    if not isinstance(height, int) or height <= 0:
        return "unknown"
    for limit, name in ((480, "480p"), (720, "720p"), (1080, "1080p"), (1440, "1440p")):
        if height <= limit:
            return name
    if height <= 2160:
        return "4K"
    if height <= 4320:
        return "8K"
    return f"{height}p"


def render_dest(
    template: str,
    *,
    root: Path,
    rel: str,
    probe: dict,
    day: str,
) -> Path:
    """Render the organize template for one file.

    ``source_relpath`` is the file's folder below the source root, as the CLI's
    organize step defines it, so subfolders survive the copy.
    """
    relative = Path(rel)
    parent = relative.parent.as_posix()
    values = {
        "root": str(root),
        "source_relpath": "" if parent in ("", ".") else parent,
        "filename": relative.stem,
        "ext": relative.suffix,
        "date": day,
        "codec_family": codec_family(probe),
        "resolution_bucket": resolution_bucket(probe),
    }
    rendered = template.format(**values)
    # A template rendered with an empty {source_relpath} leaves a doubled separator;
    # Path collapses it, which is all that is needed.
    return Path(rendered)


def free_name(path: Path) -> Path:
    """``clip.mov`` -> ``clip-1.mov`` when the first name is taken."""
    if not path.exists():
        return path
    for counter in range(1, 1000):
        candidate = path.with_name(f"{path.stem}-{counter}{path.suffix}")
        if not candidate.exists():
            return candidate
    raise FileExistsError(f"no free name beside {path}")


def build(
    source: Path | str,
    destination: Path | str,
    config: Config,
    *,
    label: str | None = None,
    read_probe: bool = True,
    ffprobe: str | None = None,
    now: datetime | None = None,
) -> dict:
    """Scan ``source`` and describe the plan in one JSON-safe dict.

    Probing is best effort: a file ffprobe cannot read is planned anyway, with the
    reason kept in the item so the plan explains itself later.
    """
    source = Path(source).expanduser()
    destination = Path(destination).expanduser()
    if not source.is_dir():
        raise NotADirectoryError(str(source))
    moment = now or datetime.now(UTC)
    day = moment.strftime("%Y-%m-%d")
    binary = find_ffprobe(config.ffmpeg_path) if read_probe else None

    items: list[Item] = []
    for found in scan(source):
        probe: dict = {}
        if binary and found.kind in PROBED_KINDS:
            try:
                probe = probe_file(found.path, ffprobe=binary)
            except ProbeError as exc:
                probe = {"error": str(exc)}
        dest = render_dest(config.template, root=destination, rel=found.rel, probe=probe, day=day)
        action = COPY
        if dest.exists() and dest.is_file():
            if config.on_conflict == "skip":
                action = SKIP
            elif config.on_conflict == "rename":
                dest = free_name(dest)
        items.append(
            Item(
                rel=found.rel,
                source=str(found.path),
                dest=str(dest),
                size=found.size,
                mtime=found.mtime,
                kind=found.kind,
                action=action,
                probe=probe,
            )
        )

    by_kind: dict[str, int] = {}
    for item in items:
        by_kind[item.kind] = by_kind.get(item.kind, 0) + 1
    plan = {
        "label": label or source.name,
        "source": str(source),
        "destination": str(destination),
        "template": config.template,
        "on_conflict": config.on_conflict,
        "files": len(items),
        "copies": sum(1 for item in items if item.action == COPY),
        "skips": sum(1 for item in items if item.action == SKIP),
        "bytes": sum(item.size for item in items),
        "by_kind": by_kind,
        "items": [item.to_dict() for item in items],
    }
    plan["fingerprint"] = fingerprint(plan["items"])
    return plan


def bin_names(source: Path | str) -> list[str]:
    """Resolve bins for a folder: the folder's own name, then one per subfolder below it."""
    source = Path(source)
    root = source.name
    names = {root}
    for path in sorted(source.rglob("*")):
        if path.is_dir() and not is_system_artifact(path, source):
            names.add((Path(root) / path.relative_to(source)).as_posix())
    return sorted(names)


def fingerprint(items: list[dict]) -> str:
    """A digest of what the plan says, so a later run can tell plans apart."""
    body = [
        [item["rel"], item["size"], item["dest"], item["action"]]
        for item in sorted(items, key=lambda one: one["rel"])
    ]
    return sha256(json.dumps(body, separators=(",", ":")).encode("utf-8")).hexdigest()


def video_items(items: list[dict]) -> list[dict]:
    """The items a proxy pass would encode."""
    return [item for item in items if item["kind"] == "video"]


__all__ = [
    "COPY",
    "SKIP",
    "Item",
    "bin_names",
    "build",
    "codec_family",
    "fingerprint",
    "free_name",
    "render_dest",
    "resolution_bucket",
    "video_items",
]
