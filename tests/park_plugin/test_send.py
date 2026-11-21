"""Send a folder from one machine's disk to another's: stage, land, verify, receipt.

Both machines are simulated in one process. The stage job hashes and registers the source in
place; the land job fetches every file by hash through Park's real node file server and its real
direct-fetch code (the hub stand-in only says where the file server is), writes it under the
destination folder, and records a receipt that names both machines.
"""

from __future__ import annotations

import hashlib
import types
from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

from park.machines.contract import LocalJobContext
from park.node.client import Client, NodeError
from park.ops import REGISTRY, op

if not hasattr(Client, "download_from"):  # pragma: no cover - an older park
    pytest.skip("needs park's direct fetch (fetch_asset from_machine)", allow_module_level=True)

from park.node.files_service import FileServer
from park.node.worker import fetch_asset
from park_plugins.ferry import jobs
from park_plugins.ferry.copy import hash_file

TOKEN = "node-token"


class Hub(Client):
    """What a node's client and a file server ask the hub: valid tokens, where files are, where a machine is."""

    def __init__(self, *, url: str | None = None, files: dict | None = None):
        super().__init__("http://hub.invalid", token=TOKEN)
        self.url, self.files, self.asked = url, files or {}, []

    def call(self, op_name, args=None, *, token=None, timeout=None, request_key=None):
        self.asked.append(op_name)
        if op_name == "plugin.list":
            if token == TOKEN:
                return []
            raise NodeError("unauthorized", status=401)
        if op_name == "asset.paths":
            path = self.files.get((args or {}).get("sha256"))
            return [{"node": "node-a", "path": str(path)}] if path else []
        if op_name == "machines.get":
            return {"id": "node-a", "url": self.url}
        raise NodeError(f"{op_name} is not part of this test", status=404)


class LandContext(LocalJobContext):
    """A land job whose fetch goes over a real HTTP connection, the way a node's does."""

    hub: Hub

    def fetch_asset(self, sha256, dest=None, *, from_machine=None):
        return fetch_asset(
            self.hub, self.machine, self.workdir, sha256, dest, from_machine=from_machine
        )


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
def machines(call):
    for name in ("node-a", "node-b"):
        call("machines.register", {"name": name, "id": name})


@pytest.fixture
def source_server(card):
    """node-a's file server, serving the card's files by hash."""
    files = {
        hashlib.sha256((card.path / rel).read_bytes()).hexdigest(): card.path / rel
        for rel in card.names
    }
    settings = types.SimpleNamespace(name="node-a", port=0, allow_unauthenticated_files=False)
    server = FileServer(settings, Hub(files=files), bind="127.0.0.1", port=0)
    server.start()
    yield types.SimpleNamespace(url=f"http://127.0.0.1:{server.port}", files=files)
    server.stop()


def send(call, card, tmp_path, **extra):
    return call(
        "ferry.send",
        {
            "source_machine": "node-a",
            "source": str(card.path),
            "destination_machine": "node-b",
            "destination": str(tmp_path / "ssd-b" / "ingest"),
            **extra,
        },
    )


def stage_context(park, submission, tmp_path):
    return LocalJobContext(
        park,
        plugin="ferry",
        name="stage",
        params=submission["params"],
        workdir=tmp_path / "work-a",
        machine="node-a",
    )


def land_context(park, submission, tmp_path, source_server):
    (tmp_path / "work-b").mkdir(exist_ok=True)
    context = LandContext(
        park,
        plugin="ferry",
        name="land",
        params=submission["params"],
        workdir=tmp_path / "work-b",
        machine="node-b",
        job_id="job-2",
    )
    context.hub = Hub(url=source_server.url, files=source_server.files)
    return context


def snapshot(root: Path) -> dict:
    return {
        str(p.relative_to(root)): (p.read_bytes(), p.stat().st_mtime_ns)
        for p in sorted(root.rglob("*"))
        if p.is_file()
    }


# -- queueing -----------------------------------------------------------------------


