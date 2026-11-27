"""The ferry engine as Park sees it: a client for the local Go daemon.

Park keeps its ops, events, records and project filing. Everything that touches bytes —
planning, the verified copy, machine-to-machine transport and re-verification — is done
by ``ferry daemon`` on the machine running the op or job, reached over its user-only
socket. Nothing here copies, hashes for publication or moves a file itself.

The socket is ``$FERRY_SOCKET`` or ``~/.ferry/ferry.sock``; the binary for receipt
verification is ``$FERRY_BIN`` or ``ferry`` on ``PATH``.
"""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import tempfile
import time
from collections.abc import Callable, Iterator
from contextlib import contextmanager, suppress
from pathlib import Path
from typing import Any

from .probe import kind_for

# Park's default organize template, which the Go "mirror-source" preset reproduces:
# every file lands at <destination>/<its path below the source>.
DEFAULT_TEMPLATE = "{root}/{source_relpath}/{filename}{ext}"
MIRROR_PRESET = "mirror-source"
CONFLICT_POLICY = {"skip": "skip_identical", "rename": "keep_both"}
TERMINAL = {"completed", "partial", "failed", "canceled", "needs_attention"}
PAGE = 1000

# JSON-RPC codes the daemon uses (docs/IPC-PROTOCOL.md).
STALE, BLOCKED, NOT_FOUND = -32001, -32002, -32004


class EngineError(Exception):
    """The daemon answered with an error, or could not be reached."""

    def __init__(self, message: str, code: int = 0):
        super().__init__(message)
        self.code = code


class EngineUnavailableError(EngineError):
    """No daemon is listening."""


class AwaitingApprovalError(EngineError):
    """The engine will not start this plan until a person approves it.

    Not a failure: the plan is parked in ``awaiting_approval`` by the engine's agent policy,
    and the job that asked turns it into a Park clearance (``ops._request_clearance``) rather
    than ending as an error. The plan's own ``reasons`` say what the policy objected to.
    """

    def __init__(self, message: str, *, plan_id: str = "", reasons: list[str] | None = None):
        super().__init__(message)
        self.plan_id = plan_id
        self.reasons = list(reasons or [])


def socket_path() -> str:
    return os.environ.get("FERRY_SOCKET") or str(Path.home() / ".ferry" / "ferry.sock")


def binary() -> str:
    found = os.environ.get("FERRY_BIN") or shutil.which("ferry")
    if not found:
        raise EngineUnavailableError(
            "the ferry binary is not installed (set FERRY_BIN or put ferry on PATH)"
        )
    return found


class _Conn:
    """One newline-delimited JSON-RPC connection."""

    def __init__(self, path: str, timeout: float | None):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(timeout)
        try:
            self.sock.connect(path)
        except OSError as exc:
            self.sock.close()
            raise EngineUnavailableError(
                f"the ferry daemon is not running at {path} (start it with `ferry daemon`): {exc}"
            ) from exc
        self.buf = b""
        self.next = 0

    def close(self) -> None:
        self.sock.close()

    def _lines(self) -> Iterator[dict]:
        while True:
            while b"\n" in self.buf:
                line, self.buf = self.buf.split(b"\n", 1)
                if line.strip():
                    yield json.loads(line)
            chunk = self.sock.recv(1 << 16)
            if not chunk:
                raise EngineError("the ferry daemon closed the connection")
            self.buf += chunk

    def call(self, method: str, params: dict | None = None, events: list | None = None) -> Any:
        self.next += 1
        msg = {"jsonrpc": "2.0", "id": self.next, "method": method, "params": params or {}}
        self.sock.sendall(json.dumps(msg).encode() + b"\n")
        for reply in self._lines():
            if reply.get("method") == "event":
                if events is not None:
                    events.append(reply["params"])
                continue
            if reply.get("id") != self.next:
                continue
            if reply.get("error"):
                err = reply["error"]
                raise EngineError(err.get("message", "ferry daemon error"), err.get("code", 0))
            return reply.get("result")
        raise EngineError("no reply")

    def event(self) -> dict:
        for reply in self._lines():
            if reply.get("method") == "event":
                return reply["params"]
        raise EngineError("no event")


@contextmanager
def connect(timeout: float | None = 600) -> Iterator[_Conn]:
    conn = _Conn(socket_path(), timeout)
    try:
        yield conn
    finally:
        conn.close()


def call(method: str, params: dict | None = None, timeout: float | None = 600) -> Any:
    with connect(timeout) as conn:
        return conn.call(method, params)


