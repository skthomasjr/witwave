"""Outbound webhook delivery for completion-conditioned notifications.

Each .md file under WEBHOOKS_DIR defines one subscription. Frontmatter controls
filtering, delivery, and optional LLM-based body generation. The markdown body
(after the closing ---) provides context for LLM extraction prompts.

Filters (all must pass — AND logic):
  notify-when:        always | on_success (default) | on_error
  notify-on-kind:     fnmatch glob list matched against prompt kind string
  notify-on-response: fnmatch glob list matched against response_preview

LLM extraction:
  extract:
    var_name: prompt sent to LLM with rendered markdown body as context

Body template (frontmatter body: | block):
  Supports {{variable}} substitution with built-in variables, {{env.VAR}},
  and any variables defined under extract:.

Kind examples: a2a, heartbeat, job:daily-report, task:standup,
               trigger:github-push, continuation:followup
"""

import asyncio
import hashlib
import hmac
import ipaddress
import json
import logging
import os
import re
import socket
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from fnmatch import fnmatch
from pathlib import Path
from urllib.parse import urlsplit

import httpx
import yaml

from tracing import inject_traceparent, set_span_error, start_span
from metrics import (
    harness_file_watcher_restarts_total,
    harness_webhooks_delivery_shed_total,
    harness_webhooks_delivery_total,
    harness_webhooks_items_registered,
    harness_webhooks_parse_errors_total,
    harness_webhooks_reloads_total,
    harness_watcher_events_total,
)
from utils import parse_duration, parse_frontmatter, run_awatch_loop

logger = logging.getLogger(__name__)

WEBHOOKS_DIR = os.environ.get("WEBHOOKS_DIR", "/home/agent/.nyx/webhooks")
AGENT_NAME = os.environ.get("AGENT_NAME", "nyx")

# Global cap on total in-flight webhook delivery tasks across all subscriptions.
# When the cap is reached, new deliveries are shed (logged and counted).
# Override via WEBHOOK_MAX_CONCURRENT_DELIVERIES env var.
WEBHOOK_MAX_CONCURRENT_DELIVERIES = int(os.environ.get("WEBHOOK_MAX_CONCURRENT_DELIVERIES", "50"))

# Default per-subscription cap on in-flight delivery tasks.  Each subscription
# may override this via the max-concurrent-deliveries frontmatter field.
# Override the global default via WEBHOOK_MAX_CONCURRENT_DELIVERIES_PER_SUB env var.
WEBHOOK_MAX_CONCURRENT_DELIVERIES_PER_SUB = int(os.environ.get("WEBHOOK_MAX_CONCURRENT_DELIVERIES_PER_SUB", "10"))

# Maximum seconds to wait for a single LLM extraction call inside deliver().
# Prevents a slow or unresponsive backend from holding a delivery slot
# indefinitely.  Override via WEBHOOK_EXTRACTION_TIMEOUT env var.
WEBHOOK_EXTRACTION_TIMEOUT = float(os.environ.get("WEBHOOK_EXTRACTION_TIMEOUT", "120"))

# Total wall-clock ceiling for a single delivery's retry chain (#786). A
# stuck downstream receiver could otherwise hold a concurrency slot for
# `retries * timeout_seconds + sum(backoffs)` — up to several minutes on
# default settings. Bounding the whole chain with a single deadline keeps
# one bad peer from starving legitimate traffic. Default 300 s (5 min) is
# generous enough to accommodate a default retry chain (1 + 2 + 4 + 8 s
# backoff + per-attempt timeouts) on a flaky-but-recovering receiver,
# and short enough that a wedged receiver releases its slot promptly.
# Set to 0 to disable the ceiling (legacy unbounded behaviour).
WEBHOOK_TOTAL_TIMEOUT_SECONDS = float(os.environ.get("WEBHOOK_TOTAL_TIMEOUT_SECONDS", "300"))

# Connection-pool limits for the shared outbound httpx.AsyncClient (#567).
# Mirrors the A2ABackend shape so operators get consistent tuning knobs across
# subsystems. max_connections caps total simultaneous sockets; keepalive lets
# repeated deliveries to the same host reuse TCP+TLS state rather than paying
# the full handshake every time. Override via env vars.
WEBHOOK_CLIENT_MAX_CONNECTIONS = int(os.environ.get("WEBHOOK_CLIENT_MAX_CONNECTIONS", "50"))
WEBHOOK_CLIENT_MAX_KEEPALIVE = int(os.environ.get("WEBHOOK_CLIENT_MAX_KEEPALIVE", "20"))

# HTTP status codes that are safe to retry — mirrors the inbound A2A pattern
# at backends/a2a.py:35 (#598). 408 (Request Timeout) and 429 (Too Many
# Requests) are the only legitimately retryable 4xx codes; everything else in
# the 4xx range is treated as a permanent client error (bad URL, bad auth,
# validation failure) that will not recover by resending the same POST with
# the same body and headers. 5xx and connection errors remain retryable.
_RETRYABLE_STATUS_CODES: frozenset[int] = frozenset({408, 429, 500, 502, 503, 504})


def _is_retryable_http(status_code: int) -> bool:
    """Return True when an HTTP status code warrants another delivery attempt.

    Retries any 5xx response plus the explicit retryable 4xx codes (408, 429).
    All other 4xx codes are treated as permanent client errors (#598).
    """
    if status_code in _RETRYABLE_STATUS_CODES:
        return True
    # Any other 5xx (e.g. 501, 507) — still transient server-side.
    return 500 <= status_code < 600


def _build_shared_client() -> httpx.AsyncClient:
    """Construct the shared outbound AsyncClient used across all webhook
    deliveries and retries (#567).

    Per-delivery timeouts are passed at `client.post(..., timeout=...)` time
    so each subscription's `timeout_seconds` is still honored; the client-
    level timeout is a defensive upper bound only. `follow_redirects=False`
    is preserved from the prior per-call construction to avoid turning a
    single webhook POST into an SSRF fan-out via a 30x to an internal host
    (#524 coordination).
    """
    return httpx.AsyncClient(
        timeout=httpx.Timeout(connect=10.0, read=30.0, write=30.0, pool=5.0),
        follow_redirects=False,
        limits=httpx.Limits(
            max_connections=WEBHOOK_CLIENT_MAX_CONNECTIONS,
            max_keepalive_connections=WEBHOOK_CLIENT_MAX_KEEPALIVE,
        ),
    )

_VALID_NOTIFY_WHEN = ("always", "on_success", "on_error")

_DISABLED = object()

_ENV_VAR_RE = re.compile(r"\{\{env\.(\w+)\}\}")
_VAR_RE = re.compile(r"\{\{(\w+)\}\}")

# Documented allow-list of built-in variables that may appear in the URL
# template (see README "Outbound Webhooks"). Extraction-defined variables and
# {{env.VAR}} references are intentionally NOT permitted in URLs, to prevent
# env-var exfiltration and LLM-steered SSRF via webhook .md files (#524).
_URL_TEMPLATE_ALLOWED_VARS = frozenset({
    "agent",
    "kind",
    "session_id",
    "source",
    "model",
    "success",
    "error",
    "response_preview",
    "duration_seconds",
    "timestamp",
    "delivery_id",
})

