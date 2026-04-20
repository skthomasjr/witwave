# Test Agents

This directory contains agents used for testing — validating agent behavior, verifying configuration changes, and
exercising the agent runtime without affecting active development agents.

## Agents

| Agent | Role                                                | Backends active                                      |
| ----- | --------------------------------------------------- | ---------------------------------------------------- |
| bob   | Multi-backend smoke test; all three backends wired  | `.claude/` + `.codex/` + `.gemini/`                  |
| fred  | Pure-Claude validation agent                        | `.claude/` (backend.yaml routes all kinds to claude) |
| jack  | Pure-Codex validation agent                         | `.codex/` (backend.yaml routes all kinds to codex)   |
| luke  | Pure-Gemini validation agent                        | `.gemini/` (backend.yaml routes all kinds to gemini) |

Each agent's `.witwave/` directory carries the harness runtime config (agent-card, HEARTBEAT, backend.yaml, and minimal
`jobs/` + `continuations/` entries). Single-backend test agents (fred, jack, luke) deliberately omit the backend
directories they don't use, so the filesystem shape matches the backend the agent actually runs.

Currently only bob and fred are wired into `charts/witwave/values-test.yaml`. jack and luke exist as filesystem scaffolds
and will land in values-test.yaml when they're needed for smoke runs.

See `AGENTS.md` at the repo root for the full layout and the `/remote` interaction pattern.
