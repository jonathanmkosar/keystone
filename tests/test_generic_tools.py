"""Round-trip test: bootstrap a temp stone, run all six generic tools."""

from __future__ import annotations

import json
import os
import sqlite3
import time
from pathlib import Path

import pytest


TEST_STONE = "test_stone"
TEST_STONE_PKG_DIR = Path(__file__).resolve().parent / "fixtures"


@pytest.fixture
def stone_dir(tmp_path: Path, monkeypatch) -> Path:
    """Build a minimal test_stone user-data dir and migrate it.

    Points KEYSTONE_STONES_PKG_DIR at ``tests/fixtures/`` so the fixture
    package at ``tests/fixtures/test_stone/`` supplies config.json + schema.sql.
    """

    monkeypatch.setenv("KEYSTONE_STONES_DIR", str(tmp_path))
    monkeypatch.setenv("KEYSTONE_STONES_PKG_DIR", str(TEST_STONE_PKG_DIR))

    # Force re-import so SETTINGS picks up the new env vars.
    import importlib

    import keystone.config as cfg

    importlib.reload(cfg)
    import keystone.stones.registry as reg

    importlib.reload(reg)

    src_config = TEST_STONE_PKG_DIR / TEST_STONE / "config.json"
    stone_root = tmp_path / TEST_STONE
    stone_root.mkdir()
    (stone_root / "config.json").write_text(
        src_config.read_text(encoding="utf-8"), encoding="utf-8"
    )

    from keystone.stones.registry import REGISTRY
    from keystone.storage.migrations import migrate_stone

    stone = REGISTRY.get(TEST_STONE)
    migrate_stone(stone)
    return stone_root


