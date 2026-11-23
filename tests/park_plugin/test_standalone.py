"""What the plugin promises without Park installed.

Every other module here needs Park (and builds the ferry binary), so they skip on a
machine without it. These checks run anywhere with Python 3.11+: the manifest Park will
load, the modules that must import without Park, the adapter's mapping of Park's config
onto the engine, and the engine client's wire protocol against a stand-in daemon.
"""

from __future__ import annotations

import ast
import importlib
import json
import os
import socket
import subprocess
import sys
import tempfile
import threading
import tomllib
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = ROOT / "park_plugins" / "ferry"


def manifest() -> dict:
    with open(PACKAGE / "plugin.toml", "rb") as fh:
        return tomllib.load(fh)


def test_the_manifest_declares_what_the_plugin_builds():
    data = manifest()
    assert data["id"] == "ferry"
    assert set(data["modules"]) == {"park_plugins.ferry.records", "park_plugins.ferry.ops"}
    assert [call["name"] for call in data["calls"]] == ["plan", "engine"]
    assert [job["name"] for job in data["jobs"]] == ["copy", "send", "proxy", "resolve"]
    jobs = {job["name"]: job for job in data["jobs"]}
    assert jobs["send"]["egress"] == "lan"
    assert jobs["copy"]["egress"] == "none"
    assert jobs["proxy"]["needs"] == ["ffmpeg"]
    for entry in data["calls"] + data["jobs"]:
        assert set(entry) <= {"name", "handler", "needs", "timeout", "egress"}, entry


def test_every_handler_resolves_without_park(monkeypatch):
    monkeypatch.setitem(sys.modules, "park", None)
    data = manifest()
    for entry in data["calls"] + data["jobs"]:
        module, _, name = entry["handler"].partition(":")
        assert callable(getattr(importlib.import_module(module), name)), entry["handler"]


def test_the_pure_modules_import_without_park():
    code = (
        "import sys; sys.modules['park'] = None;"
        "import park_plugins.ferry, park_plugins.ferry.config, park_plugins.ferry.probe,"
        "park_plugins.ferry.layout, park_plugins.ferry.engine, park_plugins.ferry.jobs;"
        "print('ok')"
    )
    done = subprocess.run(
        [sys.executable, "-c", code], cwd=str(ROOT), capture_output=True, text=True, check=False
    )
    assert done.returncode == 0, done.stderr
    assert done.stdout.strip() == "ok"


def test_the_plugin_parses_on_python_3_11():
    for path in sorted(PACKAGE.rglob("*.py")):
        ast.parse(path.read_text(encoding="utf-8"), filename=str(path), feature_version=(3, 11))


def test_the_shipped_config_example_loads_with_the_defaults():
    from park_plugins.ferry import config

    example = config.load(PACKAGE / "config.toml.example")
    assert example.template == config.DEFAULT_TEMPLATE
    assert example.on_conflict == "skip"


def test_parks_config_maps_onto_the_engine_and_never_onto_an_overwrite():
    from park_plugins.ferry import engine

    assert engine.preset_for(engine.DEFAULT_TEMPLATE) == "mirror-source"
    with pytest.raises(engine.PlanError, match="Ferry presets"):
        engine.preset_for("{root}/{date}/{filename}{ext}")
    assert engine.policy_for("skip") == "skip_identical"
    assert engine.policy_for("rename") == "keep_both"
    with pytest.raises(engine.PlanError, match="never overwrites"):
        engine.policy_for("overwrite")


def test_a_missing_daemon_is_named_with_how_to_start_it(monkeypatch, tmp_path):
    from park_plugins.ferry import engine

    monkeypatch.setenv("FERRY_SOCKET", str(tmp_path / "nobody.sock"))
    with pytest.raises(engine.EngineUnavailableError, match="ferry daemon"):
        engine.call("daemon.status")


