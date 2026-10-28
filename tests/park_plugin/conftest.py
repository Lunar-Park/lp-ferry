"""Fixtures for ferry's park plugin tests.

park is not a dependency of this repo's test environment, so every module in this folder
skips when it is missing (see ``pytest.importorskip`` at the top of each test module).
The fixtures here import park inside the fixture body for the same reason.
"""

from __future__ import annotations

from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture(autouse=True)
def plugin_source(monkeypatch):
    """Load the plugin from this checkout, whatever is installed in site-packages."""
    monkeypatch.setenv("PARK_PLUGIN_PATHS", str(ROOT))
    from park.plugins import loader

    loader.reset()
    yield
    loader.reset()


@pytest.fixture
def park(tmp_path):
    from park.kernel import Park

    instance = Park(tmp_path / "home")
    yield instance
    instance.close()


@pytest.fixture
def call(park):
    from park.ops import invoke

    def run(name, args=None, **kwargs):
        return invoke(park, name, args or {}, actor="d", **kwargs)["result"]

    return run


@pytest.fixture
def card(tmp_path):
    """A synthetic camera card: nested clips, a note, and the junk a card picks up.

    ``card.files`` is what a plan must find; ``card.junk`` is what it must leave alone.
    """
    root = tmp_path / "CARD_A"
    (root / "DCIM" / "100MEDIA").mkdir(parents=True)
    (root / "PRIVATE" / "M4ROOT" / "CLIP").mkdir(parents=True)
    files = {
        "DCIM/100MEDIA/A001C001.mov": b"first clip" * 64,
        "DCIM/100MEDIA/A001C001.wav": b"first clip audio" * 32,
        "DCIM/100MEDIA/A001C002.mov": b"second clip" * 64,
        "PRIVATE/M4ROOT/CLIP/C0001.mp4": b"private clip" * 48,
        "notes.txt": b"card notes\n",
    }
    for rel, body in files.items():
        path = root / rel
        path.write_bytes(body)
    junk = {
        ".DS_Store": b"\x00\x01",
        "DCIM/100MEDIA/._A001C001.mov": b"appledouble",
        "Thumbs.db": b"\x00\x02",
        "DCIM/.Spotlight-V100/store": b"index",
    }
    for rel, body in junk.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(body)

    class Card:
        path = root
        names = sorted(files)
        junk_names = sorted(junk)
        total_bytes = sum(len(body) for body in files.values())

    return Card


@pytest.fixture
def job_context(park, tmp_path):
    """A ``LocalJobContext`` for one of ferry's jobs."""
    from park.machines.contract import LocalJobContext

    def build(name, params, workdir=None):
        workdir = Path(workdir) if workdir else tmp_path / "work"
        workdir.mkdir(parents=True, exist_ok=True)
        return LocalJobContext(
            park,
            plugin="ferry",
            name=name,
            params=params,
            workdir=workdir,
            machine="test-node",
        )

    return build
