"""Codex backend — wraps the openai-agents SDK."""

from __future__ import annotations

import logging
import os
import time

from agents import Agent, Runner, RunConfig, SQLiteSession
from agents.models.multi_provider import MultiProvider
from backends.config import BackendConfig, credential_for
from metrics import agent_sdk_query_duration_seconds, agent_sdk_query_error_duration_seconds

logger = logging.getLogger(__name__)

CODEX_SESSION_DB = os.environ.get("CODEX_SESSION_DB", "/home/agent/logs/codex_sessions.db")


class CodexBackend:
    """Agent backend powered by the OpenAI Agents SDK (Codex)."""

    def __init__(
        self,
        config: BackendConfig,
        agent_name: str,
        log_entry_fn,
    ) -> None:
        self.id = config.id
        self._config = config
        self._agent_name = agent_name
        self._log_entry = log_entry_fn
        self._model: str = config.model or "gpt-5.3-codex"
        self._api_key: str | None = credential_for(config)
        self._agent: Agent | None = None

    def _get_agent(self) -> Agent:
        if self._agent is None:
            self._agent = Agent(
                name=self._agent_name,
                instructions=f"Your name is {self._agent_name}.",
                model=self._model,
            )
        return self._agent

    async def run_query(self, prompt: str, session_id: str, is_new: bool, model: str | None = None) -> list[str]:
        log_dir = os.path.dirname(CODEX_SESSION_DB)
        if log_dir:
            os.makedirs(log_dir, exist_ok=True)

        if model and model != self._model:
            agent = Agent(
                name=self._agent_name,
                instructions=f"Your name is {self._agent_name}.",
                model=model,
            )
        else:
            agent = self._get_agent()
        session = SQLiteSession(session_id, CODEX_SESSION_DB)
        if is_new:
            await session.clear_session()
        run_config = RunConfig(model_provider=MultiProvider(openai_api_key=self._api_key)) if self._api_key else None

        collected: list[str] = []
        _query_start = time.monotonic()
        try:
            result = Runner.run_streamed(agent, prompt, session=session, run_config=run_config)
            async for event in result.stream_events():
                if event.type == "raw_response_event":
                    delta = getattr(getattr(event, "data", None), "delta", None)
                    if delta and hasattr(delta, "text") and delta.text:
                        collected.append(delta.text)
        except Exception:
            if agent_sdk_query_error_duration_seconds is not None:
                agent_sdk_query_error_duration_seconds.labels(backend=self.id).observe(time.monotonic() - _query_start)
            raise

        if agent_sdk_query_duration_seconds is not None:
            agent_sdk_query_duration_seconds.labels(backend=self.id).observe(time.monotonic() - _query_start)

        # Flush any final output not captured via streaming deltas
        final = getattr(result, "final_output", None)
        if final and isinstance(final, str) and not collected:
            collected.append(final)

        full_response = "".join(collected)
        if full_response:
            self._log_entry("agent", full_response, session_id)

        return collected

    async def close(self) -> None:
        self._agent = None
