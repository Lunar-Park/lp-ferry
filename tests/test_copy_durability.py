"""Copy durability is the engine's now: fsync the bytes, fsync the entry, then hash.

These assertions used to live here, against ``park_plugins/ferry/copy.py`` — the Python copy
lane that staged a file, flushed it, flushed the directory entry and only then hashed it into
a receipt (``db51c56``, "make verified mean out of the page cache, not just passed"). That
module was removed in ``fb5ae37`` when the plugin became a thin adapter over the ferry engine
(Go v2), so this file could not even be collected: it imported a module that no longer exists.

The ordering it pinned did not go away; it moved, and it is tested where it now lives:

    internal/storage/publish.go   Reserve fsyncs the staged bytes, Publish fsyncs the
                                  directory entry after the link/rename — the two flushes
                                  these tests counted.
    internal/storage/readback_*.go  the readback that fills a receipt reads through
                                  f_nocache (darwin) / fadvise_dontneed (linux), so the hash
                                  is of the media and not of the page cache.
    internal/storage/storage_test.go  TestReadbackMode, TestT1_2_PublishNeverClobbers,
                                  TestT1_4_BindingChangeDetected.
    internal/engine/engine_test.go    a whole copy end to end: fan-out, verification,
                                  mtimes, and the receipt that comes out of it.
    tests/park_plugin/test_offload.py  the plugin's side: a receipt is only recorded for
                                  files the engine verified and Park registered by hash.

Run ``go test ./internal/storage/... ./internal/engine/...`` for the guarantee itself. This
file stays as the pointer, so the next reader of the deleted module's tests finds them.
"""

from __future__ import annotations

import subprocess
from pathlib import Path

import pytest

REPO = Path(__file__).resolve().parents[1]


def test_the_copy_lane_is_the_engine_and_its_durability_tests_are_there():
    """The code that used to be tested here is in Go, and so are its tests."""
    assert not (REPO / "park_plugins" / "ferry" / "copy.py").exists(), (
        "the Python copy lane was retired with the Go engine (fb5ae37)"
    )
    for path in (
        "internal/storage/publish.go",
        "internal/storage/storage_test.go",
        "internal/engine/engine_test.go",
    ):
        assert (REPO / path).is_file(), path


@pytest.mark.skipif(not (REPO / "go.mod").is_file(), reason="not a Go checkout")
def test_storage_and_engine_durability_suites_pass():
    done = subprocess.run(
        ["go", "test", "./internal/storage/...", "./internal/engine/..."],
        cwd=REPO,
        capture_output=True,
        text=True,
        timeout=600,
    )
    assert done.returncode == 0, done.stdout + done.stderr
