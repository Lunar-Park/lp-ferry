"""Talking to park's machines layer: the queue call ferry makes when it is there.

park's machines lane is not in this checkout, so the tests here stand a
``machines.job.submit`` op up and check the shape ferry calls it with. That shape is what
the machines lane has to keep.
"""

from __future__ import annotations

from pathlib import Path

import pytest

park = pytest.importorskip("park", reason="park is not installed")

from park.ops import REGISTRY, op  # noqa: E402
from park_plugins.ferry import jobs  # noqa: E402 - after the park importorskip above


@pytest.fixture
def submitted():
    calls = []
    # park registers the real queue op; the stand-in replaces it for the test and the real
    # one is put back afterwards.
    real = REGISTRY.pop("machines.job.submit", None)

    @op("machines.job.submit", summary="Stand-in for park's queue.", effect="read")
    def submit(ctx, plugin: str, name: str, params: dict | None = None, machine: str | None = None):
        calls.append({"plugin": plugin, "name": name, "params": params, "machine": machine})
        return {"id": f"job-{len(calls)}"}

    try:
        yield calls
    finally:
        REGISTRY.pop("machines.job.submit", None)
        if real is not None:
            REGISTRY["machines.job.submit"] = real


def test_offload_queues_the_copy_and_records_the_job(call, worker_plan, submitted):
    plan = worker_plan()
    transfer = call("ferry.offload", {"plan": plan["id"], "machine": "Ferry Worker"})
    assert transfer["queued"] is True
    assert transfer["job"] == "job-1"
    assert submitted == [
        {
            "plugin": "ferry",
            "name": "copy",
            "params": {"plan": plan["id"], "transfer": transfer["transfer"], "machine": "worker-a"},
            "machine": "worker-a",
        }
    ]
    row = call("ferry.transfer.get", {"id": transfer["transfer"]})
    assert row["job"] == "job-1"
    assert row["machine"] == "worker-a"
    assert row["status"] == "queued"


@pytest.mark.parametrize("filing_reply_lost", [False, True])
def test_owned_transfer_reconciles_a_retry_and_files_its_receipt(
    call,
    park,
    worker_plan,
    job_context,
    monkeypatch,
    filing_reply_lost,
):
    from park.node.client import NodeError

    project = call("project.create", {"slug": "ferry-test"})
    other = call("project.create", {"slug": "other"})
    plan = worker_plan(project=project["slug"])
    park.rebuild()
    assert call("ferry.plan.get", {"id": plan["id"]})["project"] == project["id"]
    assert call("ferry.ingest.list", {"project": other["id"]})["rows"] == []
    with pytest.raises(Exception, match="plan belongs to worker-a"):
        call("ferry.offload", {"plan": plan["id"], "machine": "worker-b"})
    args = {"plan": plan["id"]}
    transfer = call("ferry.offload", args, request_key="copy-intent")
    assert call("ferry.offload", args, request_key="copy-intent") == transfer
    assert call("ferry.offload", args, request_key="second-tap") == transfer
    assert call("machines.job.lease", {"machine": "worker-b"})["job"] is None
    claimed = call("machines.job.lease", {"machine": "worker-a"})["job"]
    context = job_context("copy", claimed["params"])
    context.job_id = claimed["id"]
    with pytest.raises(jobs.JobInputError, match="refusing copy"):
        jobs.copy(context)
    context.machine = "worker-a"

    if filing_reply_lost:
        original_call = context.call

        def fail_filing(name, args):
            if name == "ferry.receipt.file":
                raise NodeError("filing reply lost")
            return original_call(name, args)

        monkeypatch.setattr(context, "call", fail_filing)
    result = jobs.copy(context)
    assert result["status"] == "ok"
    assert jobs.copy(context)["receipt"] == result["receipt"]
    receipt = call("ferry.receipt.get", {"id": result["receipt"]})
    assert receipt["filing"]["project"] == project["id"]
    if filing_reply_lost:
        assert result["filing"]["error"] == "filing reply lost"
        assert len(receipt["filing"]["pending"]) == plan["files"]
    else:
        assert result["filing"] == {"attached": plan["files"], "pending": 0}
        assert receipt["filing"]["pending"] == []
    filed = call("ferry.receipt.file", {"id": receipt["id"]})
    assert filed["role"] == "source" and filed["pending"] == []
    assert call("ferry.receipt.file", {"id": receipt["id"]}) == filed
    exact = call("review.collection", {"project": project["slug"], "assets": filed["attached"]})
    assert {item["sha256"] for item in exact["assets"]} == set(filed["attached"])
    row = call("ferry.ingest.list", {"project": project["slug"]})["rows"][0]
    assert row["job"] == claimed["id"] and row["receipt"] == result["receipt"]


def test_an_unowned_plan_cannot_enter_the_shared_copy_queue(call, card, tmp_path):
    historical = call(
        "ferry.plan",
        {
            "source": str(card.path),
            "destination": str(tmp_path / "offload"),
            "probe": False,
        },
    )
    with pytest.raises(Exception, match="plan again on the worker"):
        call("ferry.offload", {"plan": historical["id"]})
    assert call("ferry.transfer.list", {"plan": historical["id"]}) == []


def test_a_replaced_destination_mount_does_not_receive_old_plan_bytes(
    call, worker_plan, job_context
):
    plan = worker_plan()
    dest = Path(plan["destination"])
    dest.rename(dest.with_name("prior-mount"))
    dest.mkdir()
    requested = call("ferry.offload", {"plan": plan["id"]})
    leased = call("machines.job.lease", {"machine": "worker-a"})["job"]
    ctx = job_context("copy", leased["params"])
    ctx.job_id, ctx.machine = leased["id"], "worker-a"
    result = jobs.copy(ctx)
    receipt = call("ferry.receipt.get", {"id": result["receipt"]})
    assert receipt["transfer"] == requested["transfer"]
    assert receipt["status"] == "failed" and receipt["files"] == []
    assert "mount changed since planning" in receipt["errors"][0]
    assert not list(dest.iterdir())


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