def test_send_queues_the_stage_job_on_the_source_machine(call, card, tmp_path, machines, submitted):
    sent = send(call, card, tmp_path, label="Card A to SSD B")
    assert sent["queued"] is True and sent["job"] == "job-1"
    assert (sent["source_machine"], sent["destination_machine"]) == ("node-a", "node-b")
    assert [(c["name"], c["machine"]) for c in submitted] == [("stage", "node-a")]
    assert submitted[0]["params"]["destination_machine"] == "node-b"
    assert submitted[0]["params"]["label"] == "Card A to SSD B"
    row = call("ferry.transfer.get", {"id": sent["transfer"]})
    assert (row["status"], row["job"], row["machine"], row["plan"]) == (
        "queued",
        "job-1",
        "node-a",
        "",
    )


@pytest.mark.parametrize(
    "changes, message",
    [
        ({"destination_machine": "node-a"}, "same machine"),
        ({"source": "relative/folder"}, "absolute"),
        ({"destination": "ssd/ingest"}, "absolute"),
        ({"destination_machine": "nowhere"}, "no such machine"),
    ],
)
def test_send_refuses_what_cannot_work(call, card, tmp_path, machines, submitted, changes, message):
    with pytest.raises(Exception, match=message):
        send(call, card, tmp_path, **changes)
    assert submitted == []


# -- the whole send -----------------------------------------------------------------


def run_send(call, park, card, tmp_path, submitted, source_server):
    sent = send(call, card, tmp_path)
    staged = jobs.stage(stage_context(park, submitted[0], tmp_path))
    context = land_context(park, submitted[1], tmp_path, source_server)
    return sent, staged, context, jobs.land(context)


def test_a_send_lands_every_file_verified_and_never_touches_the_source(
    call, park, card, tmp_path, machines, submitted, source_server
):
    before = snapshot(card.path)
    sent = send(call, card, tmp_path)
    staged = jobs.stage(stage_context(park, submitted[0], tmp_path))
    assert (
        snapshot(card.path) == before
    )  # staging hashes in place: nothing written, nothing touched
    assert staged["files"] == len(card.names) and staged["bytes"] == card.total_bytes

    # Staging queued the landing on the other machine, and the plan belongs to it.
    assert [(c["name"], c["machine"]) for c in submitted] == [
        ("stage", "node-a"),
        ("land", "node-b"),
    ]
    assert submitted[1]["params"] == {
        "plan": staged["plan"],
        "transfer": sent["transfer"],
        "machine": "node-b",
        "source_machine": "node-a",
    }
    plan = call("ferry.plan.get", {"id": staged["plan"]})
    assert plan["machine"] == "node-b" and plan["source"] == str(card.path)
    assert plan["destination"] == str(tmp_path / "ssd-b" / "ingest")
    assert sorted(i["rel"] for i in plan["items"]) == sorted(card.names)
    for item in plan["items"]:
        assert item["probe"]["sha256"] == hash_file(card.path / item["rel"])
        assert item["dest"] == str(tmp_path / "ssd-b" / "ingest" / item["rel"])
    row = call("ferry.transfer.get", {"id": sent["transfer"]})
    assert (row["plan"], row["job"], row["machine"], row["total"]) == (
        staged["plan"],
        "job-2",
        "node-b",
        len(card.names),
    )
    for rel in card.names:  # each source file is an Asset whose path is on the source machine
        paths = call("asset.get", {"sha256": hash_file(card.path / rel)})["paths"]
        assert str(card.path / rel) in [p["path"] for p in paths]

    context = land_context(park, submitted[1], tmp_path, source_server)
    result = jobs.land(context)
    assert (result["status"], result["files"], result["failed"], result["bytes"]) == (
        "ok",
        len(card.names),
        0,
        card.total_bytes,
    )
    for rel in card.names:
        landed = tmp_path / "ssd-b" / "ingest" / rel
        assert landed.read_bytes() == (card.path / rel).read_bytes()
    assert not list((tmp_path / "ssd-b").rglob("*.part"))
    # Every byte came from node-a's file server; the hub was only asked where that is.
    assert set(context.hub.asked) == {"machines.get"}
    assert snapshot(card.path) == before

    row = call("ferry.transfer.get", {"id": sent["transfer"]})
    assert (row["status"], row["verified"], row["failed"]) == ("copied", len(card.names), 0)
    receipt = call("ferry.receipt.get", {"id": result["receipt"]})
    assert receipt["machine"] == "node-b" and receipt["detail"]["send"] == {
        "source_machine": "node-a",
        "source": str(card.path),
        "destination_machine": "node-b",
        "destination": str(tmp_path / "ssd-b" / "ingest"),
    }
    for entry in receipt["files"]:
        assert (
            entry["sha256"] == hash_file(entry["dest"]) == hash_file(entry["source"])
            and entry["verified"]
        )

    report = call(
        "ferry.verify", {"transfer": sent["transfer"], "folder": str(tmp_path / "ssd-b" / "ingest")}
    )
    assert report["clean"] is True and report["checked"] == len(card.names)


