"""Generic MCP tools, registered identically on every stone.

- run_sql          : read-only SELECT against stone's data.db
- semantic_search  : vec0 nearest-neighbour search over stone's semantic_index
- get_document     : hydrate a stored document (or document chunk) by source_ref
- get_memory       : read observations from memory.db, filtered by type/since;
                     hides soft-deleted rows by default, inlines replacement
                     content for any superseded entry it returns
- save_memory      : append an observation to memory.db
- update_memory    : partial-edit an existing observation (typo / scope tweak)
- delete_memory    : soft-delete (default) or hard-delete an observation
- supersede_memory : atomic 'replace this entry with a new one' for rule revisions
- list_tables      : enumerate tables/views in data.db + memory.db
- describe_table   : column/type/PK info for a single table

Note: the underlying ``_save_rule_impl`` helper is also defined here because
it operates on the generic ``rules`` table in memory.db, but it is NOT
registered as an MCP tool. Stone-specific tools (e.g. finance's
``save_budget_category``) call it directly.
"""

from __future__ import annotations

import json
import logging
import re
import time
from typing import Any

from keystone.stones.base import Stone
from keystone.storage.db import connection, has_table
from keystone.util.sql_guard import UnsafeSQLError, assert_read_only

log = logging.getLogger(__name__)

MAX_ROWS = 500
MAX_DOCUMENT_CHARS = 200_000
DEFAULT_MEMORY_LIMIT = 50

KNOWN_MEMORY_TYPES = {
    "fact",
    "preference",
    "observation",
    "scheduled_report",
    "rule",
}

_MEMORY_SELECT_COLS = (
    "id, timestamp, type, entity, content, source, "
    "last_updated_at, deleted_at, superseded_by_id, supersedes_id"
)


def _hydrate_supersession(conn, row) -> dict[str, Any]:
    """Convert a sqlite Row to a dict; if superseded, inline the replacement."""
    out = {k: row[k] for k in row.keys()}
    new_id = out.get("superseded_by_id")
    if new_id:
        replacement = conn.execute(
            f"SELECT {_MEMORY_SELECT_COLS} FROM observations WHERE id=?",
            (new_id,),
        ).fetchone()
        if replacement is not None:
            out["superseded_by"] = {
                "id": replacement["id"],
                "type": replacement["type"],
                "entity": replacement["entity"],
                "content": replacement["content"],
                "timestamp": replacement["timestamp"],
            }
    return out


# ---------------------------------------------------------------------------
# Tool implementations (callables; bound to a Stone via closures in register)
# ---------------------------------------------------------------------------


def _run_sql_impl(stone: Stone, query: str) -> dict[str, Any]:
    """Execute a read-only SELECT against this stone's data.db.

    Returns {"columns": [...], "rows": [[...], ...], "row_count": N, "truncated": bool}.
    """
    assert_read_only(query)

    if not stone.data_db.exists():
        return {"columns": [], "rows": [], "row_count": 0, "truncated": False}

    with connection(stone.data_db, read_only=True) as conn:
        cur = conn.execute(query)
        columns = [d[0] for d in cur.description] if cur.description else []
        rows = cur.fetchmany(MAX_ROWS + 1)

    truncated = len(rows) > MAX_ROWS
    rows = rows[:MAX_ROWS]
    return {
        "columns": columns,
        "rows": [list(r) for r in rows],
        "row_count": len(rows),
        "truncated": truncated,
    }


