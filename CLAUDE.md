# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repo Root

The repo root is the directory containing this file.

## Project Overview

autonomous-agent is an autonomous agent built on the Claude Agent SDK — persistent, self-directed, with its own
identity, memory, schedule, and the ability to communicate with other agents and humans. Multiple agents can collaborate
as a team, but the agent itself is the unit.

## Architecture

Each agentic worker runs as a containerized instance of the `claude-agent` image. Workers are configured via mounted
files and environment variables — no identity or behavior is baked into the image itself.

- **A2A protocol** — primary communication layer (HTTP/JSON-RPC). Each agent exposes `/.well-known/agent.json` for
  discovery and `/` for task execution.
- **Agent configuration** — defined under `.agents/<name>/`. Includes `agent.md` (A2A identity), `.claude/CLAUDE.md`
  (behavioral config), `HEARTBEAT.md` (proactive schedule), and `agenda/` (scheduled work items).
- **Conversation logging** — each agent writes a `conversation.log` to `.agents/<name>/logs/`.

## Project Structure

```text
.agents/
├── iris/               # Iris agent
├── nova/               # Nova agent
└── kira/               # Kira agent
agent/
├── main.py             # A2A server entrypoint
├── executor.py         # Bridges A2A and Claude Agent SDK
├── bus.py              # Internal message bus
├── heartbeat.py        # Heartbeat scheduler
├── agenda.py           # Agenda scheduler
├── metrics.py          # Prometheus metrics definitions
└── utils.py            # Shared utilities (e.g. frontmatter parser)
docker-compose.yml      # Runs all agents locally
Dockerfile              # claude-agent image
```

## Running Locally

```bash
docker build -t claude-agent:latest .
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
