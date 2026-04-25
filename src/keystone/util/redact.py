"""Redaction helpers applied during ingestion.

Currently focused on Social Security Numbers — keep last 4 digits only.
Easily extended for credit-card / account numbers as needed.
"""

from __future__ import annotations

import re

# 9-digit SSN patterns: AAA-GG-SSSS, AAA GG SSSS, or bare 9 digits
# (the bare form only matches when surrounded by non-digits to avoid
# clobbering longer numbers).
_SSN = re.compile(
    r"(?<!\d)(\d{3})[- ]?(\d{2})[- ]?(\d{4})(?!\d)"
)


def redact_ssn(text: str) -> tuple[str, int]:
    """Replace SSNs with XXX-XX-NNNN. Returns (redacted_text, count)."""
    count = 0

    def repl(m: re.Match[str]) -> str:
        nonlocal count
        count += 1
        return f"XXX-XX-{m.group(3)}"

    return _SSN.sub(repl, text), count


def redact(text: str) -> tuple[str, dict[str, int]]:
    """Apply all configured redactions and report counts per kind."""
    text, ssn_count = redact_ssn(text)
    return text, {"ssn": ssn_count}