def _semantic_search_impl(
    stone: Stone,
    query: str,
    scope: str | None = None,
    limit: int = 10,
) -> list[dict[str, Any]]:
    """Embed `query` and return nearest pointers from semantic_index.

    Each pointer is {"source_type": str, "source_ref": str, "score": float}.
    Scope filters by source_type (e.g. 'transaction', 'doc', 'observation').

    Searches BOTH data.db.semantic_index and memory.db.semantic_index, merges,
    sorts by score (lower distance = better), truncates to `limit`.
    """
    if limit <= 0 or limit > 100:
        limit = 10

    try:
        from keystone.embeddings.openai_embedder import embed_query
    except Exception as exc:  # pragma: no cover - degrades when embeddings unavailable
        log.warning("Embeddings unavailable: %s", exc)
        return []

    vector = embed_query(query)
    if vector is None:
        return []

    results: list[dict[str, Any]] = []
    for db_path in (stone.data_db, stone.memory_db):
        if not db_path.exists():
            continue
        try:
            with connection(db_path) as conn:
                if not has_table(conn, "semantic_index"):
                    continue
                params: list[Any] = [_pack_vector(vector), limit * 4]
                where = ""
                if scope:
                    where = "AND source_type = ?"
                    params.insert(1, scope)
                sql = (
                    "SELECT source_type, source_ref, distance FROM semantic_index "
                    "WHERE embedding MATCH ? "
                    f"{where} "
                    "ORDER BY distance LIMIT ?"
                )
                for row in conn.execute(sql, params):
                    results.append(
                        {
                            "source_type": row["source_type"],
                            "source_ref": row["source_ref"],
                            "score": float(row["distance"]),
                        }
                    )
        except Exception as exc:
            log.warning("semantic_search on %s failed: %s", db_path.name, exc)

    results.sort(key=lambda r: r["score"])
    # Dedupe (source_type, source_ref) preserving best score.
    seen: set[tuple[str, str]] = set()
    unique: list[dict[str, Any]] = []
    for r in results:
        key = (r["source_type"], r["source_ref"])
        if key in seen:
            continue
        seen.add(key)
        unique.append(r)
        if len(unique) >= limit:
            break
    return unique


def _get_document_impl(stone: Stone, source_ref: str) -> dict[str, Any]:
    """Resolve a source_ref returned by semantic_search and return its content.

    source_ref formats:
      - "doc:<source_ref>"            -> documents.content
      - "doc:<source_ref>#chunk=<n>"  -> document_chunks row
      - "tx:<id>"                     -> transactions row as JSON
      - "obs:<id>"                    -> observations row from memory.db
    """
    if ":" not in source_ref:
        return {"error": f"unrecognized source_ref format: {source_ref!r}"}

    kind, _, rest = source_ref.partition(":")

    if kind == "doc":
        return _get_doc(stone, rest)
    if kind == "tx":
        return _get_transaction(stone, rest)
    if kind == "obs":
        return _get_observation(stone, rest)
    return {"error": f"unsupported source_type: {kind!r}"}


def _get_doc(stone: Stone, rest: str) -> dict[str, Any]:
    if "#chunk=" in rest:
        ref, _, chunk_part = rest.partition("#chunk=")
        try:
            chunk_index = int(chunk_part)
        except ValueError:
            return {"error": f"invalid chunk index: {chunk_part!r}"}
        with connection(stone.data_db, read_only=True) as conn:
            row = conn.execute(
                "SELECT content FROM document_chunks WHERE source_ref=? AND chunk_index=?",
                (ref, chunk_index),
            ).fetchone()
        if not row:
            return {"error": f"chunk not found: {rest!r}"}
        return {"source_ref": rest, "content": _truncate(row["content"])}

    with connection(stone.data_db, read_only=True) as conn:
        row = conn.execute(
            "SELECT kind, year, ingested_at, content FROM documents WHERE source_ref=?",
            (rest,),
        ).fetchone()
    if not row:
        return {"error": f"document not found: {rest!r}"}
    return {
        "source_ref": rest,
        "kind": row["kind"],
        "year": row["year"],
        "ingested_at": row["ingested_at"],
        "content": _truncate(row["content"] or ""),
    }


def _get_transaction(stone: Stone, tx_id: str) -> dict[str, Any]:
    with connection(stone.data_db, read_only=True) as conn:
        row = conn.execute(
            "SELECT id, account_id, posted, amount, payee, category, memo "
            "FROM transactions WHERE id=?",
            (tx_id,),
        ).fetchone()
    if not row:
        return {"error": f"transaction not found: {tx_id!r}"}
    return {k: row[k] for k in row.keys()}


