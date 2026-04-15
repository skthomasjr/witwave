# Test Agents

This directory contains agents used for testing purposes — validating agent behavior, verifying configuration
changes, and exercising the agent runtime without affecting active development agents.

Each agent directory contains a `.nyx/` directory for nyx-harness runtime config, plus `a2-claude/` and `a2-codex/`
subdirectories for the backend instances (identity, logs, and memory). See `AGENTS.md` at the repo root for the full
layout.
