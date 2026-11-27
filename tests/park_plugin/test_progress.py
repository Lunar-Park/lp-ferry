"""Structured transfer progress (Slice 2A.2) and who may report it.

The engine emits several progress events a second per target. Park gets one report a second
(or sooner on a real move), carrying the detail a live view reads, and only from the job that
is running the transfer — or from a reviewer, or the node surface itself.
"""

from __future__ import annotations

import pytest

pytest.importorskip("park")

from park.ops import Forbidden, Invalid, invoke
from park_plugins.ferry import jobs


class FakeJob:
    """A job context that records what a handler asked it to say."""

    def __init__(self, *, takes_detail=True, op_takes_detail=True):
        self.progress_calls: list[tuple] = []
        self.calls: list[tuple[str, dict]] = []
        self.logs: list[str] = []
        self.takes_detail = takes_detail
        self.op_takes_detail = op_takes_detail

    def progress(self, fraction, note, detail=None):
        if detail is not None and not self.takes_detail:
            raise TypeError("progress() takes 3 positional arguments but 4 were given")
        self.progress_calls.append((fraction, note, detail))

    def call(self, op, args=None):
        # An older park refuses the argument it does not know, and says so like this.
        if (
            op == "ferry.transfer.progress"
            and "detail" in (args or {})
            and not self.op_takes_detail
        ):
            raise Invalid("unexpected argument: detail")
        self.calls.append((op, dict(args or {})))
        return {}

    def log(self, message):
        self.logs.append(message)


def event(fraction=0.25, written=2500, verified=2000, total=8000, eta=12.5, **extra):
    """A transfer.progress event as the engine sends it: totals plus one target in flight."""
    body = {
        "transfer_id": "tr_1",
        "overall_fraction": fraction,
        "bytes_written": written,
        "bytes_verified": verified,
        "total_bytes": total,
        "eta_seconds": eta,
        "targets": [
            {
                "target_id": "d1",
                "label": "SSD",
                "peer_id": "",
                "transport_badge": "local",
                "current_file": "CLIP/A001.MOV",
                "phase": "writing",
                "bytes_written": written,
                "bytes_verified": verified,
                "total_bytes": total,
                "write_bps": 1234.5,
                "verify_bps": 900.0,
                "eta_seconds": 11.0,
                "done_files": 1,
                "failed_files": 0,
                "total_files": 4,
            }
        ],
    }
    body.update(extra)
    return body


class Clock:
    """A monotonic clock the test moves by hand."""

    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now


def reporter(job, clock=None, total=4, total_bytes=8000, errors=None):
    return jobs._ProgressReporter(
        job,
        "tr_1",
        total,
        total_bytes,
        errors if errors is not None else [],
        clock=clock or Clock(),
    )


def test_a_report_carries_the_eight_pieces_of_detail_a_view_reads():
    job = FakeJob()
    rel = reporter(job)
    rel(event())
    assert len(job.progress_calls) == 1
    fraction, note, detail = job.progress_calls[0]
    assert fraction == 0.25 and note == "2000 of 8000 bytes verified"
    assert detail == {
        "bytes_written": 2500,
        "bytes_verified": 2000,
        "bytes_total": 8000,
        "rate": 1234.5,
        "eta_s": 12.5,
        "current_file": "CLIP/A001.MOV",
        "phase": "writing",
        "transport": "local",
    }
    # Park gets the same detail on its own event, so a live view streams it.
    ((_op, args),) = [(o, a) for o, a in job.calls if o == "ferry.transfer.progress"]
    assert args["detail"] == detail
    assert (args["transfer"], args["done"], args["total"]) == ("tr_1", 1, 4)
    assert args["note"] == note


def test_detail_survives_a_thin_or_odd_event():
    job = FakeJob()
    rel = reporter(job, total=2, total_bytes=4096)
    rel({"overall_fraction": 0.5})  # no targets, no bytes, no eta
    detail = job.progress_calls[0][2]
    assert detail["bytes_total"] == 4096, "the plan's total stands in for a missing one"
    assert detail["bytes_written"] == 0 and detail["rate"] is None and detail["eta_s"] is None
    assert (
        detail["current_file"] is None and detail["phase"] is None and detail["transport"] is None
    )

    job2 = FakeJob()
    rel2 = reporter(job2)
    rel2(
        event(
            targets=[{"total_bytes": 1, "write_bps": "fast", "eta_seconds": -3}], eta_seconds=None
        )
    )
    detail = job2.progress_calls[0][2]
    assert detail["rate"] is None and detail["eta_s"] is None, (
        "nonsense numbers are no number at all"
    )

    # Two targets: the rates add up, and the file in flight is the one shown.
    job3 = FakeJob()
    rel3 = reporter(job3)
    rel3(
        event(
            targets=[
                {
                    "transport_badge": "lan",
                    "phase": "publishing",
                    "current_file": None,
                    "write_bps": 100,
                },
                {
                    "transport_badge": "local",
                    "phase": "writing",
                    "current_file": "B.MOV",
                    "write_bps": 50,
                },
            ]
        )
    )
    detail = job3.progress_calls[0][2]
    assert (
        detail["rate"] == 150.0
        and detail["current_file"] == "B.MOV"
        and detail["transport"] == "local"
    )