# -- planning ------------------------------------------------------------------------


class PlanError(ValueError):
    """The request cannot become a plan Park can run (bad template, blocking findings)."""


def preset_for(template: str) -> str:
    if template != DEFAULT_TEMPLATE:
        raise PlanError(
            f"organize.template {template!r} is no longer applied by the plugin: folder layouts are "
            "Ferry presets now (`ferry preset list`). Remove the template to mirror the source."
        )
    return MIRROR_PRESET


def policy_for(on_conflict: str) -> str:
    if on_conflict not in CONFLICT_POLICY:
        raise PlanError(
            f"organize.on_conflict = {on_conflict!r}: Ferry never overwrites; use 'skip' or 'rename'"
        )
    return CONFLICT_POLICY[on_conflict]


def entries(plan_id: str) -> list[dict]:
    """Every entry of a Go plan, paged."""
    out: list[dict] = []
    cursor = ""
    while True:
        page = call("plan.entries", {"plan_id": plan_id, "cursor": cursor, "limit": PAGE})
        out.extend(page.get("entries") or [])
        cursor = page.get("next_cursor") or ""
        if not cursor:
            return out


def _probe(entry: dict) -> dict:
    meta = entry.get("metadata") or ""
    try:
        return json.loads(meta) if meta else {}
    except ValueError:
        return {}


def build(
    source: str,
    destination: str,
    *,
    template: str,
    on_conflict: str,
    label: str | None = None,
    source_peer: str | None = None,
) -> dict:
    """Plan with the Go engine and return it in the shape ``ferry.plan.created`` records.

    The plan id and fingerprint are the engine's, so the copy job starts exactly the plan
    that was reviewed. Excluded system artifacts are not Park items; anything the engine
    would need a human to decide refuses the plan here, with the engine's reasons.
    """
    src_label = label or Path(source).name
    req: dict[str, Any] = {
        "sources": [
            {
                "path": source,
                "label": src_label,
                **({"peer_id": source_peer} if source_peer else {}),
            }
        ],
        "destinations": [{"path": destination}],
        "preset_id": preset_for(template),
        "conflict_policy": policy_for(on_conflict),
        "actor": "human",
    }
    summary = call("plan.create", req)
    if summary.get("blocking_count"):
        reasons = [
            f"{f['kind']}: {f.get('source_rel', '')} {f['message']}".strip()
            for f in summary.get("findings") or []
            if f.get("severity") == "blocking"
        ]
        raise PlanError(
            "the plan needs a decision first: " + "; ".join(reasons or ["blocking items"])
        )
    # Destinations are reported under the folder as it was named (the engine itself binds
    # the resolved directory), so Park's paths match what the caller asked for.
    root = destination
    items = []
    for e in entries(summary["plan_id"]):
        if e["action"] not in ("copy", "skip_identical") or e.get("kind") == "dir":
            continue
        items.append(
            {
                "rel": e["source_rel"],
                "source": e["source_path"],
                "dest": root.rstrip("/") + "/" + e["dest_rel_path"],
                "size": e["size"],
                "mtime": e["mtime_ns"] / 1e9 if e.get("mtime_ns") else None,
                "kind": kind_for(Path(e["source_rel"])),
                "action": "skip" if e["action"] == "skip_identical" else "copy",
                "probe": _probe(e),
            }
        )
    items.sort(key=lambda item: item["rel"])
    by_kind: dict[str, int] = {}
    for item in items:
        by_kind[item["kind"]] = by_kind.get(item["kind"], 0) + 1
    return {
        "id": summary["plan_id"],
        "label": src_label,
        "source": source,
        "destination": destination,
        "template": template,
        "on_conflict": on_conflict,
        "fingerprint": summary["fingerprint"],
        "files": len(items),
        "copies": sum(1 for item in items if item["action"] == "copy"),
        "skips": sum(1 for item in items if item["action"] == "skip"),
        "bytes": sum(item["size"] for item in items),
        "by_kind": by_kind,
        "items": items,
    }


# -- running -------------------------------------------------------------------------


def find_transfer(plan_id: str) -> dict | None:
    """The engine transfer that already ran (or is running) this plan, if any."""
    for row in call("transfer.list", {"limit": 1000}) or []:
        if row.get("plan_id") == plan_id:
            return row
    return None


