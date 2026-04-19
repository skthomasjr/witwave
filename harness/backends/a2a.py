"""A2A backend — forwards requests to a remote A2A agent over HTTP/JSON-RPC."""

from __future__ import annotations

import asyncio
import json
import logging
import os
import random
import time
import uuid

import httpx

from backends.config import BackendConfig
from metrics import (
    harness_a2a_backend_circuit_state,
    harness_a2a_backend_circuit_transitions_total,
    harness_a2a_backend_request_duration_seconds,
    harness_a2a_backend_requests_total,
)
from tracing import TraceContext, inject_traceparent, set_span_error, start_span

logger = logging.getLogger(__name__)

TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))
# Inner HTTP timeout must be shorter than the outer asyncio timeout so that
# the client call finishes before asyncio cancels the outer coroutine,
# preventing a dangling connection.
_HTTP_TIMEOUT_SECONDS = max(TASK_TIMEOUT_SECONDS - 10, 10)

# Retry configuration for transient network errors.
_MAX_RETRIES = int(os.environ.get("A2A_BACKEND_MAX_RETRIES", "3"))
if _MAX_RETRIES < 1:
    raise ValueError(f"A2A_BACKEND_MAX_RETRIES must be >= 1, got {_MAX_RETRIES}")
if _MAX_RETRIES > 10:
    logging.getLogger(__name__).warning("A2A_BACKEND_MAX_RETRIES=%d is unusually high", _MAX_RETRIES)
_RETRY_BACKOFF_BASE = float(os.environ.get("A2A_BACKEND_RETRY_BACKOFF", "1.0"))

# Transient status codes that are safe to retry.
_RETRYABLE_STATUS_CODES: frozenset[int] = frozenset({429, 502, 503, 504})

# Circuit-breaker configuration (#609). A simple consecutive-failure breaker
# avoids running the full retry cycle against a known-bad backend. When the
# breaker is `open`, run_query fast-fails with ConnectionError before issuing
# any HTTP. After the cool-off, the next call is served in `half_open` mode
# (one probe) — a success closes the circuit, a failure re-opens it.
_CIRCUIT_THRESHOLD = int(os.environ.get("A2A_BACKEND_CIRCUIT_THRESHOLD", "5"))
if _CIRCUIT_THRESHOLD < 1:
    raise ValueError(
        f"A2A_BACKEND_CIRCUIT_THRESHOLD must be >= 1, got {_CIRCUIT_THRESHOLD}"
    )
_CIRCUIT_COOLOFF_SECONDS = float(
    os.environ.get("A2A_BACKEND_CIRCUIT_COOLOFF_SECONDS", "30")
)
if _CIRCUIT_COOLOFF_SECONDS < 0:
    raise ValueError(
        f"A2A_BACKEND_CIRCUIT_COOLOFF_SECONDS must be >= 0, got {_CIRCUIT_COOLOFF_SECONDS}"
    )

_CIRCUIT_STATES: tuple[str, ...] = ("closed", "open", "half_open")


# Registry of all live A2ABackend instances ever constructed (#1279). Every
# __init__ registers into this set and close() removes itself. The harness's
# close_backends() path walks this set in addition to executor._backends so that
# backends freshly constructed by backends_watcher mid-shutdown — or otherwise
# not yet swapped into the live dict — still get their pooled httpx.AsyncClient
# closed. Without it, a reload that finishes building new backends after the
# lifespan `finally` has already snapshotted executor._backends leaks those
# clients. Using a WeakSet keeps this from extending backend lifetimes.
import weakref as _weakref

_pending_backends: "_weakref.WeakSet[A2ABackend]" = _weakref.WeakSet()


