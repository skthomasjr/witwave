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
import random
import threading
import time
from typing import Any

import httpx

logger = logging.getLogger(__name__)

HARNESS_EVENTS_URL = os.environ.get("HARNESS_EVENTS_URL", "") or ""
# Backend→harness generic event channel (#1110 phase 3). Defaults to the
# hook-decision base URL (HARNESS_EVENTS_URL) when not set explicitly so
# existing deployments don't need a second env var. The path suffix is
# ``/internal/events/publish``; if the operator set HARNESS_EVENTS_URL to
# a value that already includes the hook-decision suffix we strip it
# before appending /publish (defensive — both paths live on the same
# host:port).
HARNESS_EVENTS_PUBLISH_URL = os.environ.get("HARNESS_EVENTS_PUBLISH_URL", "") or ""
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

# Per-POST timeout (#1045). Default 2.0s — previously 5.0s, which allowed a
# slow-but-alive harness to occupy an inflight slot long enough to saturate
# HOOK_POST_MAX_INFLIGHT within a handful of denied tool calls. Tunable via
# HOOK_POST_TIMEOUT_SECONDS; a single jittered retry still gives the
# harness room on transient latency.
HOOK_POST_TIMEOUT_SECONDS = float(os.environ.get("HOOK_POST_TIMEOUT_SECONDS", "2.0"))
# httpx connection-pool limits — without these, httpx defaults can let a
# stalled harness grow an unbounded pool when requests pile up.
HOOK_POST_MAX_CONNECTIONS = int(os.environ.get("HOOK_POST_MAX_CONNECTIONS", "16"))
HOOK_POST_MAX_KEEPALIVE = int(os.environ.get("HOOK_POST_MAX_KEEPALIVE", "8"))
# Circuit breaker: if a rolling window of recent posts exceeds the failure
# ratio, open for HOOK_POST_CB_COOLDOWN_SECONDS and short-circuit future
# posts so we don't keep a stalled harness occupying inflight slots.
HOOK_POST_CB_WINDOW = int(os.environ.get("HOOK_POST_CB_WINDOW", "20"))
HOOK_POST_CB_FAIL_RATIO = float(os.environ.get("HOOK_POST_CB_FAIL_RATIO", "0.8"))
HOOK_POST_CB_COOLDOWN_SECONDS = float(os.environ.get("HOOK_POST_CB_COOLDOWN_SECONDS", "15.0"))
# Single jittered retry window (#1045). 0 disables.
HOOK_POST_RETRY_MAX_DELAY = float(os.environ.get("HOOK_POST_RETRY_MAX_DELAY_SECONDS", "0.25"))

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
# Counter + re-arm threshold (#1044). Mirrors _AUTH_REARM_EVERY so a
# sustained non-2xx misconfig (e.g. mistyped HARNESS_EVENTS_URL) doesn't
# go silent after the first WARN — re-emit every N dropped non-2xx
# responses. Previously _status_warned flipped once and only reset on a
# 2xx, which never arrived on a wrong-path deployment.
_status_dropped_since_warn = 0
_STATUS_REARM_EVERY = int(os.environ.get("HOOK_EVENTS_STATUS_REARM_EVERY", "500"))

# Module-level strong-ref set. ``asyncio.create_task`` only keeps a weak
# reference to the task from the event loop, so without a strong ref the
# task may be garbage-collected before it completes.
_INFLIGHT: set[asyncio.Task] = set()
# #1233: cross-thread (OTel worker thread) dispatches go through
# run_coroutine_threadsafe; track those Futures here so the inflight
# cap governs them too.
import concurrent.futures as _cf_mod

_INFLIGHT_CF: set[_cf_mod.Future[Any]] = set()

# One-shot INFO flag for the URL-unset path in ``schedule_event_post``
# (#1143). Logged at INFO (not WARN) because an empty URL is a
# legitimate deployment mode — no harness event channel wired up — and
# must not be confused with the auth-token-missing WARNING that fires
# when the URL *is* set but the bearer is empty.
_url_unset_info_lock = threading.Lock()
_warned_url_unset: bool = False

