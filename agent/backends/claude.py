"""Claude backend — wraps the claude-agent-sdk."""

from __future__ import annotations

import asyncio
import json
import logging
import os
import time

from backends.config import BackendConfig, auth_env_name, credential_for
from claude_agent_sdk import ClaudeAgentOptions, ClaudeSDKClient
from claude_agent_sdk.types import AssistantMessage, ResultMessage, TextBlock, ToolResultBlock, ToolUseBlock
from metrics import (
    agent_task_retries_total,
    agent_context_exhaustion_total,
    agent_context_tokens,
    agent_context_tokens_remaining,
    agent_context_usage_percent,
    agent_context_warnings_total,
    agent_file_watcher_restarts_total,
    agent_mcp_config_errors_total,
    agent_mcp_config_reloads_total,
    agent_mcp_servers_active,
    agent_sdk_client_errors_total,
    agent_sdk_context_fetch_errors_total,
    agent_sdk_errors_total,
    agent_sdk_messages_per_query,
    agent_sdk_query_duration_seconds,
    agent_sdk_query_error_duration_seconds,
    agent_sdk_result_errors_total,
    agent_sdk_session_duration_seconds,
    agent_sdk_subprocess_spawn_duration_seconds,
    agent_sdk_time_to_first_message_seconds,
    agent_sdk_tokens_per_query,
    agent_sdk_tool_call_input_size_bytes,
    agent_sdk_tool_calls_per_query,
    agent_sdk_tool_calls_total,
    agent_sdk_tool_duration_seconds,
    agent_sdk_tool_errors_total,
    agent_sdk_tool_result_size_bytes,
    agent_sdk_turns_per_query,
    agent_stderr_lines_per_task,
    agent_tasks_with_stderr_total,
    agent_text_blocks_per_query,
    agent_watcher_events_total,
)
from watchfiles import awatch

logger = logging.getLogger(__name__)

CONTEXT_USAGE_WARN_THRESHOLD = float(os.environ.get("CONTEXT_USAGE_WARN_THRESHOLD", "0.9"))
MCP_CONFIG_PATH = os.environ.get("MCP_CONFIG_PATH", "/home/agent/.claude/mcp.json")


def _log_tool_event(event_type: str, block, session_id: str, log_fn, model: str | None = None) -> None:
    try:
        ts = __import__("datetime").datetime.now(__import__("datetime").timezone.utc).isoformat()
        if event_type == "tool_use":
            entry = {
                "ts": ts, "session_id": session_id, "event_type": event_type,
                "model": model, "id": block.id, "name": block.name, "input": block.input,
            }
        else:
            entry = {
                "ts": ts, "session_id": session_id, "event_type": event_type,
                "model": model, "tool_use_id": block.tool_use_id,
                "content": block.content, "is_error": block.is_error,
            }
        log_fn(json.dumps(entry))
    except Exception as e:
        logger.error(f"_log_tool_event error: {e}")