def run(
    plan_id: str,
    fingerprint: str,
    *,
    on_progress: Callable[[dict], None],
    cancelled: Callable[[], bool],
    poll: float = 0.25,
) -> dict:
    """Start (or, after a retry, resume) the engine transfer for a plan and wait for it.

    Events arrive in order, so every progress event is passed on before the terminal
    ``transfer.state`` (sent once the receipt exists). Cancellation is relayed to the
    engine, which stops within one chunk, keeps verified files and records a partial
    receipt. Returns ``transfer.get`` for the finished transfer.
    """
    with connect(timeout=30) as conn:
        events: list[dict] = []
        conn.call("events.subscribe", {"topics": ["transfer.*"]}, events)
        existing = find_transfer(plan_id)
        if existing is None:
            started = conn.call(
                "transfer.start", {"plan_id": plan_id, "fingerprint": fingerprint}, events
            )
            tid = started.get("transfer_id")
            if started.get("status") != "started":
                if started.get("status") == "awaiting_approval":
                    # The engine's agent policy parked the plan. A person has to say yes; the
                    # job turns this into a clearance instead of a failure.
                    raise AwaitingApprovalError(
                        "the engine is waiting for a person to approve this plan",
                        plan_id=plan_id,
                        reasons=[str(reason) for reason in (started.get("reasons") or [])],
                    )
                raise EngineError(
                    f"the engine did not start the plan: {started.get('status')} {started.get('reason', '')}".strip()
                )
        else:
            tid = existing["id"]
            if existing["status"] in ("needs_attention", "partial", "failed", "canceled"):
                conn.call("transfer.resume", {"transfer_id": tid}, events)
        conn.sock.settimeout(poll)
        cancel_sent = False
        last_check = time.monotonic()
        while True:
            done = False
            for ev in events:
                data = ev.get("data") or {}
                if data.get("transfer_id") != tid:
                    continue
                if ev.get("topic") == "transfer.progress":
                    on_progress(data)
                elif ev.get("topic") == "transfer.state" and data.get("status") in TERMINAL:
                    done = True
                elif ev.get("topic") == "transfer.error":
                    raise EngineError(f"the engine stopped transfer {tid}: {data.get('error')}")
            events.clear()
            if cancelled() and not cancel_sent:
                with suppress(EngineError):
                    call("transfer.cancel", {"transfer_id": tid})
                cancel_sent = True
            if done or time.monotonic() - last_check > 2:
                # The state event is authoritative; polling covers a dropped event.
                last_check = time.monotonic()
                state = call("transfer.get", {"transfer_id": tid})
                if (
                    state["transfer"]["status"] in TERMINAL
                    and not state.get("running")
                    and state.get("receipt_id")
                ):
                    return state
                if not state.get("running") and state["transfer"]["status"] not in TERMINAL:
                    raise EngineError(
                        f"the engine is no longer running transfer {tid} but it did not finish "
                        f"({state['transfer']['status']}); `ferry resume {tid}` continues it"
                    )
            with suppress(TimeoutError):
                events.append(conn.event())


def receipt(transfer_id: str) -> dict:
    return call("receipt.get", {"transfer_id": transfer_id})


# -- verification --------------------------------------------------------------------


def verify_receipt(body: dict, root: str | None = None) -> dict:
    """Re-verify an engine receipt with physical readback, without needing a daemon.

    Returns the engine's report; exit codes 0-3 are findings, 4 means it could not verify.
    """
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as fh:
        json.dump(body, fh)
        path = fh.name
    try:
        args = [binary(), "verify", "--receipt", path, "--json"]
        if root:
            args += ["--root", root]
        out = subprocess.run(args, capture_output=True, text=True, timeout=24 * 3600, check=False)
        if out.returncode == 4 and not out.stdout.strip():
            raise EngineError(out.stderr.strip() or "ferry verify could not run")
        return json.loads(out.stdout)
    finally:
        os.unlink(path)


# -- machines ------------------------------------------------------------------------


def info() -> dict:
    """This machine's engine: its peer id and paired peers."""
    status = call("daemon.status")
    if not status.get("peer_id"):
        raise EngineUnavailableError(
            "this machine's ferry daemon runs without P2P (`ferry daemon --no-p2p`)"
        )
    peers = call("peer.list") or []
    return {
        "peer_id": status["peer_id"],
        "paired": [p["id"] for p in peers if not p.get("revoked")],
    }


def pair_start() -> str:
    return call("peer.pair.start", {"ttl_seconds": 120})["code"]


def pair_complete(code: str) -> dict:
    return call("peer.pair.complete", {"code": code})
