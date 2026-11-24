"""A card offload end to end: plan, queue, copy (by the Go engine), register, receipt, verify.

The byte-level guarantees — job-owned temps, physical readback, no-replace publication,
symlink refusal, crash windows — are the engine's and are proven in its Go suite
(internal/engine, internal/storage). These tests prove the plugin records exactly what
the engine did, and that Park's own cross-check (registration) holds.
"""

from __future__ import annotations

import hashlib
from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

from park_plugins.ferry import jobs

pytestmark = pytest.mark.usefixtures("engine", "no_queue")


def hash_file(path) -> str:
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def _plan(call, card, destination):
    return call(
        "ferry.plan",
        {"source": str(card.path), "destination": str(destination), "probe": False},
    )


def _offload(call, plan):
    return call("ferry.offload", {"plan": plan["id"]})


def test_offload_queues_a_transfer_and_says_whether_a_queue_took_it(call, card, tmp_path, no_queue):
    plan = _plan(call, card, tmp_path / "offload")
    transfer = _offload(call, plan)
    assert transfer["plan"] == plan["id"]
    assert transfer["files"] == plan["copies"]
    assert transfer["bytes"] == plan["bytes"]
    # With no machines layer, the record is written and nothing is queued.
    assert transfer["queued"] is False
    assert transfer["job"] is None
    row = call("ferry.transfer.get", {"id": transfer["transfer"]})
    assert row["status"] == "queued"
    assert row["total"] == plan["copies"]


def test_the_copy_job_lands_every_file_as_an_asset(call, card, tmp_path, job_context):
    destination = tmp_path / "offload"
    plan = _plan(call, card, destination)
    transfer = _offload(call, plan)
    context = job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]})
    result = jobs.copy(context)

    assert result["status"] == "ok"
    assert result["files"] == len(card.names)
    assert result["failed"] == 0
    assert result["bytes"] == card.total_bytes

    row = call("ferry.transfer.get", {"id": transfer["transfer"]})
    assert row["status"] == "copied"
    assert row["verified"] == len(card.names)
    assert row["failed"] == 0
    assert row["fraction"] == 1.0

    # One Asset per file, by the hash of its bytes, at the path it landed on.
    for rel in card.names:
        landed = destination / rel
        digest = hash_file(landed)
        asset = call("asset.get", {"sha256": digest})
        assert asset["size"] == landed.stat().st_size
        assert str(landed) in [path["path"] for path in asset["paths"]]
        assert (card.path / rel).read_bytes() == landed.read_bytes()


def test_every_transfer_leaves_a_receipt(call, card, tmp_path, job_context):
    plan = _plan(call, card, tmp_path / "offload")
    transfer = _offload(call, plan)
    jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))

    receipts = call("ferry.receipt.list", {"transfer": transfer["transfer"]})
    assert len(receipts) == 1
    summary = receipts[0]
    assert summary["kind"] == "copy"
    assert summary["status"] == "ok"
    assert summary["files"] == len(card.names)
    assert len(summary["digest"]) == 64

    receipt = call("ferry.receipt.get", {"id": summary["id"]})
    assert receipt["plan"] == plan["id"]
    assert receipt["machine"] == "test-node"
    assert [row["rel"] for row in receipt["files"]] == sorted(card.names)
    for row in receipt["files"]:
        assert row["sha256"] == hash_file(row["dest"])
        assert row["verified"] is True


def test_verifying_a_good_transfer_is_clean(call, card, tmp_path, job_context):
    destination = tmp_path / "offload"
    plan = _plan(call, card, destination)
    transfer = _offload(call, plan)
    jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))

    report = call("ferry.verify", {"transfer": transfer["transfer"], "folder": str(destination)})
    assert report["clean"] is True
    assert report["checked"] == len(card.names)
    assert report["missing"] == []
    assert report["modified"] == []
    assert report["added"] == []


def test_a_corrupted_copy_is_caught_by_verification(call, card, tmp_path, job_context):
    destination = tmp_path / "offload"
    plan = _plan(call, card, destination)
    transfer = _offload(call, plan)
    jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))

    victim = destination / "DCIM" / "100MEDIA" / "A001C001.mov"
    victim.write_bytes(b"one byte flipped")
    report = call("ferry.verify", {"transfer": transfer["transfer"], "folder": str(destination)})
    assert report["clean"] is False
    assert report["modified"] == ["DCIM/100MEDIA/A001C001.mov"]


