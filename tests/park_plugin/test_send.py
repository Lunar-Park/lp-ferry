"""Machine to machine through the engines.

``ferry.send`` pairs the two machines' ferry engines the first time (each pins the other's
key), then queues one job on the destination machine. That job plans against the source
machine's engine and pulls every file straight from it — mutual TLS on the pinned keys, a
grant scoped to the transfer, physical readback on arrival — and registers what landed
with Park. Two real daemons on loopback stand in for the two machines.
"""

from __future__ import annotations

import hashlib
import types
from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

from park.machines.contract import LocalCallContext, LocalJobContext
from park.ops import REGISTRY, op
from park.ops.registry import OpContext
from park_plugins.ferry import engine, jobs


@pytest.fixture
def submitted():
    calls = []
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


@pytest.fixture
def two(daemons, ferry_bin, card, tmp_path, call, park, monkeypatch):
    """node-a holds the card (exported read-only); node-b is where it lands. ``node_call``
    reaches the named machine's engine, as park-node would."""
    landing = tmp_path / "landing"
    landing.mkdir()
    a = daemons("node-a", roots=[(str(card.path), "ro")], p2p=True)
    b = daemons("node-b", p2p=True)
    sockets = {"node-a": a.socket, "node-b": b.socket}
    for name in sockets:
        call("machines.register", {"name": name, "id": name})
    monkeypatch.setenv("FERRY_BIN", ferry_bin)

    def on(machine: str) -> None:
        monkeypatch.setenv("FERRY_SOCKET", sockets[machine])

    def node_call(self, name, params=None, *, timeout=None, machine=None):
        assert name == "ferry.engine", name
        on(machine)
        return jobs.engine_call(
            LocalCallContext(park, "ferry", "engine", params or {}, tmp_path, machine=machine)
        )

    monkeypatch.setattr(OpContext, "node_call", node_call)
    return types.SimpleNamespace(landing=landing, on=on)


def send(call, card, two, **extra):
    return call(
        "ferry.send",
        {
            "source_machine": "node-a",
            "source": str(card.path),
            "destination_machine": "node-b",
            "destination": str(two.landing),
            **extra,
        },
    )


def run_send(park, submitted, two, tmp_path):
    job = submitted[-1]
    two.on("node-b")
    ctx = LocalJobContext(
        park,
        plugin="ferry",
        name="send",
        params=job["params"],
        workdir=tmp_path / "work",
        machine="node-b",
        job_id=f"job-{len(submitted)}",
    )
    (tmp_path / "work").mkdir(exist_ok=True)
    return jobs.send(ctx), ctx


def digest(path) -> str:
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def test_send_pairs_the_engines_and_queues_one_job_on_the_destination(call, card, two, submitted):
    result = send(call, card, two)
    assert result["source_machine"] == "node-a" and result["destination_machine"] == "node-b"
    assert [(job["name"], job["machine"]) for job in submitted] == [("send", "node-b")]
    two.on("node-a")
    source = engine.info()
    two.on("node-b")
    landing = engine.info()
    assert submitted[0]["params"]["source_peer"] == source["peer_id"]
    assert landing["peer_id"] in source["paired"] and source["peer_id"] in landing["paired"]
    row = call("ferry.transfer.get", {"id": result["transfer"]})
    assert row["status"] == "queued" and row["machine"] == "node-b"
    # A second send reuses the pairing instead of opening another window.
    send(call, card, two)
    two.on("node-a")
    assert engine.info()["paired"] == source["paired"]


@pytest.mark.parametrize(
    "changes, message",
    [
        ({"destination_machine": "node-a"}, "same machine"),
        ({"source": "relative/path"}, "absolute path"),
    ],
)
def test_send_refuses_what_cannot_work(call, card, two, submitted, changes, message):
    with pytest.raises(Exception, match=message):
        send(call, card, two, **changes)
    assert submitted == []


