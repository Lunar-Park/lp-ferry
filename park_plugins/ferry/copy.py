"""Copying a file and proving the bytes arrived: streaming hashing, atomic publish.

A copy is two reads and one write: the source is hashed as it is read, then the file
that landed is hashed again and compared. The destination is written under a
``.ferry-part`` suffix and renamed into place, so a reader never sees half a file and a
failed copy leaves no file pretending to be complete.
"""

from __future__ import annotations

import contextlib
import os
from hashlib import sha256
from pathlib import Path

CHUNK_SIZE = 1 << 20
PART_SUFFIX = ".ferry-part"


class CopyError(Exception):
    """A copy could not be made, or the bytes that landed do not match the source."""


def hash_file(path: Path | str) -> str:
    """The SHA-256 of a file's bytes, read in 1 MiB chunks."""
    digest = sha256()
    with open(path, "rb") as fh:
        while chunk := fh.read(CHUNK_SIZE):
            digest.update(chunk)
    return digest.hexdigest()


def copy_verified(
    source: Path | str, dest: Path | str, *, overwrite: bool = True
) -> tuple[str, int]:
    """Copy ``source`` to ``dest`` and verify it.

    Returns ``(sha256, size)`` of the destination, once the destination's own hash
    matches the hash taken while reading the source.
    """
    source = Path(source)
    dest = Path(dest)
    if not source.is_file():
        raise CopyError(f"source file is not there: {source}")
    if dest.exists() and not overwrite:
        raise CopyError(f"destination exists: {dest}")
    dest.parent.mkdir(parents=True, exist_ok=True)
    part = dest.with_name(dest.name + PART_SUFFIX)
    digest = sha256()
    size = 0
    try:
        with open(source, "rb") as reader, open(part, "wb") as writer:
            while chunk := reader.read(CHUNK_SIZE):
                digest.update(chunk)
                writer.write(chunk)
                size += len(chunk)
            writer.flush()
            os.fsync(writer.fileno())
        os.replace(part, dest)
    except OSError as exc:
        raise CopyError(f"cannot copy {source} to {dest}: {exc}") from exc
    finally:
        if part.exists():
            with contextlib.suppress(OSError):
                part.unlink()
    landed = hash_file(dest)
    if landed != digest.hexdigest():
        raise CopyError(f"copy of {source} landed as {landed}, expected {digest.hexdigest()}")
    return landed, size


__all__ = ["CHUNK_SIZE", "PART_SUFFIX", "CopyError", "copy_verified", "hash_file"]
