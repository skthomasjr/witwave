"""Shared exception types used across all backend executors."""


class BudgetExceededError(Exception):
    """Raised when cumulative token usage exceeds the per-dispatch max_tokens budget."""

    def __init__(self, total: int, limit: int, collected: "list[str] | None" = None) -> None:
        super().__init__(f"Token budget exceeded: {total} tokens used of {limit} limit.")
        self.total = total
        self.limit = limit
        self.collected: list[str] = collected or []


class PromptTooLargeError(Exception):
    """Raised when an inbound prompt exceeds the configured MAX_PROMPT_BYTES cap (#1620).

    The cap exists to prevent a single oversized prompt (e.g. a 1 GB request)
    from OOM-killing the backend pod. Backends translate this into an
    A2A-friendly rejection rather than letting the SDK crash mid-flight.
    """

    def __init__(self, size_bytes: int, limit_bytes: int) -> None:
        super().__init__(
            f"Prompt size {size_bytes} bytes exceeds MAX_PROMPT_BYTES limit of {limit_bytes} bytes."
        )
        self.size_bytes = size_bytes
        self.limit_bytes = limit_bytes
