"""Backend→harness transport for ``hook.decision`` events (#641, #779).

The harness exposes ``POST /internal/events/hook-decision`` so backends can
forward PreToolUse decisions; webhook subscribers matching the
``hook.decision`` kind then receive the payload. Historically only the
claude backend emitted these events — codex and gemini were documented
as deferred (#779), leaving operators with a partial observability
picture on multi-backend deployments.

This module centralises the transport so any backend can share the same
configuration surface (``HARNESS_EVENTS_URL`` /
``HOOK_EVENTS_AUTH_TOKEN``) and the same behavioural guarantees:

The canonical env var for the bearer token is ``HOOK_EVENTS_AUTH_TOKEN``
(matching ``harness/main.py``'s endpoint). ``HARNESS_EVENTS_AUTH_TOKEN``
is retained as a back-compat alias so previously-deployed agents keep
working during the rename (#859); ``TRIGGERS_AUTH_TOKEN`` remains as a
second fallback for the pre-#700 deployments.

* Fire-and-forget: scheduling a post never stalls tool execution.
* Bounded in-flight: a single cap across all posts on this backend,
  configurable via ``HOOK_POST_MAX_INFLIGHT`` (default 32), prevents
  an unreachable harness from blowing out the httpx connection pool
  or the backend's memory budget.
* Shed counting: when at cap, the caller is told to drop the event
  (the backend's own OTel span event still captures the decision);
  ``shed_counter.inc()`` is invoked if provided.
* One-shot auth warning: if ``HARNESS_EVENTS_URL`` is configured but
  the bearer token is missing, log once at WARNING and then stay
  silent so the misconfig surfaces without flooding logs.

The module keeps its own inflight-set + warning guards so each
consumer (claude, codex, …) doesn't have to track them separately.
Claude's executor predates this module and carries its own
equivalent implementation; call sites are free to migrate over time
— both paths share the same env vars so the operator experience is
identical.
"""

from __future__ import annotations

import asyncio
import logging
import os
import threading
from typing import Any

import httpx

logger = logging.getLogger(__name__)

HARNESS_EVENTS_URL = os.environ.get("HARNESS_EVENTS_URL", "") or ""
# Canonical: HOOK_EVENTS_AUTH_TOKEN (matches the harness endpoint #859).
# Back-compat aliases preserve existing deployments during the rename:
#   HARNESS_EVENTS_AUTH_TOKEN — historical name used by this module
#   TRIGGERS_AUTH_TOKEN       — pre-#700 name
HOOK_EVENTS_AUTH_TOKEN = (
    os.environ.get("HOOK_EVENTS_AUTH_TOKEN")
    or os.environ.get("HARNESS_EVENTS_AUTH_TOKEN")
    or os.environ.get("TRIGGERS_AUTH_TOKEN")
    or ""
)
# Back-compat alias for any external importer that grabs the old name.
HARNESS_EVENTS_AUTH_TOKEN = HOOK_EVENTS_AUTH_TOKEN
HOOK_POST_MAX_INFLIGHT = int(os.environ.get("HOOK_POST_MAX_INFLIGHT", "32"))

# One-shot warning flags — guarded by a threading.Lock so two concurrent
# first-posts don't race the write.
_auth_warn_lock = threading.Lock()
_auth_warned = False
# Counter + re-arm threshold (#936). Re-emit the auth-disabled WARN
# every _AUTH_REARM_EVERY dropped events so a sustained misconfig
# doesn't go silent after the initial log line is shipped.
_auth_dropped_since_warn = 0
_AUTH_REARM_EVERY = int(os.environ.get("HOOK_EVENTS_AUTH_REARM_EVERY", "500"))
_shed_warn_lock = threading.Lock()
# Serialises the cap check-and-add path in schedule_post (#878). Under
# current single-loop asyncio this is redundant, but any future refactor
# that introduces an await between `len(_INFLIGHT)` and `_INFLIGHT.add(t)`
# could let two coroutines both observe len==cap-1 and push to cap+1.
_inflight_lock = threading.Lock()
_shed_warned = False
# One-shot warning flag for non-2xx responses (#881). Distinct from
# _auth_warned (which fires when the token is empty before the POST);
# this one fires when the POST returned a 4xx/5xx, e.g. wrong bearer
# or a misconfigured endpoint.
_status_warn_lock = threading.Lock()
_status_warned = False

# Module-level strong-ref set. ``asyncio.create_task`` only keeps a weak
# reference to the task from the event loop, so without a strong ref the
# task may be garbage-collected before it completes.
_INFLIGHT: set[asyncio.Task] = set()

# Module-level httpx client. Created lazily on first post to avoid
# instantiating one before the backend's event loop is running.
_client: httpx.AsyncClient | None = None
_client_lock = asyncio.Lock()


async def _get_client() -> httpx.AsyncClient:
    global _client
    async with _client_lock:
        if _client is None or _client.is_closed:
            _client = httpx.AsyncClient(timeout=5.0)
    return _client