# Event loop reference for cross-thread scheduling (#1144). The OTel
# SpanProcessor runs ``_emit_trace_span_event`` on a worker thread
# (BatchSpanProcessor's exporter thread or the in-memory on_end
# callback) which has no running asyncio loop, so a plain
# ``asyncio.create_task`` raises ``RuntimeError: no running event
# loop`` and silently drops the event.  Backends call
# :func:`bind_event_loop` at startup; when the scheduler detects a
# worker-thread caller (``asyncio.get_running_loop()`` raises) it
# falls back to :func:`asyncio.run_coroutine_threadsafe` against this
# reference so the coroutine still lands on the backend's main loop.
_bound_loop: asyncio.AbstractEventLoop | None = None
# #1362: one-shot WARN when threadsafe dispatch path is reached but
# bind_event_loop was never called — otherwise trace.span silently drops.
_NO_LOOP_WARNED: bool = False


def _build_iso_ts() -> str:
    """RFC3339 ts with millisecond precision from a single clock sample (#1232)."""
    _s = time.time()
    return time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(_s)) + f".{int((_s % 1) * 1000):03d}Z"


def bind_event_loop(loop: asyncio.AbstractEventLoop) -> None:
    """Remember *loop* as the target for cross-thread schedule calls (#1144).

    Idempotent; last caller wins. Called once during each backend's
    ``main()`` after the event loop is running so worker-thread
    publishers (notably the OTel span processor) can still reach the
    transport.
    """
    global _bound_loop
    _bound_loop = loop


# Module-level httpx client. Created lazily on first post to avoid
# instantiating one before the backend's event loop is running.
_client: httpx.AsyncClient | None = None
# #1581: lazy-init the lock under the running loop. Instantiating
# ``asyncio.Lock()`` at import bound it to whichever loop existed (or
# none), so tests with fresh loops and hot-restart paths hit
# "different loop" errors the first time they awaited _get_client.
_client_lock: asyncio.Lock | None = None


async def _get_client() -> httpx.AsyncClient:
    global _client, _client_lock
    if _client_lock is None:
        _client_lock = asyncio.Lock()
    async with _client_lock:
        if _client is None or _client.is_closed:
            _client = httpx.AsyncClient(
                timeout=HOOK_POST_TIMEOUT_SECONDS,
                limits=httpx.Limits(
                    max_connections=HOOK_POST_MAX_CONNECTIONS,
                    max_keepalive_connections=HOOK_POST_MAX_KEEPALIVE,
                ),
            )
    return _client


# Circuit-breaker state (#1045). ``_cb_recent`` tracks the last
# HOOK_POST_CB_WINDOW outcomes as booleans (True = failed). ``_cb_open_until``
# is a monotonic deadline; while non-zero and in the future, _post_once
# short-circuits so a stalled harness can't keep occupying inflight slots.
_cb_lock = threading.Lock()
_cb_recent: list[bool] = []
_cb_open_until: float = 0.0


def _cb_is_open() -> bool:
    with _cb_lock:
        return _cb_open_until > time.monotonic()


def _cb_record(failed: bool) -> None:
    global _cb_open_until
    with _cb_lock:
        _cb_recent.append(failed)
        if len(_cb_recent) > HOOK_POST_CB_WINDOW:
            del _cb_recent[: len(_cb_recent) - HOOK_POST_CB_WINDOW]
        if len(_cb_recent) >= HOOK_POST_CB_WINDOW:
            fails = sum(1 for x in _cb_recent if x)
            ratio = fails / len(_cb_recent)
            if ratio >= HOOK_POST_CB_FAIL_RATIO:
                _cb_open_until = time.monotonic() + HOOK_POST_CB_COOLDOWN_SECONDS
                _cb_recent.clear()
                logger.warning(
                    "hook.decision circuit breaker OPEN: %.2f failure ratio " "over %d posts; shedding for %.1fs",
                    ratio,
                    HOOK_POST_CB_WINDOW,
                    HOOK_POST_CB_COOLDOWN_SECONDS,
                )


