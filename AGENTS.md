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

- A **nyx-harness** container — the infrastructure layer (A2A relay, heartbeat scheduler, job scheduler). It owns
  no LLM itself; it forwards all work to a backend.
- One or more **backend** containers (`a2-claude`, `a2-codex`, `a2-gemini`) — the LLM execution layer. Each backend is a full A2A
  server that manages its own sessions, memory, conversation logs, and Prometheus metrics.
- Zero or more **MCP** containers (`mcp-kubernetes`, `mcp-helm`, …) — the tool layer. Each MCP server exposes a
  focused set of cluster- or system-level capabilities to backends over the Model Context Protocol. All entries
  under `tools/` are equal MCP components; backends opt into them via their own MCP configuration.

Multiple named agents can collaborate as a team via the A2A protocol, but the named agent (nyx + its backends) is the
deployable unit. MCP components are shared infrastructure — one deployment typically serves every agent in the
cluster rather than being replicated per agent.

## Architecture

### nyx-harness (router / scheduler)

Each named agent runs a containerized instance of the `nyx-harness` image. nyx-harness is the infrastructure layer:

- **A2A relay** — receives external A2A requests and forwards them to the configured backend; returns the backend
  response verbatim.
- **Heartbeat scheduler** — fires on the schedule defined in `HEARTBEAT.md`; dispatches the heartbeat prompt to the
  configured backend.
- **Job scheduler** — reads `jobs/*.md` files with cron frontmatter; dispatches triggered items to the configured
  backend.
- **Task scheduler** — reads `tasks/*.md` files with calendar frontmatter (days, time window, date range); dispatches
  triggered items to the configured backend.
- **Trigger handler** — serves `POST /triggers/{endpoint}` HTTP endpoints defined in `triggers/*.md` files; dispatches
  the request payload as a prompt to the configured backend and returns 202 immediately.
- **Continuation runner** — reads `continuations/*.md` files; fires a follow-up prompt whenever a named upstream
  (job, task, trigger, a2a, or another continuation) completes, enabling prompt chaining without hardcoded sequences.
- **Router** — reads `backend.yaml` to decide which named backend handles each concern (a2a, heartbeat, job, task, trigger, continuation).

nyx-harness retains no LLM of its own. All conversation state, session continuity, memory, and conversation logging
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
- Manages its own session state, conversation log (`conversation.jsonl`), and memory (`/memory/`)
- Receives behavioral instructions via a mounted file (`CLAUDE.md` for a2-claude, `AGENTS.md` for a2-codex, `GEMINI.md` for a2-gemini) and A2A identity via a mounted `agent-card.md`

Each named agent has its own dedicated backend instances. For example, iris has `iris-a2-claude`, `iris-a2-codex`, and `iris-a2-gemini`.

### MCP components

Tool capabilities are delivered as MCP servers. Every subdirectory under `tools/` is an MCP component and is
treated equally regardless of what it wraps. Current MCP components:

- **`mcp-kubernetes`** — Kubernetes API access via the official Python client. Source in `tools/kubernetes/`.
  Image: `mcp-kubernetes:latest`.
- **`mcp-helm`** — Helm release management via the `helm` CLI (Helm has no Python/REST API). Source in
  `tools/helm/`. Image: `mcp-helm:latest`.

Each MCP component:

- Speaks the Model Context Protocol (not A2A) and is consumed by backends via their MCP configuration
  (`mcp.json` for a2-claude, `config.toml` for a2-codex, equivalent for a2-gemini).
- Targets only the cluster where it is deployed; auth is in-cluster ServiceAccount + RBAC, not arbitrary
  kubeconfigs.
- Is independently deployable and typically **shared across all agents** in a cluster rather than replicated
  per-agent. Agents opt into an MCP component by referencing it from their backend config.

