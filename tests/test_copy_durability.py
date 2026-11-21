"""A copy is only verified if the bytes are on the media before anything calls it verified.

The hash in a receipt is taken after the write, and a write is not durable just because the
call returned: the bytes can still be in the page cache. These tests pin the ordering —
flush the file, flush the directory entry, then hash and record.
"""

from __future__ import annotations

import errno
import os

import pytest
from park_plugins.ferry import copy as copy_module
from park_plugins.ferry.copy import copy_verified, fsync_dir, fsync_file, hash_file


@pytest.fixture
def flushes(monkeypatch):
    """Record every fsync of the copy lane, without touching the real device."""
    seen: list[int] = []
    real = os.fsync

    def record(fd):
        seen.append(fd)
        return real(fd)

    monkeypatch.setattr(copy_module.os, "fsync", record)
    return seen


def test_flushing_a_file_waits_for_the_device(tmp_path, flushes):
    target = tmp_path / "clip.mov"
    target.write_bytes(b"landed" * 100)

    fsync_file(target)

    assert len(flushes) == 1
    assert target.read_bytes() == b"landed" * 100


def test_flushing_a_file_that_is_not_there_raises(tmp_path):
    with pytest.raises(FileNotFoundError):
        fsync_file(tmp_path / "missing.mov")


def test_a_directory_that_cannot_be_opened_is_not_an_error(tmp_path, monkeypatch):
    def refuse(path, flags):
        raise OSError(errno.EACCES, "no directory for you")

    monkeypatch.setattr(copy_module.os, "open", refuse)

    fsync_dir(tmp_path)  # must not raise


def test_a_directory_that_refuses_its_flush_is_not_an_error(tmp_path, monkeypatch):
    def refuse(fd):
        raise OSError(errno.EINVAL, "not that kind of file")

    monkeypatch.setattr(copy_module.os, "fsync", refuse)

    fsync_dir(tmp_path)  # must not raise


def test_publishing_a_copy_flushes_the_bytes_and_the_directory_entry(tmp_path, flushes):
    source = tmp_path / "source.mov"
    source.write_bytes(b"frame" * 1000)
    dest = tmp_path / "out" / "dest.mov"
    dest.parent.mkdir()

    digest, size = copy_verified(source, dest)

    assert dest.read_bytes() == source.read_bytes()
    assert digest == hash_file(source) and size == source.stat().st_size
    # one flush for the staged bytes (inside copy_verified), one for the directory entry
    # that publishes them — that second one is what these tests exist for.
    assert len(flushes) >= 2