def test_a_file_already_there_is_kept_and_a_different_one_is_never_overwritten(
    call, park, card, tmp_path, machines, submitted, source_server
):
    ingest = tmp_path / "ssd-b" / "ingest"
    same, other = card.names[0], card.names[1]
    (ingest / same).parent.mkdir(parents=True)
    (ingest / same).write_bytes((card.path / same).read_bytes())
    (ingest / other).parent.mkdir(parents=True, exist_ok=True)
    (ingest / other).write_bytes(b"something else that was already on the SSD")

    _sent, _staged, _context, result = run_send(
        call, park, card, tmp_path, submitted, source_server
    )
    assert result["status"] == "partial" and result["failed"] == 1
    assert (ingest / other).read_bytes() == b"something else that was already on the SSD"
    receipt = call("ferry.receipt.get", {"id": result["receipt"]})
    assert any(
        "already exists with different contents" in line and other in line
        for line in receipt["errors"]
    )
    kept = next(e for e in receipt["files"] if e["rel"] == same)
    assert kept["skipped"] is True
    assert result["bytes"] == card.total_bytes - len((card.path / same).read_bytes()) - len(
        (card.path / other).read_bytes()
    )


def test_a_file_that_changed_after_staging_is_refused_and_deleted(
    call, park, card, tmp_path, machines, submitted, source_server
):
    send(call, card, tmp_path)
    staged = jobs.stage(stage_context(park, submitted[0], tmp_path))
    changed = card.names[0]
    (card.path / changed).write_bytes(b"edited on the source after it was hashed")
    result = jobs.land(land_context(park, submitted[1], tmp_path, source_server))
    assert (
        result["status"] == "partial"
        and result["failed"] == 1
        and result["files"] == len(card.names) - 1
    )
    assert not (tmp_path / "ssd-b" / "ingest" / changed).exists()
    assert not list((tmp_path / "ssd-b").rglob("*.part"))
    receipt = call("ferry.receipt.get", {"id": result["receipt"]})
    assert any("with hash" in line and changed in line for line in receipt["errors"])
    assert call("ferry.plan.get", {"id": staged["plan"]})["machine"] == "node-b"


def test_a_destination_without_the_room_fails_before_anything_is_fetched(
    call, park, card, tmp_path, machines, submitted, source_server, monkeypatch
):
    send(call, card, tmp_path)
    jobs.stage(stage_context(park, submitted[0], tmp_path))
    import shutil

    monkeypatch.setattr(
        shutil, "disk_usage", lambda path: types.SimpleNamespace(total=10, used=10, free=10)
    )
    context = land_context(park, submitted[1], tmp_path, source_server)
    result = jobs.land(context)
    assert result["status"] == "failed" and result["files"] == 0
    assert "not enough space" in call("ferry.receipt.get", {"id": result["receipt"]})["errors"][0]
    assert "machines.get" not in context.hub.asked
    assert not [p for p in (tmp_path / "ssd-b" / "ingest").rglob("*") if p.is_file()]


def test_a_resend_with_no_room_left_still_lands_the_files_that_are_already_there(
    call, park, card, tmp_path, machines, submitted, source_server, monkeypatch
):
    """Space is checked against the bytes this run will actually write.

    A send that landed once and is re-run on a full disk writes nothing: every file is a skip.
    Refusing it for room it will never use turns a working no-op into a failure the operator
    has to read a receipt to explain.
    """
    first = run_send(call, park, card, tmp_path, submitted, source_server)[3]
    assert first["status"] == "ok"

    import shutil

    monkeypatch.setattr(
        shutil, "disk_usage", lambda path: types.SimpleNamespace(total=10, used=10, free=0)
    )
    submitted.clear()  # this is a new send: its own stage and land submissions
    _sent, _staged, _context, result = run_send(
        call, park, card, tmp_path, submitted, source_server
    )
    assert (result["status"], result["files"], result["failed"], result["bytes"]) == (
        "ok",
        len(card.names),
        0,
        0,
    )


