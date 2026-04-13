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
import json
import logging
import os
import re
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from fnmatch import fnmatch
from pathlib import Path

import httpx
import yaml

from metrics import (
    agent_file_watcher_restarts_total,
    agent_webhooks_delivery_shed_total,
    agent_webhooks_delivery_total,
    agent_webhooks_items_registered,
    agent_webhooks_parse_errors_total,
    agent_webhooks_reloads_total,
    agent_watcher_events_total,
)
from utils import parse_duration, parse_frontmatter
from watchfiles import awatch

logger = logging.getLogger(__name__)

WEBHOOKS_DIR = os.environ.get("WEBHOOKS_DIR", "/home/agent/.nyx/webhooks")
AGENT_NAME = os.environ.get("AGENT_NAME", "nyx-agent")

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

_VALID_NOTIFY_WHEN = ("always", "on_success", "on_error")

_DISABLED = object()

_ENV_VAR_RE = re.compile(r"\{\{env\.(\w+)\}\}")
_VAR_RE = re.compile(r"\{\{(\w+)\}\}")


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


def _resolve_env_vars(text: str) -> str:
    """Replace {{env.VAR}} references with their environment variable values."""
    def _sub(m: re.Match) -> str:
        return os.environ.get(m.group(1), "")
    return _ENV_VAR_RE.sub(_sub, text)


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
            logger.info(f"Webhook file {path}: disabled, skipping.")
            return _DISABLED

        # Resolve URL — supports {{env.VAR}} interpolation
        url_template = fields.get("url") or ""
        if not url_template:
            env_var = fields.get("url-env-var") or None
            if env_var:
                url_template = os.environ.get(env_var) or ""
        if not url_template:
            logger.warning(f"Webhook file {path}: no resolvable URL — skipping.")
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
        )

    except Exception as e:
        if agent_webhooks_parse_errors_total is not None:
            agent_webhooks_parse_errors_total.inc()
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
    """Return True if all three filters pass for this subscription."""
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
) -> None:
    """Continue delivery for a subscription after the first attempt failed.

    Called as a new independent background task so the original delivery task
    can exit immediately, releasing its concurrency slot before the backoff
    sleep.  This prevents sleeping retries from holding capacity and starving
    new deliveries during a downstream outage (#362).
    """
    result = "unknown"
    while attempt < max_attempts:
        backoff = 2 ** (attempt - 1)  # 1s, 2s, 4s, ...
        await asyncio.sleep(backoff)
        attempt += 1
        try:
            async with httpx.AsyncClient(timeout=sub.timeout_seconds, follow_redirects=False) as client:
                resp = await client.post(url, content=body_bytes, headers=headers)
            if resp.status_code < 400:
                result = "success"
                logger.info(f"Webhook '{sub.name}' delivered to {url} — {resp.status_code} (attempt {attempt})")
                break
            else:
                result = f"http_{resp.status_code}"
                logger.warning(f"Webhook '{sub.name}' attempt {attempt} got {resp.status_code}")
        except Exception as exc:
            result = "error"
            logger.warning(f"Webhook '{sub.name}' attempt {attempt} failed: {exc!r}")

    if agent_webhooks_delivery_total is not None:
        agent_webhooks_delivery_total.labels(result=result, subscription=sub.name).inc()


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
    backends: dict | None = None,
    default_backend_id: str | None = None,
    on_retry_task=None,
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

    # Run LLM extractions if defined and backends are available
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

    # Resolve URL ({{env.VAR}} and context variables)
    url = _substitute(sub.url_template, context)
    if not url:
        logger.warning(f"Webhook '{sub.name}': URL resolved to empty string — skipping delivery.")
        return

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

    # First delivery attempt — make one HTTP POST without holding a retry slot.
    # If it fails and retries are configured, schedule the next attempt as a
    # new background task after the backoff delay so the current task exits
    # immediately and releases its concurrency slot.  This prevents sleeping
    # retries from exhausting the per-subscription and global caps and starving
    # new deliveries during a downstream outage (#362).
    result = "unknown"
    try:
        async with httpx.AsyncClient(timeout=sub.timeout_seconds, follow_redirects=False) as client:
            resp = await client.post(url, content=body_bytes, headers=headers)
        if resp.status_code < 400:
            result = "success"
            logger.info(f"Webhook '{sub.name}' delivered to {url} — {resp.status_code}")
        else:
            result = f"http_{resp.status_code}"
            logger.warning(f"Webhook '{sub.name}' attempt 1 got {resp.status_code}")
    except Exception as exc:
        result = "error"
        logger.warning(f"Webhook '{sub.name}' attempt 1 failed: {exc!r}")

    if result != "success" and sub.retries > 0:
        # Schedule retry as a new independent task so this task can exit now,
        # freeing its slot before the backoff delay begins.
        _retry_task = asyncio.ensure_future(
            _retry_deliver(
                sub=sub,
                url=url,
                body_bytes=body_bytes,
                headers=headers,
                attempt=1,
                max_attempts=1 + sub.retries,
            )
        )
        # Notify the caller (e.g. WebhookRunner.fire) so it can register the
        # retry task in its concurrency-tracking structures (#376).
        if on_retry_task is not None:
            on_retry_task(_retry_task)
        # Do not count this as a terminal result — the retry task will record it.
        return

    if agent_webhooks_delivery_total is not None:
        agent_webhooks_delivery_total.labels(result=result, subscription=sub.name).inc()