# Scheme / host allow-list for outbound webhook URLs (#524). Only http(s) is
# permitted; file://, gopher://, ftp://, data:, etc. are rejected. Hosts that
# resolve to loopback/link-local/private/reserved IP literals are rejected so
# that cloud metadata endpoints (169.254.169.254) and arbitrary internal
# service IPs cannot be reached via a webhook .md file. Operators can widen
# the host allow-list via the WEBHOOK_URL_ALLOWED_HOSTS env var (comma-
# separated list of host[:port] entries) when internal delivery targets are
# legitimate (e.g. in-cluster sinks for smoke tests).
_URL_ALLOWED_SCHEMES = ("http", "https")
_URL_ALLOWED_HOSTS = tuple(
    h.strip().lower()
    for h in os.environ.get("WEBHOOK_URL_ALLOWED_HOSTS", "").split(",")
    if h.strip()
)


# Event kinds this runner knows how to fan out. The default-event used when
# a subscription omits ``events:`` is ``completion`` — the legacy
# "agent prompt completed" fan-out that predates #633 — so existing .md
# files keep their behavior. ``hook.decision`` is the scaffolded kind added
# for #633; its backend→harness transport is still to-do (see the close
# comment and follow-up gap), but subscribers can already opt in to
# hook.decision[:deny|:warn|:allow] so the filter path can be covered by
# tests and documentation today.
EVENT_KIND_COMPLETION = "completion"
EVENT_KIND_HOOK_DECISION = "hook.decision"
_DEFAULT_EVENT_KINDS: tuple[str, ...] = (EVENT_KIND_COMPLETION,)
_KNOWN_EVENT_KINDS: frozenset[str] = frozenset({EVENT_KIND_COMPLETION, EVENT_KIND_HOOK_DECISION})
_HOOK_DECISION_SUB_KINDS: frozenset[str] = frozenset({"allow", "warn", "deny"})


@dataclass
class WebhookSubscription:
    path: str
    name: str
    url_template: str                          # may contain {{env.VAR}}
    enabled: bool = True
    signing_secret: str | None = None
    notify_when: str = "on_success"
    notify_on_kind: list[str] = field(default_factory=list)
    notify_on_response: list[str] = field(default_factory=list)
    content_type: str = "application/json"
    description: str | None = None
    headers: dict[str, str] = field(default_factory=dict)  # values may contain {{env.VAR}}
    timeout_seconds: float = 10.0
    retries: int = 0
    extract: dict[str, str] = field(default_factory=dict)  # var_name -> prompt
    body_template: str | None = None          # frontmatter body: | block
    context_template: str | None = None       # markdown body — context for LLM extractions
    backend_id: str | None = None
    model: str | None = None
    max_concurrent_deliveries: int = WEBHOOK_MAX_CONCURRENT_DELIVERIES_PER_SUB
    # Subscribed event kinds (#633). ``None`` / missing in frontmatter means
    # "legacy completion-only fan-out" so existing subscribers are unaffected.
    # Accepted entries: "completion", "hook.decision", or the qualified forms
    # "hook.decision:allow" / ":warn" / ":deny" for finer-grained filtering.
    events: list[str] = field(default_factory=lambda: list(_DEFAULT_EVENT_KINDS))


def _normalize_events_field(raw: object, path: str) -> list[str]:
    """Normalize and validate a frontmatter ``events:`` value.

    Returns the default (``["completion"]``) when the field is missing or
    empty, preserving the pre-#633 behavior for subscribers that do not opt
    in to the new kinds. Unknown kinds are dropped with a warning so a typo
    never silently widens the subscription.
    """
    if raw is None or raw == "":
        return list(_DEFAULT_EVENT_KINDS)
    items = _parse_list_field(raw)
    if not items:
        return list(_DEFAULT_EVENT_KINDS)
    cleaned: list[str] = []
    for item in items:
        base, _, sub = item.partition(":")
        base = base.strip()
        sub = sub.strip()
        if base not in _KNOWN_EVENT_KINDS:
            logger.warning(
                f"Webhook file {path}: unknown event kind {item!r} — dropping. "
                f"Known kinds: {sorted(_KNOWN_EVENT_KINDS)}."
            )
            continue
        if sub:
            if base != EVENT_KIND_HOOK_DECISION or sub not in _HOOK_DECISION_SUB_KINDS:
                logger.warning(
                    f"Webhook file {path}: unsupported event qualifier {item!r} — dropping. "
                    f"hook.decision supports {sorted(_HOOK_DECISION_SUB_KINDS)}."
                )
                continue
            cleaned.append(f"{base}:{sub}")
        else:
            cleaned.append(base)
    return cleaned or list(_DEFAULT_EVENT_KINDS)


def _events_match(sub_events: list[str], event_kind: str, qualifier: str | None) -> bool:
    """Return True when *sub_events* subscribes to this (kind, qualifier) pair.

    Matching rules:
      * ``completion`` — plain kind match, no qualifier.
      * ``hook.decision`` — matches either the bare kind (any qualifier) or
        the exact ``hook.decision:<qualifier>`` entry.
    """
    if event_kind == EVENT_KIND_COMPLETION:
        return EVENT_KIND_COMPLETION in sub_events
    if event_kind == EVENT_KIND_HOOK_DECISION:
        if EVENT_KIND_HOOK_DECISION in sub_events:
            return True
        if qualifier and f"{EVENT_KIND_HOOK_DECISION}:{qualifier}" in sub_events:
            return True
        return False
    return False


def _resolve_env_vars(text: str) -> str:
    """Replace {{env.VAR}} references with their environment variable values."""
    def _sub(m: re.Match) -> str:
        return os.environ.get(m.group(1), "")
    return _ENV_VAR_RE.sub(_sub, text)


def _url_template_has_forbidden_refs(template: str) -> str | None:
    """Return a diagnostic string if the URL template references variables
    outside the documented allow-list, else None.

    Enforced at parse time so misconfigured subscriptions fail fast rather
    than silently allowing env-var exfiltration or extraction-steered SSRF
    (#524).
    """
    if _ENV_VAR_RE.search(template):
        return "{{env.VAR}} is not allowed in the url template"
    for m in _VAR_RE.finditer(template):
        key = m.group(1).strip()
        if key not in _URL_TEMPLATE_ALLOWED_VARS:
            return f"{{{{{key}}}}} is not allowed in the url template"
    return None


def _substitute_url(template: str, context: dict) -> str:
    """Substitute ONLY built-in allow-listed {{var}} references in a URL
    template. {{env.VAR}} and extraction-defined variables are never
    expanded here — they are already rejected at parse time, but this
    function strips any unexpected reference (including {{env.VAR}}) to
    empty string as a defense-in-depth measure (#524).
    """
    # Strip any {{env.VAR}} references first so they cannot survive in
    # the final URL even if parse-time validation was bypassed.
    result = _ENV_VAR_RE.sub("", template)
    def _replacer(m: re.Match) -> str:
        key = m.group(1).strip()
        if key not in _URL_TEMPLATE_ALLOWED_VARS:
            return ""
        return str(context.get(key, ""))
    return _VAR_RE.sub(_replacer, result)


# Literal hostnames that always target loopback. Reject these outright in
# _validate_url so a misconfigured or hostile resolver cannot let them
# through by mapping to a non-loopback address (#654).
_LOOPBACK_HOST_ALIASES = frozenset(
    {
        "localhost",
        "localhost.localdomain",
        "ip6-localhost",
        "ip6-loopback",
    }
)


