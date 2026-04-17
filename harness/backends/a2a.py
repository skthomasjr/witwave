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


class A2ABackend:
    """Backend that forwards run_query calls to a remote A2A agent."""

    def __init__(self, config: BackendConfig) -> None:
        self.id = config.id
        self._config = config
        self._auth_env = config.auth_env
        # Allow per-backend URL override via env var: A2A_URL_<ID_UPPERCASED>
        # e.g. for id "iris-a2-claude" the env var is "A2A_URL_IRIS_A2_CLAUDE"
        _env_var = "A2A_URL_" + config.id.upper().replace("-", "_")
        self._url = os.environ.get(_env_var) or config.url or ""
        if not self._url:
            raise ValueError(f"A2A backend '{config.id}' has no url configured.")
        # Shared AsyncClient with connection pooling; initialized eagerly so that
        # concurrent run_query calls all share the same client without racing on
        # a lazy None-check (#398).
        self._client: httpx.AsyncClient = httpx.AsyncClient(
            timeout=httpx.Timeout(connect=10.0, read=_HTTP_TIMEOUT_SECONDS, write=30.0, pool=5.0),
            limits=httpx.Limits(max_connections=10, max_keepalive_connections=5),
        )

    def _get_client(self) -> httpx.AsyncClient:
        if self._client.is_closed:
            self._client = httpx.AsyncClient(
                timeout=httpx.Timeout(connect=10.0, read=_HTTP_TIMEOUT_SECONDS, write=30.0, pool=5.0),
                limits=httpx.Limits(max_connections=10, max_keepalive_connections=5),
            )
        return self._client

    async def run_query(
        self,
        prompt: str,
        session_id: str,
        is_new: bool,
        model: str | None = None,
        max_tokens: int | None = None,
        trace_context: TraceContext | None = None,
    ) -> list[str]:
        """Forward the prompt to the remote A2A agent and return collected text chunks.

        When *trace_context* is supplied, a fresh child span_id is minted for this
        outbound call and sent as the W3C ``traceparent`` header (#468). The
        downstream backend sees this harness as the immediate parent in the trace.
        """
        _start = time.monotonic()
        message_id = str(uuid.uuid4())

        payload = {
            "jsonrpc": "2.0",
            "method": "message/send",
            "id": 1,
            "params": {
                "message": {
                    "messageId": message_id,
                    "contextId": session_id,
                    "role": "user",
                    "parts": [{"kind": "text", "text": prompt}],
                }
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
            if _metadata:
                payload["params"]["message"]["metadata"] = _metadata

            body = json.dumps(payload).encode()
            try:
                response_text = await self._post_with_retry(self._url, body, traceparent=_outbound_traceparent)
            except Exception as _exc:
                set_span_error(_span, _exc)
                raise

            elapsed = time.monotonic() - _start
            logger.debug(f"A2A backend '{self.id}' responded in {elapsed:.2f}s")

            try:
                data = json.loads(response_text)
            except Exception as exc:
                set_span_error(_span, exc)
                raise ValueError(f"A2A backend '{self.id}' returned non-JSON response: {response_text!r}") from exc

            error = data.get("error")
            if error:
                _err = RuntimeError(f"A2A backend '{self.id}' returned error: {error}")
                set_span_error(_span, _err)
                raise _err

            result = data.get("result") or {}
            return self._extract_text(result)

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
                    # Fall through to the shared backoff block below so that
                    # retryable HTTP codes (429, 502, 503, 504) wait the same
                    # exponential delay as connection-level errors.
                else:
                    resp.raise_for_status()
                    return resp.text
            except (httpx.ConnectError, httpx.ReadTimeout, httpx.WriteTimeout, httpx.PoolTimeout) as exc:
                logger.warning(
                    f"A2A backend '{self.id}' transient error on attempt {attempt + 1}/{_MAX_RETRIES}: {exc!r}"
                )
                last_exc = exc
                # Close client after a connection-level error; _get_client() will
                # recreate it on the next attempt.
                try:
                    await self._client.aclose()
                except Exception:
                    pass
            except httpx.HTTPStatusError as exc:
                # Non-retryable HTTP error — surface immediately.
                logger.error(f"A2A backend '{self.id}' HTTP error: {exc!r}")
                raise ConnectionError(
                    f"A2A backend '{self.id}' returned HTTP {exc.response.status_code}"
                ) from exc
            except Exception as exc:
                logger.error(f"A2A backend '{self.id}' unexpected error: {exc!r}")
                raise

            if attempt < _MAX_RETRIES - 1:
                delay = _RETRY_BACKOFF_BASE * (2 ** attempt) + random.uniform(0, _RETRY_BACKOFF_BASE)
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
