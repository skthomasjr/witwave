# AGENTS.md

This file provides guidance to Claude Code (https://claude.ai/code) and Codex (https://openai.com/codex/) when working
with code in this repository.

## Repo Root

The repo root is referred to as `<repo-root>`. For this environment, `<repo-root>` is the directory containing this
file.

## Skills

Skills under `.claude/skills/` are for local use. Skills that agents need must also be copied to each agent's
`.agents/active/<name>/.claude/skills/` directory. When a shared skill is updated, sync the change to all agents that have a
copy.

## Agent Identity

The acting agent is referred to as `<agent-name>`. For containerized workers, `<agent-name>` is the value of the
`AGENT_NAME` environment variable (e.g. `iris`, `nova`, `kira`). When running as a local session (Claude Code, Codex,
or otherwise), `AGENT_NAME` is not set — in that case, `<agent-name>` is `local-agent`.

## Working with Claude Code and Codex

- Do not run `git commit` unless explicitly asked.
- Do not run `git push` unless explicitly asked.

## Project Overview

autonomous-agent is an autonomous agent built on the Claude Agent SDK — persistent, self-directed, with its own
identity, memory, schedule, and the ability to communicate with other agents and humans. Multiple agents can collaborate
as a team, but the agent itself is the unit.

## Architecture

Each agentic worker runs as a containerized instance of the `nyx-agent` image. Workers are configured via mounted
files and environment variables — no identity or behavior is baked into the image itself.

- **A2A protocol** — primary communication layer (HTTP/JSON-RPC). Each agent exposes `/.well-known/agent.json` for
  discovery and `/` for task execution.
- **Agent configuration** — defined under `.agents/active/<name>/`. Runtime config lives in `.nyx/`: `agent.md` (A2A
  identity), `backends.yaml` (backend selection), `HEARTBEAT.md` (proactive schedule), and `agenda/` (scheduled work
  items). `agent-card.md` is the description text served in the A2A agent card. Behavioral config for Claude
  Code lives in `.claude/CLAUDE.md`.
- **Conversation logging** — each agent writes a `conversation.log` to `.agents/active/<name>/logs/`.

## Project Structure

```text
.agents/
└── active/
    ├── iris/
    │   ├── .nyx/           # Runtime config (agent-card.md, backends.yaml, agenda/)
    │   └── .claude/        # Claude Code config (CLAUDE.md, skills/, memory/)
    ├── nova/
    │   ├── .nyx/
    │   └── .claude/
    └── kira/
        ├── .nyx/
        └── .claude/
agent/
├── main.py             # A2A server entrypoint
├── executor.py         # Bridges A2A and Claude Agent SDK
├── bus.py              # Internal message bus
├── heartbeat.py        # Heartbeat scheduler
├── agenda.py           # Agenda scheduler
├── metrics.py          # Prometheus metrics definitions
└── utils.py            # Shared utilities (e.g. frontmatter parser)
docker-compose.yml      # Runs all agents locally
Dockerfile              # nyx-agent image
```

## Running Locally

```bash
docker build -t nyx-agent:latest .
docker compose up -d
```

## Interacting with Agents

Use the `/remote` skill to interact with running agents.

| Agent | Port |
| ----- | ---- |
| iris  | 8000 |
| nova  | 8001 |
| kira  | 8002 |

The `/remote` skill derives the session ID automatically from the current Claude Code session. Pass it explicitly only
when you need to target a specific session.
