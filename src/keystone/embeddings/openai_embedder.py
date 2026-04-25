"""OpenAI text-embedding-3-small embedder.

Reads the API key from Windows Credential Manager via `keyring`
(service='keystone', username='openai'), falling back to the OPENAI_API_KEY
environment variable. Returns None when no key is configured so the calling
tools can degrade gracefully (Phase 1/2 may not have embeddings wired yet).
"""

from __future__ import annotations

import logging
import os
import threading
from functools import lru_cache

from keystone.config import SETTINGS

log = logging.getLogger(__name__)

EMBEDDING_DIM = 1536
_KEYRING_SERVICE = "keystone"
_KEYRING_USER = "openai"

_client_lock = threading.Lock()
_client = None
_warned_missing_key = False


def _get_api_key() -> str | None:
    try:
        import keyring

        key = keyring.get_password(_KEYRING_SERVICE, _KEYRING_USER)
        if key:
            return key
    except Exception as exc:
        log.debug("keyring lookup failed: %s", exc)
    return os.environ.get("OPENAI_API_KEY")


def _get_client():
    global _client, _warned_missing_key
    with _client_lock:
        if _client is not None:
            return _client
        api_key = _get_api_key()
        if not api_key:
            if not _warned_missing_key:
                log.info(
                    "No OpenAI API key configured (set via "
                    "`keyring set keystone openai` or OPENAI_API_KEY); "
                    "semantic_search will return empty results."
                )
                _warned_missing_key = True
            return None
        from openai import OpenAI

        _client = OpenAI(api_key=api_key)
        return _client


@lru_cache(maxsize=2048)
def embed_query(text: str) -> tuple[float, ...] | None:
    """Embed a single string, returning a tuple of floats (cacheable) or None.

    The cache decorator means repeated identical queries within a process don't
    incur repeated API calls; semantic_search frequently re-embeds the same text.
    """
    if not text or not text.strip():
        return None
    client = _get_client()
    if client is None:
        return None
    try:
        resp = client.embeddings.create(
            model=SETTINGS.openai_embedding_model, input=text
        )
        return tuple(resp.data[0].embedding)
    except Exception as exc:
        log.warning("OpenAI embedding failed: %s", exc)
        return None


def embed_batch(texts: list[str]) -> list[tuple[float, ...] | None]:
    """Embed many strings in a single API call. Used by ingestion pipelines."""
    if not texts:
        return []
    client = _get_client()
    if client is None:
        return [None] * len(texts)
    try:
        resp = client.embeddings.create(
            model=SETTINGS.openai_embedding_model, input=texts
        )
        return [tuple(item.embedding) for item in resp.data]
    except Exception as exc:
        log.warning("OpenAI batch embedding failed: %s", exc)
        return [None] * len(texts)
