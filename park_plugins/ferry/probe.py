"""Reading a source folder: what is in it, what kind of file it is, and what ffprobe says.

This is the plugin's own probe. It walks a folder, skips the junk operating systems
leave on camera cards, classifies each file by extension, and reads media metadata
with ``ffprobe`` when that binary can be found. Nothing here imports park.
"""

from __future__ import annotations

import json
import shutil
import subprocess
from dataclasses import dataclass
from fractions import Fraction
from pathlib import Path

# Names operating systems scatter on removable media. A dot-prefixed component covers
# .Trashes, .Spotlight-V100, .DS_Store and the AppleDouble ._clip.MP4 sidecars macOS
# writes on exFAT cards, which carry real media extensions and choke ffprobe.
SYSTEM_ARTIFACT_NAMES = frozenset(
    {
        "$RECYCLE.BIN",
        "System Volume Information",
        "LOST.DIR",
        "Thumbs.db",
        "desktop.ini",
    }
)

VIDEO_EXTENSIONS = frozenset(
    {
        ".ari",
        ".avchd",
        ".avi",
        ".braw",
        ".crm",
        ".dng",
        ".m2ts",
        ".m4v",
        ".mkv",
        ".mov",
        ".mp4",
        ".mpeg",
        ".mpg",
        ".mts",
        ".mxf",
        ".r3d",
        ".rmd",
        ".ts",
        ".webm",
        ".wmv",
    }
)
AUDIO_EXTENSIONS = frozenset(
    {
        ".aac",
        ".aif",
        ".aiff",
        ".alac",
        ".bwf",
        ".flac",
        ".m4a",
        ".mp3",
        ".ogg",
        ".opus",
        ".wav",
        ".wma",
    }
)
IMAGE_EXTENSIONS = frozenset(
    {
        ".arw",
        ".bmp",
        ".cr2",
        ".cr3",
        ".dpx",
        ".exr",
        ".gif",
        ".heic",
        ".heif",
        ".jpeg",
        ".jpg",
        ".nef",
        ".orf",
        ".png",
        ".raf",
        ".rw2",
        ".tif",
        ".tiff",
        ".webp",
    }
)
DOCUMENT_EXTENSIONS = frozenset(
    {
        ".csv",
        ".doc",
        ".docx",
        ".htm",
        ".html",
        ".json",
        ".jsonl",
        ".md",
        ".odt",
        ".pdf",
        ".ppt",
        ".pptx",
        ".rtf",
        ".tex",
        ".toml",
        ".tsv",
        ".txt",
        ".xls",
        ".xlsx",
        ".xml",
        ".yaml",
        ".yml",
    }
)
MODEL_EXTENSIONS = frozenset(
    {
        ".3ds",
        ".abc",
        ".blend",
        ".c4d",
        ".fbx",
        ".glb",
        ".gltf",
        ".obj",
        ".ply",
        ".stl",
        ".usd",
        ".usda",
        ".usdc",
        ".usdz",
    }
)

# Fields taken from ffprobe's JSON output. Everything else is dropped: a plan holds one
# small dict per file, not a copy of ffprobe's report.
PROBE_FIELDS = (
    "codec",
    "container",
    "width",
    "height",
    "frame_rate",
    "color_space",
    "color_transfer",
    "color_primaries",
    "bit_depth",
    "sample_aspect_ratio",
    "timecode",
    "audio_codec",
    "audio_channels",
    "duration_seconds",
)


class ProbeError(Exception):
    """ffprobe could not be found, or could not read the file."""


@dataclass(frozen=True)
class SourceFile:
    """One file under a source folder."""

    path: Path
    rel: str
    size: int
    mtime: float
    kind: str


def is_system_artifact(path: Path, root: Path) -> bool:
    """True when ``path`` is OS or index junk rather than media.

    Judged on the components below ``root``, so a scan started inside a hidden
    folder still sees its files.
    """
    try:
        parts = path.relative_to(root).parts
    except ValueError:
        parts = path.parts
    return any(part.startswith(".") or part in SYSTEM_ARTIFACT_NAMES for part in parts)


def kind_for(path: Path) -> str:
    """Classify a file by its extension: video, audio, image, document, model or other."""
    suffix = path.suffix.lower()
    if suffix in VIDEO_EXTENSIONS:
        return "video"
    if suffix in AUDIO_EXTENSIONS:
        return "audio"
    if suffix in IMAGE_EXTENSIONS:
        return "image"
    if suffix in DOCUMENT_EXTENSIONS:
        return "document"
    if suffix in MODEL_EXTENSIONS:
        return "model"
    return "other"