def test_run_sql_read_only(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _run_sql_impl

    stone = REGISTRY.get(TEST_STONE)
    # Seed a row directly so SELECT has something to return.
    with sqlite3.connect(str(stone.data_db)) as conn:
        conn.execute(
            "INSERT INTO accounts(id, institution, name, type, balance, last_synced) "
            "VALUES (?, ?, ?, ?, ?, ?)",
            ("a1", "Chase", "Checking", "checking", 1234.56, int(time.time())),
        )

    result = _run_sql_impl(stone, "SELECT id, institution, balance FROM accounts")
    assert result["row_count"] == 1
    assert result["columns"] == ["id", "institution", "balance"]
    assert result["rows"][0][0] == "a1"
    assert result["rows"][0][2] == 1234.56


def test_save_and_get_memory(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _get_memory_impl, _save_memory_impl

    stone = REGISTRY.get(TEST_STONE)
    saved = _save_memory_impl(
        stone, content="User prefers monthly summaries on Sundays.", type="preference"
    )
    assert "id" in saved
    assert saved["type"] == "preference"

    rows = _get_memory_impl(stone, type="preference")
    assert len(rows) == 1
    assert "monthly summaries" in rows[0]["content"]


def test_save_rule_upsert(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _save_rule_impl

    stone = REGISTRY.get(TEST_STONE)
    r1 = _save_rule_impl(stone, "payee:STARBUCKS", {"category": "Coffee"})
    r2 = _save_rule_impl(stone, "payee:STARBUCKS", {"category": "Food & Drink"})
    assert r1["pattern"] == r2["pattern"]

    with sqlite3.connect(str(stone.memory_db)) as conn:
        rows = list(conn.execute("SELECT pattern, value FROM rules"))
    assert len(rows) == 1
    assert json.loads(rows[0][1])["category"] == "Food & Drink"


def test_get_document_unknown(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _get_document_impl

    stone = REGISTRY.get(TEST_STONE)
    assert "error" in _get_document_impl(stone, "garbage")
    assert "error" in _get_document_impl(stone, "doc:does-not-exist")


def test_semantic_search_no_key_returns_empty(stone_dir: Path, monkeypatch):
    """Without an OpenAI key the embedder returns None and search yields []."""
    monkeypatch.delenv("OPENAI_API_KEY", raising=False)
    # Stub keyring to return None.
    import keystone.embeddings.openai_embedder as emb

    monkeypatch.setattr(emb, "_get_api_key", lambda: None)
    monkeypatch.setattr(emb, "_client", None)
    emb.embed_query.cache_clear()

    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _semantic_search_impl

    stone = REGISTRY.get(TEST_STONE)
    assert _semantic_search_impl(stone, "anything") == []


def test_list_tables(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _list_tables_impl

    stone = REGISTRY.get(TEST_STONE)
    tables = _list_tables_impl(stone)
    names_by_db = {(t["db"], t["name"]) for t in tables}
    # Every test_stone schema table should appear in data.db
    expected_data = {"accounts", "transactions", "budgets", "documents", "document_chunks"}
    for n in expected_data:
        assert ("data.db", n) in names_by_db, f"missing data.db.{n}"
    # Memory tables in memory.db
    expected_memory = {"observations", "entities", "rules"}
    for n in expected_memory:
        assert ("memory.db", n) in names_by_db, f"missing memory.db.{n}"
    # semantic_index_* shadow tables must be filtered out
    assert not any("semantic_index_" in t["name"] for t in tables)


def test_describe_table_data_db(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _describe_table_impl

    stone = REGISTRY.get(TEST_STONE)
    result = _describe_table_impl(stone, "transactions")
    assert result["db"] == "data.db"
    assert result["table"] == "transactions"
    cols = {c["name"]: c for c in result["columns"]}
    assert "id" in cols and cols["id"]["pk"] is True
    assert "posted" in cols and cols["posted"]["notnull"] is True
    assert "amount" in cols
    assert "payee" in cols


def test_describe_table_memory_db(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _describe_table_impl

    stone = REGISTRY.get(TEST_STONE)
    result = _describe_table_impl(stone, "observations")
    assert result["db"] == "memory.db"
    cols = {c["name"]: c for c in result["columns"]}
    assert "id" in cols and cols["id"]["pk"] is True
    assert "type" in cols
    assert "content" in cols


def test_describe_table_unknown(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _describe_table_impl

    stone = REGISTRY.get(TEST_STONE)
    result = _describe_table_impl(stone, "nope_does_not_exist")
    assert "error" in result


def test_describe_table_rejects_invalid_name(stone_dir: Path):
    """Defense in depth: refuse anything that isn't a plain SQL identifier."""
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _describe_table_impl

    stone = REGISTRY.get(TEST_STONE)
    for bad in ("transactions; DROP TABLE accounts", "x'; --", "tx-foo", ""):
        result = _describe_table_impl(stone, bad)
        assert "error" in result, f"should reject {bad!r}"


# ---------------------------------------------------------------------------
# Memory v2: update / delete / supersede + migration
# ---------------------------------------------------------------------------


def test_memory_v2_columns_present(stone_dir: Path):
    """Fresh migration must include the v2 audit columns + indexes."""
    from keystone.stones.registry import REGISTRY

    stone = REGISTRY.get(TEST_STONE)
    with sqlite3.connect(str(stone.memory_db)) as conn:
        cols = {r[1] for r in conn.execute("PRAGMA table_info(observations)")}
        assert {
            "last_updated_at",
            "deleted_at",
            "superseded_by_id",
            "supersedes_id",
        } <= cols
        idx_names = {r[1] for r in conn.execute("PRAGMA index_list(observations)")}
        assert "idx_observations_deleted_at" in idx_names
        assert "idx_observations_superseded_by" in idx_names
        version = conn.execute(
            "SELECT value FROM schema_meta WHERE key='version'"
        ).fetchone()[0]
        assert int(version) == 2


def test_memory_v2_alter_existing_db(tmp_path: Path):
    """Simulate a v1 install and confirm migrate_stone back-fills v2 columns."""
    from keystone.storage.db import connection
    from keystone.storage.migrations import migrate_stone
    from keystone.stones.base import Stone, StoneConfig

    stone_root = tmp_path / "legacy"
    stone_root.mkdir()
    stone = Stone(
        name=TEST_STONE,
        root=stone_root,
        config=StoneConfig(
            name=TEST_STONE,
            description="",
            schema_description="",
            system_prompt_hints=[],
            tools=[],
        ),
    )
    # Hand-build a v1 observations table (no v2 columns).
    with connection(stone.memory_db) as conn:
        conn.execute(
            "CREATE TABLE observations ("
            "id INTEGER PRIMARY KEY AUTOINCREMENT, "
            "timestamp INTEGER NOT NULL, type TEXT NOT NULL, entity TEXT, "
            "content TEXT NOT NULL, source TEXT, embedding BLOB)"
        )
        conn.execute(
            "INSERT INTO observations(timestamp, type, content) VALUES (?,?,?)",
            (123, "fact", "legacy row"),
        )

    migrate_stone(stone)

    with sqlite3.connect(str(stone.memory_db)) as conn:
        cols = {r[1] for r in conn.execute("PRAGMA table_info(observations)")}
        assert "deleted_at" in cols
        # Existing row preserved + columns default to NULL.
        row = conn.execute(
            "SELECT id, content, deleted_at, last_updated_at FROM observations"
        ).fetchone()
        assert row[1] == "legacy row"
        assert row[2] is None
        assert row[3] is None


def _seed(stone, **kw):
    from keystone.tools.generic import _save_memory_impl
    return _save_memory_impl(stone, **kw)


def test_update_memory_partial(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _get_memory_impl, _update_memory_impl

    stone = REGISTRY.get(TEST_STONE)
    saved = _seed(stone, content="old content", type="rule", entity="x")
    oid = saved["id"]

    updated = _update_memory_impl(stone, memory_id=oid, content="new content")
    assert updated["id"] == oid
    assert updated["content"] == "new content"
    assert updated["entity"] == "x"  # unchanged
    assert updated["type"] == "rule"
    assert updated["last_updated_at"] is not None
    assert updated["timestamp"] == saved["timestamp"]  # creation ts preserved

    only = _get_memory_impl(stone, type="rule")
    assert len(only) == 1 and only[0]["content"] == "new content"


def test_update_memory_validation(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _update_memory_impl

    stone = REGISTRY.get(TEST_STONE)
    assert "error" in _update_memory_impl(stone, memory_id=None)
    assert "error" in _update_memory_impl(stone, memory_id="abc")
    assert "error" in _update_memory_impl(stone, memory_id=99999, content="x")
    saved = _seed(stone, content="hello", type="fact")
    assert "error" in _update_memory_impl(stone, memory_id=saved["id"])
    assert "error" in _update_memory_impl(stone, memory_id=saved["id"], content="   ")


def test_delete_memory_soft_default(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import (
        _delete_memory_impl,
        _get_memory_impl,
    )

    stone = REGISTRY.get(TEST_STONE)
    a = _seed(stone, content="keep me", type="rule")
    b = _seed(stone, content="delete me", type="rule")

    res = _delete_memory_impl(stone, memory_id=b["id"])
    assert res["deleted"] == "soft"
    assert res["previous"]["content"] == "delete me"

    visible = _get_memory_impl(stone, type="rule")
    assert {r["id"] for r in visible} == {a["id"]}, visible

    all_rows = _get_memory_impl(stone, type="rule", include_deleted=True)
    ids = {r["id"] for r in all_rows}
    assert {a["id"], b["id"]} <= ids
    deleted_row = next(r for r in all_rows if r["id"] == b["id"])
    assert deleted_row["deleted_at"] is not None

    # Row still exists physically.
    with sqlite3.connect(str(stone.memory_db)) as conn:
        row = conn.execute(
            "SELECT id FROM observations WHERE id=?", (b["id"],)
        ).fetchone()
        assert row is not None


def test_delete_memory_hard(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _delete_memory_impl

    stone = REGISTRY.get(TEST_STONE)
    saved = _seed(stone, content="goodbye", type="observation")

    res = _delete_memory_impl(stone, memory_id=saved["id"], hard=True)
    assert res["deleted"] == "hard"

    with sqlite3.connect(str(stone.memory_db)) as conn:
        row = conn.execute(
            "SELECT id FROM observations WHERE id=?", (saved["id"],)
        ).fetchone()
        assert row is None


def test_delete_memory_unknown(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _delete_memory_impl

    stone = REGISTRY.get(TEST_STONE)
    assert "error" in _delete_memory_impl(stone, memory_id=99999)


def test_supersede_memory_links_and_inlines(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import (
        _get_memory_impl,
        _supersede_memory_impl,
    )

    stone = REGISTRY.get(TEST_STONE)
    old = _seed(
        stone,         content="always refresh transactions when asked about Q1",
        type="rule",
        entity="reporting",
    )

    res = _supersede_memory_impl(
        stone,         old_memory_id=old["id"],
        new_content="always refresh transactions before any reporting question",
    )
    new_id = res["new"]["id"]
    assert res["superseded_id"] == old["id"]
    # New entry inherits old type/entity.
    assert res["new"]["type"] == "rule"
    assert res["new"]["entity"] == "reporting"
    assert res["new"]["supersedes_id"] == old["id"]

    visible = _get_memory_impl(stone, type="rule")
    assert {r["id"] for r in visible} == {new_id}, visible

    full = _get_memory_impl(stone, type="rule", include_deleted=True)
    superseded = next(r for r in full if r["id"] == old["id"])
    assert superseded["superseded_by_id"] == new_id
    assert superseded["deleted_at"] is not None
    # Inline payload available without a follow-up call.
    assert "superseded_by" in superseded
    assert "before any reporting question" in superseded["superseded_by"]["content"]


def test_supersede_memory_validation(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import _supersede_memory_impl

    stone = REGISTRY.get(TEST_STONE)
    assert "error" in _supersede_memory_impl(
        stone, old_memory_id=None, new_content="x"
    )
    assert "error" in _supersede_memory_impl(
        stone, old_memory_id=1, new_content=""
    )
    assert "error" in _supersede_memory_impl(
        stone, old_memory_id=99999, new_content="x"
    )
    # Cannot supersede an already-deleted entry.
    saved = _seed(stone, content="hello", type="fact")
    from keystone.tools.generic import _delete_memory_impl
    _delete_memory_impl(stone, memory_id=saved["id"])
    assert "error" in _supersede_memory_impl(
        stone, old_memory_id=saved["id"], new_content="x"
    )


def test_get_memory_filters_deleted_by_default(stone_dir: Path):
    from keystone.stones.registry import REGISTRY
    from keystone.tools.generic import (
        _delete_memory_impl,
        _get_memory_impl,
    )

    stone = REGISTRY.get(TEST_STONE)
    a = _seed(stone, content="keep", type="preference")
    b = _seed(stone, content="hide", type="preference")
    _delete_memory_impl(stone, memory_id=b["id"])

    visible = _get_memory_impl(stone)
    visible_ids = {r["id"] for r in visible}
    assert a["id"] in visible_ids
    assert b["id"] not in visible_ids

    all_rows = _get_memory_impl(stone, include_deleted=True)
    assert b["id"] in {r["id"] for r in all_rows}