def _get_observation(stone: Stone, obs_id: str) -> dict[str, Any]:
    try:
        oid = int(obs_id)
    except ValueError:
        return {"error": f"invalid observation id: {obs_id!r}"}
    with connection(stone.memory_db, read_only=True) as conn:
        row = conn.execute(
            f"SELECT {_MEMORY_SELECT_COLS} FROM observations WHERE id=?",
            (oid,),
        ).fetchone()
        if not row:
            return {"error": f"observation not found: {obs_id!r}"}
        return _hydrate_supersession(conn, row)


def _get_memory_impl(
    stone: Stone,
    query: str | None = None,
    type: str | None = None,
    since: int | None = None,
    limit: int = DEFAULT_MEMORY_LIMIT,
    include_deleted: bool = False,
) -> list[dict[str, Any]]:
    """Retrieve observations from memory.db.

    - query: if set, runs semantic_search restricted to memory and hydrates results.
    - type:  filter by observation type (fact, preference, scheduled_report, ...).
    - since: only observations newer than this unix epoch.
    - limit: max rows returned (default 50).
    - include_deleted: when False (default), hides rows that have been soft-
      deleted via delete_memory or supersede_memory. Set True for audit /
      history queries.

    Each returned row that has a non-null ``superseded_by_id`` is enriched
    with ``superseded_by`` -> the replacement entry's compact view, so the
    caller never has to follow the pointer manually.
    """
    if limit <= 0 or limit > 500:
        limit = DEFAULT_MEMORY_LIMIT

    if query:
        # Pull a wider candidate set so we can still return up to `limit`
        # after filtering soft-deleted rows out of the semantic results.
        candidate_limit = limit if include_deleted else limit * 4
        pointers = _semantic_search_impl(
            stone, query, scope="observation", limit=candidate_limit
        )
        ids = [p["source_ref"].removeprefix("obs:") for p in pointers]
        if not ids:
            return []
        placeholders = ",".join(["?"] * len(ids))
        deleted_filter = "" if include_deleted else "AND deleted_at IS NULL"
        with connection(stone.memory_db, read_only=True) as conn:
            cur = conn.execute(
                f"SELECT {_MEMORY_SELECT_COLS} "
                f"FROM observations WHERE id IN ({placeholders}) {deleted_filter}",
                tuple(ids),
            )
            rows = cur.fetchall()
            order = {int(i): n for n, i in enumerate(ids)}
            sorted_rows = sorted(rows, key=lambda r: order.get(r["id"], 999_999))[:limit]
            return [_hydrate_supersession(conn, r) for r in sorted_rows]

    where = []
    params: list[Any] = []
    if type:
        where.append("type = ?")
        params.append(type)
    if since is not None:
        where.append("timestamp >= ?")
        params.append(int(since))
    if not include_deleted:
        where.append("deleted_at IS NULL")
    where_sql = ("WHERE " + " AND ".join(where)) if where else ""
    params.append(limit)

    with connection(stone.memory_db, read_only=True) as conn:
        cur = conn.execute(
            f"SELECT {_MEMORY_SELECT_COLS} "
            f"FROM observations {where_sql} "
            f"ORDER BY timestamp DESC LIMIT ?",
            tuple(params),
        )
        rows = cur.fetchall()
        return [_hydrate_supersession(conn, r) for r in rows]


