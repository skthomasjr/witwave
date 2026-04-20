# Active Agents

This directory contains agents used for active development — real, persistent agents that are configured, deployed,
and iterated on as part of ongoing work. These agents have full identities, memory, and behavioral configuration, and
are expected to run continuously as part of the development environment.

Each agent directory contains a `.witwave/` directory for harness runtime config, plus `claude/` and `codex/`
subdirectories for the backend instances (identity, logs, and memory). See `AGENTS.md` at the repo root for the full
layout.
