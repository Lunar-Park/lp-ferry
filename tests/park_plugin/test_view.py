"""The Ingest view: an ES module that parses and exports mount(el, api)."""

from __future__ import annotations

import json
import re
import shutil
import subprocess
from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

VIEW = Path(__file__).resolve().parents[2] / "park_plugins" / "ferry" / "views" / "ingest.js"
NODE = shutil.which("node")


def test_the_view_file_is_there_and_names_mount():
    source = VIEW.read_text(encoding="utf-8")
    assert re.search(r"export\s+async\s+function\s+mount\s*\(\s*el\s*,\s*api\s*\)", source)
    assert "ferry.plan.list" in source
    assert "ferry.receipt.list" in source
    assert "ferry.transfer.list" in source
    # Comments and text in the view carry no em dashes.
    assert "\u2014" not in source


@pytest.mark.skipif(NODE is None, reason="node is not installed")
def test_the_view_parses_as_an_es_module(tmp_path):
    module = tmp_path / "ingest.mjs"
    module.write_text(VIEW.read_text(encoding="utf-8"), encoding="utf-8")
    done = subprocess.run(
        [NODE, "--check", str(module)], capture_output=True, text=True, check=False
    )
    assert done.returncode == 0, done.stderr


@pytest.mark.skipif(NODE is None, reason="node is not installed")
def test_the_view_loads_its_exports_and_mounts_against_a_stub_api(tmp_path):
    module = tmp_path / "ingest.mjs"
    module.write_text(VIEW.read_text(encoding="utf-8"), encoding="utf-8")
    script = tmp_path / "run.mjs"
    script.write_text(
        f"""
const stub = {{
  calls: [],
  subscribe(kinds, fn) {{ this.kinds = kinds; this.listener = fn; return () => {{}}; }},
  async call(op) {{
    this.calls.push(op);
    if (op === "ferry.source.list") return [{{ source: "/card", label: "CARD_A",
      plans: 1, files: 2, bytes: 2048, last_plan: "2026-01-01T00:00:00Z", mounted: true }}];
    if (op === "ferry.plan.list") return [];
    if (op === "ferry.transfer.list") return [];
    if (op === "ferry.receipt.list") return [];
    return {{}};
  }},
}};
const dom = [];
const fakeElement = {{
  className: "", textContent: "", style: {{}},
  children: [],
  classList: {{ add() {{}}, remove() {{}} }},
  append(...kids) {{ this.children.push(...kids); }},
  addEventListener() {{}},
  setAttribute() {{}},
}};
globalThis.document = {{
  createElement: () => Object.assign({{}}, fakeElement, {{ children: [], append() {{}} }}),
  createTextNode: (text) => ({{ text }}),
  getElementById: () => null,
  head: {{ append() {{}} }},
}};
globalThis.Node = function Node() {{}};
const module = await import({json.dumps(str(module))});
if (typeof module.mount !== "function") {{
  console.error("mount is not exported");
  process.exit(1);
}}
const mounted = await module.mount(fakeElement, stub);
const order = stub.calls.join(",");
if (!order.includes("ferry.source.list") || !order.includes("ferry.receipt.list")) {{
  console.error("mount did not read the ferry ops: " + order);
  process.exit(1);
}}
if (mounted && typeof mounted.unmount === "function") mounted.unmount();
console.log("ok");
"""
    )
    done = subprocess.run([NODE, str(script)], capture_output=True, text=True, check=False)
    assert done.returncode == 0, done.stderr
    assert done.stdout.strip() == "ok"