def _save_memory_impl(
    stone: Stone,
    content: str,
    type: str = "observation",
    entity: str | None = None,
    source: str = "claude",
) -> dict[str, Any]:
    """Append an observation to memory.db. Embeds and indexes when possible."""
    if not content or not content.strip():
        return {"error": "content is empty"}
    if type not in KNOWN_MEMORY_TYPES:
        log.info("save_memory: storing unknown type %r as-is", type)

    ts = int(time.time())
    vector_blob: bytes | None = None
    try:
        from keystone.embeddings.openai_embedder import embed_query

        vec = embed_query(content)
        if vec is not None:
            vector_blob = _pack_vector(vec)
    except Exception as exc:
        log.debug("Skipping embedding for save_memory: %s", exc)

    with connection(stone.memory_db) as conn:
        cur = conn.execute(
            "INSERT INTO observations(timestamp, type, entity, content, source, embedding) "
            "VALUES (?, ?, ?, ?, ?, ?)",
            (ts, type, entity, content, source, vector_blob),
        )
        obs_id = cur.lastrowid

        if vector_blob is not None and has_table(conn, "semantic_index"):
            try:
                conn.execute(
                    "INSERT INTO semantic_index(embedding, source_type, source_ref) "
                    "VALUES (?, ?, ?)",
                    (vector_blob, "observation", f"obs:{obs_id}"),
                )
            except Exception as exc:
                log.warning("Failed to insert into semantic_index: %s", exc)

    return {"id": obs_id, "timestamp": ts, "type": type, "entity": entity}


def _update_memory_impl(
    stone: Stone,
    memory_id: int,
    content: str | None = None,
    entity: str | None = None,
    type: str | None = None,
) -> dict[str, Any]:
    """Partial-edit an existing observation. Pass only the fields you want
    to change. Bumps ``last_updated_at`` (the immutable ``timestamp`` is
    preserved). When ``content`` changes, the embedding + semantic_index
    row is regenerated so semantic_search stays accurate.

    Refuses to edit a soft-deleted row (call delete_memory(hard=True) and
    save_memory afresh if you really want a clean slate).
    """
    if memory_id is None:
        return {"error": "memory_id is required"}
    try:
        oid = int(memory_id)
    except (TypeError, ValueError):
        return {"error": f"invalid memory_id: {memory_id!r}"}
    if content is None and entity is None and type is None:
        return {"error": "nothing to update; pass content, entity, and/or type"}
    if content is not None and not content.strip():
        return {"error": "content cannot be blank"}
    if type is not None and type not in KNOWN_MEMORY_TYPES:
        log.info("update_memory: storing unknown type %r as-is", type)

    ts = int(time.time())
    new_vector_blob: bytes | None = None
    if content is not None:
        try:
            from keystone.embeddings.openai_embedder import embed_query

            vec = embed_query(content)
            if vec is not None:
                new_vector_blob = _pack_vector(vec)
        except Exception as exc:
            log.debug("Skipping embedding for update_memory: %s", exc)

    with connection(stone.memory_db) as conn:
        existing = conn.execute(
            f"SELECT {_MEMORY_SELECT_COLS} FROM observations WHERE id=?",
            (oid,),
        ).fetchone()
        if existing is None:
            return {"error": f"memory_id {oid} not found"}
        if existing["deleted_at"] is not None:
            return {
                "error": (
                    f"memory_id {oid} is deleted; restore it manually or "
                    "save_memory a new entry instead"
                )
            }

        sets: list[str] = ["last_updated_at = ?"]
        params: list[Any] = [ts]
        if content is not None:
            sets.append("content = ?")
            params.append(content)
            sets.append("embedding = ?")
            params.append(new_vector_blob)
        if entity is not None:
            sets.append("entity = ?")
            params.append(entity)
        if type is not None:
            sets.append("type = ?")
            params.append(type)
        params.append(oid)
        conn.execute(
            f"UPDATE observations SET {', '.join(sets)} WHERE id = ?",
            tuple(params),
        )

        if content is not None and has_table(conn, "semantic_index"):
            conn.execute(
                "DELETE FROM semantic_index WHERE source_ref = ?",
                (f"obs:{oid}",),
            )
            if new_vector_blob is not None:
                try:
                    conn.execute(
                        "INSERT INTO semantic_index(embedding, source_type, source_ref) "
                        "VALUES (?, ?, ?)",
                        (new_vector_blob, "observation", f"obs:{oid}"),
                    )
                except Exception as exc:
                    log.warning("Failed to refresh semantic_index for obs:%s: %s", oid, exc)

        updated = conn.execute(
            f"SELECT {_MEMORY_SELECT_COLS} FROM observations WHERE id=?",
            (oid,),
        ).fetchone()
        return _hydrate_supersession(conn, updated)


