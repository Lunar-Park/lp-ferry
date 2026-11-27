"""The clearance bridge: an agent's Ferry transfer waits for a person, in Park's terms.

Two halves, one record. A transfer an *agent* starts is queued with a clearance, so Park
pauses the queued job (``waiting_clearance``) and no node picks it up; a person grants or
denies it, and granting puts the job back in the queue. A plan the *engine* parks in
``awaiting_approval`` while the job is already running becomes a clearance too, recorded
against the transfer (a running job cannot be paused), and the job ends saying so instead of
failing. Against a Park without clearances both halves are skipped and nothing changes.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

from park.machines.contract import LocalJobContext
from park.ops import invoke
from park_plugins.ferry import engine, jobs

PLUGIN = Path(__file__).resolve().parents[2] / "park_plugins" / "ferry"


def write_plan(park, transfer_root, *, machine="studio", bytes_total=1 << 20):
    """A plan as the engine would have recorded it, owned by ``machine``."""
    landing = transfer_root / "landing"
    landing.mkdir(parents=True, exist_ok=True)
    park.write(
        "ferry.plan.created",
        {
            "id": "pln_clearance",
            "label": "card",
            "source": str(transfer_root / "card"),
            "destination": str(landing),
            "template": "{root}/{filename}",
            "on_conflict": "skip",
            "fingerprint": "f" * 8,
            "files": 1,
            "copies": 1,
            "skips": 0,
            "bytes": bytes_total,
            "machine": machine,
            "mounts": {"source": "/card", "destination": str(landing)},
            "created": "2026-11-27T00:00:00.000Z",
            "items": [
                {
                    "rel": "A001.MOV",
                    "action": "copy",
                    "size": bytes_total,
                    "sha256": "0" * 64,
                    "source": "/card/A001.MOV",
                    "dest": str(landing / "A001.MOV"),
                    "kind": "video",
                }
            ],
        },
        actor="d",
    )
    return "pln_clearance"


def agent_call(park, actor="agent:claude"):
    def call(name, args=None):
        return invoke(park, name, args or {}, actor=actor)["result"]

    return call


@pytest.fixture()
def studio(call, tmp_path, park):
    """A registered worker and a plan it owns."""
    call("machines.register", {"name": "studio", "id": "studio"})
    return write_plan(park, tmp_path)


def test_an_agents_copy_waits_for_a_person_and_is_paused(park, call, studio):
    result = agent_call(park)("ferry.offload", {"plan": studio})
    assert result["clearance"], "an agent's copy is queued with a clearance"
    job = call("machines.job.get", {"job": result["job"]})
    assert job["status"] == "waiting_clearance", "no node picks it up until a person says yes"

    clearance = call("machines.clearance.list", {"job": result["job"]})[0]
    assert clearance["op"] == "ferry.offload" and clearance["status"] == "pending"
    assert clearance["preview"]["transfer"] == result["transfer"]
    assert clearance["preview"]["bytes"] > 0

    # A person decides: the job goes back in the queue, where a node can take it again.
    call("machines.clearance.grant", {"id": result["clearance"]})
    assert call("machines.job.get", {"job": result["job"]})["status"] == "queued"
    granted = [row for row in call("machines.clearance.list") if row["id"] == result["clearance"]]
    assert granted and granted[0]["status"] == "granted"


def test_a_person_copy_is_queued_without_a_clearance(park, call, studio):
    result = call("ferry.offload", {"plan": studio})
    assert "clearance" not in result
    assert call("machines.job.get", {"job": result["job"]})["status"] == "queued"


def test_denying_a_clearance_retires_the_job(park, call, studio):
    result = agent_call(park)("ferry.offload", {"plan": studio})
    call("machines.clearance.deny", {"id": result["clearance"]})
    assert call("machines.job.get", {"job": result["job"]})["status"] == "canceled"


def test_a_park_without_clearances_queues_exactly_as_before(park, call, studio, monkeypatch):
    from park.ops import REGISTRY

    for name in (
        "machines.clearance.request",
        "machines.clearance.list",
        "machines.clearance.grant",
        "machines.clearance.deny",
        "machines.clearance.get",
    ):
        monkeypatch.delitem(REGISTRY, name, raising=False)
    result = agent_call(park)("ferry.offload", {"plan": studio})
    assert "clearance" not in result, "an older hub has no clearances, and the copy still queues"
    assert call("machines.job.get", {"job": result["job"]})["status"] == "queued"


def test_a_plan_the_engine_parks_ends_as_waiting_not_failed(
    park, call, studio, tmp_path, monkeypatch
):
    """The job is already running, so Park records the decision against the transfer."""
    queued = agent_call(park)("ferry.offload", {"plan": studio})
    transfer, job_id = queued["transfer"], queued["job"]

    def parked(*args, **kwargs):
        raise engine.AwaitingApprovalError(
            "parked", plan_id=studio, reasons=["agent:claude may not send this much unattended"]
        )

    monkeypatch.setattr(engine, "run", parked)
    # The mounts are the engine's business and are proved elsewhere; this is about the answer.
    monkeypatch.setattr(jobs.layout, "validate_mounts", lambda plan: None)
    context = LocalJobContext(
        park,
        plugin="ferry",
        name="copy",
        params={"plan": studio, "transfer": transfer, "machine": "studio"},
        workdir=tmp_path,
        machine="studio",
        job_id=job_id,
    )
    result = jobs.copy(context)

    assert result["status"] == "waiting_clearance", "not a failed copy"
    assert result["reasons"] == ["agent:claude may not send this much unattended"]
    clearance = next(row for row in call("machines.clearance.list") if row["op"] == "ferry.copy")
    assert clearance["status"] == "pending"
    assert clearance["preview"]["transfer"] == transfer, "the decision points at the transfer"
    assert clearance["preview"]["plan"] == studio
    # The running job could not be paused, and the record says so rather than pretending.
    assert clearance["preview"].get("paused") is False


def test_the_card_pack_matches_slates_visual_schema():
    """The shapes Slate's resolver and Visuals.swift draw, exactly.

    A card that names a visual Slate does not know, or puts a field where that visual does not
    read it, is drawn as the generic fallback instead: the state still shows, but none of what
    the plugin meant. These are the four mismatches a cross-repo review caught, kept out.
    """
    from park.plugins.manifest import Manifest

    manifest = Manifest.load(PLUGIN / "plugin.toml")
    template = json.loads((PLUGIN / "cards" / "transfer.json").read_text(encoding="utf-8"))
    states = template["states"]

    # `tree` expects root/children; this state is a path of three steps, which is a flow.
    waiting = states["waiting_clearance"]
    assert waiting["type"] == "flow"
    assert [node["id"] for node in waiting["nodes"]] == ["source", "destination", "receipt"]
    assert waiting["edges"] == [["source", "destination"], ["destination", "receipt"]]
    assert waiting["nodes"][0]["note"] == {
        "$path": "$.params.source",
        "fallback": "the folder on the machine that holds it",
    }
    assert waiting["nodes"][1]["note"]["$path"] == "$.params.destination"
    assert waiting["focus"] == "destination"

    # Progress reads the numbers the job writes, with the fraction and the note as fallbacks.
    running = states["running"]
    assert running["type"] == "progress"
    assert running["done"]["$path"] == "$.progress.detail.bytes_verified"
    assert running["done"]["fallback"]["$path"] == "$.progress.fraction"
    assert running["total"] == {"$path": "$.progress.detail.bytes_total", "fallback": 1}
    assert running["note"]["$path"] == "$.progress.detail.current_file"
    assert running["note"]["fallback"]["$path"] == "$.progress.note"

    # A receipt is rows with a label, a value and its evidence, not loose top-level fields.
    done = states["done"]
    assert done["type"] == "receipt" and done["status"] == "ok"
    assert [row["label"] for row in done["rows"]] == ["Files", "Bytes", "Checksum", "Verified"]
    assert all(row["evidence"] == "verified" for row in done["rows"])
    assert done["rows"][2]["value"] == {"$path": "$.result.checksum_algo", "fallback": "xxh64"}

    # A status is items with a label, a state and a note.
    failed = states["failed"]
    assert failed["type"] == "status"
    assert [(item["label"], item["state"]) for item in failed["items"]] == [
        ("Transfer failed", "bad"),
        ("File in flight", "warn"),
    ]
    assert failed["items"][0]["note"] == {"$path": "$.error", "fallback": "Unknown error"}
    assert failed["items"][1]["note"]["$path"] == "$.progress.detail.current_file"

    # Decision buttons carry the decision tones, which the app styles as approve and reject.
    tones = {action.id: action.tone for card in manifest.cards for action in card.actions}
    assert tones["approve"] == "approve" and tones["decline"] == "reject"


def test_the_cards_declared_in_the_manifest_pass_parks_own_check():
    from park.plugins import contract
    from park.plugins.manifest import CARD_VISUALS, Manifest

    manifest = Manifest.load(PLUGIN / "plugin.toml")
    assert [card.id for card in manifest.cards] == ["transfer", "copy"]
    assert manifest.cli is not None and manifest.cli.command == "ferry"
    for card in manifest.cards:
        assert card.resource == "park.job" and card.match["plugin"] == "ferry"
        template = json.loads((PLUGIN / card.template).read_text())
        for state, spec in template["states"].items():
            assert spec["type"] in CARD_VISUALS, state
            for action in spec.get("actions") or []:
                assert action in {declared.id for declared in card.actions}, (card.id, action)
    assert contract._cards(PLUGIN, manifest).ok, contract._cards(PLUGIN, manifest).problems
    assert agent_call  # the helper above is part of this module's contract
