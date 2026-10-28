"""Planning a source folder: what is in it, where it lands and what a plan records."""

from __future__ import annotations

from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

from park_plugins.ferry import probe


def test_a_card_folder_is_planned(call, card, tmp_path):
    destination = tmp_path / "offload"
    plan = call("ferry.plan", {"source": str(card.path), "destination": str(destination)})
    assert plan["label"] == "CARD_A"
    assert plan["source"] == str(card.path)
    assert plan["destination"] == str(destination)
    assert plan["files"] == len(card.names)
    assert plan["copies"] == len(card.names)
    assert plan["skips"] == 0
    assert plan["bytes"] == card.total_bytes
    assert sorted(item["rel"] for item in plan["items"]) == card.names
    assert plan["by_kind"] == {"audio": 1, "document": 1, "video": 3}
    assert len(plan["fingerprint"]) == 64


def test_the_operating_systems_junk_stays_out_of_the_plan(call, card, tmp_path):
    plan = call(
        "ferry.plan",
        {"source": str(card.path), "destination": str(tmp_path / "offload"), "probe": False},
    )
    rels = {item["rel"] for item in plan["items"]}
    for junk in card.junk_names:
        assert junk not in rels
    assert ".Spotlight-V100/store" not in rels


def test_subfolders_survive_the_copy(call, card, tmp_path):
    destination = tmp_path / "offload"
    plan = call(
        "ferry.plan",
        {"source": str(card.path), "destination": str(destination), "probe": False},
    )
    by_rel = {item["rel"]: item for item in plan["items"]}
    assert by_rel["DCIM/100MEDIA/A001C001.mov"]["dest"] == str(
        destination / "DCIM" / "100MEDIA" / "A001C001.mov"
    )
    assert by_rel["notes.txt"]["dest"] == str(destination / "notes.txt")


def test_the_plan_is_readable_after_it_is_written(call, card, tmp_path):
    plan = call(
        "ferry.plan",
        {"source": str(card.path), "destination": str(tmp_path / "offload"), "probe": False},
    )
    stored = call("ferry.plan.get", {"id": plan["id"]})
    assert stored["fingerprint"] == plan["fingerprint"]
    assert len(stored["items"]) == plan["files"]
    assert [item["rel"] for item in stored["items"]] == sorted(
        item["rel"] for item in stored["items"]
    )
    listed = call("ferry.plan.list", {"source": str(card.path)})
    assert [row["id"] for row in listed] == [plan["id"]]


def test_a_second_plan_against_a_filled_destination_skips(call, card, tmp_path):
    destination = tmp_path / "offload"
    first = call(
        "ferry.plan",
        {"source": str(card.path), "destination": str(destination), "probe": False},
    )
    for item in first["items"]:
        path = Path(item["dest"])
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes((card.path / item["rel"]).read_bytes())
    second = call(
        "ferry.plan",
        {"source": str(card.path), "destination": str(destination), "probe": False},
    )
    assert second["skips"] == len(card.names)
    assert second["copies"] == 0
    assert second["fingerprint"] != first["fingerprint"]


def test_rename_keeps_both_files(call, card, tmp_path):
    destination = tmp_path / "offload"
    config_path = tmp_path / "ferry.toml"
    config_path.write_text('[organize]\non_conflict = "rename"\n', encoding="utf-8")
    first = call(
        "ferry.plan",
        {
            "source": str(card.path),
            "destination": str(destination),
            "config_path": str(config_path),
            "probe": False,
        },
    )
    for item in first["items"]:
        path = Path(item["dest"])
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(b"already here")
    second = call(
        "ferry.plan",
        {
            "source": str(card.path),
            "destination": str(destination),
            "config_path": str(config_path),
            "probe": False,
        },
    )
    assert second["copies"] == len(card.names)
    assert all("-1." in Path(item["dest"]).name for item in second["items"])


def test_a_missing_source_is_not_found(call, tmp_path):
    with pytest.raises(Exception) as caught:
        call(
            "ferry.plan",
            {"source": str(tmp_path / "no-card"), "destination": str(tmp_path / "out")},
        )
    assert "not there" in str(caught.value)


def test_an_empty_source_is_refused(call, tmp_path):
    empty = tmp_path / "empty-card"
    empty.mkdir()
    with pytest.raises(Exception) as caught:
        call("ferry.plan", {"source": str(empty), "destination": str(tmp_path / "out")})
    assert "no files" in str(caught.value)


def test_probing_records_what_ffprobe_says_or_why_it_could_not(call, card, tmp_path):
    plan = call(
        "ferry.plan",
        {"source": str(card.path), "destination": str(tmp_path / "offload"), "probe": True},
    )
    video = [item for item in plan["items"] if item["rel"].endswith(".mov")]
    assert video
    for item in video:
        # The fixture bytes are not a movie, so a probe either records the fields an
        # ffprobe run found or the reason it could not read the file.
        assert item["probe"] == {} or "error" in item["probe"] or "codec" in item["probe"]


def test_a_source_folder_reports_whether_it_is_here(call, card, tmp_path):
    call(
        "ferry.plan",
        {"source": str(card.path), "destination": str(tmp_path / "offload"), "probe": False},
    )
    sources = call("ferry.source.list", {})
    assert len(sources) == 1
    assert sources[0]["source"] == str(card.path)
    assert sources[0]["label"] == "CARD_A"
    assert sources[0]["files"] == len(card.names)
    assert sources[0]["mounted"] is True


def test_probe_skips_junk_below_the_root(card, tmp_path):
    found = {one.rel for one in probe.scan(card.path)}
    assert found == set(card.names)
    kinds = {one.rel: one.kind for one in probe.scan(card.path)}
    assert kinds["notes.txt"] == "document"
    assert kinds["PRIVATE/M4ROOT/CLIP/C0001.mp4"] == "video"
