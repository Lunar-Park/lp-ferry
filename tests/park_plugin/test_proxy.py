"""Proxies: the request, the ffmpeg job, and what happens without ffmpeg."""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path

import pytest

pytest.importorskip("park", reason="park is not installed")

from park_plugins.ferry import jobs, probe

REAL_FFMPEG = shutil.which("ffmpeg")
REAL_FFPROBE = shutil.which("ffprobe")


@pytest.fixture
def media(tmp_path):
    root = tmp_path / "MEDIA"
    (root / "day1").mkdir(parents=True)
    (root / "day1" / "A001C001.mov").write_bytes(b"clip one" * 100)
    (root / "day1" / "A001C002.mov").write_bytes(b"clip two" * 100)
    (root / "still.png").write_bytes(b"not a movie")
    (root / "notes.txt").write_text("notes")
    return root


@pytest.fixture
def stub_ffmpeg(tmp_path):
    """An ffmpeg stand-in that writes its output path with fixed bytes."""
    path = tmp_path / "bin" / "ffmpeg"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        "#!/bin/sh\n"
        'out=""\n'
        'for arg in "$@"; do out="$arg"; done\n'
        """printf 'proxy bytes' > "$out"\n""",
        encoding="utf-8",
    )
    path.chmod(0o755)
    return path


def test_a_proxy_request_names_the_video_only(call, media, tmp_path, no_queue):
    request = call(
        "ferry.proxies", {"source": str(media), "destination": str(tmp_path / "proxies")}
    )
    assert request["queued"] is False
    assert request["codec"] == "ProRes422Proxy"
    assert request["target_height"] == 1080
    rels = sorted(item["rel"] for item in request["items"])
    assert rels == ["day1/A001C001.mov", "day1/A001C002.mov"]
    assert request["items"][0]["proxy"] == str(tmp_path / "proxies" / "day1" / "A001C001.mov")


def test_a_source_with_no_video_is_refused(call, tmp_path):
    quiet = tmp_path / "audio-only"
    quiet.mkdir()
    (quiet / "take.wav").write_bytes(b"audio")
    with pytest.raises(Exception) as caught:
        call("ferry.proxies", {"source": str(quiet), "destination": str(tmp_path / "proxies")})
    assert "no video" in str(caught.value)


def test_the_proxy_job_encodes_and_registers_each_proxy(
    call, media, tmp_path, job_context, stub_ffmpeg
):
    request = call(
        "ferry.proxies", {"source": str(media), "destination": str(tmp_path / "proxies")}
    )
    context = job_context(
        "proxy",
        {
            "request": request["id"],
            "items": request["items"],
            "codec": request["codec"],
            "target_height": request["target_height"],
            "ffmpeg_path": str(stub_ffmpeg),
        },
    )
    result = jobs.proxy(context)
    assert result["status"] == "ok"
    assert result["proxies"] == 2
    assert result["failed"] == 0

    receipt = call("ferry.receipt.list", {"kind": "proxy"})[0]
    assert receipt["status"] == "ok"
    assert receipt["files"] == 2
    full = call("ferry.receipt.get", {"id": receipt["id"]})
    assert full["detail"]["backend"] == "ffmpeg"
    for row in full["files"]:
        path = Path(row["dest"])
        assert path.is_file()
        assert call("asset.get", {"sha256": row["sha256"]})["sha256"] == row["sha256"]
        assert str(path) in [
            seen["path"] for seen in call("asset.paths", {"sha256": row["sha256"]})
        ]


def test_the_proxy_job_reports_ffmpeg_failing(call, media, tmp_path, job_context):
    broken = tmp_path / "bin" / "ffmpeg"
    broken.parent.mkdir(parents=True, exist_ok=True)
    broken.write_text("#!/bin/sh\necho 'no codec' >&2\nexit 1\n", encoding="utf-8")
    broken.chmod(0o755)
    request = call(
        "ferry.proxies", {"source": str(media), "destination": str(tmp_path / "proxies")}
    )
    context = job_context(
        "proxy",
        {
            "request": request["id"],
            "items": request["items"],
            "ffmpeg_path": str(broken),
        },
    )
    result = jobs.proxy(context)
    assert result["status"] == "failed"
    assert result["failed"] == 2
    assert result["proxies"] == 0
    receipt = call("ferry.receipt.list", {"kind": "proxy"})[0]
    assert receipt["status"] == "failed"
    assert any("no codec" in message for message in receipt["errors"])


def test_a_configured_ffmpeg_that_is_not_there_is_an_error(tmp_path):
    with pytest.raises(jobs.ProxyError) as caught:
        jobs.find_ffmpeg(str(tmp_path / "nowhere" / "ffmpeg"))
    assert "configured path" in str(caught.value)


def test_ffmpeg_missing_from_path_is_an_error(monkeypatch):
    monkeypatch.setattr(jobs.shutil, "which", lambda name: None)
    with pytest.raises(jobs.ProxyError) as caught:
        jobs.find_ffmpeg(None)
    assert "not installed" in str(caught.value)


def test_an_unknown_proxy_codec_is_refused(tmp_path):
    with pytest.raises(jobs.ProxyError) as caught:
        jobs.encode(
            "ffmpeg", tmp_path / "a.mov", tmp_path / "b.mov", codec="h264", target_height=1080
        )
    assert "unsupported proxy codec" in str(caught.value)


@pytest.mark.skipif(REAL_FFMPEG is None, reason="ffmpeg is not installed")
def test_ffmpeg_makes_a_prores_proxy_at_the_target_height(tmp_path):
    source = tmp_path / "src.mov"
    made = subprocess.run(
        [
            REAL_FFMPEG,
            "-y",
            "-f",
            "lavfi",
            "-i",
            "testsrc=size=320x240:rate=24:duration=1",
            "-pix_fmt",
            "yuv420p",
            str(source),
        ],
        capture_output=True,
        text=True,
        check=False,
    )
    assert made.returncode == 0, made.stderr
    output = tmp_path / "proxy.mov"
    jobs.encode(REAL_FFMPEG, source, output, codec="ProRes422Proxy", target_height=120)
    assert output.is_file()
    assert output.stat().st_size > 0
    if REAL_FFPROBE is not None:
        fields = probe.probe_file(output, ffprobe=REAL_FFPROBE)
        assert fields["codec"] == "prores"
        assert fields["height"] == 120