def _delete_memory_impl(
    stone: Stone, memory_id: int, hard: bool = False
) -> dict[str, Any]:
    """Soft-delete (default) or hard-delete an observation.

    Soft delete sets ``deleted_at`` and removes the row from semantic_index
    so semantic_search stops returning it; the observation row itself is
    preserved for audit. Hard delete removes the observation row entirely
    (and its semantic_index entry). Both are irreversible from inside the
    tool surface.
    """
    if memory_id is None:
        return {"error": "memory_id is required"}
    try:
        oid = int(memory_id)
    except (TypeError, ValueError):
        return {"error": f"invalid memory_id: {memory_id!r}"}

    ts = int(time.time())
    with connection(stone.memory_db) as conn:
        existing = conn.execute(
            f"SELECT {_MEMORY_SELECT_COLS} FROM observations WHERE id=?",
            (oid,),
        ).fetchone()
        if existing is None:
            return {"error": f"memory_id {oid} not found"}
        snapshot = {k: existing[k] for k in existing.keys()}

        if has_table(conn, "semantic_index"):
            conn.execute(
                "DELETE FROM semantic_index WHERE source_ref = ?",
                (f"obs:{oid}",),
            )

        if hard:
            conn.execute("DELETE FROM observations WHERE id = ?", (oid,))
            return {
                "id": oid,
                "deleted": "hard",
                "deleted_at": ts,
                "previous": snapshot,
            }

        conn.execute(
            "UPDATE observations SET deleted_at = ? WHERE id = ?",
            (ts, oid),
        )
        return {
            "id": oid,
            "deleted": "soft",
            "deleted_at": ts,
            "previous": snapshot,
        }


def _supersede_memory_impl(
    stone: Stone,
    old_memory_id: int,
    new_content: str,
    entity: str | None = None,
    type: str | None = None,
    source: str = "claude",
) -> dict[str, Any]:
    """Atomically replace an observation: create a new entry with
    ``supersedes_id`` pointing back at the old one, then mark the old row
    as ``superseded_by_id = <new>`` and ``deleted_at = now()``.

    The new entry inherits the old entry's ``type`` / ``entity`` unless
    overridden. Use this for rule revisions where you want to preserve
    the audit trail; for cosmetic edits use update_memory.
    """
    if old_memory_id is None:
        return {"error": "old_memory_id is required"}
    if not new_content or not new_content.strip():
        return {"error": "new_content is empty"}
    try:
        old_id = int(old_memory_id)
    except (TypeError, ValueError):
        return {"error": f"invalid old_memory_id: {old_memory_id!r}"}

    ts = int(time.time())
    new_vector_blob: bytes | None = None
    try:
        from keystone.embeddings.openai_embedder import embed_query

        vec = embed_query(new_content)
        if vec is not None:
            new_vector_blob = _pack_vector(vec)
    except Exception as exc:
        log.debug("Skipping embedding for supersede_memory: %s", exc)

    with connection(stone.memory_db) as conn:
        old_row = conn.execute(
            f"SELECT {_MEMORY_SELECT_COLS} FROM observations WHERE id=?",
            (old_id,),
        ).fetchone()
        if old_row is None:
            return {"error": f"old_memory_id {old_id} not found"}
        if old_row["deleted_at"] is not None:
            return {
                "error": (
                    f"old_memory_id {old_id} is already deleted/superseded; "
                    "use save_memory to create a fresh entry"
                )
            }
        new_type = type if type is not None else old_row["type"]
        new_entity = entity if entity is not None else old_row["entity"]
        if new_type is not None and new_type not in KNOWN_MEMORY_TYPES:
            log.info("supersede_memory: storing unknown type %r as-is", new_type)

        cur = conn.execute(
            "INSERT INTO observations("
            "timestamp, type, entity, content, source, embedding, supersedes_id"
            ") VALUES (?, ?, ?, ?, ?, ?, ?)",
            (ts, new_type, new_entity, new_content, source, new_vector_blob, old_id),
        )
        new_id = cur.lastrowid

        conn.execute(
            "UPDATE observations SET superseded_by_id = ?, deleted_at = ? WHERE id = ?",
            (new_id, ts, old_id),
        )

        if has_table(conn, "semantic_index"):
            conn.execute(
                "DELETE FROM semantic_index WHERE source_ref = ?",
                (f"obs:{old_id}",),
            )
            if new_vector_blob is not None:
                try:
                    conn.execute(
                        "INSERT INTO semantic_index(embedding, source_type, source_ref) "
                        "VALUES (?, ?, ?)",
                        (new_vector_blob, "observation", f"obs:{new_id}"),
                    )
                except Exception as exc:
                    log.warning("Failed to insert semantic_index for obs:%s: %s", new_id, exc)

        new_row = conn.execute(
            f"SELECT {_MEMORY_SELECT_COLS} FROM observations WHERE id=?",
            (new_id,),
        ).fetchone()
        return {
            "new": {k: new_row[k] for k in new_row.keys()},
            "superseded_id": old_id,
            "superseded_at": ts,
        }


