"""Fixtures for ferry's park plugin tests.

park is not a dependency of this repo's test environment, so every module in this folder
skips when it is missing (see ``pytest.importorskip`` at the top of each test module).
The fixtures here import park inside the fixture body for the same reason.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]


# -- the Go engine ---------------------------------------------------------------------


@pytest.fixture(scope="session")
def ferry_bin(tmp_path_factory):
    """The ferry binary from this checkout (or $FERRY_BIN)."""
    given = os.environ.get("FERRY_BIN")
    if given and Path(given).is_file():
        return given
    go = shutil.which("go")
    if not go:
        pytest.skip("the ferry engine needs Go to build")
    out = tmp_path_factory.mktemp("bin") / "ferry"
    subprocess.run([go, "build", "-o", str(out), "./cmd/ferry"], cwd=ROOT, check=True)
    return str(out)


class Daemon:
    """One running ``ferry daemon``: its own home, config, socket and (optionally) P2P."""

    def __init__(self, binary: str, home: Path, roots: list[tuple[str, str]], p2p: bool):
        self.home = home
        self.sockdir = tempfile.mkdtemp(prefix="fy", dir="/tmp")  # socket paths are length-limited
        self.socket = os.path.join(self.sockdir, "ferry.sock")
        config = home / ".ferry" / "ferry.toml"
        config.parent.mkdir(parents=True, exist_ok=True)
        body = "".join(
            f'[[roots]]\npath = "{path}"\naccess = "{access}"\n' for path, access in roots
        )
        config.write_text(body)
        args = [binary, "--socket", self.socket, "--config", str(config), "daemon"]
        args += ["--listen", "127.0.0.1:0", "--no-mdns"] if p2p else ["--no-p2p"]
        env = {**os.environ, "HOME": str(home)}
        env.pop("FERRY_DB", None)
        self.proc = subprocess.Popen(args, env=env, stderr=subprocess.PIPE, text=True)
        deadline = time.monotonic() + 15
        while not os.path.exists(self.socket):
            if self.proc.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError(
                    f"ferry daemon did not start: {self.proc.stderr.read() if self.proc.poll() is not None else 'timeout'}"
                )
            time.sleep(0.02)

    def stop(self) -> None:
        self.proc.terminate()
        try:
            self.proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            self.proc.kill()
        shutil.rmtree(self.sockdir, ignore_errors=True)


@pytest.fixture
def daemons(ferry_bin, tmp_path):
    """Start ferry daemons: ``daemons(name, roots=[(path, "ro"|"rw")], p2p=False)``."""
    running: list[Daemon] = []

    def start(name: str = "engine", roots=(), p2p: bool = False) -> Daemon:
        daemon = Daemon(ferry_bin, tmp_path / f"home-{name}", list(roots), p2p)
        running.append(daemon)
        return daemon

    yield start
    for daemon in running:
        daemon.stop()


@pytest.fixture
def engine(daemons, ferry_bin, monkeypatch):
    """This machine's engine: one daemon, and the plugin pointed at it."""
    daemon = daemons()
    monkeypatch.setenv("FERRY_SOCKET", daemon.socket)
    monkeypatch.setenv("FERRY_BIN", ferry_bin)
    return daemon


@pytest.fixture(autouse=True)
def plugin_source(monkeypatch):
    """Load the plugin from this checkout, whatever is installed in site-packages."""
    monkeypatch.setenv("PARK_PLUGIN_PATHS", str(ROOT))
    try:
        from park.plugins import loader
    except ImportError:  # test_standalone runs without park
        yield
        return

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


@pytest.fixture
def no_queue():
    """A home whose park has no machines layer: ferry records the work and queues nothing."""
    import park.machines.jobs  # noqa: F401 - register it first, or a later import would bring it back
    from park.ops import REGISTRY

    real = REGISTRY.pop("machines.job.submit", None)
    try:
        yield
    finally:
        if real is not None:
            REGISTRY["machines.job.submit"] = real


@pytest.fixture
def worker_plan(park, call, card, tmp_path, engine):
    """The real broker offers planning to a simulated worker with the fixture card."""
    from park.machines import calls
    from park.machines.contract import LocalCallContext
    from park_plugins.ferry import jobs

    owner = call("machines.register", {"name": "Ferry Worker", "id": "worker-a"})
    other = call("machines.register", {"name": "Other Worker", "id": "worker-b"})
    owner_row = park.one("SELECT * FROM machines WHERE id=?", (owner["id"],))
    other_row = park.one("SELECT * FROM machines WHERE id=?", (other["id"],))
    destination = tmp_path / "destination"
    destination.mkdir()
    broker = calls.start(park)

    def build(**extra):
        with ThreadPoolExecutor(max_workers=1) as pool:
            reply = pool.submit(
                call,
                "ferry.plan",
                {
                    "machine": owner["name"],
                    "source": str(card.path),
                    "destination": str(destination),
                    "probe": False,
                    **extra,
                },
            )
            try:
                assert broker.poll(other_row, wait=0.05, plugins=["ferry"]) is None
                offered = broker.poll(owner_row, wait=2, plugins=["ferry"])
                assert offered and offered["name"] == "plan"
                ctx = LocalCallContext(
                    park, "ferry", "plan", offered["params"], tmp_path, machine=owner["id"]
                )
                try:
                    result = jobs.plan(ctx)
                except (OSError, ValueError) as exc:
                    broker.result(offered["id"], owner["id"], ok=False, result=None, error=str(exc))
                else:
                    broker.result(offered["id"], owner["id"], ok=True, result=result, error=None)
                return reply.result(timeout=2)
            finally:
                if not reply.done():
                    calls.stop(broker)

    yield build
    calls.stop(broker)
