"""Ferry against Park's plugin contract: the same check Park's deploy runs on what it ships."""

from __future__ import annotations

import json
import shutil
import subprocess
from pathlib import Path

import pytest

park = pytest.importorskip("park", reason="park is not installed")
contract = pytest.importorskip("park.plugins.contract", reason="park plugin contract is not installed")

REPO = Path(__file__).resolve().parents[2]
PACKAGE = REPO / "park_plugins" / "ferry"
PARK_KIT = Path(park.__file__).resolve().parents[1] / "tests/app/workspaces/view_dom.mjs"
NODE = shutil.which("node")

needs_node = pytest.mark.skipif(NODE is None, reason="node is not on PATH")
needs_kit = pytest.mark.skipif(not PARK_KIT.is_file(), reason="park is installed without its test kit")


def test_ferry_meets_every_rule_of_the_contract():
    report = contract.check_plugin(REPO)
    assert report.id == "ferry"
    assert {r.rule: r.problems for r in report.results if not r.ok} == {}


def test_ingest_screen_keeps_nothing_about_a_mount_at_module_level():
    """Served from the plugin: it imports nothing by path and builds itself from api.kit per mount."""
    source = (PACKAGE / "views" / "ingest.js").read_text(encoding="utf-8")
    assert "\nimport " not in "\n" + source and "import(" not in source and "import.meta" not in source
    assert "api?.kit" in source and "function screen(kit)" in source
    assert [line for line in source.splitlines() if line.startswith(("let ", "var "))] == []


@needs_node
@needs_kit
def test_ingest_screen_draws_and_acts_with_parks_shared_parts():
    done = subprocess.run(
        [
            NODE,
            str(Path(__file__).with_name("ingest_view_scenarios.mjs")),
            str(PACKAGE / "views/ingest.js"),
            str(PARK_KIT),
        ],
        capture_output=True,
        text=True,
        timeout=120,
        check=False,
    )
    assert done.returncode == 0, done.stdout + done.stderr
    assert done.stdout.strip().endswith("ok")


@needs_node
@needs_kit
def test_ingest_screen_refuses_a_park_without_api_kit():
    script = f"""
    const dom = await import({PARK_KIT.as_uri()!r});
    dom.install();
    const kit = await dom.kit();
    const view = await import({(PACKAGE / 'views/ingest.js').as_uri()!r});
    const said = [];
    for (const api of [undefined, {{}}, {{ kit: {{ dom: kit.dom }} }}]) {{
      try {{ view.mount(new dom.Element("div"), api); said.push("mounted"); }}
      catch (error) {{ said.push(error.message); }}
    }}
    console.log(JSON.stringify({{ said }}));
    """
    done = subprocess.run(
        [NODE, "--input-type=module", "-e", script],
        capture_output=True,
        text=True,
        timeout=120,
        check=False,
    )
    assert done.returncode == 0, done.stderr
    seen = json.loads(done.stdout.strip().splitlines()[-1])
    assert len(seen["said"]) == 3 and all("api.kit" in line for line in seen["said"])
