"""Reservation-conflict lookups (spec §6.4, cross-job serialization).

One rule underneath: a path held by a *different* live job is a conflict
and stops the plan; a path this execution already holds is not a
conflict, because a job cannot be in conflict with itself.

The lookup binds one SQL parameter per path, so it is chunked under
SQLite's variable limit. A 1.7M-file source is an ordinary backup
drive, and before the chunking the whole transfer died with "too many
SQL variables" before a byte was copied.
"""

from __future__ import annotations

import sqlite3
from pathlib import Path

from file_ferry.persistence import runner
from file_ferry.persistence.connection import transaction
from file_ferry.persistence.repositories import transfer_executions as exec_repo


def _fresh_db(tmp_path: Path) -> Path:
    db = tmp_path / "ferry.db"
    conn = sqlite3.connect(str(db))
    conn.close()
    runner.apply_pending(db, runner.discover_migrations(), tmp_path / "backups")
    return db


def _add_execution(
    conn: sqlite3.Connection, execution_id: str, job_id: str, dest_root: str
) -> None:
    conn.execute(
        "INSERT INTO transfer_executions (id, job_id, plan_id, fingerprint, dest_root, "
        "binding_json, state, started_at, updated_at) "
        "VALUES (?, ?, 'plan-1', 'fingerprint-1', ?, '{}', 'running', "
        "'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')",
        (execution_id, job_id, dest_root),
    )


def _reserve(
    conn: sqlite3.Connection, dest_root: str, rel_paths: list[str], execution_id: str
) -> None:
    exec_repo.insert_reservations(
        conn,
        dest_root=dest_root,
        rel_paths=rel_paths,
        execution_id=execution_id,
        acquired_at="2026-01-01T00:00:00Z",
    )


def test_a_path_held_by_another_execution_is_a_conflict(tmp_path: Path) -> None:
    db = _fresh_db(tmp_path)
    with transaction(db) as conn:
        _add_execution(conn, "exec-a", "job-a", "/dest")
        _reserve(conn, "/dest", ["held.txt"], "exec-a")

        conflicts = exec_repo.active_reservation_conflicts(conn, "/dest", ["held.txt", "free.txt"])

    assert conflicts == {"held.txt": "job-a"}, "the holder is named by job id, for the operator"
    assert "free.txt" not in conflicts, "an unreserved path is not a conflict"


def test_a_path_held_by_this_execution_is_not_a_conflict(tmp_path: Path) -> None:
    db = _fresh_db(tmp_path)
    with transaction(db) as conn:
        _add_execution(conn, "exec-a", "job-a", "/dest")
        _reserve(conn, "/dest", ["held.txt"], "exec-a")

        conflicts = exec_repo.active_reservation_conflicts(
            conn, "/dest", ["held.txt"], exclude_execution_id="exec-a"
        )

    assert conflicts == {}, "a job cannot be in conflict with itself"


def test_a_released_reservation_is_not_a_conflict(tmp_path: Path) -> None:
    db = _fresh_db(tmp_path)
    with transaction(db) as conn:
        _add_execution(conn, "exec-a", "job-a", "/dest")
        _reserve(conn, "/dest", ["held.txt"], "exec-a")
        exec_repo.release_reservations(conn, "exec-a", released_at="2026-01-02T00:00:00Z")

        conflicts = exec_repo.active_reservation_conflicts(conn, "/dest", ["held.txt"])

    assert conflicts == {}, "a released reservation no longer blocks anything"


def test_a_reservation_on_another_destination_is_not_a_conflict(tmp_path: Path) -> None:
    db = _fresh_db(tmp_path)
    with transaction(db) as conn:
        _add_execution(conn, "exec-a", "job-a", "/dest")
        _reserve(conn, "/other-dest", ["held.txt"], "exec-a")

        conflicts = exec_repo.active_reservation_conflicts(conn, "/dest", ["held.txt"])

    assert conflicts == {}, "reservations are per destination root"


def test_a_path_list_larger_than_the_chunk_size_gives_the_same_mapping(tmp_path: Path) -> None:
    """The chunked answer must equal the unchunked one, exactly.

    Before the chunking this raised "too many SQL variables" instead of
    answering, so the comparison is against the same question asked with
    the limit raised — not against a hand-written expectation.
    """
    db = _fresh_db(tmp_path)
    with transaction(db) as conn:
        _add_execution(conn, "exec-a", "job-a", "/dest")
        held = [f"held-{i}.txt" for i in range(5)]
        _reserve(conn, "/dest", held, "exec-a")
        paths = [f"free-{i}.txt" for i in range(5)] + held

        # A variable limit small enough that the whole list cannot be bound
        # in one statement. The chunk size is read from the connection, so
        # this is the real code path and not a patched-in constant.
        conn.setlimit(sqlite3.SQLITE_LIMIT_VARIABLE_NUMBER, 10)
        chunked = exec_repo.active_reservation_conflicts(conn, "/dest", paths)

        conn.setlimit(sqlite3.SQLITE_LIMIT_VARIABLE_NUMBER, 100_000)
        unchunked = exec_repo.active_reservation_conflicts(conn, "/dest", paths)

    assert chunked == unchunked, "chunking must not change the answer"
    assert chunked == {path: "job-a" for path in held}, "every held path is found, across chunks"


def test_an_empty_path_list_asks_nothing(tmp_path: Path) -> None:
    db = _fresh_db(tmp_path)
    with transaction(db) as conn:
        assert exec_repo.active_reservation_conflicts(conn, "/dest", []) == {}
