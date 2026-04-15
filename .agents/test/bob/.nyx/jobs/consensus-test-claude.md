---
name: consensus-test-claude
description: Multi-model consensus test — three backends answer independently, Claude synthesizes the result.
agent: claude
consensus:
  - backend: "codex"
    model: "gpt-5.1-codex-max"
  - backend: "claude"
    model: "claude-sonnet-4-6"
  - backend: "claude"
    model: "claude-haiku-4-5"
enabled: true
---

What are the three most important things an autonomous agent platform should prioritize to be reliable in production?
Answer independently based on your own reasoning. Be concise — 3 bullet points max.