class WebhookRunner:
    def __init__(self, backends: dict | None = None, default_backend_id: str | None = None):
        self._items: dict[str, WebhookSubscription] = {}
        self._backends = backends
        self._default_backend_id = default_backend_id
        self._active_deliveries: set[asyncio.Task] = set()
        # Per-subscription in-flight tasks, keyed by subscription name.
        self._deliveries_by_name: dict[str, set[asyncio.Task]] = {}

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
            })
        return result

    def _register(self, path: str, *, count_reload: bool = False) -> None:
        result = parse_webhook_file(path)
        if result is _DISABLED:
            self._unregister(path, count_reload=count_reload)
            return
        if result is None:
            return
        sub = result
        self._unregister(path)
        self._items[path] = sub
        if agent_webhooks_items_registered is not None:
            agent_webhooks_items_registered.set(len(self._items))
        if count_reload and agent_webhooks_reloads_total is not None:
            agent_webhooks_reloads_total.inc()
        logger.info(f"Webhook subscription '{sub.name}' registered.")

    def _unregister(self, path: str, *, count_reload: bool = False) -> None:
        existing = self._items.pop(path, None)
        if existing:
            logger.info(f"Webhook subscription '{existing.name}' unregistered.")
            if agent_webhooks_items_registered is not None:
                agent_webhooks_items_registered.set(len(self._items))
            if count_reload and agent_webhooks_reloads_total is not None:
                agent_webhooks_reloads_total.inc()

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
    ) -> None:
        """Evaluate all subscriptions and fire matching ones as background tasks."""
        response_preview = response[:2048] if response else ""
        for sub in self._items.values():
            if _matches_filters(sub, success, kind, response_preview):
                # Per-subscription cap: prevents a single high-volume or slow
                # subscription from consuming all global capacity and starving others.
                sub_deliveries = self._deliveries_by_name.get(sub.name, set())
                if len(sub_deliveries) >= sub.max_concurrent_deliveries:
                    logger.warning(
                        f"Webhook '{sub.name}': per-subscription max concurrent deliveries "
                        f"({sub.max_concurrent_deliveries}) reached — shedding delivery for kind {kind!r}."
                    )
                    if agent_webhooks_delivery_shed_total is not None:
                        agent_webhooks_delivery_shed_total.labels(subscription=sub.name).inc()
                    continue
                # Global safety net: absolute cap across all subscriptions.
                if len(self._active_deliveries) >= WEBHOOK_MAX_CONCURRENT_DELIVERIES:
                    logger.warning(
                        f"Webhook '{sub.name}': global max concurrent deliveries "
                        f"({WEBHOOK_MAX_CONCURRENT_DELIVERIES}) reached — shedding delivery for kind {kind!r}."
                    )
                    if agent_webhooks_delivery_shed_total is not None:
                        agent_webhooks_delivery_shed_total.labels(subscription=sub.name).inc()
                    continue
                def _make_on_retry_task(
                    _sub_name: str = sub.name,
                ) -> "callable":
                    """Return a callback that registers a retry task in the tracking sets."""
                    def _on_retry_task(retry_t: asyncio.Task) -> None:
                        self._active_deliveries.add(retry_t)
                        self._deliveries_by_name.setdefault(_sub_name, set()).add(retry_t)
                        retry_t.add_done_callback(
                            lambda t, _n=_sub_name: (
                                self._active_deliveries.discard(t),
                                self._deliveries_by_name.get(_n, set()).discard(t),
                            )
                        )
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
                    backends=self._backends,
                    default_backend_id=self._default_backend_id,
                    on_retry_task=_make_on_retry_task(),
                ))
                self._active_deliveries.add(_t)
                deliveries = self._deliveries_by_name.setdefault(sub.name, set())
                deliveries.add(_t)
                def _cleanup(t: asyncio.Task, _name: str = sub.name) -> None:
                    self._active_deliveries.discard(t)
                    self._deliveries_by_name.get(_name, set()).discard(t)
                _t.add_done_callback(_cleanup)

    async def run(self) -> None:
        logger.info(f"Webhook runner watching {WEBHOOKS_DIR}")

        while True:
            if not os.path.isdir(WEBHOOKS_DIR):
                logger.info("Webhooks directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue

            _scan_task = asyncio.ensure_future(self._scan())

            def _scan_done(t: asyncio.Task) -> None:
                if not t.cancelled() and t.exception() is not None:
                    logger.error("Webhook runner _scan crashed: %r", t.exception())

            _scan_task.add_done_callback(_scan_done)
            async for changes in awatch(WEBHOOKS_DIR):
                if agent_watcher_events_total is not None:
                    agent_watcher_events_total.labels(watcher="webhooks").inc()
                for _, path in changes:
                    if not path.endswith(".md"):
                        continue
                    if os.path.exists(path):
                        logger.info(f"Webhook file changed: {path}")
                        self._register(path, count_reload=True)
                    else:
                        logger.info(f"Webhook file removed: {path}")
                        self._unregister(path, count_reload=True)

            logger.warning("Webhooks directory watcher exited — retrying in 10s.")
            if agent_file_watcher_restarts_total is not None:
                agent_file_watcher_restarts_total.labels(watcher="webhooks").inc()
            for path in list(self._items.keys()):
                self._unregister(path)
            await asyncio.sleep(10)