def test_a_file_that_went_missing_is_reported(call, card, tmp_path, job_context):
    destination = tmp_path / "offload"
    plan = _plan(call, card, destination)
    transfer = _offload(call, plan)
    jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))

    (destination / "notes.txt").unlink()
    report = call("ferry.verify", {"transfer": transfer["transfer"], "folder": str(destination)})
    assert report["clean"] is False
    assert report["missing"] == ["notes.txt"]


def test_a_file_the_receipt_never_saw_is_added(call, card, tmp_path, job_context):
    destination = tmp_path / "offload"
    plan = _plan(call, card, destination)
    transfer = _offload(call, plan)
    jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))

    (destination / "stray.txt").write_text("someone put this here")
    report = call("ferry.verify", {"transfer": transfer["transfer"], "folder": str(destination)})
    assert report["clean"] is False
    assert report["added"] == ["stray.txt"]


def test_a_source_that_is_not_mounted_fails_the_transfer(call, card, tmp_path, job_context):
    destination = tmp_path / "offload"
    plan = _plan(call, card, destination)
    transfer = _offload(call, plan)
    for path in sorted(card.path.rglob("*"), reverse=True):
        if path.is_file():
            path.unlink()
    result = jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))
    # The engine re-checks the sources before it starts and refuses the stale plan whole.
    assert result["status"] == "failed" and result["files"] == 0
    claims = call("ferry.transfer.get", {"id": transfer["transfer"]})["errors"]
    assert any("changed since planning" in message for message in claims), claims
    assert not any(destination.rglob("*.*"))


def test_a_skipped_file_is_registered_where_it_already_is(call, card, tmp_path, job_context):
    destination = tmp_path / "offload"
    first = _plan(call, card, destination)
    jobs.copy(
        job_context("copy", {"plan": first["id"], "transfer": _offload(call, first)["transfer"]})
    )
    second = _plan(call, card, destination)
    assert second["copies"] == 0
    transfer = _offload(call, second)
    result = jobs.copy(
        job_context("copy", {"plan": second["id"], "transfer": transfer["transfer"]})
    )
    assert result["status"] == "ok"
    receipt = call("ferry.receipt.list", {"transfer": transfer["transfer"]})[0]
    full = call("ferry.receipt.get", {"id": receipt["id"]})
    assert all(row["skipped"] is True for row in full["files"])
    assert all(row["verified"] is True for row in full["files"])
    row = call("ferry.transfer.get", {"id": transfer["transfer"]})
    assert row["skipped"] == len(card.names)


@pytest.mark.parametrize("operation", ["registration", "transfer progress"])
def test_a_node_failure_keeps_earlier_verified_files_in_the_receipt(
    call,
    card,
    tmp_path,
    job_context,
    monkeypatch,
    operation,
):
    from park.node.client import NodeError

    plan = _plan(call, card, tmp_path / "offload")
    transfer = _offload(call, plan)
    ctx = job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]})
    register, ask = ctx.register_output, ctx.call

    def register_output(*args, **kwargs):
        if ctx.outputs:
            raise NodeError("reply was lost")
        return register(*args, **kwargs)

    def node_call(name, args):
        if name == "ferry.transfer.progress":
            raise NodeError("reply was lost")
        return ask(name, args)

    if operation == "registration":
        monkeypatch.setattr(ctx, "register_output", register_output)
    else:
        monkeypatch.setattr(ctx, "call", node_call)
    result = jobs.copy(ctx)
    receipt = call("ferry.receipt.get", {"id": result["receipt"]})
    assert receipt["status"] == "partial"
    assert f"Park did not confirm {operation}" in receipt["errors"][0]
    if operation == "registration":
        # Only what Park confirmed is claimed as verified.
        assert [row["rel"] for row in receipt["files"]] == [card.names[0]]
    else:
        # The engine finished the copy; the receipt says Park missed the progress.
        assert len(receipt["files"]) == len(card.names)


