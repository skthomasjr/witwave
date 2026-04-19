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

Scope notes (#1034)
-------------------
Callers should apply :func:`redact_text` only to human-prompt /
agent-response *value* fields, not to serialised JSON lines. Applying
the credit-card or high-entropy catch-all to a full JSONL row would
match UUID / trace-id / session-id shapes and break downstream joins
with OpenTelemetry spans. The helpers below preserve UUID and common
trace-id shapes explicitly, and the generic high-entropy catch-all is
now gated behind ``LOG_REDACT_HIGH_ENTROPY=true`` so it only fires for
operators who have accepted the trade-off.
"""

from __future__ import annotations

import os
import re

__all__ = ["redact_text", "should_redact", "high_entropy_enabled"]

_REDACTED = "[REDACTED]"


def should_redact() -> bool:
    """True when LOG_REDACT env var is set to a truthy string.

    Cheap; callers can short-circuit when disabled without paying the
    regex cost.
    """
    return os.environ.get("LOG_REDACT", "").strip().lower() in {"1", "true", "yes", "on"}


def high_entropy_enabled() -> bool:
    """True when the opt-in generic high-entropy catch-all is armed (#1034).

    The generic ``high_entropy_token`` pattern matches UUIDs, OTel
    trace/span ids, SHA-256 digests, and other benign identifiers that
    log readers want to keep joinable across systems. Operators who
    still want the catch-all must explicitly set
    ``LOG_REDACT_HIGH_ENTROPY=true``.
    """
    return os.environ.get("LOG_REDACT_HIGH_ENTROPY", "").strip().lower() in {"1", "true", "yes", "on"}


# UUID (any version), canonical 8-4-4-4-12 hex. Left lower/upper mix tolerant.
_UUID_RE = re.compile(
    r"\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b"
)
# OTel W3C trace-id (32 lower-hex) and span-id (16 lower-hex).
_OTEL_TRACE_RE = re.compile(r"\b[0-9a-f]{32}\b")
_OTEL_SPAN_RE = re.compile(r"\b[0-9a-f]{16}\b")

# Sentinel used to mask identifier shapes so the subsequent redaction
# pass can't clobber them. Chosen to be ASCII-only, unlikely to appear
# in real text.
_IDENT_SENTINEL = "\x00NYX_IDENT_{}\x00"


# Ordered from most-specific to most-generic. Shape-specific rules fire
# first; the gated generic high-entropy rule fires last.
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
    # Credit card (Visa/MC/Amex/Discover-ish — 13-19 digits in groups).
    # Require at least one separator so bare 13-19 digit numeric strings
    # (timestamps, correlation ids) aren't swept up (#1034). Real CC
    # numbers almost always render with group separators when they
    # leak into logs.
    ("credit_card", re.compile(r"\b\d{4}[ -]\d{4}[ -]\d{4}[ -]\d{1,7}\b")),
    # US SSN
    ("ssn", re.compile(r"\b\d{3}-\d{2}-\d{4}\b")),
    # Email — full shape; redact to stop PII leaking into logs
    ("email", re.compile(r"\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b")),
]

# Gated separately (#1034) — only runs when LOG_REDACT_HIGH_ENTROPY is on.
_HIGH_ENTROPY_PATTERN: tuple[str, re.Pattern[str]] = (
    "high_entropy_token",
    re.compile(r"\b[A-Za-z0-9_-]{32,}\b"),
)


def _shield_identifiers(text: str) -> tuple[str, dict[str, str]]:
    """Replace UUID / OTel trace / span shapes with opaque sentinels.

    Returns the shielded text and a mapping that the caller can use
    to restore the originals after pattern substitution has run.
    """
    mapping: dict[str, str] = {}

    def _sub(match: re.Match[str]) -> str:
        original = match.group(0)
        key = _IDENT_SENTINEL.format(len(mapping))
        mapping[key] = original
        return key

    shielded = _UUID_RE.sub(_sub, text)
    shielded = _OTEL_TRACE_RE.sub(_sub, shielded)
    shielded = _OTEL_SPAN_RE.sub(_sub, shielded)
    return shielded, mapping


def _restore_identifiers(text: str, mapping: dict[str, str]) -> str:
    for sentinel, original in mapping.items():
        text = text.replace(sentinel, original)
    return text


def redact_text(text: str) -> str:
    """Return ``text`` with matched credential / PII spans replaced.

    The input is returned unchanged when LOG_REDACT is falsy so callers
    can unconditionally invoke this helper at log time without paying
    the regex cost on non-redacted deployments.

    UUIDs, W3C trace-ids (32 hex), and span-ids (16 hex) are shielded
    before pattern substitution and restored afterwards so downstream
    tooling can still join across traces (#1034).
    """
    if not text or not should_redact():
        return text
    shielded, mapping = _shield_identifiers(text)
    out = shielded
    for name, pat in _PATTERNS:
        if name == "authorization_header":
            out = pat.sub(r"\1" + _REDACTED, out)
        else:
            out = pat.sub(_REDACTED, out)
    if high_entropy_enabled():
        _, hi_pat = _HIGH_ENTROPY_PATTERN
        out = hi_pat.sub(_REDACTED, out)
    return _restore_identifiers(out, mapping)
