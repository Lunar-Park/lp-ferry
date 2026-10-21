"""The legacy/vNext ``projects`` name collision.

Two different entities wanted the table name ``projects``: the legacy
Resolve audit table (``file_ferry.log``) and the vNext project entity
(migration 002). Both created it with ``CREATE TABLE IF NOT EXISTS``, so
whichever landed first won the name and the other was silently wrong.

On a fresh database that broke the second command, in either order:

* vNext first, then ``ferry log`` —
  ``sqlite3.OperationalError: no such column: run_id``
* ``ferry log`` first, then ``ferry destination save`` —
  ``RuntimeError: database schema_version 7 is newer than target 6``

The green test suite did not catch either, because every test used one
owner at a time.

See ADR-0003 (application persistence model).
"""

from __future__ import annotations

import sqlite3
from datetime import UTC, datetime
from pathlib import Path

import pytest

from file_ferry.application.service import ApplicationService
from file_ferry.legacy_schema import LEGACY_PROJECTS_TABLE, LEGACY_VERSION_KEY
from file_ferry.log import LogStore
from file_ferry.models import ProjectRecord
from file_ferry.persistence import runner
from file_ferry.persistence.schema_meta import CURRENT_VERSION_KEY


def _bootstrap_service(db_path: Path, app_data_dir: Path) -> ApplicationService:
    service = ApplicationService(db_path=db_path, app_data_dir=app_data_dir)
    service.bootstrap()
    return service


def _columns(db_path: Path, table: str) -> set[str]:
    with sqlite3.connect(db_path) as conn:
        return {row[1] for row in conn.execute(f"PRAGMA table_info({table})")}


def _tables(db_path: Path) -> set[str]:
    with sqlite3.connect(db_path) as conn:
        rows = conn.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall()
    return {row[0] for row in rows}


def _stamp(db_path: Path, key: str) -> str | None:
    with sqlite3.connect(db_path) as conn:
        row = conn.execute("SELECT value FROM schema_meta WHERE key = ?", (key,)).fetchone()
    return None if row is None else row[0]


def _make_legacy_log_database(db_path: Path) -> None:
    """Build a database the way the legacy audit log used to build one.

    That is: the legacy Resolve table under the bare name ``projects``,
    and the log's own counter stamped into the migration runner's key.
    """
    conn = sqlite3.connect(db_path)
    conn.executescript(
        """
        CREATE TABLE schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
        CREATE TABLE runs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            started_at TEXT NOT NULL,
            finished_at TEXT,
            command TEXT NOT NULL,
            config_hash TEXT,
            status TEXT NOT NULL,
            error TEXT
        );
        CREATE TABLE projects (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            path TEXT NOT NULL,
            manifest_path TEXT,
            run_id INTEGER REFERENCES runs(id),
            resolution TEXT,
            frame_rate TEXT,
            color_space TEXT,
            bin_count INTEGER,
            timeline_count INTEGER,
            resolve_version TEXT,
            created_at TEXT NOT NULL
        );
        CREATE INDEX idx_projects_run_id ON projects(run_id);
        INSERT INTO schema_meta (key, value) VALUES ('schema_version', '7');
        INSERT INTO runs (started_at, command, status)
            VALUES ('2026-10-01T00:00:00+00:00', 'ferry run ./raw', 'success');
        INSERT INTO projects (name, path, run_id, created_at)
            VALUES ('legacy show', '/shows/legacy show', 1, '2026-10-01T00:01:00+00:00');
        """
    )
    conn.commit()
    conn.close()


class TestBothOrdersWork:
    """The two orderings that used to brick a fresh install."""

    def test_legacy_log_then_vnext_service(self, tmp_path: Path) -> None:
        db_path = tmp_path / "ferry.db"
        LogStore(db_path).initialize()
        _bootstrap_service(db_path, tmp_path / "app")

        assert "organization_profiles" in _tables(db_path)
        assert "projects" in _tables(db_path)

    def test_vnext_service_then_legacy_log(self, tmp_path: Path) -> None:
        db_path = tmp_path / "ferry.db"
        _bootstrap_service(db_path, tmp_path / "app")
        LogStore(db_path).initialize()

        assert "organization_profiles" in _tables(db_path)
        assert "projects" in _tables(db_path)

    def test_both_orders_leave_the_same_two_tables(self, tmp_path: Path) -> None:
        """The name ``projects`` belongs to the vNext entity either way."""
        first = tmp_path / "first.db"
        LogStore(first).initialize()
        _bootstrap_service(first, tmp_path / "app-a")

        second = tmp_path / "second.db"
        _bootstrap_service(second, tmp_path / "app-b")
        LogStore(second).initialize()

        for db_path in (first, second):
            assert "working_root" in _columns(db_path, "projects")
            assert "run_id" in _columns(db_path, LEGACY_PROJECTS_TABLE)

    def test_the_legacy_log_does_not_stamp_the_runners_key(self, tmp_path: Path) -> None:
        """``schema_meta.schema_version`` belongs to the migration runner."""
        db_path = tmp_path / "ferry.db"
        LogStore(db_path).initialize()

        assert _stamp(db_path, CURRENT_VERSION_KEY) is None
        assert _stamp(db_path, LEGACY_VERSION_KEY) == "7"

    def test_the_runner_owns_the_version_after_both_have_run(self, tmp_path: Path) -> None:
        db_path = tmp_path / "ferry.db"
        LogStore(db_path).initialize()
        _bootstrap_service(db_path, tmp_path / "app")

        target = max(m.version for m in runner.discover_migrations())
        assert _stamp(db_path, CURRENT_VERSION_KEY) == str(target)


