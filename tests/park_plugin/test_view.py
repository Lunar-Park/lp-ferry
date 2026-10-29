"""The Ingest view: an ES module exporting mount(el, api), drawn from one read."""

from __future__ import annotations

import re
import shutil
import subprocess
from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

HERE = Path(__file__).resolve().parent
VIEW = HERE.parents[1] / "park_plugins" / "ferry" / "views" / "ingest.js"
NODE = shutil.which("node")


def test_the_view_file_is_there_and_names_mount():
    source = VIEW.read_text(encoding="utf-8")
    # A sync mount: the shell calls what mount returns to take the view down, and a promise
    # cannot be called.
    assert re.search(r"export\s+function\s+mount\s*\(\s*el\s*,\s*api", source)
    assert "ferry.ingest.list" in source
    assert "ferry.plan.get" in source
    # Comments and text in the view carry no em dashes, and it imports nothing from the app.
    assert "—" not in source
    assert "/app/" not in source


def test_the_view_reads_with_the_ops_the_plugin_has(call):
    names = {spec["name"] for spec in call("op.list", {"plugin": "ferry"})}
    source = VIEW.read_text(encoding="utf-8")
    for op in sorted(set(re.findall(r'api\.call\("(ferry\.[a-z_.]+)"', source))):
        assert op in names, f"the view calls {op}, which the plugin does not have"


@pytest.mark.skipif(NODE is None, reason="node is not installed")
def test_the_view_parses_as_an_es_module(tmp_path):
    module = tmp_path / "ingest.mjs"
    module.write_text(VIEW.read_text(encoding="utf-8"), encoding="utf-8")
    done = subprocess.run(
        [NODE, "--check", str(module)], capture_output=True, text=True, check=False
    )
    assert done.returncode == 0, done.stderr


@pytest.mark.skipif(NODE is None, reason="node is not installed")
def test_the_view_draws_each_state_against_a_stub_api():
    done = subprocess.run(
        [NODE, str(HERE / "ingest_view_scenarios.mjs"), str(VIEW)],
        capture_output=True, text=True, check=False, timeout=60,
    )
    assert done.returncode == 0, done.stdout + done.stderr
    assert done.stdout.strip().endswith("ok")