def _host_is_private(host: str) -> bool:
    """Return True if host is an IP literal in a loopback/link-local/private/
    reserved range. Hostnames (non-IP) return False — DNS resolution is not
    performed here to avoid a TOCTOU gap; the shared httpx client is the
    chokepoint for the resolved-IP check (#524 / #567 coordination).
    """
    try:
        ip = ipaddress.ip_address(host)
    except ValueError:
        return False
    return (
        ip.is_loopback
        or ip.is_link_local
        or ip.is_private
        or ip.is_reserved
        or ip.is_multicast
        or ip.is_unspecified
    )


async def _validate_url_async(url: str) -> str | None:
    """Async counterpart of :func:`_validate_url` (#699, #705).

    Runs the entire sync validator in a worker thread via
    ``asyncio.to_thread`` so the blocking ``socket.getaddrinfo`` call
    no longer stalls the harness event loop. The synchronous check
    remains in use at parse time (runs once per file change) where
    event-loop cost is irrelevant.
    """
    return await asyncio.to_thread(_validate_url, url)


def _validate_url(url: str) -> str | None:
    """Return a diagnostic string if the URL is not safe to POST to, else
    None. Applied to both literal `url:` values and env-var-resolved URLs
    (including `url-env-var`) to close the SSRF surface (#524).
    """
    if not url:
        return "empty url"
    try:
        parts = urlsplit(url)
    except ValueError as exc:
        return f"unparseable url: {exc}"
    scheme = (parts.scheme or "").lower()
    if scheme not in _URL_ALLOWED_SCHEMES:
        return f"scheme {scheme!r} is not allowed (only http/https)"
    host = (parts.hostname or "").lower()
    if not host:
        return "url has no host component"
    # Operator-configured host allow-list takes precedence over the
    # private-IP default-deny. This lets smoke tests and in-cluster sinks
    # opt in explicitly without re-enabling general SSRF.
    port = parts.port
    host_port = f"{host}:{port}" if port is not None else host
    if _URL_ALLOWED_HOSTS and (
        host in _URL_ALLOWED_HOSTS or host_port in _URL_ALLOWED_HOSTS
    ):
        return None
    # Reject known loopback aliases up front so a misconfigured resolver
    # cannot let them through (#654).
    if host in _LOOPBACK_HOST_ALIASES:
        return (
            f"host {host!r} is a loopback alias and is not in "
            "WEBHOOK_URL_ALLOWED_HOSTS"
        )
    if _host_is_private(host):
        return (
            f"host {host!r} is a loopback/link-local/private/reserved IP "
            "and is not in WEBHOOK_URL_ALLOWED_HOSTS"
        )
    # Resolve the hostname and apply _host_is_private to each resolved
    # address. Closes the SSRF gap where a DNS name (e.g. "localhost",
    # "metadata.google.internal") mapped to a loopback/link-local/private
    # address but the old string-only check let the URL through because
    # ipaddress.ip_address(host) raised ValueError (#654).
    try:
        infos = socket.getaddrinfo(host, None)
    except socket.gaierror as _dns_exc:
        return f"host {host!r} failed DNS resolution: {_dns_exc}"
    seen_addrs: set[str] = set()
    for family, _type, _proto, _canon, sockaddr in infos:
        addr = sockaddr[0] if sockaddr else ""
        if not addr or addr in seen_addrs:
            continue
        seen_addrs.add(addr)
        if _host_is_private(addr):
            return (
                f"host {host!r} resolves to {addr!r}, which is a "
                "loopback/link-local/private/reserved IP and is not in "
                "WEBHOOK_URL_ALLOWED_HOSTS"
            )
    return None


def parse_webhook_file(path: str) -> "WebhookSubscription | object | None":
    """Parse a webhook subscription file. Returns:
    - WebhookSubscription on success
    - _DISABLED sentinel when enabled: false
    - None on parse error
    """
    try:
        with open(path) as f:
            raw = f.read()

        # Use yaml.safe_load directly to preserve dict/int types for headers/extract/retries.
        _frontmatter_re = re.compile(r"^---\s*\n(.*?)\n---\s*\n?(.*)", re.DOTALL)
        match = _frontmatter_re.match(raw)
        if match:
            parsed_yaml = yaml.safe_load(match.group(1)) or {}
            context_body = match.group(2).strip()
        else:
            parsed_yaml = {}
            context_body = raw.strip()

        # Stringified fields dict for simple scalar fields (mirrors parse_frontmatter behaviour)
        fields: dict[str, str] = {k: str(v) if v is not None else "" for k, v in parsed_yaml.items()}

        enabled = True
        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() not in ("false", "")
        if not enabled:
            # Return a minimal disabled subscription so the dashboard
            # lists it; no delivery machinery is armed until the file
            # flips enabled:true. URL / header validation is skipped —
            # the webhook isn't going to fire, a bad URL on a parked
            # subscription shouldn't prevent listing.
            filename = Path(path).stem
            name = fields.get("name") or filename
            return WebhookSubscription(
                path=path,
                name=name,
                url_template=fields.get("url") or "(disabled — url not validated)",
                enabled=False,
                description=fields.get("description") or None,
                backend_id=fields.get("agent") or None,
                model=fields.get("model") or None,
            )

        # Resolve URL. The URL template is restricted to the documented
        # allow-list of built-in variables (see README "Outbound Webhooks");
        # {{env.VAR}} interpolation in the url field is intentionally rejected
        # — env-derived URLs must use `url-env-var` so the resolved string can
        # be validated as a whole (#524). Either way, the final URL is
        # additionally filtered through the scheme / host allow-list.
        url_template = fields.get("url") or ""
        url_from_env_var = False
        if not url_template:
            env_var = fields.get("url-env-var") or None
            if env_var:
                url_template = os.environ.get(env_var) or ""
                url_from_env_var = True
        if not url_template:
            logger.warning(f"Webhook file {path}: no resolvable URL — skipping.")
            return None
        if not url_from_env_var:
            forbidden = _url_template_has_forbidden_refs(url_template)
            if forbidden is not None:
                logger.error(
                    f"Webhook file {path}: url template rejected — {forbidden}. "
                    "Move env-derived URLs to `url-env-var` and only use "
                    f"allow-listed variables {sorted(_URL_TEMPLATE_ALLOWED_VARS)} "
                    "in the url template."
                )
                return None
        # Scheme / host allow-list check. For templated URLs the substituted
        # built-in variables don't change scheme/host, so validating the
        # template literal here catches misconfiguration at parse time. For
        # env-var-resolved URLs the resolved string is validated directly.
        url_check_target = url_template
        if not url_from_env_var:
            # Strip {{var}} placeholders so urlsplit sees a clean authority.
            url_check_target = _VAR_RE.sub("", url_template)
        url_error = _validate_url(url_check_target)
        if url_error is not None:
            logger.error(f"Webhook file {path}: url rejected — {url_error}.")
            return None

        # Resolve signing secret
        signing_secret: str | None = None
        secret_env_var = fields.get("signing-secret-env-var") or fields.get("signing_secret_env_var") or None
        if secret_env_var:
            signing_secret = os.environ.get(secret_env_var) or None

        notify_when = fields.get("notify-when") or "on_success"
        if notify_when not in _VALID_NOTIFY_WHEN:
            logger.warning(f"Webhook file {path}: invalid notify-when {notify_when!r} — defaulting to on_success.")
            notify_when = "on_success"

        # notify-on-kind / notify-on-response
        notify_on_kind = _parse_list_field(parsed_yaml.get("notify-on-kind") or "")
        notify_on_response = _parse_list_field(parsed_yaml.get("notify-on-response") or "")

        content_type = fields.get("content-type") or "application/json"

        # headers — YAML map; values support {{env.VAR}}
        raw_headers = parsed_yaml.get("headers") or {}
        headers: dict[str, str] = {}
        if isinstance(raw_headers, dict):
            for k, v in raw_headers.items():
                headers[str(k)] = str(v) if v is not None else ""

        # timeout
        timeout_seconds = 10.0
        raw_timeout = fields.get("timeout") or ""
        if raw_timeout:
            try:
                timeout_seconds = parse_duration(raw_timeout)
            except ValueError:
                logger.warning(f"Webhook file {path}: invalid timeout {raw_timeout!r} — using 10s.")

        # retries
        retries = 0
        raw_retries = parsed_yaml.get("retries")
        if raw_retries is not None:
            try:
                retries = max(0, int(raw_retries))
            except (ValueError, TypeError):
                logger.warning(f"Webhook file {path}: invalid retries {raw_retries!r} — using 0.")

        # extract — YAML map of var_name -> prompt string
        raw_extract = parsed_yaml.get("extract") or {}
        extract: dict[str, str] = {}
        if isinstance(raw_extract, dict):
            for k, v in raw_extract.items():
                extract[str(k)] = str(v) if v is not None else ""

        # body — frontmatter literal block scalar (body: |)
        raw_body = parsed_yaml.get("body") or None
        body_template = str(raw_body).strip() if raw_body is not None else None

        # backend / model overrides for extraction calls
        backend_id = fields.get("agent") or None
        model = fields.get("model") or None

        # per-subscription concurrent delivery cap
        max_concurrent_deliveries = WEBHOOK_MAX_CONCURRENT_DELIVERIES_PER_SUB
        raw_max_del = parsed_yaml.get("max-concurrent-deliveries") or parsed_yaml.get("max_concurrent_deliveries")
        if raw_max_del is not None:
            try:
                max_concurrent_deliveries = max(1, int(raw_max_del))
            except (ValueError, TypeError):
                logger.warning(f"Webhook file {path}: invalid 'max-concurrent-deliveries' {raw_max_del!r} — using default {WEBHOOK_MAX_CONCURRENT_DELIVERIES_PER_SUB}.")

        filename = Path(path).stem
        name = fields.get("name") or filename
        description = fields.get("description") or None

        # events: list (#633) — defaults to legacy completion-only when absent
        # so existing .md files keep their pre-#633 fan-out semantics.
        events = _normalize_events_field(parsed_yaml.get("events"), path)

        return WebhookSubscription(
            path=path,
            name=name,
            url_template=url_template,
            enabled=enabled,
            signing_secret=signing_secret,
            notify_when=notify_when,
            notify_on_kind=notify_on_kind,
            notify_on_response=notify_on_response,
            content_type=content_type,
            description=description,
            headers=headers,
            timeout_seconds=timeout_seconds,
            retries=retries,
            extract=extract,
            body_template=body_template,
            context_template=context_body if context_body else None,
            backend_id=backend_id,
            model=model,
            max_concurrent_deliveries=max_concurrent_deliveries,
            events=events,
        )

    except Exception as e:
        if harness_webhooks_parse_errors_total is not None:
            harness_webhooks_parse_errors_total.inc()
        logger.error(f"Webhook file {path}: failed to parse — {e}, skipping.")
        return None