class TestLegacyDatabaseIsRepaired:
    """A database the legacy log built must still be usable."""

    def test_the_legacy_table_is_adopted_and_its_rows_kept(self, tmp_path: Path) -> None:
        db_path = tmp_path / "ferry.db"
        _make_legacy_log_database(db_path)

        _bootstrap_service(db_path, tmp_path / "app")

        tables = _tables(db_path)
        assert LEGACY_PROJECTS_TABLE in tables
        assert "projects" in tables
        # The vNext entity now owns the name.
        assert "working_root" in _columns(db_path, "projects")

        with sqlite3.connect(db_path) as conn:
            rows = conn.execute(f"SELECT name, path FROM {LEGACY_PROJECTS_TABLE}").fetchall()
        assert rows == [("legacy show", "/shows/legacy show")]

    def test_the_runners_stale_stamp_is_replaced(self, tmp_path: Path) -> None:
        db_path = tmp_path / "ferry.db"
        _make_legacy_log_database(db_path)

        _bootstrap_service(db_path, tmp_path / "app")

        target = max(m.version for m in runner.discover_migrations())
        assert _stamp(db_path, CURRENT_VERSION_KEY) == str(target)

    def test_the_legacy_log_can_still_write_projects(self, tmp_path: Path) -> None:
        db_path = tmp_path / "ferry.db"
        _make_legacy_log_database(db_path)
        _bootstrap_service(db_path, tmp_path / "app")

        store = LogStore(db_path)
        store.initialize()
        store.insert_project(
            ProjectRecord(
                name="new show",
                path="/shows/new show",
                manifest_path=None,
                run_id=1,
                resolution=None,
                frame_rate=None,
                color_space=None,
                bin_count=0,
                timeline_count=0,
                resolve_version=None,
                created_at=datetime.now(UTC),
            )
        )

        with sqlite3.connect(db_path) as conn:
            rows = conn.execute(f"SELECT name FROM {LEGACY_PROJECTS_TABLE} ORDER BY id").fetchall()
        assert [row[0] for row in rows] == ["legacy show", "new show"]


class TestTheRenameHelper:
    """The helper both owners share."""

    def test_rows_move_across_when_the_target_table_already_exists(self, tmp_path: Path) -> None:
        """A database the old migration 001 already touched.

        That migration created ``legacy_resolve_projects`` but never
        renamed a legacy ``projects`` table that was already there, so
        both exist and the name ``projects`` is still wrong. The rows
        have to survive the fix.
        """
        db_path = tmp_path / "ferry.db"
        conn = sqlite3.connect(db_path)
        conn.executescript(
            """
            CREATE TABLE schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
            INSERT INTO schema_meta (key, value) VALUES ('schema_version', '6');
            CREATE TABLE runs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                started_at TEXT NOT NULL,
                finished_at TEXT,
                command TEXT NOT NULL,
                config_hash TEXT,
                status TEXT NOT NULL,
                error TEXT
            );
            CREATE TABLE legacy_resolve_projects (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                path TEXT NOT NULL,
                manifest_path TEXT,
                run_id INTEGER REFERENCES runs(id),
                resolution TEXT,
                frame_rate TEXT,
                color_space TEXT,
                bin_count INTEGER,
                timeline_count INTEGER,
                resolve_version TEXT,
                created_at TEXT NOT NULL
            );
            CREATE TABLE projects (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                path TEXT NOT NULL,
                manifest_path TEXT,
                run_id INTEGER REFERENCES runs(id),
                resolution TEXT,
                frame_rate TEXT,
                color_space TEXT,
                bin_count INTEGER,
                timeline_count INTEGER,
                resolve_version TEXT,
                created_at TEXT NOT NULL
            );
            INSERT INTO projects (name, path, created_at)
                VALUES ('old show', '/shows/old show', '2026-10-01T00:01:00+00:00');
            """
        )
        conn.commit()
        conn.close()

        _bootstrap_service(db_path, tmp_path / "app")

        assert "working_root" in _columns(db_path, "projects")
        with sqlite3.connect(db_path) as conn:
            rows = conn.execute(f"SELECT name FROM {LEGACY_PROJECTS_TABLE}").fetchall()
        assert [row[0] for row in rows] == ["old show"]

    def test_a_vnext_projects_table_is_never_touched(self, tmp_path: Path) -> None:
        db_path = tmp_path / "ferry.db"
        _bootstrap_service(db_path, tmp_path / "app")

        with sqlite3.connect(db_path) as conn:
            from file_ferry.legacy_schema import rename_legacy_projects_table

            assert rename_legacy_projects_table(conn) is False
            columns = {row[1] for row in conn.execute("PRAGMA table_info(projects)")}
        assert "working_root" in columns

    @pytest.mark.parametrize("orders", [("legacy", "vnext"), ("vnext", "legacy")])
    def test_initializing_twice_is_stable(self, tmp_path: Path, orders: tuple[str, str]) -> None:
        db_path = tmp_path / "ferry.db"
        for step in orders:
            if step == "legacy":
                LogStore(db_path).initialize()
            else:
                _bootstrap_service(db_path, tmp_path / "app")
        LogStore(db_path).initialize()
        _bootstrap_service(db_path, tmp_path / "app")

        assert "working_root" in _columns(db_path, "projects")
        assert "run_id" in _columns(db_path, LEGACY_PROJECTS_TABLE)