class FakeDaemon:
    """A stand-in ``ferry daemon``: newline-delimited JSON-RPC on a real Unix socket."""

    def __init__(self, answers: dict):
        self.dir = tempfile.mkdtemp(prefix="fy", dir="/tmp")  # socket paths are length-limited
        self.path = os.path.join(self.dir, "ferry.sock")
        self.answers = answers
        self.seen: list[dict] = []
        self.server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.server.bind(self.path)
        self.server.listen()
        threading.Thread(target=self.serve, daemon=True).start()

    def serve(self) -> None:
        while True:
            try:
                conn, _ = self.server.accept()
            except OSError:
                return
            with conn:
                buf = b""
                while True:
                    chunk = conn.recv(65536)
                    if not chunk:
                        break
                    buf += chunk
                    while b"\n" in buf:
                        line, buf = buf.split(b"\n", 1)
                        req = json.loads(line)
                        self.seen.append(req)
                        answer = self.answers.get(req["method"])
                        reply = {"jsonrpc": "2.0", "id": req["id"]}
                        if isinstance(answer, Exception):
                            reply["error"] = {"code": -32001, "message": str(answer)}
                        else:
                            # An event pushed ahead of the reply must not be mistaken for it.
                            event = {
                                "jsonrpc": "2.0",
                                "method": "event",
                                "params": {"topic": "x", "data": {}},
                            }
                            conn.sendall(json.dumps(event).encode() + b"\n")
                            reply["result"] = answer
                        conn.sendall(json.dumps(reply).encode() + b"\n")

    def close(self) -> None:
        self.server.close()
        os.unlink(self.path)
        os.rmdir(self.dir)


def test_the_engine_client_speaks_json_rpc_and_keeps_error_codes(monkeypatch):
    from park_plugins.ferry import engine

    fake = FakeDaemon(
        {"daemon.status": {"protocol": 1}, "plan.approve": RuntimeError("stale approval")}
    )
    monkeypatch.setenv("FERRY_SOCKET", fake.path)
    try:
        assert engine.call("daemon.status") == {"protocol": 1}
        with pytest.raises(engine.EngineError) as caught:
            engine.call("plan.approve", {"plan_id": "p", "fingerprint": "f"})
        assert caught.value.code == engine.STALE
        assert "stale approval" in str(caught.value)
        assert [req["method"] for req in fake.seen] == ["daemon.status", "plan.approve"]
        assert all(req["jsonrpc"] == "2.0" for req in fake.seen)
        assert fake.seen[1]["params"] == {"plan_id": "p", "fingerprint": "f"}
    finally:
        fake.close()


def test_plans_come_back_in_parks_shape(monkeypatch):
    from park_plugins.ferry import engine

    summary = {
        "plan_id": "pln_1",
        "fingerprint": "f" * 64,
        "blocking_count": 0,
        "findings": [],
        "destinations": [{"root_path": "/real/dest"}],
    }
    entries = {
        "entries": [
            {
                "source_rel": "CLIP/A001.mov",
                "source_path": "/card/CLIP/A001.mov",
                "dest_rel_path": "CLIP/A001.mov",
                "size": 10,
                "mtime_ns": 2_000_000_000,
                "action": "copy",
                "kind": "file",
                "metadata": "",
            },
            {
                "source_rel": "notes.txt",
                "source_path": "/card/notes.txt",
                "dest_rel_path": "notes.txt",
                "size": 3,
                "mtime_ns": 0,
                "action": "skip_identical",
                "kind": "file",
                "metadata": "",
            },
            {
                "source_rel": ".DS_Store",
                "source_path": "/card/.DS_Store",
                "dest_rel_path": "",
                "size": 1,
                "mtime_ns": 0,
                "action": "exclude",
                "kind": "file",
                "metadata": "",
            },
        ]
    }
    fake = FakeDaemon({"plan.create": summary, "plan.entries": entries})
    monkeypatch.setenv("FERRY_SOCKET", fake.path)
    try:
        plan = engine.build(
            "/card", "/dest", template=engine.DEFAULT_TEMPLATE, on_conflict="skip", label="A"
        )
    finally:
        fake.close()
    assert plan["id"] == "pln_1" and plan["fingerprint"] == "f" * 64
    assert [item["rel"] for item in plan["items"]] == ["CLIP/A001.mov", "notes.txt"]
    assert plan["items"][0]["dest"] == "/dest/CLIP/A001.mov"
    assert plan["items"][0]["mtime"] == 2.0
    assert (plan["copies"], plan["skips"], plan["bytes"]) == (1, 1, 13)
    assert plan["by_kind"] == {"video": 1, "document": 1}
    request = fake.seen[0]["params"]
    assert (
        request["preset_id"] == "mirror-source" and request["conflict_policy"] == "skip_identical"
    )