def test_every_landed_file_reaches_the_disk_before_the_receipt_calls_it_verified(
    call, park, card, tmp_path, machines, submitted, source_server, monkeypatch
):
    """A landed file is flushed before it is hashed or registered.

    ``fetch_asset`` returning means the bytes are in this process's page cache, not on the
    media. If the receipt is written first, a power cut strands a transfer that the receipt
    calls verified with a file that never arrived.
    """
    send(call, card, tmp_path)
    jobs.stage(stage_context(park, submitted[0], tmp_path))
    order: list[tuple[str, str]] = []
    real_fsync_file = jobs.fsync_file

    monkeypatch.setattr(
        jobs,
        "fsync_file",
        lambda path: (order.append(("flush", str(path))), real_fsync_file(path))[1],
    )
    context = land_context(park, submitted[1], tmp_path, source_server)
    real_register = context.register_output

    def register(path, role="output", meta=None):
        order.append(("register", str(path)))
        return real_register(path, role=role, meta=meta)

    context.register_output = register

    result = jobs.land(context)
    assert result["status"] == "ok" and result["files"] == len(card.names)

    landed = [str(tmp_path / "ssd-b" / "ingest" / rel) for rel in card.names]
    assert [path for kind, path in order if kind == "flush"] == landed
    for path in landed:
        flushed = next(i for i, (kind, p) in enumerate(order) if kind == "flush" and p == path)
        registered = next(
            i for i, (kind, p) in enumerate(order) if kind == "register" and p == path
        )
        assert flushed < registered, f"{path} was recorded before it reached the disk"


# -- when it cannot start ------------------------------------------------------------


def test_a_source_that_is_not_a_folder_ends_the_transfer_with_the_reason(
    call, park, card, tmp_path, machines, submitted
):
    sent = call(
        "ferry.send",
        {
            "source_machine": "node-a",
            "source": str(tmp_path / "unmounted"),
            "destination_machine": "node-b",
            "destination": str(tmp_path / "dest"),
        },
    )
    result = jobs.stage(stage_context(park, submitted[0], tmp_path))
    assert result["status"] == "failed"
    row = call("ferry.transfer.get", {"id": sent["transfer"]})
    assert row["status"] == "failed" and "is not a folder on node-a" in row["errors"][0]
    assert [c["name"] for c in submitted] == ["stage"]  # no landing was queued


def test_land_only_takes_a_transfer_that_is_waiting_for_it(
    call, park, card, tmp_path, machines, submitted
):
    send(call, card, tmp_path)
    jobs.stage(stage_context(park, submitted[0], tmp_path))
    args = submitted[0]["params"]
    with pytest.raises(Exception, match="not waiting to be landed"):
        call(
            "ferry.send.land",
            {
                "transfer": args["transfer"],
                "source_machine": "node-a",
                "source": str(card.path),
                "destination_machine": "node-b",
                "destination": args["destination"],
                "items": [{"rel": "a", "size": 1, "sha256": "0" * 64}],
            },
        )


@pytest.mark.parametrize(
    "items, message",
    [
        ([{"rel": "../escape.mov", "size": 1, "sha256": "0" * 64}], "not a path inside"),
        ([{"rel": "/etc/passwd", "size": 1, "sha256": "0" * 64}], "not a path inside"),
        ([{"rel": "a.mov", "size": 1, "sha256": "short"}], "64 character sha256"),
        ([{"rel": "a.mov", "size": -1, "sha256": "0" * 64}], "non-negative size"),
        (
            [
                {"rel": "a.mov", "size": 1, "sha256": "0" * 64},
                {"rel": "./a.mov", "size": 1, "sha256": "1" * 64},
            ],
            "same path",
        ),
        ([], "nothing to land"),
    ],
)
def test_the_landing_plan_refuses_names_that_climb_out_or_collide(
    call, card, tmp_path, machines, submitted, items, message
):
    sent = send(call, card, tmp_path)
    with pytest.raises(Exception, match=message):
        call(
            "ferry.send.land",
            {
                "transfer": sent["transfer"],
                "source_machine": "node-a",
                "source": str(card.path),
                "destination_machine": "node-b",
                "destination": str(tmp_path / "dest"),
                "items": items,
            },
        )
    assert [c["name"] for c in submitted] == ["stage"]
    assert call("ferry.transfer.get", {"id": sent["transfer"]})["plan"] == ""
