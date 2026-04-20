---
name: budget-exceeded-claude
description: Token-budget smoke test — fires one prompt against the Claude backend with a max-tokens cap small enough to always be exceeded, verifying that BudgetExceededError surfaces as a "system" conversation-log entry.
agent: claude
max-tokens: 10
enabled: true
---

Write a detailed, multi-paragraph response explaining what autonomous agent platforms are and why they matter. Be thorough and verbose.
