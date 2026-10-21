"""Shared vocabulary for the legacy audit-log schema.

ADR-0003 records two decisions that the code has to agree with, and the
legacy audit log in :mod:`file_ferry.log` had quietly drifted away from
both of them:

* The legacy Resolve project table is named ``legacy_resolve_projects``
  so that the name ``projects`` belongs to the vNext entity created by
  migration 002.
* ``schema_meta.schema_version`` belongs to the numbered migration
  runner. The legacy log keeps its own counter, under
  ``legacy_schema_version``.

Two different entities both wanting the table name ``projects`` is what
broke a fresh install: whichever schema landed first won the name, and
the loser died on the next command with ``no such column: run_id`` (or,
the other way round, ``database schema_version 7 is newer than target
6``).

This module holds the names and the shape test in one place so the
legacy log and the migration runner cannot drift apart again.
"""

from __future__ import annotations

import sqlite3

#: The legacy Resolve project table, per ADR-0003 and migration 001.
LEGACY_PROJECTS_TABLE = "legacy_resolve_projects"

#: Index over the legacy table's ``run_id`` column.
LEGACY_PROJECTS_INDEX = "idx_legacy_resolve_projects_run_id"

#: The name the legacy log used before ADR-0003's rename landed.
STALE_PROJECTS_INDEX = "idx_projects_run_id"

#: The key the migration runner stamps its version under. It is the
#: runner's key, not the legacy log's.
RUNNER_VERSION_KEY = "schema_version"

#: The key the legacy audit log stamps its own counter under. It is
#: deliberately NOT ``schema_version`` — that key is the runner's.
LEGACY_VERSION_KEY = "legacy_schema_version"

#: A table created by migration 002 and never by the legacy log. Its
#: presence proves the numbered migration runner has migrated this
#: database, which is what makes the recorded version trustworthy.
VNEXT_MARKER_TABLE = "organization_profiles"

# Columns that identify the legacy shape (a Resolve project row) and the
# vNext shape (a project entity). The two tables share the column
# ``name``; ``path``/``run_id`` versus ``working_root``/``storage_policy``
# is what separates them.
_LEGACY_MARKER_COLUMNS = ("path", "run_id")
_VNEXT_MARKER_COLUMNS = ("working_root", "storage_policy")


def table_exists(conn: sqlite3.Connection, name: str) -> bool:
    """Return True when a table (or view) named ``name`` exists."""
    row = conn.execute(
        "SELECT 1 FROM sqlite_master WHERE type IN ('table','view') AND name = ?",
        (name,),
    ).fetchone()
    return row is not None


def table_columns(conn: sqlite3.Connection, name: str) -> set[str]:
    """Return the column names of ``name``; empty when the table is absent.

    ``PRAGMA table_info`` reports the column name at index 1, which
    reads the same whether the connection returns tuples or
    ``sqlite3.Row`` objects.
    """
    return {row[1] for row in conn.execute(f"PRAGMA table_info({name})")}


def is_legacy_projects_table(conn: sqlite3.Connection) -> bool:
    """Return True when ``projects`` exists in the legacy Resolve shape.

    A vNext ``projects`` table is never treated as legacy: it is the
    entity the whole application reads and writes.
    """
    columns = table_columns(conn, "projects")
    if not columns:
        return False
    if any(marker in columns for marker in _VNEXT_MARKER_COLUMNS):
        return False
    return all(marker in columns for marker in _LEGACY_MARKER_COLUMNS)


def runner_has_migrated(conn: sqlite3.Connection) -> bool:
    """Return True when the numbered migration runner has migrated this database.

    Only migration 002 creates the vNext entity tables, and the legacy
    audit log never creates them. Their presence therefore means the
    runner has been here — and anything else in ``schema_meta`` is then
    the runner's own bookkeeping.
    """
    return table_exists(conn, VNEXT_MARKER_TABLE)


def rename_legacy_projects_table(conn: sqlite3.Connection) -> bool:
    """Move a legacy-shaped ``projects`` table to ``legacy_resolve_projects``.

    Returns True when the database was changed.

    Two shapes of existing database need handling:

    * The target table does not exist — the plain rename.
    * The target table already exists — a database that ran the old
      migration 001 (which created ``legacy_resolve_projects`` but never
      renamed an existing legacy ``projects`` table). The rows are moved
      across and the legacy table is dropped, so the name ``projects``
      is free for the vNext entity.
    """
    if not is_legacy_projects_table(conn):
        return False

    # The index keeps its name across a table rename, so drop it first
    # and recreate it under the name the rest of the repo expects.
    conn.execute(f"DROP INDEX IF EXISTS {STALE_PROJECTS_INDEX}")

    if table_exists(conn, LEGACY_PROJECTS_TABLE):
        source = table_columns(conn, "projects")
        target = table_columns(conn, LEGACY_PROJECTS_TABLE)
        shared = sorted(source & target)
        if shared:
            collist = ", ".join(f'"{column}"' for column in shared)
            conn.execute(
                f"INSERT OR IGNORE INTO {LEGACY_PROJECTS_TABLE} ({collist}) "
                f"SELECT {collist} FROM projects"
            )
        conn.execute("DROP TABLE projects")
    else:
        conn.execute(f"ALTER TABLE projects RENAME TO {LEGACY_PROJECTS_TABLE}")

    conn.execute(
        f"CREATE INDEX IF NOT EXISTS {LEGACY_PROJECTS_INDEX} ON {LEGACY_PROJECTS_TABLE}(run_id)"
    )
    return True
