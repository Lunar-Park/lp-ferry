"""The Ingest view's one read: cards and offload rows, joined, newest first."""

from __future__ import annotations

import pytest

pytest.importorskip("park", reason="park is not installed")

from park_plugins.ferry import jobs


def _plan(call, card, destination, label=None):
    args = {"source": str(card.path), "destination": str(destination), "probe": False}
    if label:
        args["label"] = label
    return call("ferry.plan", args)


def test_a_park_with_nothing_planned_reads_as_nothing(call):
    assert call("ferry.ingest.list", {}) == {"sources": [], "rows": [], "more": False}


def test_a_planned_card_is_one_planned_row(call, card, tmp_path, no_queue):
    plan = _plan(call, card, tmp_path / "landing")
    read = call("ferry.ingest.list", {})
    assert [item["source"] for item in read["sources"]] == [str(card.path)]
    assert read["sources"][0]["mounted"] is True
    (row,) = read["rows"]
    assert row["kind"] == "offload"
    assert row["id"] == row["plan"] == plan["id"]
    assert row["card"] == "CARD_A"
    assert row["destination_name"] == "landing"
    assert row["state"] == "planned"
    assert row["transfer"] is None and row["receipt"] is None
    assert row["files"] == plan["files"] and row["copies"] == plan["copies"]
    assert row["mounted"] is True


def test_a_row_follows_its_transfer_from_queued_to_done_with_the_receipt(
    call, card, tmp_path, job_context
):
    destination = tmp_path / "landing"
    plan = _plan(call, card, destination)
    transfer = call("ferry.offload", {"plan": plan["id"]})
    (queued,) = call("ferry.ingest.list", {})["rows"]
    assert queued["state"] == "queued"
    assert queued["transfer"] == transfer["transfer"]
    assert queued["attempts"] == 1

    call("ferry.transfer.start", {"transfer": transfer["transfer"], "machine": "titus"})
    call(
        "ferry.transfer.progress",
        {"transfer": transfer["transfer"], "done": 2, "total": 4, "note": "copying"},
    )
    (running,) = call("ferry.ingest.list", {})["rows"]
    assert running["state"] == "running"
    assert running["fraction"] == 0.5 and running["done"] == 2 and running["total"] == 4
    assert running["machine"] == "titus"

    context = job_context("copy", {"plan": plan["id"], "transfer": transfer["transfer"]})
    jobs.copy(context)
    (done,) = call("ferry.ingest.list", {})["rows"]
    assert done["state"] == "done"
    assert done["failed"] == 0 and done["verified"] == len(card.names)
    assert done["ended"] and done["bytes"] == card.total_bytes
    receipt = call("ferry.receipt.list", {"transfer": transfer["transfer"]})[0]
    assert done["receipt"] == receipt["id"] and done["digest"] == receipt["digest"]


def test_a_retry_shows_the_newest_transfer_and_counts_the_attempts(call, card, tmp_path, no_queue):
    plan = _plan(call, card, tmp_path / "landing")
    first = call("ferry.offload", {"plan": plan["id"]})
    call("ferry.receipt.record", {
        "kind": "copy", "status": "failed", "transfer": first["transfer"], "plan": plan["id"],
        "errors": ["A001C001.mov: the card was pulled"],
    })
    (failed,) = call("ferry.ingest.list", {})["rows"]
    assert failed["state"] == "failed"
    assert failed["errors"] == ["A001C001.mov: the card was pulled"]
    second = call("ferry.offload", {"plan": plan["id"]})
    (again,) = call("ferry.ingest.list", {})["rows"]
    assert again["transfer"] == second["transfer"]
    assert again["state"] == "queued"
    assert again["attempts"] == 2


def test_a_proxy_receipt_is_a_row_of_its_own(call, card, tmp_path, no_queue):
    _plan(call, card, tmp_path / "landing")
    call("ferry.receipt.record", {
        "kind": "proxy", "status": "partial", "errors": ["C0001.mp4: no video stream"],
        "total_bytes": 1024, "detail": {"codec": "prores_proxy"},
    })
    rows = call("ferry.ingest.list", {})["rows"]
    assert sorted(item["kind"] for item in rows) == ["offload", "proxy"]
    proxy = next(item for item in rows if item["kind"] == "proxy")
    assert proxy["state"] == "partial"
    assert proxy["label"] == "Proxies"
    assert proxy["errors"] == ["C0001.mp4: no video stream"]


def test_the_read_is_capped_and_says_when_it_left_rows_out(call, card, tmp_path):
    for index in range(3):
        _plan(call, card, tmp_path / f"landing-{index}", label=f"plan {index}")
    capped = call("ferry.ingest.list", {"limit": 2})
    assert len(capped["rows"]) == 2
    assert capped["more"] is True
    assert capped["rows"][0]["label"] == "plan 2"
    assert call("ferry.ingest.list", {})["more"] is False


def test_plan_get_can_return_a_page_of_files_and_still_say_how_many_there_are(call, card, tmp_path):
    plan = _plan(call, card, tmp_path / "landing")
    full = call("ferry.plan.get", {"id": plan["id"]})
    page = call("ferry.plan.get", {"id": plan["id"], "items_limit": 2})
    assert len(full["items"]) == plan["files"]
    assert [item["rel"] for item in page["items"]] == [item["rel"] for item in full["items"][:2]]
    assert page["files"] == plan["files"]
