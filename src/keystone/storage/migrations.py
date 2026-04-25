"""Idempotent SQL migrations applied per stone.

Each stone gets:
  - ``data.db``    : ``stones_pkg/<name>/schema.sql`` (lives with the stone package)
  - ``memory.db``  : ``schema/memory.sql``              (generic, lives with the core)

Schemas use ``IF NOT EXISTS`` so re-running is safe. We track ``schema_version``
on each DB so future migrations can guard themselves.
"""

from __future__ import annotations

import logging
from pathlib import Path

from keystone.config import SETTINGS
from keystone.stones.base import Stone
from keystone.storage.db import connection

log = logging.getLogger(__name__)

CORE_SCHEMA_DIR = Path(__file__).parent / "schema"


def _read_core(name: str) -> str:
    return (CORE_SCHEMA_DIR / name).read_text(encoding="utf-8")


def _stone_schema(stone: Stone) -> str:
    """Locate ``schema.sql`` for this stone.

    Looks first inside the stone package directory
    (``stones_pkg/<name>/schema.sql``). Raises ``FileNotFoundError`` with a
    helpful hint if nothing is found.
    """

    pkg_root = SETTINGS.stone_packages_dir / stone.name
    candidate = pkg_root / "schema.sql"
    if candidate.is_file():
        return candidate.read_text(encoding="utf-8")
    raise FileNotFoundError(
        f"No schema.sql found for stone {stone.name!r}. "
        f"Expected at {candidate}. "
        f"Drop the stone package under {SETTINGS.stone_packages_dir} or set "
        f"KEYSTONE_STONES_PKG_DIR to point at the right folder."
    )


def _ensure_meta(conn) -> int:
    conn.execute(
        "CREATE TABLE IF NOT EXISTS schema_meta ("
        " key TEXT PRIMARY KEY, value TEXT NOT NULL)"
    )
    cur = conn.execute("SELECT value FROM schema_meta WHERE key='version'")
    row = cur.fetchone()
    return int(row[0]) if row else 0


def _set_version(conn, version: int) -> None:
    conn.execute(
        "INSERT INTO schema_meta(key, value) VALUES('version', ?)"
        " ON CONFLICT(key) DO UPDATE SET value=excluded.value",
        (str(version),),
    )


_MEMORY_V2_COLUMNS: tuple[tuple[str, str], ...] = (
    ("last_updated_at", "INTEGER"),
    ("deleted_at", "INTEGER"),
    ("superseded_by_id", "INTEGER"),
    ("supersedes_id", "INTEGER"),
)

_MEMORY_V2_INDEXES: tuple[tuple[str, str], ...] = (
    (
        "idx_observations_deleted_at",
        "CREATE INDEX IF NOT EXISTS idx_observations_deleted_at "
        "ON observations(deleted_at)",
    ),
    (
        "idx_observations_superseded_by",
        "CREATE INDEX IF NOT EXISTS idx_observations_superseded_by "
        "ON observations(superseded_by_id)",
    ),
)


def _migrate_memory_v2(conn) -> None:
    """Backfill v2 columns + indexes on the observations table.

    Idempotent. For fresh installs the columns already exist (memory.sql
    creates them inline); we still ensure the indexes and the version bump.
    """

    existing = {r[1] for r in conn.execute("PRAGMA table_info(observations)")}
    for col, decl in _MEMORY_V2_COLUMNS:
        if col not in existing:
            conn.execute(f"ALTER TABLE observations ADD COLUMN {col} {decl}")
    for _name, sql in _MEMORY_V2_INDEXES:
        conn.execute(sql)


def migrate_stone(stone: Stone) -> None:
    """Apply schema to ``data.db`` and ``memory.db`` for a stone."""

    data_sql = _stone_schema(stone)
    memory_sql = _read_core("memory.sql")

    stone.root.mkdir(parents=True, exist_ok=True)

    with connection(stone.data_db) as conn:
        conn.executescript(data_sql)
        _ensure_meta(conn)
        _set_version(conn, 1)
    log.info("Migrated data.db for %s", stone.name)

    with connection(stone.memory_db) as conn:
        conn.executescript(memory_sql)
        _ensure_meta(conn)
        _migrate_memory_v2(conn)
        _set_version(conn, 2)
    log.info("Migrated memory.db for %s (v2)", stone.name)
