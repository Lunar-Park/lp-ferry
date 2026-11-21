"""Stage and verify each file before publishing it without replacing a destination."""

from __future__ import annotations

import contextlib
import errno
import os
from collections.abc import Callable
from hashlib import sha256
from pathlib import Path
from uuid import uuid4

CHUNK_SIZE = 1 << 20
PART_SUFFIX = ".ferry-part"


class CopyError(Exception):
    """A copy could not be made, or the bytes that landed do not match the source."""


def hash_file(path: Path | str, *, check_cancelled: Callable[[], None] | None = None) -> str:
    """The SHA-256 of a file's bytes, read in 1 MiB chunks."""
    digest = sha256()
    with open(path, "rb") as fh:
        while chunk := fh.read(CHUNK_SIZE):
            if check_cancelled:
                check_cancelled()
            digest.update(chunk)
    return digest.hexdigest()


def fsync_file(path: Path | str) -> None:
    """Push a file's bytes out of the page cache and wait for the device to take them.

    Called on a file another process wrote (a send's landing) before any hash of it is
    recorded as a receipt: a hash taken before the flush proves what is in the page cache,
    not what is on the disk, and a power cut after the receipt would leave a verified
    transfer with a file that never arrived. This is ``fsync``, not a drive-cache flush: it
    covers the kernel's copy, not a disk that loses its own write cache.
    """
    fd = os.open(os.fspath(path), os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def fsync_dir(path: Path | str) -> None:
    """Flush a directory entry, so a file published here survives a crash.

    Best effort: not every platform or filesystem lets you fsync a directory, and a
    refusal there does not change whether the bytes were published.
    """
    try:
        fd = os.open(os.fspath(path), os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(fd)
    except OSError:
        pass
    finally:
        os.close(fd)


def copy_verified(
    source: Path | str, dest: Path | str, *, check_cancelled: Callable[[], None] | None = None
) -> tuple[str, int]:
    """Copy ``source`` to ``dest`` and verify it.

    Returns ``(sha256, size)`` of the destination, once the destination's own hash
    matches the hash taken while reading the source.
    """
    source = Path(source)
    dest = Path(dest)
    dest.parent.mkdir(parents=True, exist_ok=True)
    part = dest.parent / f".ferry-{uuid4().hex}{PART_SUFFIX}"
    staged_path: Path | None = None
    digest = sha256()
    size = 0
    try:
        with source.open("rb") as reader:
            before = os.fstat(reader.fileno())
            with part.open("xb") as writer:
                staged_path = part
                while chunk := reader.read(CHUNK_SIZE):
                    if check_cancelled:
                        check_cancelled()
                    digest.update(chunk)
                    writer.write(chunk)
                    size += len(chunk)
                writer.flush()
                os.fsync(writer.fileno())
            after = os.fstat(reader.fileno())
            if (before.st_size, before.st_mtime_ns) != (after.st_size, after.st_mtime_ns):
                raise CopyError(f"source changed during the copy: {source}")
        staged = hash_file(staged_path, check_cancelled=check_cancelled)
        if staged != digest.hexdigest():
            raise CopyError(f"staged bytes for {source} did not verify")
        if check_cancelled:
            check_cancelled()
        try:
            os.link(staged_path, dest)
        except OSError as exc:
            if exc.errno in (errno.ENOTSUP, errno.EOPNOTSUPP, errno.ENOSYS):
                raise CopyError(
                    f"destination does not support safe no-replace publication: {dest.parent}"
                ) from exc
            raise
        fsync_dir(dest.parent)
    except FileExistsError:
        raise
    except OSError as exc:
        raise CopyError(f"cannot copy {source} to {dest}: {exc}") from exc
    finally:
        if staged_path:
            with contextlib.suppress(OSError):
                staged_path.unlink()
    return staged, size


__all__ = [
    "CHUNK_SIZE",
    "PART_SUFFIX",
    "CopyError",
    "copy_verified",
    "fsync_dir",
    "fsync_file",
    "hash_file",
]