def _parse_list_field(value) -> list[str]:
    """Parse a frontmatter field that may be a YAML list, string, or comma-separated string."""
    if not value:
        return []
    if isinstance(value, list):
        return [str(x).strip() for x in value if str(x).strip()]
    stripped = str(value).strip()
    if not stripped or stripped == "[]":
        return []
    if stripped.startswith("["):
        try:
            import ast
            parsed = ast.literal_eval(stripped)
            if isinstance(parsed, list):
                return [str(x).strip() for x in parsed if str(x).strip()]
        except Exception:
            pass
    return [x.strip() for x in stripped.split(",") if x.strip()]


def _matches_filters(
    sub: WebhookSubscription,
    success: bool,
    kind: str,
    response_preview: str,
) -> bool:
    """Return True if all filters pass for this subscription (legacy completion fan-out).

    A subscription that does not list ``completion`` in its ``events:`` field
    is considered opted out of the legacy fan-out path, so completion events
    skip it entirely (#633).
    """
    if not _events_match(sub.events, EVENT_KIND_COMPLETION, None):
        return False

    if sub.notify_when == "on_success" and not success:
        return False
    if sub.notify_when == "on_error" and success:
        return False

    if sub.notify_on_kind:
        if not any(fnmatch(kind, pattern) for pattern in sub.notify_on_kind):
            return False

    if sub.notify_on_response:
        if not any(fnmatch(response_preview, pattern) for pattern in sub.notify_on_response):
            return False

    return True


def _substitute(template: str, context: dict) -> str:
    """Substitute {{var}} and {{env.VAR}} references in a template string."""
    # First resolve env vars
    result = _resolve_env_vars(template)
    # Then substitute context variables
    def _replacer(m: re.Match) -> str:
        key = m.group(1).strip()
        return str(context.get(key, ""))
    return _VAR_RE.sub(_replacer, result)


def _render_default_envelope(context: dict) -> str:
    """Build the default JSON envelope when no body template is provided."""
    envelope = {
        "event": "agent.prompt.completed",
        "agent": context.get("agent", AGENT_NAME),
        "timestamp": context.get("timestamp", ""),
        "delivery_id": context.get("delivery_id", ""),
        "payload": {
            "kind": context.get("kind", ""),
            "session_id": context.get("session_id", ""),
            "success": context.get("success", False),
            "error": context.get("error") or None,
            "duration_seconds": context.get("duration_seconds", 0.0),
            "response_preview": context.get("response_preview", ""),
            "model": context.get("model") or None,
        },
    }
    return json.dumps(envelope)


def _render_hook_decision_envelope(context: dict) -> str:
    """Default JSON envelope for the ``hook.decision`` webhook event kind (#633).

    The ``traceparent`` field carries the W3C trace-context header so webhook
    receivers can correlate with the trace that produced the decision. All
    fields match the attribute set stamped onto the OTel span event in
    :func:`claude/executor._make_pre_tool_use_hook._hook`, so operators
    see the same shape whether they consume traces or webhooks.
    """
    envelope = {
        "event": "hook.decision",
        "agent": context.get("agent", AGENT_NAME),
        "timestamp": context.get("timestamp", ""),
        "delivery_id": context.get("delivery_id", ""),
        "payload": {
            "session_id": context.get("session_id", ""),
            "tool": context.get("tool", ""),
            "decision": context.get("decision", ""),
            "rule_name": context.get("rule_name", ""),
            "reason": context.get("reason", ""),
            "source": context.get("source", ""),
            "traceparent": context.get("traceparent") or None,
        },
    }
    return json.dumps(envelope)


def _sign_body(body_bytes: bytes, secret: str) -> str:
    """Compute X-Hub-Signature-256 over raw body bytes."""
    mac = hmac.new(secret.encode(), body_bytes, hashlib.sha256)
    return f"sha256={mac.hexdigest()}"