def test_reports_are_throttled_to_a_second_or_a_real_move():
    job, clock = FakeJob(), Clock()
    rel = reporter(job, clock)
    for _ in range(5):
        rel(event(fraction=0.250))  # the same figure, five times, inside one second
    assert len(job.progress_calls) == 1, "one report for a second of the engine's chatter"

    clock.now = 0.6
    rel(event(fraction=0.255))  # still inside the second, and less than 2% on
    assert len(job.progress_calls) == 1, "a move too small to see is not worth a report"
    rel(event(fraction=0.30))  # inside the second, but 5% on: worth saying now
    assert len(job.progress_calls) == 2

    clock.now = 1.7  # a second after the last report
    rel(event(fraction=0.31))
    assert len(job.progress_calls) == 3, "a second has passed: said again"
    clock.now = 1.9
    rel(event(fraction=0.312))  # and not again until a second is up, or it really moves
    assert len(job.progress_calls) == 3


def test_a_legacy_job_context_still_gets_a_plain_progress_call():
    job = FakeJob(takes_detail=False)
    rel = reporter(job)
    rel(event())
    # progress() refused the detail, so it was called again the way it takes; park still heard.
    assert job.progress_calls == [(0.25, "2000 of 8000 bytes verified", None)]
    assert job.calls[0][1]["detail"]["bytes_verified"] == 2000, "this park takes the detail even so"


def test_a_park_older_than_detail_is_reported_to_without_it():
    job = FakeJob(op_takes_detail=False)
    errors: list[str] = []
    rel = reporter(job, errors=errors)
    rel(event())  # refused, then retried without detail
    rel(event(fraction=0.9))  # and from then on it is not tried again
    assert [o for o, _ in job.calls] == ["ferry.transfer.progress", "ferry.transfer.progress"]
    assert all("detail" not in args for _, args in job.calls)
    assert errors == [], "a park without detail is not a failure"
    assert job.logs and "without detail" in job.logs[0]


def test_park_refusing_progress_does_not_stop_the_copy():
    class Refusing(FakeJob):
        def call(self, op, args=None):
            if op == "ferry.transfer.progress":
                raise Invalid("no such transfer")
            return super().call(op, args)

    job = Refusing()
    errors: list[str] = []
    rel = reporter(job, errors=errors)
    rel(event())
    rel(event(fraction=0.9))
    assert len(job.progress_calls) == 1, "the copy's own reporting stops after park refuses"
    assert errors and "did not confirm transfer progress" in errors[0]


# -- who may report ----------------------------------------------------------------


@pytest.fixture()
def transfer_with_a_job(call, card, tmp_path, no_queue):
    """A plan and its queued offload, with no machines layer: the transfer names no job."""
    destination = tmp_path / "landing"
    destination.mkdir()
    plan = call(
        "ferry.plan", {"source": str(card.path), "destination": str(destination), "probe": False}
    )
    return call("ferry.offload", {"plan": plan["id"]})


def test_an_agent_cannot_report_on_a_transfer_it_does_not_run(park, call, transfer_with_a_job):
    transfer = transfer_with_a_job["transfer"]
    for name, args in [
        ("ferry.transfer.start", {"transfer": transfer, "machine": "studio"}),
        ("ferry.transfer.progress", {"transfer": transfer, "done": 1, "total": 4}),
        ("ferry.receipt.record", {"kind": "copy", "status": "ok", "transfer": transfer}),
        ("ferry.send.fail", {"transfer": transfer, "errors": ["no"]}),
    ]:
        with pytest.raises(Forbidden, match="job that ran it"):
            invoke(park, name, args, actor="janet", surface="python")
    # Nothing was written: the transfer is still waiting, with no progress of its own.
    row = call("ferry.transfer.get", {"id": transfer})
    assert row["status"] == "queued" and row["done"] == 0


def test_a_reviewer_and_the_node_surface_may_still_report(park, call, transfer_with_a_job):
    from park.machines.contract import LocalJobContext

    transfer = transfer_with_a_job["transfer"]
    call(
        "ferry.transfer.progress",
        {"transfer": transfer, "done": 2, "total": 4, "detail": {"phase": "writing"}},
    )
    assert call("ferry.transfer.get", {"id": transfer})["done"] == 2

    # The node surface is how a job reaches the op; the detail rides on its event.
    context = LocalJobContext(
        park, plugin="ferry", name="copy", params={}, workdir=park.home, machine="studio"
    )
    context.call(
        "ferry.transfer.progress",
        {"transfer": transfer, "done": 3, "total": 4, "detail": {"phase": "verifying"}},
    )
    assert call("ferry.transfer.get", {"id": transfer})["done"] == 3
    seen = [e["data"] for e in park.events_since(None) if e["kind"] == "ferry.transfer.progress"]
    assert seen[-1]["detail"] == {"phase": "verifying"}
    assert seen[-2]["detail"] == {"phase": "writing"}