def scan(source: Path) -> list[SourceFile]:
    """Every real file under ``source``, in path order. Raises ``NotADirectoryError``."""
    source = Path(source)
    if not source.is_dir():
        raise NotADirectoryError(str(source))
    found: list[SourceFile] = []
    for path in sorted(source.rglob("*")):
        if path.is_symlink() or not path.is_file():
            continue
        if is_system_artifact(path, source):
            continue
        stat = path.stat()
        found.append(
            SourceFile(
                path=path,
                rel=path.relative_to(source).as_posix(),
                size=stat.st_size,
                mtime=stat.st_mtime,
                kind=kind_for(path),
            )
        )
    return found


def find_ffprobe(ffmpeg_path: str | None = None) -> str | None:
    """The ffprobe binary: beside ``ffmpeg_path`` when given, else on PATH."""
    if ffmpeg_path:
        candidate = Path(ffmpeg_path).expanduser().with_name("ffprobe")
        if candidate.is_file():
            return str(candidate)
    return shutil.which("ffprobe")


def probe_file(path: Path | str, ffprobe: str | None = None) -> dict:
    """Read one file with ffprobe and keep the fields a plan records."""
    binary = ffprobe or find_ffprobe()
    if not binary:
        raise ProbeError("ffprobe is not installed")
    command = [
        binary,
        "-v",
        "error",
        "-print_format",
        "json",
        "-show_format",
        "-show_streams",
        str(path),
    ]
    try:
        done = subprocess.run(command, capture_output=True, text=True, check=False, timeout=120)
    except OSError as exc:
        raise ProbeError(f"cannot run ffprobe: {exc}") from exc
    except subprocess.TimeoutExpired as exc:
        raise ProbeError(f"ffprobe timed out on {path}") from exc
    if done.returncode != 0:
        reason = (done.stderr or "").strip().splitlines()
        raise ProbeError(reason[-1] if reason else f"ffprobe exited {done.returncode}")
    try:
        raw = json.loads(done.stdout or "{}")
    except json.JSONDecodeError as exc:
        raise ProbeError(f"ffprobe returned no JSON: {exc}") from exc
    return _fields(raw)


def _fields(raw: dict) -> dict:
    streams = [item for item in raw.get("streams", []) if isinstance(item, dict)]
    video = next((item for item in streams if item.get("codec_type") == "video"), None)
    audio = next((item for item in streams if item.get("codec_type") == "audio"), None)
    container = raw.get("format", {}) or {}
    tags = {str(k).lower(): v for k, v in (container.get("tags") or {}).items()}
    out: dict = {"codec": None, "container": container.get("format_name")}
    if video:
        out["codec"] = video.get("codec_name")
        out["width"] = _int(video.get("width"))
        out["height"] = _int(video.get("height"))
        out["frame_rate"] = _rate(video.get("avg_frame_rate") or video.get("r_frame_rate"))
        out["color_space"] = video.get("color_space")
        out["color_transfer"] = video.get("color_transfer")
        out["color_primaries"] = video.get("color_primaries")
        out["bit_depth"] = _depth(video.get("pix_fmt"))
        out["sample_aspect_ratio"] = video.get("sample_aspect_ratio")
        stream_tags = {str(k).lower(): v for k, v in (video.get("tags") or {}).items()}
        out["timecode"] = stream_tags.get("timecode") or tags.get("timecode")
    if audio:
        out["audio_codec"] = audio.get("codec_name")
        out["audio_channels"] = _int(audio.get("channels"))
    out["duration_seconds"] = _float(container.get("duration"))
    return {key: value for key, value in out.items() if key in PROBE_FIELDS and value is not None}


def _int(value: object) -> int | None:
    if isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, str) and value.strip().lstrip("-").isdigit():
        return int(value)
    return None


def _float(value: object) -> float | None:
    if isinstance(value, bool):
        return None
    if isinstance(value, (int, float)):
        return float(value)
    if isinstance(value, str):
        try:
            return float(value)
        except ValueError:
            return None
    return None


def _rate(value: object) -> float | None:
    """``"24000/1001"`` -> 23.976."""
    if not isinstance(value, str) or not value or value == "0/0":
        return None
    try:
        return round(float(Fraction(value)), 3)
    except (ValueError, ZeroDivisionError):
        return None


def _depth(pix_fmt: object) -> int | None:
    if not isinstance(pix_fmt, str):
        return None
    for tag, depth in (("16", 16), ("12", 12), ("10", 10), ("9", 9)):
        if tag in pix_fmt:
            return depth
    return 8


__all__ = [
    "PROBE_FIELDS",
    "ProbeError",
    "SourceFile",
    "find_ffprobe",
    "is_system_artifact",
    "kind_for",
    "probe_file",
    "scan",
]
