"""Opt-in redaction helpers for conversation logs (#714).

When ``LOG_REDACT=true``, backends wrap user-prompt and agent-response
text through :func:`redact_text` before persisting to
``conversation.jsonl``. The default is permissive (no redaction) so
existing deployments behave identically after this module lands; the
operator must explicitly opt in.

The patterns target common credential / PII formats: AWS access keys,
GitHub/Slack/OpenAI/Anthropic tokens, Bearer headers, generic
high-entropy long hex/base64 blobs, JWTs, credit-card numbers, SSNs,
and US-style phone / email addresses. The intent is defence-in-depth —
not a replacement for a full DLP pipeline — so a small set of
well-anchored regexes is favoured over an enormous allow-list that
would drift out of sync with the upstream leak vectors.

Import as ``from redact import redact_text, should_redact`` from any
shared/-mounted caller.
"""

from __future__ import annotations

import os
import re

__all__ = ["redact_text", "should_redact"]

_REDACTED = "[REDACTED]"


def should_redact() -> bool:
    """True when LOG_REDACT env var is set to a truthy string.

    Cheap; callers can short-circuit when disabled without paying the
    regex cost.
    """
    return os.environ.get("LOG_REDACT", "").strip().lower() in {"1", "true", "yes", "on"}


# Ordered from most-specific to most-generic. The generic high-entropy
# rule fires last so it doesn't stomp shape-specific matches.
_PATTERNS: list[tuple[str, re.Pattern[str]]] = [
    # AWS Access Key ID
    ("aws_access_key", re.compile(r"\b(?:AKIA|ASIA|AIDA|AROA|AIPA|ANPA|ANVA|ACCA)[0-9A-Z]{16}\b")),
    # GitHub tokens (classic PAT, fine-grained PAT, OAuth)
    ("github_token", re.compile(r"\b(?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{20,255}\b")),
    # Slack tokens
    ("slack_token", re.compile(r"\bxox[abprs]-[A-Za-z0-9-]{10,}\b")),
    # OpenAI / Anthropic
    ("openai_key", re.compile(r"\bsk-[A-Za-z0-9_-]{16,}\b")),
    ("anthropic_key", re.compile(r"\bsk-ant-[A-Za-z0-9_-]{16,}\b")),
    # Generic Bearer / Authorization header values (Authorization: Bearer XYZ)
    ("authorization_header", re.compile(r"(?i)(authorization\s*[:=]\s*)(bearer\s+\S+)")),
    # JWT — three base64url segments separated by dots
    ("jwt", re.compile(r"\b[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b")),
    # Credit card (Visa/MC/Amex/Discover-ish — 13-19 digits in groups)
    ("credit_card", re.compile(r"\b(?:\d[ -]?){13,19}\b")),
    # US SSN
    ("ssn", re.compile(r"\b\d{3}-\d{2}-\d{4}\b")),
    # Email — full shape; redact to stop PII leaking into logs
    ("email", re.compile(r"\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b")),
    # High-entropy 32+ hex/base64url blob (last, catches generic tokens)
    ("high_entropy_token", re.compile(r"\b[A-Za-z0-9_-]{32,}\b")),
]


def redact_text(text: str) -> str:
    """Return ``text`` with matched credential / PII spans replaced.

    The input is returned unchanged when LOG_REDACT is falsy so callers
    can unconditionally invoke this helper at log time without paying
    the regex cost on non-redacted deployments.
    """
    if not text or not should_redact():
        return text
    out = text
    for name, pat in _PATTERNS:
        if name == "authorization_header":
            out = pat.sub(r"\1" + _REDACTED, out)
        else:
            out = pat.sub(_REDACTED, out)
    return out