async def _run_extraction(
    prompt: str,
    backends: dict,
    default_backend_id: str,
    backend_id: str | None,
    model: str | None,
    session_id: str,
) -> str:
    """Send an extraction prompt to the backend and return the response text."""
    resolved_id = backend_id or default_backend_id
    backend = backends.get(resolved_id)
    if backend is None:
        raise ValueError(f"No backend configured with id '{resolved_id}'")
    chunks = await backend.run_query(prompt, session_id, is_new=True, model=model)
    return "\n\n".join(chunks).strip() if chunks else ""


async def _retry_deliver(
    sub: WebhookSubscription,
    url: str,
    body_bytes: bytes,
    headers: dict,
    attempt: int,
    max_attempts: int,
    client: httpx.AsyncClient,
) -> None:
    """Continue delivery for a subscription after the first attempt failed.

    Called as a new independent background task so the original delivery task
    can exit immediately, releasing its concurrency slot before the backoff
    sleep.  This prevents sleeping retries from holding capacity and starving
    new deliveries during a downstream outage (#362).

    Reuses the shared `httpx.AsyncClient` owned by `WebhookRunner` instead of
    opening a fresh client (and paying a fresh TCP+TLS handshake) for every
    retry attempt (#567). Per-attempt timeout is still `sub.timeout_seconds`,
    passed at `client.post(...)` time so behavior matches the pre-shared-
    client semantics.
    """
    # Per-attempt metric emission (#865). Previously only the final chain
    # result was recorded, so attempts 2..N were invisible when the outer
    # total-chain wait_for cancelled mid-flight. Emit one
    # harness_webhooks_delivery_total per attempt inside each branch and
    # under CancelledError, so the counter sums to attempts_made rather
    # than 1.
    def _record(_result: str) -> None:
        if harness_webhooks_delivery_total is not None:
            harness_webhooks_delivery_total.labels(
                result=_result, subscription=sub.name
            ).inc()

    result = "unknown"
    while attempt < max_attempts:
        backoff = 2 ** (attempt - 1)  # 1s, 2s, 4s, ...
        try:
            await asyncio.sleep(backoff)
        except asyncio.CancelledError:
            # Cancelled during backoff sleep → no attempt was made for
            # this iteration; no metric to record for this step. Propagate.
            raise
        attempt += 1
        try:
            # TOCTOU re-check before each retry (#699). A DNS-rebinding
            # attacker could otherwise flip the A record during the
            # exponential backoff.
            url_recheck = await _validate_url_async(url)
            if url_recheck is not None:
                logger.error(
                    f"Webhook '{sub.name}': URL failed re-validation before retry {attempt} — {url_recheck}; aborting retry chain."
                )
                result = "url_revalidation_failed"
                _record(result)
                break
            resp = await client.post(
                url,
                content=body_bytes,
                headers=headers,
                timeout=sub.timeout_seconds,
            )
            if resp.status_code < 400:
                result = "success"
                logger.info(f"Webhook '{sub.name}' delivered to {url} — {resp.status_code} (attempt {attempt})")
                _record(result)
                break
            else:
                result = f"http_{resp.status_code}"
                _record(result)
                if not _is_retryable_http(resp.status_code):
                    logger.warning(
                        f"Webhook '{sub.name}' attempt {attempt} got {resp.status_code} — "
                        f"permanent client error; aborting retry chain (#598)"
                    )
                    break
                logger.warning(f"Webhook '{sub.name}' attempt {attempt} got {resp.status_code}")
        except asyncio.CancelledError:
            # Outer wait_for(total_timeout) cancelled this attempt after
            # the POST was in flight. Count the abandoned attempt so the
            # metric reflects work actually initiated (#865). The outer
            # wrapper separately emits 'timeout_total' for the chain.
            _record("cancelled")
            raise
        except Exception as exc:
            result = "error"
            logger.warning(f"Webhook '{sub.name}' attempt {attempt} failed: {exc!r}")
            _record(result)