def _resolve_publish_url() -> str:
    """Return the absolute URL for POST /internal/events/publish, or ''.

    Resolution order:
    * HARNESS_EVENTS_PUBLISH_URL if explicitly set — treated as absolute.
    * HARNESS_EVENTS_URL with ``/internal/events/publish`` appended. If
      the operator set HARNESS_EVENTS_URL to the historical full
      hook-decision endpoint, strip the ``/internal/events/hook-decision``
      suffix before appending.
    * Empty string when neither is configured — transport disabled.
    """
    if HARNESS_EVENTS_PUBLISH_URL:
        return HARNESS_EVENTS_PUBLISH_URL
    if not HARNESS_EVENTS_URL:
        return ""
    base = HARNESS_EVENTS_URL.rstrip("/")
    # Defensive: strip /internal/events/hook-decision if it's already on the
    # base URL (some older deployments embed it).
    if base.endswith("/internal/events/hook-decision"):
        base = base[: -len("/internal/events/hook-decision")]
    return base + "/internal/events/publish"


async def _post_once_to(url: str, body: dict[str, Any]) -> None:
    """Shared POST path for all backend→harness events.

    Bearer, circuit breaker, retry, and status-warn state are shared
    across endpoints — all traffic targets the same harness process, so
    one-shot warnings should not re-fire just because the caller is now
    emitting to /internal/events/publish instead of /internal/events/
    hook-decision.
    """
    if not HOOK_EVENTS_AUTH_TOKEN:
        global _auth_warned, _auth_dropped_since_warn
        with _auth_warn_lock:
            _auth_dropped_since_warn += 1
            _should_warn = not _auth_warned or _auth_dropped_since_warn >= _AUTH_REARM_EVERY
            if _should_warn:
                _auth_warned = True
                _count = _auth_dropped_since_warn
                _auth_dropped_since_warn = 0
                logger.warning(
                    "harness-events transport DISABLED: HARNESS_EVENTS_URL is set "
                    "but HOOK_EVENTS_AUTH_TOKEN (and its HARNESS_EVENTS_AUTH_TOKEN/"
                    "TRIGGERS_AUTH_TOKEN aliases) are all empty. %d event(s) dropped "
                    "since the last warning; will re-warn every %d dropped events.",
                    _count,
                    _AUTH_REARM_EVERY,
                )
        return
    if _cb_is_open():
        return
    try:
        client = await _get_client()
        resp = None
        for attempt in range(2):
            try:
                resp = await client.post(
                    url,
                    json=body,
                    headers={"Authorization": f"Bearer {HOOK_EVENTS_AUTH_TOKEN}"},
                )
                break
            except (httpx.TimeoutException, httpx.ConnectError, httpx.ReadError):
                if attempt == 0 and HOOK_POST_RETRY_MAX_DELAY > 0:
                    await asyncio.sleep(random.uniform(0, HOOK_POST_RETRY_MAX_DELAY))
                    continue
                raise
        assert resp is not None
        _cb_record(failed=(resp.status_code >= 500))
        if resp.status_code >= 400:
            global _status_warned, _status_dropped_since_warn
            with _status_warn_lock:
                _status_dropped_since_warn += 1
                _should_warn = not _status_warned or _status_dropped_since_warn >= _STATUS_REARM_EVERY
                if _should_warn:
                    _status_warned = True
                    _count = _status_dropped_since_warn
                    _status_dropped_since_warn = 0
                    logger.warning(
                        "harness-events POST to %s returned HTTP %d (check "
                        "HOOK_EVENTS_AUTH_TOKEN and harness endpoint config); "
                        "%d non-2xx response(s) since last warning; will "
                        "re-warn every %d dropped events.",
                        url,
                        resp.status_code,
                        _count,
                        _STATUS_REARM_EVERY,
                    )
        else:
            with _status_warn_lock:
                _status_warned = False
                _status_dropped_since_warn = 0
    except Exception as exc:
        _cb_record(failed=True)
        logger.warning("harness-events POST to %s failed: %r", url, exc)