def _save_rule_impl(
    stone: Stone, pattern: str, value: dict[str, Any] | str
) -> dict[str, Any]:
    """Upsert a structured rule (e.g. categorization preference) into memory.db."""
    if not pattern:
        return {"error": "pattern is empty"}
    payload = value if isinstance(value, str) else json.dumps(value)
    ts = int(time.time())
    with connection(stone.memory_db) as conn:
        conn.execute(
            "INSERT INTO rules(pattern, value, updated_at) VALUES (?, ?, ?) "
            "ON CONFLICT(pattern) DO UPDATE SET "
            "value=excluded.value, updated_at=excluded.updated_at",
            (pattern, payload, ts),
        )
    return {"pattern": pattern, "value": value, "updated_at": ts}


# Match SQLite identifiers: letters, digits, underscores. We refuse anything
# else to keep the table name out of any SQL string interpolation hazard.
_TABLE_NAME_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


def _describe_table_impl(stone: Stone, name: str) -> dict[str, Any]:
    """Return columns + primary key info for a table or view in this stone.

    Looks in data.db first; falls back to memory.db. Uses PRAGMA table_info
    server-side, which the user's run_sql tool blocks for safety.
    """
    if not name or not _TABLE_NAME_RE.match(name):
        return {"error": f"invalid table name: {name!r}"}

    for db_path, db_label in (
        (stone.data_db, "data.db"),
        (stone.memory_db, "memory.db"),
    ):
        if not db_path.exists():
            continue
        with connection(db_path, read_only=True) as conn:
            if not has_table(conn, name):
                continue
            # PRAGMA table_info(<name>) returns: cid, name, type, notnull, dflt_value, pk
            try:
                rows = conn.execute(f"PRAGMA table_info({name})").fetchall()
            except Exception as exc:  # pragma: no cover - defensive
                return {"error": f"PRAGMA failed: {exc}"}
            columns = [
                {
                    "name": r["name"],
                    "type": r["type"],
                    "notnull": bool(r["notnull"]),
                    "default": r["dflt_value"],
                    "pk": bool(r["pk"]),
                }
                for r in rows
            ]
            return {"db": db_label, "table": name, "columns": columns}
    return {"error": f"table {name!r} not found in data.db or memory.db"}


def _list_tables_impl(stone: Stone) -> list[dict[str, Any]]:
    """List all tables / views across data.db and memory.db.

    Useful as a starting point before describe_table or run_sql.
    """
    out: list[dict[str, Any]] = []
    for db_path, db_label in (
        (stone.data_db, "data.db"),
        (stone.memory_db, "memory.db"),
    ):
        if not db_path.exists():
            continue
        with connection(db_path, read_only=True) as conn:
            for r in conn.execute(
                "SELECT name, type FROM sqlite_master "
                "WHERE type IN ('table','view') "
                "AND name NOT LIKE 'sqlite_%' "
                "AND name NOT LIKE 'semantic_index_%' "
                "ORDER BY name"
            ):
                out.append({"db": db_label, "name": r["name"], "type": r["type"]})
    return out


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _pack_vector(values) -> bytes:
    """Serialize a sequence of floats into the float32 little-endian blob sqlite-vec expects."""
    import struct

    return struct.pack(f"{len(values)}f", *values)