def test_a_transfer_cannot_be_closed_twice(call, card, tmp_path, job_context):
    plan = _plan(call, card, tmp_path / "offload")
    transfer = _offload(call, plan)
    jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))
    with pytest.raises(Exception) as caught:
        call(
            "ferry.receipt.record",
            {"kind": "copy", "status": "ok", "transfer": transfer["transfer"], "files": []},
        )
    assert "already ended" in str(caught.value)


def test_the_transfer_ops_refuse_an_unknown_id(call):
    for name in ("ferry.transfer.get", "ferry.transfer.start", "ferry.verify"):
        args = {"id": "nope"} if name == "ferry.transfer.get" else {"transfer": "nope"}
        with pytest.raises(Exception) as caught:
            call(name, args)
        assert "no such transfer" in str(caught.value)


def test_receipts_carry_the_engines_proof(call, card, tmp_path, job_context):
    plan = _plan(call, card, tmp_path / "offload")
    transfer = _offload(call, plan)
    result = jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))
    receipt = call("ferry.receipt.get", {"id": result["receipt"]})
    engine = receipt["detail"]["engine"]
    assert engine["plan"] == plan["id"] and engine["status"] == "completed"
    body = receipt["detail"]["engine_receipt"]
    assert body["schema"] == "ferry-receipt-v2" and body["receipt_digest"] == engine["digest"]
    assert body["targets"][0]["readback_mode"] in ("f_nocache", "fadvise_dontneed")


def test_park_registering_a_different_hash_is_not_counted_as_verified(
    call, card, tmp_path, job_context, monkeypatch
):
    plan = _plan(call, card, tmp_path / "offload")
    transfer = _offload(call, plan)
    ctx = job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]})
    monkeypatch.setattr(ctx, "register_output", lambda *args, **kwargs: "0" * 64)
    result = jobs.copy(ctx)
    assert result["status"] == "failed" and result["files"] == 0
    row = call("ferry.transfer.get", {"id": transfer["transfer"]})
    assert row["status"] == "failed"
    assert all("Park registered" in message for message in row["errors"])


def test_a_different_file_already_there_is_kept_and_the_new_one_lands_beside_it(
    call, card, tmp_path, job_context
):
    dest = tmp_path / "offload"
    dest.mkdir()
    (dest / "notes.txt").write_bytes(b"X" * (card.path / "notes.txt").stat().st_size)
    plan = _plan(call, card, dest)
    transfer = _offload(call, plan)
    result = jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))
    assert result["status"] == "ok"
    assert (dest / "notes.txt").read_bytes().startswith(b"XXXX")
    assert (dest / "notes (2).txt").read_bytes() == (card.path / "notes.txt").read_bytes()


def test_a_file_that_appears_after_planning_stops_the_plan_for_review(
    call, card, tmp_path, job_context
):
    dest = tmp_path / "offload"
    plan = _plan(call, card, dest)
    (dest / "notes.txt").write_bytes(b"appeared while queueing")
    transfer = _offload(call, plan)
    result = jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))
    assert (dest / "notes.txt").read_bytes() == b"appeared while queueing"
    assert result["status"] == "failed" and result["files"] == 0
    errors = call("ferry.transfer.get", {"id": transfer["transfer"]})["errors"]
    assert any("no longer free" in message for message in errors), errors


def test_a_canceled_copy_records_what_it_kept(call, card, tmp_path, job_context):
    from park.machines.contract import JobCancelled

    big = card.path / "BIG.mov"
    big.write_bytes(b"\x5a" * (192 << 20))
    plan = _plan(call, card, tmp_path / "offload")
    transfer = _offload(call, plan)
    context = job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]})
    context.cancel()
    with pytest.raises(JobCancelled):
        jobs.copy(context)
    summary = call("ferry.receipt.list", {"transfer": transfer["transfer"]})[0]
    receipt = call("ferry.receipt.get", {"id": summary["id"]})
    assert receipt["detail"]["canceled"] is True
    assert receipt["detail"]["engine"]["status"] == "canceled"
    assert not (tmp_path / "offload" / "BIG.mov").exists()
    for row in receipt["files"]:
        assert hash_file(row["dest"]) == row["sha256"]
