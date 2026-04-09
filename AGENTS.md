# AGENTS.md

This file provides guidance to Claude Code (https://claude.ai/code) and Codex (https://openai.com/codex/) when working
with code in this repository.

## Repo Root

The repo root is referred to as `<repo-root>`. For this environment, `<repo-root>` is the directory containing this
file.

## Skills

Skills under `.claude/skills/` are mounted directly into each backend container — `a2-claude` at
`/home/agent/.claude/skills/` and `a2-codex` at `/home/agent/.codex/skills/`. Agents always have the same skills as
the local Claude Code session — no per-agent copying required.

## Agent Identity

The acting agent is referred to as `<agent-name>`. For containerized workers, `<agent-name>` is the value of the
`AGENT_NAME` environment variable (e.g. `iris`, `nova`, `kira`). When running as a local session (Claude Code, Codex,
or otherwise), `AGENT_NAME` is not set — in that case, `<agent-name>` is `local-agent`.

## Working with Claude Code and Codex

- Do not run `git commit` unless explicitly asked.
- Do not run `git push` unless explicitly asked.

## Project Overview

autonomous-agent is a multi-container autonomous agent platform. Each named agent (iris, nova, kira, …) consists of:

- A **nyx-agent** container — the infrastructure layer (A2A relay, heartbeat scheduler, job scheduler). It owns
  no LLM itself; it forwards all work to a backend.
- One or more **backend** containers (`a2-claude`, `a2-codex`, `a2-gemini`) — the LLM execution layer. Each backend is a full A2A
  server that manages its own sessions, memory, conversation logs, and Prometheus metrics.

Multiple named agents can collaborate as a team via the A2A protocol, but the named agent (nyx + its backends) is the
deployable unit.

## Architecture

### nyx-agent (router / scheduler)

Each named agent runs a containerized instance of the `nyx-agent` image. nyx-agent is the infrastructure layer:

- **A2A relay** — receives external A2A requests and forwards them to the configured backend; returns the backend
  response verbatim.
- **Heartbeat scheduler** — fires on the schedule defined in `HEARTBEAT.md`; dispatches the heartbeat prompt to the
  configured backend.
- **Job scheduler** — reads `jobs/*.md` files with cron frontmatter; dispatches triggered items to the configured
  backend.
- **Task scheduler** — reads `tasks/*.md` files with calendar frontmatter (days, time window, date range); dispatches
  triggered items to the configured backend.
- **Router** — reads `backends.yaml` to decide which named backend handles each concern (a2a, heartbeat, job, task).

nyx-agent retains no LLM of its own. All conversation state, session continuity, memory, and conversation logging
live in the backend container.

### Backend containers

Three backend types exist, each implemented as a standalone A2A server:

- **`a2-claude`** — Claude Agent SDK backend. Source in `a2-claude/`. Image: `a2-claude:latest`.
- **`a2-codex`** — OpenAI Agents SDK (Codex) backend. Source in `a2-codex/`. Image: `a2-codex:latest`.
- **`a2-gemini`** — Google Gemini backend (google-genai SDK). Source in `a2-gemini/`. Image: `a2-gemini:latest`.

Each backend:

- Exposes `/.well-known/agent.json` for A2A discovery
- Exposes `/` as the A2A JSON-RPC task endpoint
- Exposes `/health` for health checks
- Exposes `/metrics` for Prometheus scraping (when `METRICS_ENABLED` is set)
- Manages its own session state, conversation log (`conversation.log`), and memory (`/memory/`)
- Receives identity via a mounted `agent.md` file (equivalent to `CLAUDE.md`)

Each named agent has its own dedicated backend instances. For example, iris has `iris-a2-claude`, `iris-a2-codex`, and `iris-a2-gemini`.

### Routing configuration

`backends.yaml` (in `.nyx/`) controls which backend handles each concern:

```yaml
backends:
  - id: iris-a2-claude
    type: a2a
    url: http://iris-a2-claude:8080

  - id: iris-a2-codex
    type: a2a
    url: http://iris-a2-codex:8080

  - id: iris-a2-gemini
    type: a2a
    url: http://iris-a2-gemini:8080

routing:
  default: iris-a2-claude    # fallback backend when no per-concern override matches
  a2a: iris-a2-claude        # handles incoming A2A requests
  heartbeat: iris-a2-claude  # handles heartbeat-triggered work
  job: iris-a2-claude        # handles job execution
  task: iris-a2-claude       # handles task execution
```

The `url` field can be overridden at deploy time via the environment variable
`A2A_URL_<ID_UPPERCASED_WITH_UNDERSCORES>` (e.g. `A2A_URL_IRIS_A2_CLAUDE`). This enables the same config file to
work with Docker Compose service DNS, Kubernetes service DNS, or localhost sidecars without modification.

### Agent configuration layout

Agent identity and behavior are file-based — nothing is baked into images.

```text
.agents/active/<name>/
├── .nyx/                    # Runtime config (mounted into nyx-agent)
│   ├── AGENTS.md            # Agent-specific behavioral guidance (mounted as CLAUDE.md and AGENTS.md in backends)
│   ├── agent-card.md        # A2A identity description text
│   ├── backends.yaml        # Backend selection and routing
│   ├── HEARTBEAT.md         # Proactive heartbeat schedule and prompt
│   ├── jobs/                # Scheduled job definitions (*.md with cron frontmatter)
│   └── tasks/               # Scheduled task definitions (*.md with calendar frontmatter)
├── .claude/                 # Claude backend config (mounted into a2-claude)
│   ├── mcp.json             # MCP server configuration
│   └── settings.json        # Claude Code settings
├── .codex/                  # Codex backend config (mounted into a2-codex)
│   └── config.toml
├── .gemini/                 # Gemini backend config (mounted into a2-gemini; no extra config required)
├── logs/                    # nyx-agent logs (runtime, not committed)
├── a2-claude/               # Claude backend instance for this agent
│   ├── agent.md             # Backend identity (injected at startup)
│   ├── logs/                # Backend conversation log (runtime, not committed)
│   └── memory/              # Backend persistent memory (runtime, not committed)
├── a2-codex/                # Codex backend instance for this agent
│   ├── agent.md
│   ├── logs/
│   └── memory/
└── a2-gemini/               # Gemini backend instance for this agent
    ├── agent.md
    ├── logs/
    └── memory/              # Includes sessions/ subdir for JSON session history
```

## Project Structure

```text
.agents/
├── active/                  # Active (production-like) agents: iris, nova, kira
│   ├── manifest.json        # Registry of all agents in this deployment
│   └── <name>/              # Per-agent directory (see layout above)
└── test/                    # Test agents: bob
    ├── manifest.json
    └── <name>/

agent/                       # nyx-agent source (router/scheduler)
├── Dockerfile
├── main.py                  # A2A server entrypoint
├── executor.py              # Routes A2A requests to configured backend
├── bus.py                   # Internal async message bus
├── heartbeat.py             # Heartbeat scheduler
├── jobs.py                  # Job scheduler
├── tasks.py                 # Task scheduler
├── metrics.py               # Prometheus metrics definitions
├── utils.py                 # Shared utilities (frontmatter parser, etc.)
└── backends/
    ├── base.py              # AgentBackend abstract base class
    ├── a2a.py               # A2ABackend — forwards requests to remote A2A backend
    └── config.py            # Backend config loader (supports type: a2a)

a2-claude/                   # Claude backend source
├── Dockerfile
├── main.py                  # A2A server entrypoint
├── executor.py              # Claude Agent SDK executor; owns sessions and logging
├── metrics.py               # Prometheus metrics (superset of a2-codex/a2-gemini; adds tool, context, MCP metrics)
└── requirements.txt

a2-codex/                    # Codex backend source
├── Dockerfile
├── main.py                  # A2A server entrypoint
├── executor.py              # OpenAI Agents SDK executor; owns sessions and logging
├── metrics.py               # Prometheus metrics (common a2_* set; subset of a2-claude)
└── requirements.txt

a2-gemini/                   # Gemini backend source
├── Dockerfile
├── main.py                  # A2A server entrypoint
├── executor.py              # google-genai SDK executor; owns sessions and logging
├── metrics.py               # Prometheus metrics (common a2_* set; subset of a2-claude)
└── requirements.txt

ui/                          # Web UI
docker-compose.active.yml    # Active environment (iris, nova, kira + backends + ui)
docker-compose.test.yml      # Test environment (bob + backends + ui)
```

## Building Images

```bash
# nyx-agent (router/scheduler)
docker build -f agent/Dockerfile -t nyx-agent:latest .

# Claude backend
docker build -f a2-claude/Dockerfile -t a2-claude:latest .

# Codex backend
docker build -f a2-codex/Dockerfile -t a2-codex:latest .

# Gemini backend
docker build -f a2-gemini/Dockerfile -t a2-gemini:latest .
```

## Running Locally

```bash
docker build -f agent/Dockerfile -t nyx-agent:latest . \
  && docker build -f a2-claude/Dockerfile -t a2-claude:latest . \
  && docker build -f a2-codex/Dockerfile -t a2-codex:latest . \
  && docker build -f a2-gemini/Dockerfile -t a2-gemini:latest . \
  && docker compose -f docker-compose.active.yml up -d
```

## Interacting with Agents

Use the `/remote` skill to interact with running agents. Always target the **nyx agent by name** — nyx routes the
request internally to its configured backend (e.g. `iris-a2-claude`). Never target backend services directly.

| Agent | Port | a2-claude | a2-codex | a2-gemini |
| ----- | ---- | --------- | -------- | --------- |
| iris  | 8000 | 8010      | 8011     | 8012      |
| nova  | 8001 | 8020      | 8021     | 8022      |
| kira  | 8002 | 8030      | 8031     | 8032      |
| bob   | 8099 | 8090      | 8091     | 8092      |

The `/remote` skill derives the session ID automatically from the current Claude Code session. Pass it explicitly only
when you need to target a specific session.

## Memory

Each backend manages its own memory under `.agents/<env>/<name>/<backend>/memory/` (e.g.
`.agents/active/iris/a2-claude/memory/`). For `a2-claude` and `a2-codex`, memory files are markdown documents. For `a2-gemini`, conversation history is stored as JSON in `memory/sessions/`. Memory files are not committed to source control. nyx-agent has no memory layer of its own.
