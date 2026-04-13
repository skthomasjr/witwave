"""Shared exception types used across all backend executors."""


class BudgetExceededError(Exception):
    """Raised when cumulative token usage exceeds the per-dispatch max_tokens budget."""

    def __init__(self, total: int, limit: int, collected: "list[str] | None" = None) -> None:
        super().__init__(f"Token budget exceeded: {total} tokens used of {limit} limit.")
        self.total = total
        self.limit = limit
        self.collected: list[str] = collected or []
