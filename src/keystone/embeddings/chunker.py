"""Text chunking utility for ingestion pipelines.

Splits text on paragraph then sentence boundaries, packs into chunks of
roughly `target_chars` characters with `overlap_chars` of overlap. This is
deliberately simple — no model-specific tokenizer — because text-embedding-3-small
handles up to 8191 tokens and we keep chunks well below that.
"""

from __future__ import annotations

import re

DEFAULT_TARGET_CHARS = 1500
DEFAULT_OVERLAP_CHARS = 150
MIN_CHUNK_CHARS = 50

_PARAGRAPH = re.compile(r"\n{2,}")
_SENTENCE = re.compile(r"(?<=[.!?])\s+")


def chunk_text(
    text: str,
    *,
    target_chars: int = DEFAULT_TARGET_CHARS,
    overlap_chars: int = DEFAULT_OVERLAP_CHARS,
) -> list[str]:
    """Return a list of overlapping text chunks.

    Strategy:
      1. Split on blank lines (paragraphs).
      2. If a paragraph exceeds target_chars, split it on sentences.
      3. Greedily pack pieces until the running chunk exceeds target_chars,
         then emit and start the next chunk seeded with `overlap_chars` of tail.
    """
    if not text or not text.strip():
        return []

    pieces: list[str] = []
    for para in _PARAGRAPH.split(text):
        para = para.strip()
        if not para:
            continue
        if len(para) <= target_chars:
            pieces.append(para)
        else:
            pieces.extend(s.strip() for s in _SENTENCE.split(para) if s.strip())

    chunks: list[str] = []
    current = ""
    for piece in pieces:
        if not current:
            current = piece
            continue
        candidate = current + "\n\n" + piece
        if len(candidate) <= target_chars:
            current = candidate
        else:
            chunks.append(current)
            tail = current[-overlap_chars:] if overlap_chars > 0 else ""
            current = (tail + "\n\n" + piece).strip() if tail else piece

    if current:
        if not chunks or len(current) >= MIN_CHUNK_CHARS:
            chunks.append(current)
        else:
            chunks[-1] = chunks[-1] + "\n\n" + current

    return chunks
