"""Outbound webhook delivery for completion-conditioned notifications.

Each .md file under WEBHOOKS_DIR defines one subscription. The markdown body
is an optional {{variable}} template for the POST payload. If omitted, a
default JSON envelope is sent.

Filters (all must pass — AND logic):
  notify-when:      always | on_success (default) | on_error
  notify-on-kind:   fnmatch glob list matched against prompt kind string
  notify-on-response: fnmatch glob list matched against response_preview

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

from metrics import (
    agent_file_watcher_restarts_total,
    agent_webhooks_delivery_total,
    agent_webhooks_items_registered,
    agent_webhooks_parse_errors_total,
    agent_webhooks_reloads_total,
    agent_watcher_events_total,
)
from utils import parse_frontmatter
from watchfiles import awatch

logger = logging.getLogger(__name__)

WEBHOOKS_DIR = os.environ.get("WEBHOOKS_DIR", "/home/agent/.nyx/webhooks")
AGENT_NAME = os.environ.get("AGENT_NAME", "nyx-agent")

_VALID_NOTIFY_WHEN = ("always", "on_success", "on_error")

_DISABLED = object()


@dataclass
class WebhookSubscription:
    path: str
    name: str
    url: str
    enabled: bool = True
    signing_secret: str | None = None
    notify_when: str = "on_success"
    notify_on_kind: list[str] = field(default_factory=list)
    notify_on_response: list[str] = field(default_factory=list)
    content_type: str = "application/json"
    description: str | None = None
    body_template: str | None = None


def parse_webhook_file(path: str) -> "WebhookSubscription | object | None":
    """Parse a webhook subscription file. Returns:
    - WebhookSubscription on success
    - _DISABLED sentinel when enabled: false
    - None on parse error
    """
    try:
        with open(path) as f:
            raw = f.read()

        fields, body = parse_frontmatter(raw)

        enabled = True
        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() not in ("false", "")
        if not enabled:
            logger.info(f"Webhook file {path}: disabled, skipping.")
            return _DISABLED

        # Resolve URL from 'url' or 'url-env-var'
        url = fields.get("url") or None
        if not url:
            env_var = fields.get("url-env-var") or None
            if env_var:
                url = os.environ.get(env_var) or None
        if not url:
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

        # notify-on-kind: parse from YAML list or comma-separated string
        raw_kind = fields.get("notify-on-kind") or ""
        notify_on_kind = _parse_list_field(raw_kind)

        # notify-on-response: same
        raw_resp = fields.get("notify-on-response") or ""
        notify_on_response = _parse_list_field(raw_resp)

        content_type = fields.get("content-type") or "application/json"

        filename = Path(path).stem
        name = fields.get("name") or filename
        description = fields.get("description") or None
        body_template = body if body else None

        return WebhookSubscription(
            path=path,
            name=name,
            url=url,
            enabled=enabled,
            signing_secret=signing_secret,
            notify_when=notify_when,
            notify_on_kind=notify_on_kind,
            notify_on_response=notify_on_response,
            content_type=content_type,
            description=description,
            body_template=body_template,
        )

    except Exception as e:
        if agent_webhooks_parse_errors_total is not None:
            agent_webhooks_parse_errors_total.inc()
        logger.error(f"Webhook file {path}: failed to parse — {e}, skipping.")
        return None


def _parse_list_field(value: str) -> list[str]:
    """Parse a frontmatter field that may be a YAML list or comma-separated string."""
    if not value or value.strip() == "[]":
        return []
    # parse_frontmatter returns all values as strings; a YAML list becomes "['a', 'b']"
    # Try to eval as a Python list literal first, then fall back to comma split
    stripped = value.strip()
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
    # notify-when filter
    if sub.notify_when == "on_success" and not success:
        return False
    if sub.notify_when == "on_error" and success:
        return False

    # notify-on-kind filter (empty = match all)
    if sub.notify_on_kind:
        if not any(fnmatch(kind, pattern) for pattern in sub.notify_on_kind):
            return False

    # notify-on-response filter (empty = match all)
    if sub.notify_on_response:
        if not any(fnmatch(response_preview, pattern) for pattern in sub.notify_on_response):
            return False

    return True


def _render_body(sub: WebhookSubscription, context: dict) -> str:
    """Render the body template or build the default JSON envelope."""
    if sub.body_template:
        def _replacer(m: re.Match) -> str:
            key = m.group(1).strip()
            return str(context.get(key, ""))
        return re.sub(r"\{\{(\w+)\}\}", _replacer, sub.body_template)

    # Default envelope
    envelope = {
        "event": "agent.prompt.completed",
        "agent": context.get("agent", AGENT_NAME),
        "timestamp": context.get("timestamp", ""),
        "delivery_id": context.get("delivery_id", ""),
        "payload": {
            "kind": context.get("kind", ""),
            "session_id": context.get("session_id", ""),
            "success": context.get("success", False),
            "error": context.get("error"),
            "duration_seconds": context.get("duration_seconds", 0.0),
            "response_preview": context.get("response_preview", ""),
            "model": context.get("model"),
        },
    }
    return json.dumps(envelope)


def _sign_body(body: str, secret: str) -> str:
    """Compute X-Hub-Signature-256 over raw UTF-8 body bytes."""
    mac = hmac.new(secret.encode(), body.encode("utf-8"), hashlib.sha256)
    return f"sha256={mac.hexdigest()}"


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
) -> None:
    """Deliver one webhook POST. Called as a fire-and-forget background task."""
    delivery_id = str(uuid.uuid4())
    timestamp = datetime.now(timezone.utc).isoformat()
    response_preview = response[:2048] if response else ""

    context = {
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

    body = _render_body(sub, context)

    # Cap at 256 KiB
    body_bytes = body.encode("utf-8")
    if len(body_bytes) > 256 * 1024:
        body_bytes = body_bytes[: 256 * 1024]

    headers = {"Content-Type": sub.content_type}
    if sub.signing_secret:
        headers["X-Hub-Signature-256"] = _sign_body(body_bytes.decode("utf-8", errors="replace"), sub.signing_secret)

    result = "unknown"
    try:
        async with httpx.AsyncClient(timeout=10.0, follow_redirects=False) as client:
            resp = await client.post(sub.url, content=body_bytes, headers=headers)
        result = "success" if resp.status_code < 400 else f"http_{resp.status_code}"
        logger.info(f"Webhook '{sub.name}' delivered to {sub.url} — {resp.status_code}")
    except Exception as exc:
        result = "error"
        logger.warning(f"Webhook '{sub.name}' delivery failed: {exc!r}")
    finally:
        if agent_webhooks_delivery_total is not None:
            agent_webhooks_delivery_total.labels(result=result, subscription=sub.name).inc()


class WebhookRunner:
    def __init__(self):
        self._items: dict[str, WebhookSubscription] = {}

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
                asyncio.create_task(deliver(
                    sub=sub,
                    source=source,
                    kind=kind,
                    session_id=session_id,
                    success=success,
                    response=response,
                    duration_seconds=duration_seconds,
                    error=error,
                    model=model,
                ))

    async def run(self) -> None:
        logger.info(f"Webhook runner watching {WEBHOOKS_DIR}")

        while True:
            if not os.path.isdir(WEBHOOKS_DIR):
                logger.info("Webhooks directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue

            asyncio.ensure_future(self._scan())
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
