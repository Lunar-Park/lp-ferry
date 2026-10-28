"""The Resolve project: the request, the manifest, and the manifest-only fallback."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

from park_plugins.ferry import jobs


@pytest.fixture
def media(tmp_path):
    root = tmp_path / "CARD_A"
    (root / "DCIM" / "100MEDIA").mkdir(parents=True)
    (root / "SOUND").mkdir()
    (root / "DCIM" / "100MEDIA" / "A001C001.mov").write_bytes(b"clip one" * 50)
    (root / "SOUND" / "take.wav").write_bytes(b"audio" * 50)
    (root / ".DS_Store").write_bytes(b"junk")
    return root


def test_a_project_request_records_its_settings_and_bins(call, media, tmp_path):
    request = call(
        "ferry.resolve_project",
        {
            "source": str(media),
            "destination": str(tmp_path / "project"),
            "resolution": "4K",
            "frame_rate": "25",
            "color_space": "Rec.709",
        },
    )
    assert request["name"] == "CARD_A"
    assert request["resolution"] == "4K"
    assert request["frame_rate"] == "25"
    assert request["manifest"] == str(tmp_path / "project" / "CARD_A.resolve.json")
    assert request["bins"] == ["CARD_A", "CARD_A/DCIM", "CARD_A/DCIM/100MEDIA", "CARD_A/SOUND"]
    assert request["queued"] is False


def test_the_resolve_job_writes_and_registers_the_manifest(call, media, tmp_path, job_context):
    request = call(
        "ferry.resolve_project",
        {"source": str(media), "destination": str(tmp_path / "project")},
    )
    context = job_context(
        "resolve",
        {
            "request": request["id"],
            "source": str(media),
            "name": request["name"],
            "manifest": request["manifest"],
            "resolution": request["resolution"],
            "frame_rate": request["frame_rate"],
            "color_space": request["color_space"],
        },
    )
    result = jobs.resolve(context)
    assert result["backend"] == "manifest"
    assert result["project"] == ""
    assert "Resolve is not installed" in result["note"]
    assert result["bins"] == 4

    manifest_path = Path(request["manifest"])
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    assert manifest["name"] == "CARD_A"
    assert manifest["settings"] == {
        "resolution": "1080",
        "frame_rate": "24",
        "color_space": "Rec.709",
    }
    assert manifest["bins"]["CARD_A/DCIM/100MEDIA"] == [
        str(media / "DCIM" / "100MEDIA" / "A001C001.mov")
    ]
    # Junk on the card stays out of the project.
    assert ".DS_Store" not in json.dumps(manifest)

    receipt = call("ferry.receipt.list", {"kind": "resolve"})[0]
    assert receipt["status"] == "ok"
    full = call("ferry.receipt.get", {"id": receipt["id"]})
    assert full["detail"]["backend"] == "manifest"
    assert full["files"][0]["dest"] == str(manifest_path)
    assert call("asset.get", {"sha256": full["files"][0]["sha256"]})["kind"] == "document"


def test_the_manifest_mirrors_the_folders_below_the_source(media):
    manifest = jobs.build_manifest(
        media,
        "CARD_A",
        {"resolution": "1080", "frame_rate": "24", "color_space": "Rec.709"},
    )
    assert sorted(manifest["bins"]) == [
        "CARD_A",
        "CARD_A/DCIM",
        "CARD_A/DCIM/100MEDIA",
        "CARD_A/SOUND",
    ]
    assert manifest["timeline"] == {"name": "CARD_A", "clips": []}


def test_a_proxy_folder_turns_into_timeline_clips(media, tmp_path):
    proxies = tmp_path / "proxies"
    (proxies / "DCIM" / "100MEDIA").mkdir(parents=True)
    (proxies / "DCIM" / "100MEDIA" / "A001C001.mov").write_bytes(b"proxy")
    manifest = jobs.build_manifest(
        media,
        "CARD_A",
        {"resolution": "1080", "frame_rate": "24", "color_space": "Rec.709"},
        proxy_dir=proxies,
    )
    assert manifest["timeline"]["clips"] == [
        {
            "source": str(media / "DCIM" / "100MEDIA" / "A001C001.mov"),
            "proxy": str(proxies / "DCIM" / "100MEDIA" / "A001C001.mov"),
        }
    ]


def test_a_source_that_is_not_there_is_refused(call, tmp_path):
    with pytest.raises(Exception) as caught:
        call(
            "ferry.resolve_project",
            {"source": str(tmp_path / "absent"), "destination": str(tmp_path / "project")},
        )
    assert "not there" in str(caught.value)


def test_the_job_without_a_source_fails_clearly(job_context, tmp_path):
    context = job_context("resolve", {"source": str(tmp_path / "absent"), "name": "CARD_A"})
    with pytest.raises(jobs.JobInputError) as caught:
        jobs.resolve(context)
    assert "source folder is not there" in str(caught.value)