class ClaudeBackend:
    """Agent backend powered by the Claude Agent SDK."""

    def __init__(
        self,
        config: BackendConfig,
        agent_name: str,
        allowed_tools: list[str],
        log_entry_fn,
        log_trace_fn,
    ) -> None:
        self.id = config.id
        self._config = config
        self._agent_name = agent_name
        self._allowed_tools = allowed_tools
        self._log_entry = log_entry_fn
        self._log_trace = log_trace_fn
        self._mcp_servers: dict = {}
        self._model: str | None = config.model
        self._auth_env: str | None = auth_env_name(config)
        self._credential: str | None = credential_for(config)

    def _load_mcp_config(self) -> dict:
        if not os.path.exists(MCP_CONFIG_PATH):
            return {}
        try:
            with open(MCP_CONFIG_PATH) as f:
                return json.load(f)
        except Exception as e:
            if agent_mcp_config_errors_total is not None:
                agent_mcp_config_errors_total.inc()
            logger.warning(f"Failed to load MCP config from {MCP_CONFIG_PATH}: {e}")
            return {}

    async def mcp_config_watcher(self) -> None:
        self._mcp_servers = self._load_mcp_config()
        if agent_mcp_servers_active is not None:
            agent_mcp_servers_active.set(len(self._mcp_servers))
        if self._mcp_servers:
            logger.info(f"[{self.id}] MCP config loaded: {list(self._mcp_servers.keys())}")

        watch_dir = os.path.dirname(MCP_CONFIG_PATH)
        while True:
            if not os.path.isdir(watch_dir):
                logger.info("MCP config directory not found — retrying in 10s.")
                await asyncio.sleep(10)
                continue
            async for changes in awatch(watch_dir):
                if agent_watcher_events_total is not None:
                    agent_watcher_events_total.labels(watcher="mcp").inc()
                for _, path in changes:
                    if os.path.abspath(path) == os.path.abspath(MCP_CONFIG_PATH):
                        self._mcp_servers = self._load_mcp_config()
                        if agent_mcp_servers_active is not None:
                            agent_mcp_servers_active.set(len(self._mcp_servers))
                        logger.info(f"[{self.id}] MCP config reloaded: {list(self._mcp_servers.keys())}")
                        if agent_mcp_config_reloads_total is not None:
                            agent_mcp_config_reloads_total.inc()
                        break
            logger.warning("MCP config directory watcher exited — retrying in 10s.")
            if agent_file_watcher_restarts_total is not None:
                agent_file_watcher_restarts_total.labels(watcher="mcp").inc()
            await asyncio.sleep(10)

    def _make_options(self, session_id: str, resume: bool, stderr_fn, model: str | None = None) -> ClaudeAgentOptions:
        # Build subprocess env override when a specific credential is configured.
        # The Claude SDK subprocess reads CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY
        # from its environment. We inject the resolved credential under the correct
        # var name so multiple Claude backends with different accounts work correctly.
        env: dict | None = None
        if self._credential and self._auth_env:
            env = {self._auth_env: self._credential}

        return ClaudeAgentOptions(
            allowed_tools=self._allowed_tools,
            system_prompt=f"Your name is {self._agent_name}. Your session ID is {session_id}.",
            resume=session_id if resume else None,
            session_id=None if resume else session_id,
            stderr=stderr_fn,
            mcp_servers=self._mcp_servers,
            model=model or self._model,
            **({"env": env} if env else {}),
        )

    async def _run_query(self, prompt: str, options: ClaudeAgentOptions, session_id: str, effective_model: str | None = None) -> list[str]:
        collected: list[str] = []
        _query_start = time.monotonic()
        _message_count = 0
        _tool_names: dict[str, str] = {}
        _tool_start_times: dict[str, float] = {}
        _last_total_tokens = 0
        _session_start = time.monotonic()
        try:
            _spawn_start = time.monotonic()
            async with ClaudeSDKClient(options=options) as client:
                if agent_sdk_subprocess_spawn_duration_seconds is not None:
                    agent_sdk_subprocess_spawn_duration_seconds.labels(backend=self.id).observe(time.monotonic() - _spawn_start)
                await client.query(prompt)
                _query_sent_at = time.monotonic()
                _assistant_turn_count = 0
                async for message in client.receive_response():
                    _message_count += 1
                    if isinstance(message, AssistantMessage):
                        if _assistant_turn_count == 0:
                            if agent_sdk_time_to_first_message_seconds is not None:
                                agent_sdk_time_to_first_message_seconds.labels(backend=self.id).observe(time.monotonic() - _query_sent_at)
                        _assistant_turn_count += 1
                        for block in message.content:
                            if isinstance(block, TextBlock):
                                collected.append(block.text)
                                self._log_entry("agent", block.text, session_id)
                            elif isinstance(block, ToolUseBlock):
                                _tool_names[block.id] = block.name
                                _tool_start_times[block.id] = time.monotonic()
                                if agent_sdk_tool_calls_total is not None:
                                    agent_sdk_tool_calls_total.labels(backend=self.id, tool=block.name).inc()
                                if agent_sdk_tool_call_input_size_bytes is not None:
                                    agent_sdk_tool_call_input_size_bytes.labels(backend=self.id, tool=block.name).observe(
                                        len(json.dumps(block.input).encode())
                                    )
                                _log_tool_event("tool_use", block, session_id, self._log_trace, model=effective_model)
                            elif isinstance(block, ToolResultBlock):
                                tool_name = _tool_names.get(block.tool_use_id, "unknown")
                                if block.is_error and agent_sdk_tool_errors_total is not None:
                                    agent_sdk_tool_errors_total.labels(backend=self.id, tool=tool_name).inc()
                                _t_start = _tool_start_times.pop(block.tool_use_id, None)
                                if _t_start is not None and agent_sdk_tool_duration_seconds is not None:
                                    agent_sdk_tool_duration_seconds.labels(backend=self.id, tool=tool_name).observe(
                                        time.monotonic() - _t_start
                                    )
                                if agent_sdk_tool_result_size_bytes is not None:
                                    agent_sdk_tool_result_size_bytes.labels(backend=self.id, tool=tool_name).observe(
                                        len(str(block.content).encode())
                                    )
                                _log_tool_event("tool_result", block, session_id, self._log_trace, model=effective_model)
                        try:
                            usage = await client.get_context_usage()
                            pct = usage.get("percentage", 0.0)
                            _last_total_tokens = usage.get("totalTokens", 0)
                            if agent_context_tokens is not None:
                                agent_context_tokens.observe(_last_total_tokens)
                            if agent_context_tokens_remaining is not None:
                                agent_context_tokens_remaining.observe(
                                    usage.get("maxTokens", 0) - _last_total_tokens
                                )
                            if agent_context_usage_percent is not None:
                                agent_context_usage_percent.observe(pct)
                            if pct >= 100 and agent_context_exhaustion_total is not None:
                                agent_context_exhaustion_total.inc()
                            if pct >= CONTEXT_USAGE_WARN_THRESHOLD * 100:
                                if agent_context_warnings_total is not None:
                                    agent_context_warnings_total.inc()
                                logger.warning(
                                    f"[{self.id}] Session {session_id!r}: context usage {pct:.1f}% "
                                    f"exceeds threshold {CONTEXT_USAGE_WARN_THRESHOLD * 100:.0f}%"
                                )
                        except Exception as e:
                            if agent_sdk_context_fetch_errors_total is not None:
                                agent_sdk_context_fetch_errors_total.labels(backend=self.id).inc()
                            logger.warning(f"[{self.id}] Session {session_id!r}: get_context_usage failed: {e}")
                    elif isinstance(message, ResultMessage) and message.is_error:
                        if agent_sdk_result_errors_total is not None:
                            agent_sdk_result_errors_total.labels(backend=self.id).inc()
                        if agent_sdk_query_error_duration_seconds is not None:
                            agent_sdk_query_error_duration_seconds.labels(backend=self.id).observe(time.monotonic() - _query_start)
                        raise RuntimeError("\n".join(message.errors or []))
        except (OSError, ConnectionError):
            if agent_sdk_client_errors_total is not None:
                agent_sdk_client_errors_total.labels(backend=self.id).inc()
            if agent_sdk_query_error_duration_seconds is not None:
                agent_sdk_query_error_duration_seconds.labels(backend=self.id).observe(time.monotonic() - _query_start)
            if agent_sdk_session_duration_seconds is not None:
                agent_sdk_session_duration_seconds.labels(backend=self.id).observe(time.monotonic() - _session_start)
            raise

        if agent_sdk_session_duration_seconds is not None:
            agent_sdk_session_duration_seconds.labels(backend=self.id).observe(time.monotonic() - _session_start)
        if agent_sdk_query_duration_seconds is not None:
            agent_sdk_query_duration_seconds.labels(backend=self.id).observe(time.monotonic() - _query_start)
        if agent_sdk_messages_per_query is not None:
            agent_sdk_messages_per_query.labels(backend=self.id).observe(_message_count)
        if agent_sdk_tokens_per_query is not None:
            agent_sdk_tokens_per_query.labels(backend=self.id).observe(_last_total_tokens)
        if agent_sdk_tool_calls_per_query is not None:
            agent_sdk_tool_calls_per_query.labels(backend=self.id).observe(len(_tool_names))
        if agent_sdk_turns_per_query is not None:
            agent_sdk_turns_per_query.labels(backend=self.id).observe(_assistant_turn_count)
        if agent_text_blocks_per_query is not None:
            agent_text_blocks_per_query.observe(len(collected))
        return collected

    async def run_query(self, prompt: str, session_id: str, is_new: bool, model: str | None = None) -> list[str]:
        stderr_lines: list[str] = []
        _query_start = time.monotonic()

        def capture_stderr(line: str) -> None:
            stderr_lines.append(line)
            if agent_sdk_errors_total is not None:
                agent_sdk_errors_total.labels(backend=self.id).inc()
            logger.error(f"[{self.id}] [claude stderr] {line}")

        effective_model = model or self._model
        try:
            return await self._run_query(prompt, self._make_options(session_id, resume=not is_new, stderr_fn=capture_stderr, model=model), session_id, effective_model=effective_model)
        except Exception:
            if is_new and any("already in use" in line.lower() for line in stderr_lines):
                if agent_task_retries_total is not None:
                    agent_task_retries_total.inc()
                if agent_sdk_query_error_duration_seconds is not None:
                    agent_sdk_query_error_duration_seconds.labels(backend=self.id).observe(time.monotonic() - _query_start)
                return await self._run_query(prompt, self._make_options(session_id, resume=True, stderr_fn=capture_stderr, model=model), session_id, effective_model=effective_model)
            raise
        finally:
            if agent_stderr_lines_per_task is not None:
                agent_stderr_lines_per_task.observe(len(stderr_lines))
            if stderr_lines and agent_tasks_with_stderr_total is not None:
                agent_tasks_with_stderr_total.inc()

    async def close(self) -> None:
        pass