def _truncate(text: str) -> str:
    if len(text) <= MAX_DOCUMENT_CHARS:
        return text
    head = text[:MAX_DOCUMENT_CHARS]
    return head + f"\n\n[... truncated, original length {len(text)} chars ...]"


# ---------------------------------------------------------------------------
# Registration with FastMCP
# ---------------------------------------------------------------------------


def register(mcp, stone: Stone) -> None:  # noqa: ANN001
    """Bind every generic tool to the given stone and register with FastMCP."""

    def run_sql(query: str) -> dict[str, Any]:
        """Run a read-only SELECT (or WITH ... SELECT) against this stone's data.db.

        Multiple statements, INSERT/UPDATE/DELETE, DDL, and PRAGMA are rejected.
        Returns columns, rows (capped at 500), and a `truncated` flag.
        """
        try:
            return _run_sql_impl(stone, query)
        except UnsafeSQLError as exc:
            return {"error": f"unsafe query: {exc}"}

    def semantic_search(
        query: str, scope: str | None = None, limit: int = 10
    ) -> list[dict[str, Any]]:
        """Find pointers (source_type, source_ref, score) most semantically similar to `query`.

        Use scope to filter by source_type (e.g. 'transaction', 'doc', 'observation').
        Hydrate results with get_document (or run_sql for transactions).
        """
        return _semantic_search_impl(stone, query, scope=scope, limit=limit)

    def get_document(source_ref: str) -> dict[str, Any]:
        """Fetch the full content of a document, transaction, or observation by source_ref.

        source_ref examples:
          doc:2024-W2.pdf          -> full document
          doc:2024-W2.pdf#chunk=3  -> single chunk
          tx:abc123                -> a transaction row
          obs:42                   -> an observation row from memory
        """
        return _get_document_impl(stone, source_ref)

    def get_memory(
        query: str | None = None,
        type: str | None = None,
        since: int | None = None,
        limit: int = DEFAULT_MEMORY_LIMIT,
        include_deleted: bool = False,
    ) -> list[dict[str, Any]]:
        """Retrieve persistent observations.

        Call this at conversation start (limit=100 with no other filters) to
        recover context. Optionally filter by type ('fact' | 'preference' |
        'observation' | 'scheduled_report' | 'rule'), by `since` (unix epoch),
        or pass a `query` for semantic search over memory.

        Soft-deleted and superseded rows are hidden by default. Pass
        include_deleted=True for audit/history. When a returned row was
        superseded, its replacement's content is inlined under
        `superseded_by` -- you do not need a follow-up call.
        """
        return _get_memory_impl(
            stone,             query=query,
            type=type,
            since=since,
            limit=limit,
            include_deleted=include_deleted,
        )

    def save_memory(
        content: str,
        type: str = "observation",
        entity: str | None = None,
        source: str = "claude",
    ) -> dict[str, Any]:
        """Persist a fact, preference, observation, rule, or scheduled report to memory.

        Call proactively whenever the user shares information that should
        outlast the current conversation.

        For revising an existing entry, prefer update_memory (cosmetic edits
        / scope tweaks) or supersede_memory (materially different replacement
        with audit trail) over appending a new contradictory entry.
        """
        return _save_memory_impl(stone, content, type=type, entity=entity, source=source)

    def update_memory(
        memory_id: int,
        content: str | None = None,
        entity: str | None = None,
        type: str | None = None,
    ) -> dict[str, Any]:
        """Edit an existing observation in place. Use for typo fixes, scope
        tweaks, or factual corrections to an existing entry. Preserves the
        original id and creation timestamp; bumps last_updated_at.

        Pass only the fields you want to change. When `content` changes,
        the embedding is regenerated so semantic_search stays accurate.
        """
        return _update_memory_impl(
            stone, memory_id=memory_id, content=content, entity=entity, type=type
        )

    def delete_memory(memory_id: int, hard: bool = False) -> dict[str, Any]:
        """Forget an observation.

        Default (hard=False): soft delete -- sets deleted_at and removes the
        row from semantic_index but preserves the row for audit. The
        previously-stored content is returned in the response so you can
        confirm you killed the right entry.

        hard=True: permanent delete. Use only for entries that should be
        wiped (accidentally saved, sensitive data) -- cannot be undone.
        """
        return _delete_memory_impl(stone, memory_id=memory_id, hard=hard)

    def supersede_memory(
        old_memory_id: int,
        new_content: str,
        entity: str | None = None,
        type: str | None = None,
    ) -> dict[str, Any]:
        """Replace an observation with a materially different one in a single
        call. Creates a new entry with `supersedes_id` pointing at the old
        one, then marks the old entry as superseded_by the new one and
        soft-deletes it.

        Use when a rule or fact is functionally replaced (not just edited)
        and you want the audit trail kept. For minor edits, use update_memory.
        """
        return _supersede_memory_impl(
            stone,             old_memory_id=old_memory_id,
            new_content=new_content,
            entity=entity,
            type=type,
        )

    def list_tables() -> list[dict[str, Any]]:
        """List every queryable table or view across this stone's data.db
        and memory.db. Use as the starting point before describe_table or run_sql.
        """
        return _list_tables_impl(stone)

    def describe_table(name: str) -> dict[str, Any]:
        """Return columns, types, primary keys, and not-null flags for a named table.

        Pair with list_tables to discover names. Saves you from running
        SELECT * LIMIT 1 just to figure out the schema. Looks in data.db first,
        then memory.db.
        """
        return _describe_table_impl(stone, name)

    for fn, desc in (
        (run_sql, "Read-only SQL against this stone's data.db"),
        (semantic_search, "Semantic search returning pointers"),
        (get_document, "Hydrate a document/transaction/observation by source_ref"),
        (get_memory, "Retrieve persistent observations from memory.db (hides soft-deleted by default)"),
        (save_memory, "Append a persistent observation to memory.db"),
        (update_memory, "Edit an existing observation in place; preserves id and history"),
        (delete_memory, "Soft delete (default) or hard delete an observation"),
        (supersede_memory, "Replace an observation with a new one and link the audit trail"),
        (list_tables, "List all tables/views in data.db and memory.db"),
        (describe_table, "Describe a table's columns, types, and PKs"),
    ):
        mcp.add_tool(fn, name=fn.__name__, description=desc)


