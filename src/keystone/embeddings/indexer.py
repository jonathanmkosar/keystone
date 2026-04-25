"""Shared writer for the semantic_index virtual table.

Used by every ingestion pipeline (PDF docs, transactions, players, observations).
Embeds in batches via OpenAI and inserts into the appropriate database's
`semantic_index` (data.db for content, memory.db for observations).

All ingestion runs OUTSIDE the MCP server (per spec): scheduled jobs and
ad-hoc CLI scripts. Query-time embedding is handled by openai_embedder.embed_query.
"""

from __future__ import annotations

import logging
import struct
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable

from keystone.embeddings.openai_embedder import EMBEDDING_DIM, embed_batch
from keystone.storage.db import connection, has_table

log = logging.getLogger(__name__)

BATCH_SIZE = 64


@dataclass
class IndexItem:
    """One row to embed and index."""

    source_type: str    # 'transaction' | 'doc' | 'observation' | 'player' | 'team'
    source_ref: str     # e.g. 'tx:abc123', 'doc:2024-W2.pdf#chunk=2'
    text: str           # the text to embed (description, chunk, observation content)


def _pack_vector(values) -> bytes:
    return struct.pack(f"{len(values)}f", *values)


def index_items(db_path: Path, items: Iterable[IndexItem]) -> int:
    """Embed and insert items into db_path's semantic_index. Returns rows written.

    De-duplicates against existing (source_type, source_ref) by deleting any
    prior row first so re-ingestion replaces rather than duplicates.

    Skips items whose embedding fails (no API key, transient errors).
    """
    items_list = [i for i in items if i.text and i.text.strip()]
    if not items_list:
        return 0

    written = 0
    with connection(db_path) as conn:
        if not has_table(conn, "semantic_index"):
            log.warning("%s has no semantic_index table; skipping indexing", db_path.name)
            return 0

        for batch_start in range(0, len(items_list), BATCH_SIZE):
            batch = items_list[batch_start : batch_start + BATCH_SIZE]
            vectors = embed_batch([i.text for i in batch])

            for item, vec in zip(batch, vectors):
                if vec is None or len(vec) != EMBEDDING_DIM:
                    continue
                conn.execute(
                    "DELETE FROM semantic_index "
                    "WHERE source_type=? AND source_ref=?",
                    (item.source_type, item.source_ref),
                )
                conn.execute(
                    "INSERT INTO semantic_index(embedding, source_type, source_ref) "
                    "VALUES (?, ?, ?)",
                    (_pack_vector(vec), item.source_type, item.source_ref),
                )
                written += 1

    log.info("Indexed %d items into %s", written, db_path.name)
    return written
