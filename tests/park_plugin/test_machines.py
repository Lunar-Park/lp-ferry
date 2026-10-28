"""Talking to park's machines layer: the queue call ferry makes when it is there.

park's machines lane is not in this checkout, so the tests here stand a
``machines.job.submit`` op up and check the shape ferry calls it with. That shape is what
the machines lane has to keep.
"""

from __future__ import annotations

import pytest

park = pytest.importorskip("park", reason="park is not installed")

from park.ops import REGISTRY, op  # noqa: E402


@pytest.fixture
def submitted():
    calls = []

    @op("machines.job.submit", summary="Stand-in for park's queue.", effect="read")
    def submit(ctx, plugin: str, name: str, params: dict | None = None, machine: str | None = None):
        calls.append({"plugin": plugin, "name": name, "params": params, "machine": machine})
        return {"id": f"job-{len(calls)}"}

    try:
        yield calls
    finally:
        REGISTRY.pop("machines.job.submit", None)


def test_offload_queues_the_copy_and_records_the_job(call, card, tmp_path, submitted):
    plan = call(
        "ferry.plan",
        {"source": str(card.path), "destination": str(tmp_path / "offload"), "probe": False},
    )
    transfer = call("ferry.offload", {"plan": plan["id"], "machine": "node-a"})
    assert transfer["queued"] is True
    assert transfer["job"] == "job-1"
    assert submitted == [
        {
            "plugin": "ferry",
            "name": "copy",
            "params": {"plan": plan["id"], "transfer": transfer["transfer"]},
            "machine": "node-a",
        }
    ]
    row = call("ferry.transfer.get", {"id": transfer["transfer"]})
    assert row["job"] == "job-1"
    assert row["machine"] == "node-a"
    assert row["status"] == "queued"


def test_proxies_queues_one_encode_job(call, tmp_path, submitted):
    media = tmp_path / "MEDIA"
    media.mkdir()
    (media / "clip.mov").write_bytes(b"clip")
    request = call(
        "ferry.proxies", {"source": str(media), "destination": str(tmp_path / "proxies")}
    )
    assert request["queued"] is True
    assert request["job"] == "job-1"
    assert submitted[0]["name"] == "proxy"
    assert submitted[0]["params"]["items"] == request["items"]


def test_a_resolve_project_is_queued_and_a_machine_picks_the_node(call, tmp_path, submitted):
    media = tmp_path / "MEDIA"
    media.mkdir()
    (media / "clip.mov").write_bytes(b"clip")
    queued = call(
        "ferry.resolve_project",
        {"source": str(media), "destination": str(tmp_path / "project"), "machine": "node-a"},
    )
    assert queued["queued"] is True
    assert queued["job"] == "job-1"
    assert submitted[0]["name"] == "resolve"
    assert submitted[0]["machine"] == "node-a"
    assert submitted[0]["params"]["manifest"] == queued["manifest"]