class A2ABackend:
    """Backend that forwards run_query calls to a remote A2A agent."""

    def __init__(self, config: BackendConfig) -> None:
        self.id = config.id
        self._config = config
        self._auth_env = config.auth_env
        # Allow per-backend URL override via env var: A2A_URL_<ID_UPPERCASED>
        # e.g. for id "iris-claude" the env var is "A2A_URL_IRIS_CLAUDE"
        _env_var = "A2A_URL_" + config.id.upper().replace("-", "_")
        self._url = os.environ.get(_env_var) or config.url or ""
        if not self._url:
            raise ValueError(f"A2A backend '{config.id}' has no url configured.")
        # Auth-env startup warning (#787). Parity with the backend-side
        # CONVERSATIONS_AUTH_TOKEN warning (#517): when auth_env is unset or
        # points at an empty env var, every forwarded request goes without
        # an Authorization header and the backend will silently 401. Emit a
        # one-shot WARNING at init so operators notice the misconfiguration
        # in startup logs rather than discovering it from failed traffic.
        if not self._auth_env:
            logger.warning(
                "A2A backend '%s' has no auth_env configured — outbound requests "
                "will be sent without an Authorization header.",
                config.id,
            )
        elif not os.environ.get(self._auth_env):
            logger.warning(
                "A2A backend '%s' auth_env=%s is empty or unset — outbound "
                "requests will be sent without an Authorization header.",
                config.id,
                self._auth_env,
            )
        # Shared AsyncClient with connection pooling; initialized eagerly so that
        # concurrent run_query calls all share the same client without racing on
        # a lazy None-check (#398).
        self._client: httpx.AsyncClient = httpx.AsyncClient(
            timeout=httpx.Timeout(connect=10.0, read=_HTTP_TIMEOUT_SECONDS, write=30.0, pool=5.0),
            limits=httpx.Limits(max_connections=10, max_keepalive_connections=5),
        )

        # Circuit-breaker state (#609). Starts closed; transitions to `open`
        # after `_CIRCUIT_THRESHOLD` consecutive non-OK outcomes. After the
        # cool-off elapses, the next call runs in `half_open` mode: a success
        # closes the circuit, a failure re-opens it. Protected by an asyncio
        # Lock to keep state changes atomic under concurrent run_query calls.
        self._circuit_state: str = "closed"
        self._circuit_consecutive_failures: int = 0
        self._circuit_opened_at: float = 0.0
        self._circuit_lock: asyncio.Lock = asyncio.Lock()
        # #1289: track whether the remote backend has minted a context_id
        # for each session_id yet. A2A semantics say the caller omits
        # contextId on the first turn of a new context and echoes the
        # server-minted value on subsequent turns. Previously every
        # outbound call reused session_id as contextId, which collides
        # with whatever id the remote backend would have chosen itself.
        self._session_has_context: set[str] = set()
        # Initialize gauge labels so scrapes see this backend even before its
        # first request — absent series are harder to alert on than 0-valued
        # ones.
        self._publish_circuit_state_gauge()
        # Register into the module-level pending set (#1279) so close_backends()
        # reaches instances that the backends_watcher constructed after the
        # lifespan finally snapshot.
        _pending_backends.add(self)

    def _get_client(self) -> httpx.AsyncClient:
        if self._client.is_closed:
            self._client = httpx.AsyncClient(
                timeout=httpx.Timeout(connect=10.0, read=_HTTP_TIMEOUT_SECONDS, write=30.0, pool=5.0),
                limits=httpx.Limits(max_connections=10, max_keepalive_connections=5),
            )
        return self._client

    # ------------------------------------------------------------------ #
    # Circuit breaker (#609)
    # ------------------------------------------------------------------ #

    def _publish_circuit_state_gauge(self) -> None:
        """Mirror ``self._circuit_state`` onto the Prometheus gauge.

        Each possible state has its own labelset; exactly one reports 1 and
        the rest report 0. This keeps alerting rules simple
        (``max by (backend) (harness_a2a_backend_circuit_state{state="open"}) == 1``).
        """
        if harness_a2a_backend_circuit_state is None:
            return
        for _state in _CIRCUIT_STATES:
            try:
                harness_a2a_backend_circuit_state.labels(
                    backend=self.id, state=_state
                ).set(1.0 if _state == self._circuit_state else 0.0)
            except Exception:
                pass

    def _transition_circuit(self, new_state: str) -> None:
        """Record a circuit-state transition and update the gauge.

        Caller must hold ``self._circuit_lock``. No-op when already in
        ``new_state`` — avoids emitting phantom transitions.
        """
        prev = self._circuit_state
        if prev == new_state:
            return
        self._circuit_state = new_state
        if harness_a2a_backend_circuit_transitions_total is not None:
            try:
                harness_a2a_backend_circuit_transitions_total.labels(
                    backend=self.id, **{"from": prev, "to": new_state}
                ).inc()
            except Exception:
                pass
        self._publish_circuit_state_gauge()
        logger.info(
            "A2A backend '%s' circuit %s -> %s (consecutive_failures=%d)",
            self.id, prev, new_state, self._circuit_consecutive_failures,
        )
        # Invalidate the /health/ready cache when a backend flips
        # unhealthy so the next probe re-sweeps instead of returning
        # the cached "healthy" body for up to HEALTH_READY_CACHE_TTL
        # seconds after a real backend crash (#703). Import locally to
        # avoid a circular import at module load.
        if new_state == "open":
            try:
                from main import invalidate_health_ready_cache
                invalidate_health_ready_cache()
            except Exception:
                # Best-effort — cache invalidation is a latency optimisation,
                # not a correctness requirement. A failure here just means
                # the cache rides out its TTL.
                pass

    async def _circuit_acquire(self) -> None:
        """Gate an outbound call against the circuit breaker.

        Raises ``ConnectionError`` when the breaker is `open` and its cool-off
        has not elapsed. When the cool-off has elapsed, transitions to
        `half_open` and lets the caller proceed as a probe.
        """
        async with self._circuit_lock:
            if self._circuit_state == "open":
                elapsed = time.monotonic() - self._circuit_opened_at
                if elapsed < _CIRCUIT_COOLOFF_SECONDS:
                    remaining = _CIRCUIT_COOLOFF_SECONDS - elapsed
                    raise ConnectionError(
                        f"circuit open for {self.id}; retry in {remaining:.1f}s"
                    )
                # Cool-off elapsed — allow a single probe.
                self._transition_circuit("half_open")

    async def _circuit_record(self, ok: bool) -> None:
        """Record the outcome of one completed outbound call.

        * On ``ok=True``, clears the consecutive-failure counter and
          closes the breaker if it was `half_open`.
        * On ``ok=False``, increments the counter; when it reaches
          ``_CIRCUIT_THRESHOLD`` the breaker opens. A failure while in
          `half_open` re-opens the breaker and resets the timer.
        """
        async with self._circuit_lock:
            if ok:
                self._circuit_consecutive_failures = 0
                if self._circuit_state != "closed":
                    self._transition_circuit("closed")
                return
            self._circuit_consecutive_failures += 1
            if self._circuit_state == "half_open":
                # Half-open probe failed — re-open and restart the timer.
                self._circuit_opened_at = time.monotonic()
                self._transition_circuit("open")
                return
            if (
                self._circuit_state == "closed"
                and self._circuit_consecutive_failures >= _CIRCUIT_THRESHOLD
            ):
                self._circuit_opened_at = time.monotonic()
                self._transition_circuit("open")

    async def run_query(
        self,
        prompt: str,
        session_id: str,
        is_new: bool,
        model: str | None = None,
        max_tokens: int | None = None,
        trace_context: TraceContext | None = None,
        caller_id: str | None = None,
    ) -> list[str]:
        """Forward the prompt to the remote A2A agent and return collected text chunks.

        When *trace_context* is supplied, a fresh child span_id is minted for this
        outbound call and sent as the W3C ``traceparent`` header (#468). The
        downstream backend sees this harness as the immediate parent in the trace.
        """
        _start = time.monotonic()
        message_id = str(uuid.uuid4())

        # #1289: omit contextId on the first turn of a session so the
        # remote backend can mint its own id; include it on subsequent
        # turns so the backend resumes the same conversation. We treat
        # the local session_id as an opaque correlation key for the
        # "have we seen this session before?" check.
        _message: dict = {
            "messageId": message_id,
            "role": "user",
            "parts": [{"kind": "text", "text": prompt}],
        }
        if session_id in self._session_has_context:
            _message["contextId"] = session_id
        # #1287: use the freshly minted message_id as the JSON-RPC id
        # rather than a hardcoded 1. The id is echoed in the response,
        # so concurrent requests sharing the same outbound connection
        # can now be correlated request-to-response unambiguously.
        payload = {
            "jsonrpc": "2.0",
            "method": "message/send",
            "id": message_id,
            "params": {
                "message": _message,
                # #1289: declare accepted output modes so servers that
                # branch on the streaming capability know this caller
                # wants plain text / JSON rather than, say, SSE frames.
                "configuration": {
                    "acceptedOutputModes": ["text/plain", "application/json"],
                },
            },
        }
        # Mint the child traceparent once so the HTTP header and the JSON-RPC
        # metadata echo refer to the same outbound span_id — mirroring the
        # header inside metadata lets backends that only surface the JSON-RPC
        # envelope (not raw HTTP headers) still correlate the call.
        _outbound_traceparent: str | None = None
        if trace_context is not None:
            _outbound_traceparent = trace_context.child().to_header()

        _span_attrs = {
            "nyx.backend_id": self.id,
            "nyx.url": self._url,
            "nyx.model": model or "",
            "nyx.session_id": session_id,
            "http.request.method": "POST",
        }
        # Gate this call against the circuit breaker (#655). When the
        # breaker is open and still cooling off, _circuit_acquire raises
        # ConnectionError before we spend any retry budget. When the
        # cool-off has elapsed the call is allowed through as a probe.
        await self._circuit_acquire()
        with start_span("a2a.backend.run_query", kind="client", attributes=_span_attrs) as _span:
            # When OTel is enabled, inject() overwrites the traceparent we
            # pre-computed from the bare TraceContext with one whose
            # parent_id matches the active OTel span. This keeps the
            # downstream backend linked to the correct ancestor span in
            # the collector. When OTel is disabled, inject() is a no-op
            # and our bare traceparent wins — the end-to-end trace_id
            # invariant holds either way (#469).
            _carrier: dict[str, str] = {}
            inject_traceparent(_carrier)
            if _carrier.get("traceparent"):
                _outbound_traceparent = _carrier["traceparent"]

            _metadata: dict = {}
            if model:
                _metadata["model"] = model
            if max_tokens is not None:
                _metadata["max_tokens"] = max_tokens
            if _outbound_traceparent is not None:
                _metadata["traceparent"] = _outbound_traceparent
            # Stamp caller_id on outbound A2A relay (#1084). Completes the
            # #867/#929/#935/#941 multi-tenant chain — backends receive a
            # caller identity they can feed into derive_session_id so two
            # callers presenting the same raw session_id land in disjoint
            # sessions. Forwarded opaque so downstream sees whatever the
            # top-most relay derived from its inbound principal.
            if caller_id:
                _metadata["caller_id"] = caller_id
            if _metadata:
                payload["params"]["message"]["metadata"] = _metadata

            body = json.dumps(payload).encode()
            try:
                response_text = await self._post_with_retry(self._url, body, traceparent=_outbound_traceparent)
            except Exception as _exc:
                # Record the failure so the breaker can open after
                # _CIRCUIT_THRESHOLD consecutive failures (#655).
                await self._circuit_record(ok=False)
                set_span_error(_span, _exc)
                raise

            elapsed = time.monotonic() - _start
            logger.debug(f"A2A backend '{self.id}' responded in {elapsed:.2f}s")

            try:
                data = json.loads(response_text)
            except Exception as exc:
                await self._circuit_record(ok=False)
                set_span_error(_span, exc)
                raise ValueError(f"A2A backend '{self.id}' returned non-JSON response: {response_text!r}") from exc

            error = data.get("error")
            if error:
                await self._circuit_record(ok=False)
                _err = RuntimeError(f"A2A backend '{self.id}' returned error: {error}")
                set_span_error(_span, _err)
                raise _err

            result = data.get("result") or {}
            # #1289: remember that the remote backend has now minted /
            # accepted a context for this session so the next call
            # echoes contextId instead of omitting it. We only mark the
            # session on a successful response; failures don't promote
            # the session to "has context" since the server may not have
            # persisted anything.
            self._session_has_context.add(session_id)
            # Record a successful outcome so the failure counter resets
            # and the breaker closes from half_open if applicable.
            await self._circuit_record(ok=True)
            return self._extract_text(result)

    def _observe_backend_request(self, start_monotonic: float, result: str) -> None:
        """Record one outbound-request observation for this backend (#622).

        ``result`` must be one of: ``"ok"``, ``"error_status"``,
        ``"error_connection"``, ``"error_timeout"``. Raw HTTP status codes
        are deliberately NOT used as label values to bound cardinality.
        """
        if harness_a2a_backend_requests_total is not None:
            try:
                harness_a2a_backend_requests_total.labels(
                    backend=self.id, result=result
                ).inc()
            except Exception:
                pass
        if harness_a2a_backend_request_duration_seconds is not None:
            try:
                harness_a2a_backend_request_duration_seconds.labels(
                    backend=self.id
                ).observe(time.monotonic() - start_monotonic)
            except Exception:
                pass

    async def _post_with_retry(self, url: str, body: bytes, traceparent: str | None = None) -> str:
        """POST body to url using the shared AsyncClient, retrying on transient errors.

        *traceparent* — optional W3C trace-context header value; attached to every
        retry attempt so downstream observability correlates all retries to the
        same caller span (#468).
        """
        last_exc: Exception | None = None
        _headers = {"Content-Type": "application/json"}
        if traceparent is not None:
            _headers["traceparent"] = traceparent
        # Resolve auth token at call time (not __init__) so token rotation takes
        # effect without a container restart. When auth_env is unset or the env
        # var is empty, no Authorization header is added. Never log the token
        # value — only its presence is safe to surface.
        if self._auth_env:
            _token = os.environ.get(self._auth_env) or ""
            if _token:
                _headers["Authorization"] = f"Bearer {_token}"
        for attempt in range(_MAX_RETRIES):
            client = self._get_client()
            _req_start = time.monotonic()
            _result_label = "ok"  # re-set below in each error branch
            try:
                resp = await client.post(
                    url,
                    content=body,
                    headers=_headers,
                )
                if resp.status_code in _RETRYABLE_STATUS_CODES:
                    logger.warning(
                        f"A2A backend '{self.id}' returned HTTP {resp.status_code} "
                        f"(attempt {attempt + 1}/{_MAX_RETRIES}) — retrying"
                    )
                    last_exc = ConnectionError(
                        f"A2A backend '{self.id}' returned HTTP {resp.status_code}"
                    )
                    _result_label = "error_status"
                    # Fall through to the shared backoff block below so that
                    # retryable HTTP codes (429, 502, 503, 504) wait the same
                    # exponential delay as connection-level errors.
                else:
                    resp.raise_for_status()
                    return resp.text
            except (httpx.ReadTimeout, httpx.WriteTimeout, httpx.PoolTimeout) as exc:
                logger.warning(
                    f"A2A backend '{self.id}' transient error on attempt {attempt + 1}/{_MAX_RETRIES}: {exc!r}"
                )
                last_exc = exc
                _result_label = "error_timeout"
                # Do NOT aclose() the shared client here — sibling concurrent
                # requests reuse this pool and aclose() would cancel them too
                # (#975). httpx recycles broken connections internally.
            except httpx.ConnectError as exc:
                logger.warning(
                    f"A2A backend '{self.id}' transient error on attempt {attempt + 1}/{_MAX_RETRIES}: {exc!r}"
                )
                last_exc = exc
                _result_label = "error_connection"
                # See #975 — same rationale as the timeout branch above.
            except httpx.HTTPStatusError as exc:
                # Non-retryable HTTP error — surface immediately.
                logger.error(f"A2A backend '{self.id}' HTTP error: {exc!r}")
                _result_label = "error_status"
                raise ConnectionError(
                    f"A2A backend '{self.id}' returned HTTP {exc.response.status_code}"
                ) from exc
            except Exception as exc:
                logger.error(f"A2A backend '{self.id}' unexpected error: {exc!r}")
                _result_label = "error_connection"
                raise
            finally:
                # Record one observation per attempt (#622). Success path sets
                # _result_label to "ok"; error paths set "error_*" before the
                # raise/fall-through. Runs exactly once per attempt.
                self._observe_backend_request(_req_start, _result_label)

            if attempt < _MAX_RETRIES - 1:
                # #1186: cap exponential backoff at 30s so high _MAX_RETRIES
                # values (or future tuning) can't produce multi-minute sleeps
                # that exceed upstream task timeouts. Jitter is preserved.
                delay = min(
                    _RETRY_BACKOFF_BASE * (2 ** attempt) + random.uniform(0, _RETRY_BACKOFF_BASE),
                    30.0,
                )
                await asyncio.sleep(delay)

        raise ConnectionError(
            f"A2A backend '{self.id}' unreachable at {url} after {_MAX_RETRIES} attempts"
        ) from last_exc

    @staticmethod
    def _extract_text(result: dict) -> list[str]:
        """Extract text parts from an A2A JSON-RPC result payload.

        Primary path: A2A Task structure with artifacts.
          result["artifacts"][*]["parts"][*]["text"]

        The JSON-RPC envelope wraps the Task as result["result"], so callers
        pass result = data["result"].  However the Task object itself may also
        appear directly at the top level (streaming/final Task), so we check
        both result["artifacts"] and result["result"]["artifacts"].
        """
        texts: list[str] = []

        def _collect_from_artifacts(obj: dict) -> None:
            artifacts = obj.get("artifacts")
            if not isinstance(artifacts, list):
                return
            for artifact in artifacts:
                if not isinstance(artifact, dict):
                    continue
                parts = artifact.get("parts") or []
                for part in parts:
                    if not isinstance(part, dict):
                        continue
                    text = part.get("text")
                    if isinstance(text, str) and text:
                        texts.append(text)

        # Try Task at top level (result is the Task object directly)
        _collect_from_artifacts(result)

        # Try Task nested one level deeper (result["result"] is the Task object)
        if not texts:
            nested = result.get("result")
            if isinstance(nested, dict):
                _collect_from_artifacts(nested)

        if texts:
            return texts

        # A2A message response: result has kind="message" with parts directly on it
        if result.get("kind") == "message":
            for part in result.get("parts") or []:
                if isinstance(part, dict) and part.get("kind") == "text":
                    text = part.get("text") or ""
                    if text:
                        texts.append(text)

        if texts:
            return texts

        # Legacy fallback: some A2A implementations use messages/message lists
        messages = result.get("messages") or []
        if not messages:
            msg = result.get("message")
            if msg:
                messages = [msg]

        for message in messages:
            if not isinstance(message, dict):
                continue
            parts = message.get("parts") or []
            for part in parts:
                if not isinstance(part, dict):
                    continue
                if part.get("kind") == "text":
                    text = part.get("text") or ""
                    if text:
                        texts.append(text)

        # Final fallback: direct text/content field
        if not texts:
            direct = result.get("text") or result.get("content") or ""
            if isinstance(direct, str) and direct:
                texts.append(direct)

        return texts

    async def close(self) -> None:
        if not self._client.is_closed:
            await self._client.aclose()