def test_a_send_lands_every_file_verified_and_never_touches_the_source(
    call, park, card, two, submitted, tmp_path
):
    before = {rel: digest(card.path / rel) for rel in card.names}
    queued = send(call, card, two, label="A-cam card")
    result, ctx = run_send(park, submitted, two, tmp_path)
    assert result["status"] == "ok", result
    assert result["files"] == len(card.names)
    for rel, sha in before.items():
        assert digest(two.landing / rel) == sha
        assert digest(card.path / rel) == sha
    for junk in card.junk_names:
        assert not (two.landing / junk).exists()
    # Park registered each landed file on the destination machine.
    assert sorted(
        Path(o["path"]).relative_to(two.landing).as_posix() for o in ctx.outputs
    ) == sorted(card.names)
    row = call("ferry.transfer.get", {"id": queued["transfer"]})
    assert row["status"] == "copied" and row["verified"] == len(card.names)
    plan = call("ferry.plan.get", {"id": row["plan"]})
    assert plan["machine"] == "node-b" and plan["label"] == "A-cam card"
    receipt = call(
        "ferry.receipt.get",
        {"id": call("ferry.receipt.list", {"transfer": queued["transfer"]})[0]["id"]},
    )
    assert receipt["detail"]["engine"]["status"] == "completed"
    assert receipt["detail"]["engine_receipt"]["targets"][0]["readback_mode"] in (
        "f_nocache",
        "fadvise_dontneed",
    )


def test_a_file_already_there_is_kept_and_a_different_one_lands_beside_it(
    call, park, card, two, submitted, tmp_path
):
    same, other = "notes.txt", "DCIM/100MEDIA/A001C001.wav"
    (two.landing / "DCIM" / "100MEDIA").mkdir(parents=True)
    (two.landing / same).write_bytes((card.path / same).read_bytes())
    (two.landing / other).write_bytes(b"someone else's take")
    send(call, card, two)
    result, _ = run_send(park, submitted, two, tmp_path)
    assert result["status"] == "ok", result
    assert (two.landing / other).read_bytes() == b"someone else's take"
    assert digest(two.landing / "DCIM/100MEDIA/A001C001 (2).wav") == digest(card.path / other)
    receipt = call("ferry.receipt.list", {"transfer": submitted[-1]["params"]["transfer"]})[0]
    full = call("ferry.receipt.get", {"id": receipt["id"]})
    skipped = {row["rel"] for row in full["files"] if row["skipped"]}
    assert skipped == {same}


def test_a_source_outside_the_source_machines_roots_is_refused(
    call, park, card, two, submitted, tmp_path
):
    elsewhere = tmp_path / "not-exported"
    elsewhere.mkdir()
    (elsewhere / "secret.txt").write_text("no")
    call(
        "ferry.send",
        {
            "source_machine": "node-a",
            "source": str(elsewhere),
            "destination_machine": "node-b",
            "destination": str(two.landing),
        },
    )
    result, _ = run_send(park, submitted, two, tmp_path)
    assert result["status"] == "failed"
    assert any("allowed roots" in line for line in result["errors"]), result
    assert list(two.landing.iterdir()) == []
    row = call("ferry.transfer.get", {"id": submitted[-1]["params"]["transfer"]})
    assert row["status"] == "failed"


def test_a_destination_that_is_not_a_folder_ends_the_transfer_with_the_reason(
    call, park, card, two, submitted, tmp_path
):
    call(
        "ferry.send",
        {
            "source_machine": "node-a",
            "source": str(card.path),
            "destination_machine": "node-b",
            "destination": str(tmp_path / "nowhere"),
        },
    )
    result, _ = run_send(park, submitted, two, tmp_path)
    assert result["status"] == "failed" and "not a folder" in result["errors"][0]


def test_send_plan_only_takes_a_transfer_waiting_for_its_machine_and_job(
    call, park, card, two, submitted
):
    queued = send(call, card, two)
    plan = {
        "id": "pln_x",
        "fingerprint": "f",
        "source": "/s",
        "destination": "/d",
        "items": [],
        "label": "x",
        "template": "t",
        "on_conflict": "skip",
        "files": 0,
        "copies": 0,
        "skips": 0,
        "bytes": 0,
    }
    with pytest.raises(Exception, match="different machine or job"):
        call(
            "ferry.send.plan",
            {"transfer": queued["transfer"], "plan": plan, "machine": "node-a", "job": "job-1"},
        )
    with pytest.raises(Exception, match="different machine or job"):
        call(
            "ferry.send.plan",
            {"transfer": queued["transfer"], "plan": plan, "machine": "node-b", "job": "job-9"},
        )