async def _post_once(event_dict: dict[str, Any]) -> None:
    """Actual POST. Called from ``asyncio.create_task`` in post_event."""
    if not HARNESS_EVENTS_URL:
        return
    if not HOOK_EVENTS_AUTH_TOKEN:
        # Periodic re-WARN (#936). Previously _auth_warned flipped True
        # on first drop and never reset, so a sustained misconfig where
        # the initial WARN was lost to log shipping became completely
        # invisible. Re-arm every N drops so operators see the signal
        # repeatedly while the misconfig persists.
        global _auth_warned, _auth_dropped_since_warn
        with _auth_warn_lock:
            _auth_dropped_since_warn += 1
            # Re-emit every _AUTH_REARM_EVERY drops.
            _should_warn = (
                not _auth_warned
                or _auth_dropped_since_warn >= _AUTH_REARM_EVERY
            )
            if _should_warn:
                _auth_warned = True
                _count = _auth_dropped_since_warn
                _auth_dropped_since_warn = 0
                logger.warning(
                    "hook.decision transport DISABLED: HARNESS_EVENTS_URL is set "
                    "but HOOK_EVENTS_AUTH_TOKEN (and its HARNESS_EVENTS_AUTH_TOKEN/"
                    "TRIGGERS_AUTH_TOKEN aliases) are all empty. Set the token so "
                    "the harness endpoint accepts the POST. %d event(s) dropped "
                    "since the last warning; will re-warn every %d dropped events.",
                    _count, _AUTH_REARM_EVERY,
                )
        return
    url = HARNESS_EVENTS_URL.rstrip("/") + "/internal/events/hook-decision"
    try:
        client = await _get_client()
        resp = await client.post(
            url,
            json=event_dict,
            headers={"Authorization": f"Bearer {HOOK_EVENTS_AUTH_TOKEN}"},
        )
        # Branch on resp.status_code (#881). Previously the response
        # was discarded; a wrong HOOK_EVENTS_AUTH_TOKEN (401/403) or a
        # misconfigured harness endpoint (404/5xx) was silently
        # swallowed — only the empty-token case was surfaced. Log the
        # first non-2xx at WARNING and stay silent afterwards to avoid
        # flooding logs on a sustained misconfig.
        if resp.status_code >= 400:
            global _status_warned
            with _status_warn_lock:
                if not _status_warned:
                    _status_warned = True
                    logger.warning(
                        "hook.decision POST to %s returned HTTP %d (check "
                        "HOOK_EVENTS_AUTH_TOKEN and harness endpoint config); "
                        "further non-2xx responses will be suppressed until "
                        "a 2xx re-arms the warning.",
                        url,
                        resp.status_code,
                    )
        else:
            # Re-arm the warning so a subsequent sustained failure is
            # visible without a process restart.
            with _status_warn_lock:
                _status_warned = False
    except Exception as exc:
        logger.warning("hook.decision POST to %s failed: %r", url, exc)


def schedule_post(event_dict: dict[str, Any], shed_counter: Any = None) -> bool:
    """Schedule a hook.decision POST. Returns True when scheduled, False
    when shed due to the inflight cap.

    ``shed_counter`` may be a Prometheus counter (or any object with an
    ``inc()`` method); it is incremented once per shed event so
    dashboards can alert on sustained harness-unreachability.
    """
    if not HARNESS_EVENTS_URL:
        # Transport disabled by config — treat as a silent no-op. No
        # shed counter bump: this is operator intent, not starvation.
        return False

    # Check-and-add under _inflight_lock (#878). Keeps len(_INFLIGHT) <=
    # HOOK_POST_MAX_INFLIGHT even if a future refactor introduces an await
    # between the check and the set mutation. The Lock is held across
    # create_task so a second coroutine sees the post-add size.
    with _inflight_lock:
        if len(_INFLIGHT) >= HOOK_POST_MAX_INFLIGHT:
            _over_cap = True
        else:
            try:
                t = asyncio.create_task(_post_once(event_dict))
            except RuntimeError:
                # No running loop (e.g. module imported for tests). Silent drop.
                return False
            _INFLIGHT.add(t)
            _over_cap = False

    if _over_cap:
        global _shed_warned
        with _shed_warn_lock:
            if not _shed_warned:
                _shed_warned = True
                logger.warning(
                    "hook.decision POST shed: %d in-flight at cap=%d "
                    "(further shed suppressed until drain)",
                    len(_INFLIGHT),
                    HOOK_POST_MAX_INFLIGHT,
                )
        if shed_counter is not None:
            try:
                shed_counter.inc()
            except Exception:
                pass
        return False

    def _done(tt: asyncio.Task, _inflight: set = _INFLIGHT) -> None:
        _inflight.discard(tt)
        # Re-arm the shed warning once we drop back below half cap.
        # Guard the write with the same _shed_warn_lock used by the
        # set-path above (#882) so concurrent task completions can't
        # both flip the flag after one has already cleared it.
        if len(_inflight) < HOOK_POST_MAX_INFLIGHT // 2:
            global _shed_warned
            with _shed_warn_lock:
                _shed_warned = False

    t.add_done_callback(_done)
    return True


async def close() -> None:
    """Close the module-level client on backend shutdown."""
    global _client
    if _client is not None and not _client.is_closed:
        await _client.aclose()
    _client = None
