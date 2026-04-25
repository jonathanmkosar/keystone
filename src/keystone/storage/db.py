"""SQLite connection helpers with sqlite-vec extension loading."""

from __future__ import annotations

import logging
import sqlite3
from contextlib import contextmanager
from pathlib import Path
from typing import Iterator

import sqlite_vec

log = logging.getLogger(__name__)


def _enable_extensions(conn: sqlite3.Connection) -> None:
    """Load sqlite-vec into the connection. Required for the semantic_index table."""
    try:
        conn.enable_load_extension(True)
        sqlite_vec.load(conn)
    finally:
        conn.enable_load_extension(False)


def connect(path: Path | str, *, read_only: bool = False) -> sqlite3.Connection:
    """Open a SQLite connection with sqlite-vec loaded.

    When read_only=True, the database is opened with mode=ro so writes raise
    sqlite3.OperationalError. Used by the run_sql tool to enforce read-only.
    """
    if read_only:
        uri = f"file:{Path(path).as_posix()}?mode=ro"
        conn = sqlite3.connect(uri, uri=True, isolation_level=None)
    else:
        conn = sqlite3.connect(str(path), isolation_level=None)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA foreign_keys = ON")
    conn.execute("PRAGMA journal_mode = WAL") if not read_only else None
    _enable_extensions(conn)
    return conn


@contextmanager
def connection(path: Path | str, *, read_only: bool = False) -> Iterator[sqlite3.Connection]:
    conn = connect(path, read_only=read_only)
    try:
        yield conn
    finally:
        conn.close()


def has_table(conn: sqlite3.Connection, name: str) -> bool:
    cur = conn.execute(
        "SELECT 1 FROM sqlite_master WHERE type IN ('table','view') AND name=?",
        (name,),
    )
    return cur.fetchone() is not None