Add a new MCP component by creating `tools/<name>/` with a `Dockerfile`, `server.py`, and `requirements.txt`,
then register a `mcp-<name>:latest` build in the [Building Images](#building-images) section. Tag related
issues/PRs with the `mcp` GitHub label.

### Routing configuration

`backend.yaml` (in `.nyx/`) controls which backend handles each concern:

```yaml
backend:
  agents:
    - id: iris-a2-claude
      url: http://iris-a2-claude:8080
      model: claude-opus-4-6

    - id: iris-a2-codex
      url: http://iris-a2-codex:8080
      model: gpt-5.1-codex

    - id: iris-a2-gemini
      url: http://iris-a2-gemini:8080

  routing:
    default:
      agent: iris-a2-claude
      model: claude-opus-4-6
    a2a:
      agent: iris-a2-claude
      model: claude-opus-4-6
    heartbeat:
      agent: iris-a2-claude
      model: claude-opus-4-6
    job:
      agent: iris-a2-claude
      model: claude-opus-4-6
    task:
      agent: iris-a2-claude
      model: claude-opus-4-6
    trigger:
      agent: iris-a2-claude
      model: claude-opus-4-6
    continuation:
      agent: iris-a2-claude
      model: claude-opus-4-6
```

Routing values can be a plain agent ID string or an object with `agent:` and optional `model:` fields.
Model resolution order: per-message override → routing entry model → per-backend config model.

The `url` field can be overridden at deploy time via the environment variable
`A2A_URL_<ID_UPPERCASED_WITH_UNDERSCORES>` (e.g. `A2A_URL_IRIS_A2_CLAUDE`). This enables the same config file to
work with Docker Compose service DNS, Kubernetes service DNS, or localhost sidecars without modification.

### Agent configuration layout

Agent identity and behavior are file-based — nothing is baked into images.

```text
.agents/active/<name>/
├── agent-card.md            # A2A identity description text (mounted into all containers at /home/agent/agent-card.md)
├── .nyx/                    # Runtime config (mounted into nyx-harness)
│   ├── backend.yaml         # Backend selection and routing
│   ├── HEARTBEAT.md         # Proactive heartbeat schedule and prompt
│   ├── jobs/                # Scheduled job definitions (*.md with cron frontmatter)
│   ├── tasks/               # Scheduled task definitions (*.md with calendar frontmatter)
│   ├── triggers/            # Inbound HTTP trigger definitions (*.md with endpoint frontmatter)
│   ├── continuations/       # Continuation definitions (*.md with continues-after frontmatter)
│   └── webhooks/            # Outbound webhook subscriptions (*.md with url frontmatter)
├── .claude/                 # Claude backend config (mounted into a2-claude)
│   ├── CLAUDE.md            # Behavioral instructions / system prompt
│   ├── agent-card.md        # A2A identity description text
│   ├── mcp.json             # MCP server configuration
│   ├── hooks.yaml           # Optional PreToolUse/PostToolUse extension rules (#467)
│   ├── settings.json        # Claude Code settings
│   └── skills/              # Skill definitions (*.md)
├── .codex/                  # Codex backend config (mounted into a2-codex)
│   ├── AGENTS.md            # Behavioral instructions / system prompt
│   ├── agent-card.md        # A2A identity description text
│   └── config.toml
├── .gemini/                 # Gemini backend config (mounted into a2-gemini)
│   ├── GEMINI.md            # Behavioral instructions / system prompt
│   └── agent-card.md        # A2A identity description text
├── logs/                    # nyx-harness logs (runtime, not committed)
├── a2-claude/               # Claude backend instance for this agent
│   ├── logs/                # Backend conversation log + trace.jsonl + tool-audit.jsonl (runtime, not committed)
│   └── memory/              # Backend persistent memory (runtime, not committed)
├── a2-codex/                # Codex backend instance for this agent
│   ├── logs/
│   └── memory/
└── a2-gemini/               # Gemini backend instance for this agent
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

harness/                     # nyx-harness source (router/scheduler)
├── Dockerfile
├── main.py                  # A2A server entrypoint
├── executor.py              # Routes A2A requests to configured backend
├── bus.py                   # Internal async message bus (carries trace_context)
├── heartbeat.py             # Heartbeat scheduler
├── jobs.py                  # Job scheduler
├── tasks.py                 # Task scheduler
├── triggers.py              # Inbound HTTP trigger handler
├── continuations.py         # Continuation runner (fires on upstream completion)
├── webhooks.py              # Outbound webhook delivery (stamps traceparent + OTel span)
├── tracing.py               # W3C trace-context helpers + OTel re-exports (#468, #469)
├── metrics.py               # Prometheus metrics definitions
├── utils.py                 # Shared utilities (frontmatter parser, duration parser, etc.)
└── backends/
    ├── base.py              # AgentBackend abstract base class
    ├── a2a.py               # A2ABackend — forwards requests to remote A2A backend
    └── config.py            # Backend config loader (backend.yaml)

a2-claude/                   # Claude backend source
├── Dockerfile
├── main.py                  # A2A server entrypoint
├── executor.py              # Claude Agent SDK executor; owns sessions, logging, hooks (#467)
├── hooks.py                 # PreToolUse/PostToolUse policy engine + baseline deny rules (#467)
├── metrics.py               # Prometheus metrics (superset of a2-codex/a2-gemini; adds tool, context, MCP, hooks metrics)
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

tools/                       # MCP components (one directory per server)
├── kubernetes/              # mcp-kubernetes — Kubernetes API via Python client
│   ├── Dockerfile
│   ├── server.py
│   └── requirements.txt
└── helm/                    # mcp-helm — Helm release management via the CLI
    ├── Dockerfile
    ├── server.py
    └── requirements.txt

ui/                          # Web UI — legacy single-file app (currently primary)
dashboard/                   # Vue 3 + Vite + PrimeVue dashboard — future replacement for ui/ (#470)
charts/                      # Helm charts
├── nyx/                     # nyx Helm chart (deploys agents to Kubernetes)
└── nyx-operator/            # nyx-operator Helm chart (deploys the NyxAgent controller)
operator/                    # Kubernetes operator (Go) — reconciles NyxAgent CRDs
shared/                      # Shared Python modules mounted into harness + backends
                             #   otel.py      — OpenTelemetry bootstrap and helpers (#469)
                             #   log_utils.py — structured log append helpers
                             #   exceptions.py, conversations.py
```

## Building Images

```bash
# nyx-harness (router/scheduler)
docker build -f harness/Dockerfile -t nyx-harness:latest .

# Claude backend
docker build -f a2-claude/Dockerfile -t a2-claude:latest .

# Codex backend
docker build -f a2-codex/Dockerfile -t a2-codex:latest .

# Gemini backend
docker build -f a2-gemini/Dockerfile -t a2-gemini:latest .

# Kubernetes MCP tool
docker build -f tools/kubernetes/Dockerfile -t mcp-kubernetes:latest .

# Helm MCP tool
docker build -f tools/helm/Dockerfile -t mcp-helm:latest .

# Dashboard — future UI, coexists with ui/ (#470). Optional; only needed when dashboard.enabled=true.
docker build -f dashboard/Dockerfile -t dashboard:latest .
```

## Running Locally

```bash
docker build -f harness/Dockerfile -t nyx-harness:latest . \
  && docker build -f a2-claude/Dockerfile -t a2-claude:latest . \
  && docker build -f a2-codex/Dockerfile -t a2-codex:latest . \
  && docker build -f a2-gemini/Dockerfile -t a2-gemini:latest . \
  && docker build -f tools/kubernetes/Dockerfile -t mcp-kubernetes:latest . \
  && docker build -f tools/helm/Dockerfile -t mcp-helm:latest . \
  && helm upgrade --install nyx ./charts/nyx -f ./charts/nyx/values-test.yaml -n nyx --create-namespace
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
`.agents/active/iris/a2-claude/memory/`). For `a2-claude` and `a2-codex`, memory files are markdown documents. For `a2-gemini`, conversation history is stored as JSON in `memory/sessions/`. Memory files are not committed to source control. nyx-harness has no memory layer of its own.