async def deliver(
    sub: WebhookSubscription,
    source: str,
    kind: str,
    session_id: str,
    success: bool,
    response: str,
    duration_seconds: float,
    error: str | None,
    model: str | None,
    client: httpx.AsyncClient,
    backends: dict | None = None,
    default_backend_id: str | None = None,
    on_retry_task=None,
    trace_context=None,
) -> None:
    """Deliver one webhook POST. Called as a fire-and-forget background task.

    If a retry is needed and retries are configured, the retry task is created
    and passed to on_retry_task(task) (if provided) so that the caller can
    register it in its concurrency-tracking structures.
    """
    delivery_id = str(uuid.uuid4())
    timestamp = datetime.now(timezone.utc).isoformat()
    response_preview = response[:2048] if response else ""

    context: dict = {
        "agent": AGENT_NAME,
        "kind": kind,
        "session_id": session_id,
        "success": success,
        "error": error or "",
        "duration_seconds": round(duration_seconds, 3),
        "response_preview": response_preview,
        "model": model or "",
        "timestamp": timestamp,
        "delivery_id": delivery_id,
        "source": source,
    }

    # Resolve URL BEFORE running extractions so LLM-extracted variables
    # cannot participate in URL resolution (#524). Only the documented
    # built-in variables are substituted; {{env.VAR}} was already rejected
    # at parse time for literal templates, and env-var-resolved URLs are
    # used verbatim (they contain no templating by construction).
    url = _substitute_url(sub.url_template, context)
    if not url:
        logger.warning(f"Webhook '{sub.name}': URL resolved to empty string — skipping delivery.")
        return
    url_error = await _validate_url_async(url)
    if url_error is not None:
        logger.error(f"Webhook '{sub.name}': resolved URL rejected — {url_error}.")
        return

    # Run LLM extractions if defined and backends are available. Extraction
    # results are only used for body/headers rendering — never for the URL.
    if sub.extract and backends and default_backend_id:
        extraction_session_id = f"webhook-extract-{delivery_id}"
        # Render the context template (markdown body) with built-in variables
        context_text = _substitute(sub.context_template, context) if sub.context_template else response_preview

        for var_name, extraction_prompt in sub.extract.items():
            full_prompt = f"{context_text}\n\n{extraction_prompt}" if context_text else extraction_prompt
            try:
                result = await asyncio.wait_for(
                    _run_extraction(
                        prompt=full_prompt,
                        backends=backends,
                        default_backend_id=default_backend_id,
                        backend_id=sub.backend_id,
                        model=sub.model or model,
                        session_id=extraction_session_id,
                    ),
                    timeout=WEBHOOK_EXTRACTION_TIMEOUT,
                )
                context[var_name] = result
            except asyncio.TimeoutError:
                logger.warning(
                    f"Webhook '{sub.name}': extraction '{var_name}' timed out after "
                    f"{WEBHOOK_EXTRACTION_TIMEOUT}s. Using empty string."
                )
                context[var_name] = ""
            except Exception as exc:
                logger.warning(f"Webhook '{sub.name}': extraction '{var_name}' failed — {exc!r}. Using empty string.")
                context[var_name] = ""

    # Render body
    if sub.body_template:
        body = _substitute(sub.body_template, context)
    else:
        body = _render_default_envelope(context)

    # Cap at 256 KiB — decode back after slicing to avoid splitting multi-byte sequences
    body_bytes = body.encode("utf-8")
    if len(body_bytes) > 256 * 1024:
        body_bytes = body_bytes[: 256 * 1024].decode("utf-8", errors="ignore").encode("utf-8")

    # Build headers — resolve {{env.VAR}} and context variables
    headers = {"Content-Type": sub.content_type}
    for k, v in sub.headers.items():
        headers[k] = _substitute(v, context)
    if sub.signing_secret:
        headers["X-Hub-Signature-256"] = _sign_body(body_bytes, sub.signing_secret)
    # Propagate W3C trace context to webhook receivers (#468). Each outbound
    # delivery gets a fresh child span_id so retries/replays can be
    # distinguished downstream while staying correlated to the same trace_id.
    if trace_context is not None:
        headers["traceparent"] = trace_context.child().to_header()

    # First delivery attempt — make one HTTP POST without holding a retry slot.
    # If it fails and retries are configured, schedule the next attempt as a
    # new background task after the backoff delay so the current task exits
    # immediately and releases its concurrency slot.  This prevents sleeping
    # retries from exhausting the per-subscription and global caps and starving
    # new deliveries during a downstream outage (#362).
    result = "unknown"
    with start_span(
        "webhook.deliver",
        kind="client",
        attributes={
            "nyx.webhook.name": sub.name,
            "nyx.kind": kind,
            "nyx.session_id": session_id,
            "http.url": url,
        },
    ) as _span:
        # Stamp OTel traceparent into the outbound headers if enabled.
        # When OTel is off, the trace_context carrier header set above
        # remains in place unchanged (#469).
        inject_traceparent(headers)
        try:
            # TOCTOU re-check against DNS rebinding (#699). Revalidate
            # the resolved addresses immediately before the POST so an
            # attacker cannot flip the A record between parse-time
            # validation and delivery. Window between this check and
            # httpx's own resolve is bounded by the resolver TTL + our
            # RTT, which is orders of magnitude smaller than the
            # multi-minute delivery window that used to exist.
            url_recheck = await _validate_url_async(url)
            if url_recheck is not None:
                logger.error(
                    f"Webhook '{sub.name}': URL failed re-validation before POST — {url_recheck}; aborting delivery."
                )
                return
            # Reuse the shared AsyncClient owned by WebhookRunner so deliveries
            # to the same receiver benefit from connection pooling and
            # keep-alive instead of paying a fresh TCP+TLS handshake per
            # attempt (#567). Per-subscription timeout is still honored by
            # passing it at post() time.
            resp = await client.post(
                url,
                content=body_bytes,
                headers=headers,
                timeout=sub.timeout_seconds,
            )
            if resp.status_code < 400:
                result = "success"
                logger.info(f"Webhook '{sub.name}' delivered to {url} — {resp.status_code}")
            else:
                result = f"http_{resp.status_code}"
                if not _is_retryable_http(resp.status_code):
                    logger.warning(
                        f"Webhook '{sub.name}' attempt 1 got {resp.status_code} — "
                        f"permanent client error; skipping retry chain (#598)"
                    )
                else:
                    logger.warning(f"Webhook '{sub.name}' attempt 1 got {resp.status_code}")
        except Exception as exc:
            result = "error"
            set_span_error(_span, exc)
            logger.warning(f"Webhook '{sub.name}' attempt 1 failed: {exc!r}")

    # Decide whether the first-attempt outcome warrants scheduling the retry
    # chain. Network/timeout errors ("error") and retryable HTTP codes follow
    # the retry path; permanent 4xx client errors fall through to the single-
    # record metric path below (#598).
    _is_retryable_result = result == "error" or (
        result.startswith("http_") and _is_retryable_http(int(result.split("_", 1)[1]))
    )
    if result != "success" and sub.retries > 0 and _is_retryable_result:
        # Record the initial failure before dispatching the retry chain so that
        # the first attempt's outcome is always visible in metrics (#375/#382).
        if harness_webhooks_delivery_total is not None:
            harness_webhooks_delivery_total.labels(result=result, subscription=sub.name).inc()
        # Schedule retry as a new independent task so this task can exit now,
        # freeing its slot before the backoff delay begins.
        #
        # Registration order matters (#515): the retry task must be present in
        # the caller's tracking structures BEFORE the underlying work coroutine
        # starts executing. We use a wrapper coroutine gated on a `registered`
        # event: the task starts, immediately awaits registration, and only
        # then proceeds to the real retry work. The caller registers the task
        # and signals the event; if registration raises, we cancel the task so
        # it cannot leak untracked.
        registered = asyncio.Event()

        async def _tracked_retry() -> None:
            await registered.wait()
            _retry_coro = _retry_deliver(
                sub=sub,
                url=url,
                body_bytes=body_bytes,
                headers=headers,
                attempt=1,
                max_attempts=1 + sub.retries,
                client=client,
            )
            # Total-chain timeout (#786): cap wall-clock across all retries
            # + backoffs so a wedged downstream can't hold a concurrency
            # slot indefinitely. 0 disables the ceiling (legacy behaviour).
            if WEBHOOK_TOTAL_TIMEOUT_SECONDS > 0:
                try:
                    await asyncio.wait_for(_retry_coro, timeout=WEBHOOK_TOTAL_TIMEOUT_SECONDS)
                except asyncio.TimeoutError:
                    logger.warning(
                        f"Webhook '{sub.name}': retry chain exceeded "
                        f"WEBHOOK_TOTAL_TIMEOUT_SECONDS={WEBHOOK_TOTAL_TIMEOUT_SECONDS:.0f}s — "
                        f"abandoning remaining attempts for url={url}"
                    )
                    if harness_webhooks_delivery_total is not None:
                        harness_webhooks_delivery_total.labels(
                            result="timeout_total", subscription=sub.name
                        ).inc()
            else:
                await _retry_coro

        _retry_task = asyncio.ensure_future(_tracked_retry())
        # Notify the caller (e.g. WebhookRunner.fire) so it can register the
        # retry task in its concurrency-tracking structures (#376). If
        # registration fails, cancel the scheduled task so it cannot run
        # untracked and leak (#515).
        if on_retry_task is not None:
            try:
                on_retry_task(_retry_task)
            except Exception:
                _retry_task.cancel()
                raise
            finally:
                registered.set()
        else:
            registered.set()
        return

    if harness_webhooks_delivery_total is not None:
        harness_webhooks_delivery_total.labels(result=result, subscription=sub.name).inc()


