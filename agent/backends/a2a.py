"""A2A backend — forwards requests to a remote A2A agent over HTTP/JSON-RPC."""

from __future__ import annotations

import asyncio
import json
import logging
import os
import time
import urllib.request
import uuid
from urllib.error import URLError

from backends.config import BackendConfig

logger = logging.getLogger(__name__)

TASK_TIMEOUT_SECONDS = int(os.environ.get("TASK_TIMEOUT_SECONDS", "300"))
# Inner urllib timeout must be shorter than the outer asyncio timeout so that
# the thread always finishes before asyncio cancels it, preventing a thread leak.
_HTTP_TIMEOUT_SECONDS = max(TASK_TIMEOUT_SECONDS - 10, 10)


class A2ABackend:
    """Backend that forwards run_query calls to a remote A2A agent."""

    def __init__(self, config: BackendConfig) -> None:
        self.id = config.id
        self._config = config
        # Allow per-backend URL override via env var: A2A_URL_<ID_UPPERCASED>
        # e.g. for id "iris-a2-claude" the env var is "A2A_URL_IRIS_A2_CLAUDE"
        _env_var = "A2A_URL_" + config.id.upper().replace("-", "_")
        self._url = os.environ.get(_env_var) or config.url or ""
        if not self._url:
            raise ValueError(f"A2A backend '{config.id}' has no url configured.")

    async def run_query(
        self,
        prompt: str,
        session_id: str,
        is_new: bool,
        model: str | None = None,
    ) -> list[str]:
        """Forward the prompt to the remote A2A agent and return collected text chunks."""
        _start = time.monotonic()
        message_id = str(uuid.uuid4())

        payload = {
            "jsonrpc": "2.0",
            "method": "message/send",
            "id": 1,
            "params": {
                "message": {
                    "messageId": message_id,
                    "role": "user",
                    "metadata": {"session_id": session_id},
                    "parts": [{"kind": "text", "text": prompt}],
                }
            },
        }
        if model:
            payload["params"]["message"]["metadata"]["model"] = model

        body = json.dumps(payload).encode()

        try:
            response_text = await asyncio.to_thread(
                self._post, self._url, body
            )
        except URLError as exc:
            logger.error(f"A2A backend '{self.id}' request failed: {exc}")
            raise ConnectionError(f"A2A backend '{self.id}' unreachable at {self._url}: {exc}") from exc
        except Exception as exc:
            logger.error(f"A2A backend '{self.id}' unexpected error: {exc}")
            raise

        elapsed = time.monotonic() - _start
        logger.debug(f"A2A backend '{self.id}' responded in {elapsed:.2f}s")

        try:
            data = json.loads(response_text)
        except Exception as exc:
            raise ValueError(f"A2A backend '{self.id}' returned non-JSON response: {response_text!r}") from exc

        error = data.get("error")
        if error:
            raise RuntimeError(f"A2A backend '{self.id}' returned error: {error}")

        result = data.get("result") or {}
        return self._extract_text(result)

    @staticmethod
    def _post(url: str, body: bytes) -> str:
        req = urllib.request.Request(
            url,
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=_HTTP_TIMEOUT_SECONDS) as resp:
            return resp.read().decode()

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
            parts = message.get("parts") or []
            for part in parts:
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
        pass