async def _post_once(event_dict: dict[str, Any]) -> None:
    """Actual POST. Called from ``asyncio.create_task`` in post_event.

    The URL-unset check that used to live here was redundant (#1142):
    :func:`schedule_post` already returns False when
    ``HARNESS_EVENTS_URL`` is empty, so this coroutine is only scheduled
    when the transport is configured.  Auth-warn plumbing is delegated
    to :func:`_post_once_to` so the two helpers don't drift.
    """
    url = HARNESS_EVENTS_URL.rstrip("/") + "/internal/events/hook-decision"
    # Delegate to _post_once_to (#1142) so the auth-warn / circuit
    # breaker / retry / status-warn logic stays in exactly one place
    # and the two entry points can no longer drift.
    await _post_once_to(url, event_dict)


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
                    "hook.decision POST shed: %d in-flight at cap=%d " "(further shed suppressed until drain)",
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
        # _INFLIGHT mutations must be serialised against schedule_post's
        # check-and-add under _inflight_lock (#1037). Previously this
        # callback used a bare ``discard`` so a concurrent check-and-add
        # on a worker thread could read a stale length mid-mutation,
        # either over-admitting (len observed < cap before discard
        # completes) or prematurely shedding (len observed >= cap while
        # this task was already logically gone). Holding the lock for
        # the discard itself and the ``len()`` check below restores the
        # intended atomicity even if a future refactor moves any of
        # this off the main event loop.
        with _inflight_lock:
            _inflight.discard(tt)
            below_half = len(_inflight) < HOOK_POST_MAX_INFLIGHT // 2
        if below_half:
            global _shed_warned
            with _shed_warn_lock:
                _shed_warned = False

    t.add_done_callback(_done)
    return True


