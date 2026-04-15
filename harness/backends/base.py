"""AgentBackend protocol — the interface every backend must implement."""

from __future__ import annotations

from typing import Protocol, runtime_checkable


@runtime_checkable
class AgentBackend(Protocol):
    """Common interface for all agent backends (Claude, Codex, etc.)."""

    id: str
    """Unique identifier for this backend instance, matching backends.yaml."""

    async def run_query(self, prompt: str, session_id: str, is_new: bool, model: str | None = None) -> list[str]:
        """Execute a prompt and return a list of collected text responses.

        Args:
            prompt:     The user prompt to execute.
            session_id: Stable identifier for the conversation session.
                        Backends use this to resume prior context.
            is_new:     True if this is the first message in the session,
                        False if the session already has history.

        Returns:
            A list of text response chunks. May be empty if the backend
            produced no textual output (e.g. only tool calls).
        """
        ...

    async def close(self) -> None:
        """Release any resources held by this backend (connections, subprocesses, etc.)."""
        ...
