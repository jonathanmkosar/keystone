"""Sanitize ingested text to mitigate prompt-injection attacks against Claude.

Applied to text before it lands in documents.content / document_chunks.content
(and is later returned by get_document / semantic_search). Goals:

  - Strip zero-width and bidirectional formatting characters that hide content
    from a reviewer but are visible to the model.
  - Neutralise common prompt-injection sentinels by inserting a separator so
    they no longer read as instructions.
  - Cap chunk size before embedding (cheaper API calls, prevents giant blobs).

This is best-effort defense in depth, NOT a guarantee. Documents are also
clearly demarcated as user data when returned to Claude.
"""

from __future__ import annotations

import re
import unicodedata

MAX_CHUNK_CHARS = 8000

# Format / control characters that are invisible but model-readable.
_INVISIBLE = re.compile(
    "[\u200b\u200c\u200d\u2060\ufeff"          # zero-width family
    "\u202a\u202b\u202c\u202d\u202e"            # bidi overrides
    "\u2066\u2067\u2068\u2069"                  # bidi isolates
    "]"
)

# Suspicious instruction sentinels that often introduce injection attempts.
_SUSPICIOUS = re.compile(
    r"(?i)\b("
    r"ignore (the )?(previous|above|prior) instructions?|"
    r"disregard (the )?(previous|above|prior)|"
    r"system prompt|"
    r"new instructions?:|"
    r"you are now|"
    r"act as (an? )?(?:admin|developer|root)"
    r")\b"
)


def sanitize_text(text: str, *, max_chars: int = MAX_CHUNK_CHARS) -> str:
    """Return a sanitized copy of `text` suitable for storage and embedding."""
    if not text:
        return ""
    # Normalise unicode to NFC so visually-identical sequences compare equal.
    cleaned = unicodedata.normalize("NFC", text)
    cleaned = _INVISIBLE.sub("", cleaned)

    # Defang suspicious instruction phrases by inserting a marker so they no
    # longer read like commands.
    cleaned = _SUSPICIOUS.sub(r"[INGESTED-TEXT-NOT-INSTRUCTIONS:\1]", cleaned)

    # Collapse runs of whitespace to keep chunk sizes predictable.
    cleaned = re.sub(r"[ \t]+", " ", cleaned)
    cleaned = re.sub(r"\n{3,}", "\n\n", cleaned)

    if len(cleaned) > max_chars:
        cleaned = cleaned[:max_chars] + "\n\n[... truncated by sanitizer ...]"
    return cleaned.strip()