def schedule_event_post(
    event_type: str,
    payload: dict[str, Any],
    *,
    agent_id: str | None = None,
    version: int = 1,
    shed_counter: Any = None,
) -> bool:
    """Schedule a generic event POST to the harness event channel (#1110 phase 3).

    Builds the ``{type, version, ts, agent_id, payload}`` body the
    harness ``POST /internal/events/publish`` endpoint accepts, then
    fans out through the same inflight cap, circuit breaker, retry
    and shed plumbing as ``schedule_post``.

    Schema validation is *best-effort*: we try to import the shared
    validator and validate before scheduling — if validation raises,
    or the validator isn't importable, we log at WARN and drop the
    event rather than letting the exception propagate into the
    caller's hot path. Callers should wrap in their own try/except as
    defense-in-depth.

    Returns ``True`` when scheduled, ``False`` on any drop
    (transport disabled, validation failure, inflight cap, etc.).
    """
    url = _resolve_publish_url()
    if not url:
        # Transport disabled by operator intent — but log once at INFO
        # (#1143) so operators grepping for why events never reached
        # the harness see an explicit signal.  Gated by a module-level
        # flag so a steady stream of drops doesn't flood logs.
        global _warned_url_unset
        with _url_unset_info_lock:
            if not _warned_url_unset:
                _warned_url_unset = True
                logger.info(
                    "schedule_event_post: HARNESS_EVENTS_PUBLISH_URL and "
                    "HARNESS_EVENTS_URL are both unset — backend-originated "
                    "events (%r and peers) will not reach the harness event "
                    "channel. This is the default single-process mode; set "
                    "HARNESS_EVENTS_URL to opt in.",
                    event_type,
                )
        return False

    # Assemble the full envelope the harness expects.
    envelope = {
        "type": event_type,
        "version": version,
        # The harness assigns a monotonic `id` on receive; our stub
        # of "0" is fine for schema-validation purposes because the
        # harness rewrites this field before publishing.
        "id": "0",
        # #1232: single time.time() sample — avoids seconds and ms
        # fragments sampled across a sub-ms clock rollover.
        "ts": _build_iso_ts(),
        "agent_id": agent_id,
        "payload": dict(payload),
    }

    # Best-effort schema validation. Never raises into the caller.
    try:
        from event_schema import validate_envelope as _validate  # type: ignore
    except Exception:
        try:
            from shared.event_schema import validate_envelope as _validate  # type: ignore
        except Exception:
            _validate = None  # type: ignore
    if _validate is not None:
        try:
            _err_msg = _validate(envelope)
            if _err_msg is not None:
                logger.warning(
                    "schedule_event_post: dropping invalid %r envelope: %s",
                    event_type,
                    _err_msg,
                )
                return False
        except Exception as exc:  # validator itself blew up — best-effort
            logger.warning(
                "schedule_event_post: validator raised on %r: %r — dropping",
                event_type,
                exc,
            )
            return False

    # Check-and-add under _inflight_lock (mirrors schedule_post).
    # #1233: count cross-thread CF dispatches against the cap too.
    with _inflight_lock:
        if len(_INFLIGHT) + len(_INFLIGHT_CF) >= HOOK_POST_MAX_INFLIGHT:
            _over_cap = True
        else:
            try:
                t = asyncio.create_task(_post_once_to(url, envelope))
            except RuntimeError:
                # No running loop on the caller's thread (#1144). The
                # OTel span processor runs its on_end callback on a
                # worker thread, so ``create_task`` raises here. Fall
                # back to ``run_coroutine_threadsafe`` against the
                # loop bound at backend startup so the POST still
                # lands on the main asyncio loop.
                if _bound_loop is not None and not _bound_loop.is_closed():
                    # #1233: threadsafe dispatch must participate in the
                    # inflight cap so OTel-driven events can't pile up
                    # unbounded on the main loop. Track the
                    # concurrent.futures.Future via a thin wrapper that
                    # mimics a done-callback into _INFLIGHT.
                    try:
                        cf = asyncio.run_coroutine_threadsafe(_post_once_to(url, envelope), _bound_loop)
                    except Exception:  # pragma: no cover
                        return False
                    # Use a module-level set of in-flight CF futures
                    # keyed by id; cleaned up via add_done_callback.
                    _INFLIGHT_CF.add(cf)

                    def _done_cb(_fut: concurrent.futures.Future[Any]) -> None:
                        # #1580: mutate _INFLIGHT_CF under _inflight_lock so
                        # the cap check (which reads len() under the same
                        # lock) can't race the discard and over-admit or
                        # prematurely shed cross-thread dispatches.
                        with _inflight_lock:
                            _INFLIGHT_CF.discard(_fut)

                    try:
                        cf.add_done_callback(_done_cb)
                    except Exception:  # pragma: no cover
                        with _inflight_lock:
                            _INFLIGHT_CF.discard(cf)
                    return True
                # #1362: one-shot WARN so operators see the gap. A
                # new backend that forgets to call bind_event_loop()
                # otherwise silently drops trace.span events forever.
                global _NO_LOOP_WARNED
                if not _NO_LOOP_WARNED:
                    _NO_LOOP_WARNED = True
                    logger.warning(
                        "shared/hook_events: schedule_event_post called "
                        "from worker thread but bind_event_loop() was "
                        "never called — trace.span events will be "
                        "dropped silently until wired. Fix: call "
                        "bind_event_loop(asyncio.get_running_loop()) "
                        "during backend startup."
                    )
                return False
            _INFLIGHT.add(t)
            _over_cap = False

    if _over_cap:
        global _shed_warned
        with _shed_warn_lock:
            if not _shed_warned:
                _shed_warned = True
                logger.warning(
                    "harness-events POST shed: %d in-flight at cap=%d " "(further shed suppressed until drain)",
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
        with _inflight_lock:
            _inflight.discard(tt)
            below_half = len(_inflight) < HOOK_POST_MAX_INFLIGHT // 2
        if below_half:
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