def format_about_user(stone: Stone) -> str:
    """Render facts + preferences + recent scheduled reports for the about-the-user resource."""
    if not stone.memory_db.exists():
        return f"(no memory yet for stone {stone.name!r})"

    try:
        with connection(stone.memory_db, read_only=True) as conn:
            facts = conn.execute(
                "SELECT type, entity, content FROM observations "
                "WHERE type IN ('fact','preference') "
                "ORDER BY timestamp DESC LIMIT 50"
            ).fetchall()
            recent_reports = conn.execute(
                "SELECT timestamp, content FROM observations "
                "WHERE type='scheduled_report' "
                "ORDER BY timestamp DESC LIMIT 5"
            ).fetchall()
    except Exception as exc:
        return f"(memory not yet initialized: {exc})"

    if not facts and not recent_reports:
        return f"(no memory yet for stone {stone.name!r})"

    parts: list[str] = [f"# About the user (stone: {stone.name})\n"]
    if facts:
        parts.append("## Facts and preferences\n")
        for f in facts:
            ent = f"[{f['entity']}] " if f["entity"] else ""
            parts.append(f"- ({f['type']}) {ent}{f['content']}")
    if recent_reports:
        parts.append("\n## Recent scheduled reports\n")
        for r in recent_reports:
            parts.append(f"- (t={r['timestamp']}) {r['content'][:300]}")
    return "\n".join(parts)