async def _deliver_hook_decision(
    sub: WebhookSubscription,
    context: dict,
    client: httpx.AsyncClient,
) -> None:
    """Slim deliverer for hook.decision events (#633).

    Unlike :func:`deliver`, hook.decision events carry no ``response`` to run
    LLM extractions against, so this path skips the extraction pipeline and
    uses the dedicated :func:`_render_hook_decision_envelope` when the
    subscription does not supply its own ``body:`` template. Retry semantics
    and per-attempt timeouts are intentionally omitted from the scaffold —
    this is a fire-once best-effort path; retries can be folded in later if
    the backend→harness transport gap (deferred follow-up to #633) shows
    they're needed in production.
    """
    url = _substitute_url(sub.url_template, context)
    if not url:
        logger.warning(f"Webhook '{sub.name}': URL resolved to empty string — skipping hook.decision delivery.")
        return
    url_error = await _validate_url_async(url)
    if url_error is not None:
        logger.error(f"Webhook '{sub.name}': resolved URL rejected — {url_error}.")
        return

    if sub.body_template:
        body = _substitute(sub.body_template, context)
    else:
        body = _render_hook_decision_envelope(context)

    body_bytes = body.encode("utf-8")
    if len(body_bytes) > 256 * 1024:
        body_bytes = body_bytes[: 256 * 1024].decode("utf-8", errors="ignore").encode("utf-8")

    headers = {"Content-Type": sub.content_type}
    for k, v in sub.headers.items():
        headers[k] = _substitute(v, context)
    if sub.signing_secret:
        headers["X-Hub-Signature-256"] = _sign_body(body_bytes, sub.signing_secret)
    # Forward the trace-context header that arrived with the event so the
    # webhook receiver stays correlated with the trace that produced the
    # hook decision. Fall back to whatever the OTel layer injects when no
    # explicit traceparent was provided.
    tp = context.get("traceparent")
    if tp:
        headers["traceparent"] = tp

    result = "unknown"
    with start_span(
        "webhook.hook_decision",
        kind="client",
        attributes={
            "nyx.webhook.name": sub.name,
            "nyx.hook.decision": context.get("decision", ""),
            "nyx.hook.tool": context.get("tool", ""),
            "nyx.hook.rule_name": context.get("rule_name", ""),
            "http.url": url,
        },
    ) as _span:
        inject_traceparent(headers)
        try:
            # TOCTOU re-check before POST (#699).
            url_recheck = await _validate_url_async(url)
            if url_recheck is not None:
                logger.error(
                    f"Webhook '{sub.name}': URL failed re-validation before hook.decision POST — {url_recheck}; aborting delivery."
                )
                return
            resp = await client.post(
                url, content=body_bytes, headers=headers, timeout=sub.timeout_seconds,
            )
            if resp.status_code < 400:
                result = "success"
                logger.info(f"Webhook '{sub.name}' hook.decision delivered to {url} — {resp.status_code}")
            else:
                result = f"http_{resp.status_code}"
                logger.warning(f"Webhook '{sub.name}' hook.decision got {resp.status_code}")
        except Exception as exc:
            result = "error"
            set_span_error(_span, exc)
            logger.warning(f"Webhook '{sub.name}' hook.decision delivery failed: {exc!r}")

    if harness_webhooks_delivery_total is not None:
        harness_webhooks_delivery_total.labels(result=result, subscription=sub.name).inc()


