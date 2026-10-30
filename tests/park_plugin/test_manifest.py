"""The manifest and the package: what ferry declares and what park loads."""

from __future__ import annotations

import ast
import subprocess
import sys
from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

from park.plugins import loader
from park.plugins.manifest import Manifest

PACKAGE = Path(__file__).resolve().parents[2] / "park_plugins" / "ferry"
ROOT = PACKAGE.parents[1]


def test_manifest_loads_and_declares_what_the_plugin_builds():
    manifest = Manifest.load(PACKAGE / "plugin.toml")
    assert manifest.id == "ferry"
    assert manifest.package == "park_plugins.ferry"
    assert manifest.satisfies_park("0.1.0")
    assert [job.name for job in manifest.jobs] == ["copy", "proxy", "resolve"]
    assert not manifest.views
    assert "ingest" in manifest.summary.lower()


def test_every_job_handler_resolves():
    from park.machines.contract import resolve_handler

    for job in Manifest.load(PACKAGE / "plugin.toml").jobs:
        handler = resolve_handler(job.handler)
        assert callable(handler)
        assert handler.__module__.startswith("park_plugins.ferry.")


def test_the_loader_loads_ferry_and_every_op_has_its_prefix():
    loaded = loader.load_path(ROOT, strict=True)
    assert [plugin.manifest.id for plugin in loaded] == ["ferry"]
    plugin = loaded[0]
    assert plugin.ops
    for name in plugin.ops:
        assert name.startswith("ferry.")


def test_the_ops_park_asks_for_exist(call):
    names = {spec["name"] for spec in call("op.list", {"plugin": "ferry"})}
    for name in (
        "ferry.plan",
        "ferry.offload",
        "ferry.verify",
        "ferry.proxies",
        "ferry.resolve_project",
        "ferry.receipt.get",
        "ferry.receipt.list",
    ):
        assert name in names


def test_the_package_imports_without_park():
    """CI runs 3.11 with no park; the package itself must not need it."""
    code = (
        "import sys; sys.modules['park'] = None;"
        "import park_plugins.ferry, park_plugins.ferry.config, park_plugins.ferry.probe,"
        "park_plugins.ferry.layout, park_plugins.ferry.copy, park_plugins.ferry.jobs;"
        "print('ok')"
    )
    done = subprocess.run(
        [sys.executable, "-c", code], cwd=str(ROOT), capture_output=True, text=True, check=False
    )
    assert done.returncode == 0, done.stderr
    assert done.stdout.strip() == "ok"


def test_the_plugin_parses_on_python_3_11():
    """The repo's CI runs 3.11, so nothing in the plugin may use newer syntax."""
    for path in sorted(PACKAGE.rglob("*.py")):
        ast.parse(path.read_text(encoding="utf-8"), filename=str(path), feature_version=(3, 11))
