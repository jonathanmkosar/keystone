"""Best-effort guard that rejects non-SELECT SQL before opening the DB.

Defense in depth: the run_sql tool also opens the DB read-only via mode=ro,
which makes write attempts fail at the SQLite layer regardless of what slips
past this regex.
"""

from __future__ import annotations

import re

# Strip line and block comments so they can't hide forbidden tokens.
_LINE_COMMENT = re.compile(r"--.*?$", re.MULTILINE)
_BLOCK_COMMENT = re.compile(r"/\*.*?\*/", re.DOTALL)

# Words that indicate writes / DDL / pragma misuse.
_FORBIDDEN = re.compile(
    r"\b(INSERT|UPDATE|DELETE|REPLACE|DROP|ALTER|CREATE|TRUNCATE|"
    r"ATTACH|DETACH|VACUUM|REINDEX|ANALYZE|PRAGMA)\b",
    re.IGNORECASE,
)

_SELECT_PREFIX = re.compile(r"^\s*(WITH|SELECT)\b", re.IGNORECASE)


class UnsafeSQLError(ValueError):
    """Raised when a SQL string is rejected by the read-only guard."""


def assert_read_only(sql: str) -> None:
    """Raise UnsafeSQLError if `sql` looks like anything other than SELECT/CTE.

    Multiple statements are also rejected — only one query at a time.
    """
    if not sql or not sql.strip():
        raise UnsafeSQLError("empty query")

    stripped = _BLOCK_COMMENT.sub(" ", _LINE_COMMENT.sub("", sql))

    # Reject multiple statements (semicolons separating non-empty fragments).
    fragments = [f.strip() for f in stripped.split(";") if f.strip()]
    if len(fragments) > 1:
        raise UnsafeSQLError("multiple statements are not allowed")

    body = fragments[0]
    if not _SELECT_PREFIX.match(body):
        raise UnsafeSQLError("only SELECT or WITH ... SELECT statements are allowed")

    if _FORBIDDEN.search(body):
        raise UnsafeSQLError("query contains a write/DDL keyword")
