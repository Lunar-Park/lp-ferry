"""A card offload end to end: plan, queue, copy, register, receipt, verify."""

from __future__ import annotations

import errno
import os
from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

from park_plugins.ferry import jobs
from park_plugins.ferry.copy import CopyError, copy_verified, hash_file

pytestmark = pytest.mark.usefixtures("no_queue")


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


def test_a_copy_that_does_not_match_is_not_registered(
    call, card, tmp_path, job_context, monkeypatch
):
    """The job hashes the file that landed and refuses to call a bad copy an asset."""

    def lying_copy(source, dest, *, check_cancelled=None):
        path = Path(dest)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(b"corrupted on the way")
        # Report the source's hash, as a copy that did not verify would.
        return hash_file(source), path.stat().st_size

    monkeypatch.setattr(jobs, "copy_verified", lying_copy)
    destination = tmp_path / "offload"
    plan = _plan(call, card, destination)
    transfer = _offload(call, plan)
    result = jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))

    assert result["status"] == "failed"
    assert result["files"] == 0
    assert result["failed"] == len(card.names)
    row = call("ferry.transfer.get", {"id": transfer["transfer"]})
    assert row["status"] == "failed"
    assert row["failed"] == len(card.names)
    assert any("registered" in message for message in row["errors"])
    receipt = call("ferry.receipt.list", {"transfer": transfer["transfer"]})[0]
    assert receipt["status"] == "failed"
    full = call("ferry.receipt.get", {"id": receipt["id"]})
    assert full["files"] == []
    # The bytes that landed are still recorded, under the hash they actually have.
    landed = hash_file(destination / card.names[0])
    assert call("asset.get", {"sha256": landed})["sha256"] == landed


def test_a_source_that_is_not_mounted_fails_the_transfer(call, card, tmp_path, job_context):
    destination = tmp_path / "offload"
    plan = _plan(call, card, destination)
    transfer = _offload(call, plan)
    for path in sorted(card.path.rglob("*"), reverse=True):
        if path.is_file():
            path.unlink()
    result = jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))
    assert result["status"] == "failed"
    assert result["failed"] == len(card.names)
    claims = call("ferry.transfer.get", {"id": transfer["transfer"]})["errors"]
    assert any("not mounted" in message for message in claims)


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


@pytest.mark.parametrize("present_at_plan", [False, True])
def test_changed_destination_is_never_overwritten_or_counted_as_verified(
    call,
    card,
    tmp_path,
    job_context,
    present_at_plan,
):
    dest = tmp_path / "offload"
    file = dest / "notes.txt"
    if present_at_plan:
        dest.mkdir()
        file.write_bytes(b"X" * (card.path / "notes.txt").stat().st_size)
    plan = _plan(call, card, dest)
    if not present_at_plan:
        dest.mkdir()
        file.write_bytes(b"appeared while queueing")
    original = file.read_bytes()
    transfer = _offload(call, plan)
    result = jobs.copy(job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]}))
    receipt = call("ferry.receipt.get", {"id": result["receipt"]})
    assert file.read_bytes() == original
    assert result["status"] == "partial"
    assert all(item["rel"] != "notes.txt" for item in receipt["files"])
    assert any("already exists with different contents" in error for error in receipt["errors"])


def test_a_canceled_copy_keeps_its_verified_partial_receipt(
    call, card, tmp_path, job_context, monkeypatch
):
    from park.machines.contract import JobCancelled

    plan = _plan(call, card, tmp_path / "offload")
    transfer = _offload(call, plan)
    context = job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]})
    monkeypatch.setattr(context, "progress", lambda *args: context.cancel())
    with pytest.raises(JobCancelled):
        jobs.copy(context)
    summary = call("ferry.receipt.list", {"transfer": transfer["transfer"]})[0]
    receipt = call("ferry.receipt.get", {"id": summary["id"]})
    assert receipt["detail"]["canceled"] is True
    assert receipt["status"] == "partial" and len(receipt["files"]) == 1


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
    assert receipt["status"] == "partial" and len(receipt["files"]) == 1
    assert receipt["files"][0]["rel"] == card.names[0]
    assert f"Park did not confirm {operation}" in receipt["errors"][0]


def test_publication_never_replaces_a_file_that_appeared_after_staging(tmp_path, monkeypatch):
    source, dest = tmp_path / "source", tmp_path / "dest"
    source.write_bytes(b"source bytes")
    link = os.link

    def competing_write(staged, target):
        assert Path(staged).read_bytes() == b"source bytes"
        Path(target).write_bytes(b"other writer")
        link(staged, target)

    monkeypatch.setattr(os, "link", competing_write)
    with pytest.raises(FileExistsError):
        copy_verified(source, dest)
    assert dest.read_bytes() == b"other writer"
    assert sorted(path.name for path in tmp_path.iterdir()) == ["dest", "source"]


def test_destination_without_safe_publication_fails_closed(tmp_path, monkeypatch):
    source, dest = tmp_path / "source", tmp_path / "dest"
    source.write_bytes(b"source bytes")

    def unsupported(*args):
        raise OSError(errno.ENOTSUP, "no hard links")

    monkeypatch.setattr(os, "link", unsupported)
    with pytest.raises(CopyError, match="does not support safe no-replace publication"):
        copy_verified(source, dest)
    assert list(tmp_path.iterdir()) == [source]


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