class WebhookRunner:
    def __init__(self, backends: dict | None = None, default_backend_id: str | None = None):
        self._items: dict[str, WebhookSubscription] = {}
        self._backends = backends
        self._default_backend_id = default_backend_id
        self._active_deliveries: set[asyncio.Task] = set()
        # Per-subscription in-flight tasks, keyed by subscription name.
        self._deliveries_by_name: dict[str, set[asyncio.Task]] = {}
        # Shared outbound AsyncClient reused across every delivery and retry
        # attempt (#567). Constructed eagerly so fire() has a client to hand
        # to deliver() even if deliveries race the first pass of run(); the
        # client is cheap to allocate, and concurrent calls sharing a single
        # instance is exactly the point (same shape as A2ABackend #398).
        self._client: httpx.AsyncClient = _build_shared_client()

    def _get_client(self) -> httpx.AsyncClient:
        """Return the shared client, re-opening if it was closed (e.g. after
        a prior shutdown path). Mirrors A2ABackend._get_client()."""
        if self._client.is_closed:
            self._client = _build_shared_client()
        return self._client

    def set_backends(self, backends: dict, default_backend_id: str) -> None:
        """Update the backend references (called when backends are reloaded)."""
        self._backends = backends
        self._default_backend_id = default_backend_id

    def items(self) -> list[dict]:
        """Return a serializable snapshot of currently registered webhook subscriptions."""
        result = []
        for sub in self._items.values():
            result.append({
                "name": sub.name,
                "url": sub.url_template,
                "notify_when": sub.notify_when,
                "notify_on_kind": sub.notify_on_kind,
                "notify_on_response": sub.notify_on_response,
                "description": sub.description,
                "enabled": sub.enabled,
                "retries": sub.retries,
                "backend_id": sub.backend_id,
                "model": sub.model,
                "active_deliveries": len(self._deliveries_by_name.get(sub.name, set())),
                "max_concurrent_deliveries": sub.max_concurrent_deliveries,
                "events": list(sub.events),
            })
        return result

    def _register(self, path: str, *, count_reload: bool = False) -> None:
        result = parse_webhook_file(path)
        if result is None:
            return
        sub = result
        self._unregister(path)
        self._items[path] = sub
        # registered-count metric only includes active (enabled) subs —
        # disabled entries are stored for display but aren't delivery
        # targets. Future delivery paths that iterate _items must filter
        # on sub.enabled.
        if harness_webhooks_items_registered is not None:
            harness_webhooks_items_registered.set(
                sum(1 for s in self._items.values() if s.enabled)
            )
        if count_reload and harness_webhooks_reloads_total is not None:
            harness_webhooks_reloads_total.inc()
        if sub.enabled:
            logger.info(f"Webhook subscription '{sub.name}' registered.")
        else:
            logger.info(f"Webhook subscription '{sub.name}' disabled — listed but not delivered.")

    def _unregister(self, path: str, *, count_reload: bool = False) -> None:
        existing = self._items.pop(path, None)
        if existing:
            logger.info(f"Webhook subscription '{existing.name}' unregistered.")
            if harness_webhooks_items_registered is not None:
                harness_webhooks_items_registered.set(len(self._items))
            if count_reload and harness_webhooks_reloads_total is not None:
                harness_webhooks_reloads_total.inc()

    async def _scan(self) -> None:
        if not os.path.isdir(WEBHOOKS_DIR):
            return
        try:
            filenames = os.listdir(WEBHOOKS_DIR)
        except OSError:
            return
        for filename in filenames:
            if filename.endswith(".md"):
                self._register(os.path.join(WEBHOOKS_DIR, filename))

    def fire(
        self,
        source: str,
        kind: str,
        session_id: str,
        success: bool,
        response: str,
        duration_seconds: float,
        error: str | None,
        model: str | None,
        trace_context=None,
    ) -> None:
        """Evaluate all subscriptions and fire matching ones as background tasks."""
        response_preview = response[:2048] if response else ""
        for sub in self._items.values():
            # Disabled subs are listed for dashboard visibility only.
            if not sub.enabled:
                continue
            if _matches_filters(sub, success, kind, response_preview):
                # Per-subscription cap: prevents a single high-volume or slow
                # subscription from consuming all global capacity and starving others.
                sub_deliveries = self._deliveries_by_name.get(sub.name, set())
                if len(sub_deliveries) >= sub.max_concurrent_deliveries:
                    logger.warning(
                        f"Webhook '{sub.name}': per-subscription max concurrent deliveries "
                        f"({sub.max_concurrent_deliveries}) reached — shedding delivery for kind {kind!r}."
                    )
                    if harness_webhooks_delivery_shed_total is not None:
                        harness_webhooks_delivery_shed_total.labels(subscription=sub.name).inc()
                    continue
                # Global safety net: absolute cap across all subscriptions.
                if len(self._active_deliveries) >= WEBHOOK_MAX_CONCURRENT_DELIVERIES:
                    logger.warning(
                        f"Webhook '{sub.name}': global max concurrent deliveries "
                        f"({WEBHOOK_MAX_CONCURRENT_DELIVERIES}) reached — shedding delivery for kind {kind!r}."
                    )
                    if harness_webhooks_delivery_shed_total is not None:
                        harness_webhooks_delivery_shed_total.labels(subscription=sub.name).inc()
                    continue
                def _make_on_retry_task(
                    _sub_name: str = sub.name,
                ) -> "callable":
                    """Return a callback that registers a retry task in the tracking sets."""
                    def _on_retry_task(retry_t: asyncio.Task) -> None:
                        self._active_deliveries.add(retry_t)
                        self._deliveries_by_name.setdefault(_sub_name, set()).add(retry_t)
                        def _retry_cleanup(t: asyncio.Task, _n: str = _sub_name) -> None:
                            self._active_deliveries.discard(t)
                            # Pop-when-empty: keep the retry path consistent
                            # with the primary delivery cleanup (#507).
                            _dels = self._deliveries_by_name.get(_n)
                            if _dels is not None:
                                _dels.discard(t)
                                if not _dels:
                                    self._deliveries_by_name.pop(_n, None)
                        retry_t.add_done_callback(_retry_cleanup)
                    return _on_retry_task

                _t = asyncio.create_task(deliver(
                    sub=sub,
                    source=source,
                    kind=kind,
                    session_id=session_id,
                    success=success,
                    response=response,
                    duration_seconds=duration_seconds,
                    error=error,
                    model=model,
                    client=self._get_client(),
                    backends=self._backends,
                    default_backend_id=self._default_backend_id,
                    on_retry_task=_make_on_retry_task(),
                    trace_context=trace_context,
                ))
                self._active_deliveries.add(_t)
                deliveries = self._deliveries_by_name.setdefault(sub.name, set())
                deliveries.add(_t)
                def _cleanup(t: asyncio.Task, _name: str = sub.name) -> None:
                    self._active_deliveries.discard(t)
                    # Pop-when-empty: drop the per-name set once it's drained
                    # so entries for unregistered/renamed subscriptions don't
                    # linger across hot reloads (#507).
                    _dels = self._deliveries_by_name.get(_name)
                    if _dels is not None:
                        _dels.discard(t)
                        if not _dels:
                            self._deliveries_by_name.pop(_name, None)
                _t.add_done_callback(_cleanup)

    def fire_hook_decision(
        self,
        agent: str,
        session_id: str,
        tool: str,
        decision: str,
        rule_name: str,
        reason: str,
        source: str,
        traceparent: str | None = None,
    ) -> None:
        """Fan a ``hook.decision`` event out to subscribed webhooks (#633).

        Scaffolded path: accepts the structured payload shape documented in
        #633 and dispatches to every subscription whose ``events:`` list
        includes ``hook.decision`` (or the specific ``hook.decision:<decision>``
        qualifier). A follow-up gap will wire the backend→harness transport
        that populates this call site from the backends' hook callbacks;
        until then, harness-side callers (e.g. future harness-owned hooks)
        can invoke it directly, and unit tests can exercise the fan-out.
        """
        qualifier = decision if decision in _HOOK_DECISION_SUB_KINDS else None
        delivery_id = str(uuid.uuid4())
        timestamp = datetime.now(timezone.utc).isoformat()
        context: dict = {
            "agent": agent or AGENT_NAME,
            "session_id": session_id,
            "tool": tool,
            "decision": decision,
            "rule_name": rule_name,
            "reason": reason,
            "source": source,
            "traceparent": traceparent,
            "timestamp": timestamp,
            "delivery_id": delivery_id,
            # ``kind`` is populated for URL-template compatibility with the
            # documented allow-list; we reuse the event name so URL templates
            # referencing {{kind}} render something meaningful.
            "kind": EVENT_KIND_HOOK_DECISION,
            "success": decision != "deny",
            "error": "",
            "duration_seconds": 0.0,
            "response_preview": "",
            "model": "",
        }

        for sub in self._items.values():
            if not sub.enabled:
                continue
            if not _events_match(sub.events, EVENT_KIND_HOOK_DECISION, qualifier):
                continue
            # Per-subscription cap mirrors the completion path so a noisy
            # receiver can't starve the rest.
            sub_deliveries = self._deliveries_by_name.get(sub.name, set())
            if len(sub_deliveries) >= sub.max_concurrent_deliveries:
                logger.warning(
                    f"Webhook '{sub.name}': per-subscription max concurrent deliveries "
                    f"({sub.max_concurrent_deliveries}) reached — shedding hook.decision delivery."
                )
                if harness_webhooks_delivery_shed_total is not None:
                    harness_webhooks_delivery_shed_total.labels(subscription=sub.name).inc()
                continue
            if len(self._active_deliveries) >= WEBHOOK_MAX_CONCURRENT_DELIVERIES:
                logger.warning(
                    f"Webhook '{sub.name}': global max concurrent deliveries "
                    f"({WEBHOOK_MAX_CONCURRENT_DELIVERIES}) reached — shedding hook.decision delivery."
                )
                if harness_webhooks_delivery_shed_total is not None:
                    harness_webhooks_delivery_shed_total.labels(subscription=sub.name).inc()
                continue

            _t = asyncio.create_task(
                _deliver_hook_decision(sub, context, self._get_client())
            )
            self._active_deliveries.add(_t)
            deliveries = self._deliveries_by_name.setdefault(sub.name, set())
            deliveries.add(_t)

            def _cleanup(t: asyncio.Task, _name: str = sub.name) -> None:
                self._active_deliveries.discard(t)
                _dels = self._deliveries_by_name.get(_name)
                if _dels is not None:
                    _dels.discard(t)
                    if not _dels:
                        self._deliveries_by_name.pop(_name, None)

            _t.add_done_callback(_cleanup)

    async def run(self) -> None:
        logger.info(f"Webhook runner watching {WEBHOOKS_DIR}")

        def _on_change(path: str) -> None:
            logger.info(f"Webhook file changed: {path}")
            self._register(path, count_reload=True)

        def _on_delete(path: str) -> None:
            logger.info(f"Webhook file removed: {path}")
            self._unregister(path, count_reload=True)

        def _cleanup() -> None:
            for path in list(self._items.keys()):
                self._unregister(path)

        # The #515 retry-registration wrapper in deliver() is orthogonal to
        # this loop and untouched by the #576 helper extraction.
        await run_awatch_loop(
            directory=WEBHOOKS_DIR,
            watcher_name="webhooks",
            scan=self._scan,
            on_change=_on_change,
            on_delete=_on_delete,
            cleanup=_cleanup,
            logger_=logger,
            not_found_message="Webhooks directory not found — retrying in 10s.",
            watcher_exited_message="Webhooks directory watcher exited — retrying in 10s.",
            watcher_events_metric=harness_watcher_events_total,
            file_watcher_restarts_metric=harness_file_watcher_restarts_total,
        )

    async def close(self) -> None:
        """Shut the runner down: drain in-flight deliveries (including any
        pending retry tasks) and then close the shared AsyncClient (#567).

        The approver explicitly flagged that retry tasks outlive their
        originating delivery task (they live on `_active_deliveries` across
        the backoff sleep), so naively closing the client before draining
        would cancel connections mid-retry. We gather the tracking set with
        `return_exceptions=True` so one failing task cannot block shutdown
        of the rest, then aclose the client. Mirrors A2ABackend.close().
        """
        pending = list(self._active_deliveries)
        if pending:
            await asyncio.gather(*pending, return_exceptions=True)
        if not self._client.is_closed:
            await self._client.aclose()
