"""What is still decided in Python: mount checks, Resolve bins and the proxy list.

Copy planning (where each file lands, conflicts, fingerprints) belongs to the Go engine
(see ``engine.build``). The helpers here serve the jobs that stay in the plugin —
proxies and Resolve projects — and the mount check a copy job makes before it hands
the plan to the engine.
"""

from __future__ import annotations

from pathlib import Path

from .probe import is_system_artifact, scan

COPY = "copy"
SKIP = "skip"


def mount_signature(root: Path) -> list[int]:
    """Observe the actual directory, so a reused mount path invalidates a plan."""
    if root.resolve(strict=True) != root or not root.is_dir():
        raise ValueError(f"not the planned folder: {root}")
    info = root.stat()
    return [info.st_dev, info.st_ino]


def validate_mounts(plan: dict) -> None:
    for side, signature in (plan.get("mounts") or {}).items():
        root = Path(plan[side])
        if mount_signature(root) != signature:
            raise ValueError(f"{side} mount changed since planning: {root}")


def proxy_items(source: Path, destination: Path | str) -> list[dict]:
    """One proxy per video file, mirrored under ``destination``."""
    target = Path(destination).expanduser()
    return [
        {"rel": found.rel, "source": str(found.path), "proxy": str(target / found.rel)}
        for found in scan(source)
        if found.kind == "video"
    ]


def bin_names(source: Path | str) -> list[str]:
    """Resolve bins for a folder: the folder's own name, then one per subfolder below it."""
    source = Path(source)
    root = source.name
    names = {root}
    for path in sorted(source.rglob("*")):
        if path.is_dir() and not is_system_artifact(path, source):
            names.add((Path(root) / path.relative_to(source)).as_posix())
    return sorted(names)


__all__ = ["COPY", "SKIP", "bin_names", "mount_signature", "proxy_items", "validate_mounts"]
