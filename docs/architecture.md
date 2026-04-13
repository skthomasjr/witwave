# Architecture

Last updated: 2026-04-12

---

## Purpose

This document describes the current architecture of the autonomous agent platform — how the runtime is structured, how
agents are configured and deployed, how they communicate, and how the skill and issue layers are organized. It also
captures known architectural patterns from the competitive landscape and serves as the reference for evaluating large
structural changes.

When a proposed change is architectural in nature — a new runtime primitive, a significant repo restructuring, a new
protocol layer, a shift in deployment model — it should be discussed here first before becoming a `feature` issue.

---

## Repository Structure

```text
.agents/
├── active/                    # Live autonomous agents
│   ├── manifest.json          # Registry of all agents in this deployment
│   ├── iris/
│   │   ├── .nyx/              # Runtime config (mounted into nyx-agent)
│   │   │   ├── AGENTS.md      # Agent-specific behavioral guidance
│   │   │   ├── agent-card.md  # A2A identity description
│   │   │   ├── backend.yaml   # Backend routing config
│   │   │   ├── HEARTBEAT.md   # Proactive heartbeat schedule
│   │   │   ├── jobs/          # Scheduled jobs (*.md, cron frontmatter)
│   │   │   ├── tasks/         # Calendar tasks (*.md, days/window frontmatter)
│   │   │   ├── triggers/      # Inbound HTTP trigger definitions (*.md)
│   │   │   ├── continuations/ # Continuation definitions (*.md)
│   │   │   └── webhooks/      # Outbound webhook subscriptions (*.md)
│   │   ├── .claude/           # Claude Code config
│   │   │   ├── mcp.json
│   │   │   └── settings.json
│   │   ├── .codex/            # Codex config
│   │   │   └── config.toml
│   │   ├── .gemini/           # Gemini backend config (no extra config required)
│   │   ├── logs/              # nyx-agent logs
│   │   ├── a2-claude/         # Claude backend instance
│   │   │   ├── agent.md       # Backend identity (mounted at startup)
│   │   │   ├── logs/          # conversation.jsonl
│   │   │   └── memory/        # Persistent markdown memory files
│   │   ├── a2-codex/          # Codex backend instance
│   │   │   ├── agent.md
│   │   │   ├── logs/
│   │   │   └── memory/
│   │   └── a2-gemini/         # Gemini backend instance
│   │       ├── agent.md
│   │       ├── logs/
│   │       └── memory/        # Includes sessions/ subdir for JSON session history
│   ├── nova/                  # Same structure as iris/
│   └── kira/                  # Same structure as iris/
└── test/                      # Test agents
    ├── manifest.json
    ├── bob/                   # Same structure as active agents
    └── fred/

agent/                         # nyx-agent source (router/scheduler)
├── Dockerfile
├── main.py                    # Entrypoint — wires all components and runs the event loop
├── executor.py                # Routes A2A requests to the configured backend; fires webhooks/continuations
├── bus.py                     # Internal async message bus (deduplication, backpressure)
├── heartbeat.py               # Heartbeat scheduler — drives proactive agent behavior
├── jobs.py                    # Job scheduler — cron-based prompt dispatch
├── tasks.py                   # Task scheduler — calendar-window prompt dispatch
├── triggers.py                # Inbound HTTP trigger handler — serves POST /triggers/{endpoint}
├── continuations.py           # Continuation runner — fires follow-up prompts on upstream completion
├── webhooks.py                # Outbound webhook runner — POSTs to subscribed URLs after prompt completion
├── metrics.py                 # Prometheus metric definitions (agent_* prefix)
├── metrics_proxy.py           # Aggregates backend /metrics with backend= label injection
├── conversations_proxy.py     # Fetches and merges /conversations and /trace from all backends
├── sqlite_task_store.py       # SQLite-backed A2A task store (used when TASK_STORE_PATH is set)
├── utils.py                   # Shared utilities (frontmatter parser, duration parser, etc.)
└── backends/
    ├── base.py                # AgentBackend abstract base class
    ├── a2a.py                 # A2ABackend — forwards requests to a remote A2A agent
    └── config.py              # Backend config loader (backend.yaml)

a2-claude/                     # Claude backend source
├── Dockerfile
├── main.py                    # A2A server entrypoint
├── executor.py                # Claude Agent SDK executor; owns session state and logging
├── metrics.py                 # Prometheus metric definitions (a2_* prefix; superset with tool/MCP metrics)
├── sqlite_task_store.py       # SQLite-backed A2A task store (used when TASK_STORE_PATH is set)
└── requirements.txt

a2-codex/                      # Codex backend source
├── Dockerfile
├── main.py                    # A2A server entrypoint
├── executor.py                # OpenAI Agents SDK executor; owns session state and logging
├── computer.py                # PlaywrightComputer — headless Chromium browser implementation
├── metrics.py                 # Prometheus metric definitions (a2_* prefix)
├── sqlite_task_store.py       # SQLite-backed A2A task store (used when TASK_STORE_PATH is set)
└── requirements.txt

a2-gemini/                     # Gemini backend source
├── Dockerfile
├── main.py                    # A2A server entrypoint
├── executor.py                # google-genai SDK executor; owns session state and logging
├── metrics.py                 # Prometheus metric definitions (a2_* prefix)
├── sqlite_task_store.py       # SQLite-backed A2A task store (used when TASK_STORE_PATH is set)
└── requirements.txt

ui/                            # Web UI

.claude/
└── skills/                    # Local Claude Code skills (user-invokable slash commands)
    ├── develop.md             # Full autonomous development cycle (bugs → risks → gaps)
    ├── docs-refinement.md     # Review and update project documentation
    ├── docs-format.md         # Lint and format markdown documents (leaf skill)
    ├── skill-development.md   # Guide for creating and auditing skills
    ├── bug-discovery.md       # Find bugs → file issues
    ├── bug-refinement.md      # Analyze and order pending bugs
    ├── bug-approval.md        # Approve or defer pending bugs
    ├── bug-fix.md             # Fix approved bugs
    ├── bug-github-issues.md   # GitHub issue operations for bugs (leaf skill)
    ├── risk-discovery.md      # Find risks → file issues
    ├── risk-refinement.md     # Analyze and order pending risks
    ├── risk-approval.md       # Approve or defer pending risks
    ├── risk-fix.md            # Mitigate approved risks
    ├── risk-github-issues.md  # GitHub issue operations for risks (leaf skill)
    ├── gap-discovery.md       # Find gaps → file issues
    ├── gap-refinement.md      # Analyze and order pending gaps
    ├── gap-approval.md        # Approve or defer pending gaps
    ├── gap-fix.md             # Implement approved gaps
    └── gap-github-issues.md   # GitHub issue operations for gaps (leaf skill)

docs/
├── architecture.md            # This document
├── competitive-landscape.md   # Competitor research and gap analysis
├── product-vision.md          # Target audience, design principles, deployment roadmap
└── prompts/                   # Prompt type reference (one file per type)
    ├── README.md              # Index and overview
    ├── heartbeat.md
    ├── jobs.md
    ├── tasks.md
    ├── triggers.md
    ├── continuations.md
    └── webhooks.md

.github/
└── ISSUE_TEMPLATE/
    ├── bug.md                 # Bug report template (label: bug)
    ├── risk.md                # Risk template (label: risk)
    ├── gap.md                 # Gap template (label: gap)
    ├── feature.md             # Feature proposal template (label: feature)
    ├── task.md                # General task template (label: task)
    └── question.md            # Question template

docker-compose.active.yml      # Active environment (iris, nova, kira + backends + ui)
docker-compose.test.yml        # Test environment (bob, fred + backends + ui)
AGENTS.md                      # Canonical repo instructions for all coding agents
CLAUDE.md                      # Claude Code compatibility shim → AGENTS.md
```

---

## Runtime Architecture

### Overview

Each named agent is a cluster of containers:

1. **nyx-agent** — the infrastructure layer. Receives external A2A requests, fires heartbeats, runs jobs/tasks, handles
   inbound triggers, fires outbound webhooks, and dispatches continuations. Owns no LLM itself.
2. **a2-claude** (per agent) — a standalone A2A server backed by the Claude Agent SDK. Owns session state, memory, and
   conversation logging.
3. **a2-codex** (per agent) — a standalone A2A server backed by the OpenAI Agents SDK. Same interface as a2-claude.
4. **a2-gemini** (per agent) — a standalone A2A server backed by the Google Gemini SDK. Same interface as a2-claude.

```text
External A2A caller
        │
        ▼
┌───────────────────────────────────────────┐
│               nyx-agent container          │
│                                           │
│  ┌──────────┐  ┌──────────┐  ┌─────────┐ │
│  │Heartbeat │  │  Agenda  │  │  A2A    │ │
│  │Scheduler │  │Scheduler │  │ Server  │ │
│  └────┬─────┘  └────┬─────┘  └────┬────┘ │
│       │              │             │      │
│       └──────────────┴─────────────┘      │
│                      │                   │
│              ┌───────▼────────┐           │
│              │  Message Bus   │           │
│              └───────┬────────┘           │
│                      │                   │
│              ┌───────▼────────┐           │
│              │   Executor     │           │
│              │ (reads routing)│           │
│              └───────┬────────┘           │
│                      │                   │
│              ┌───────▼────────┐           │
│              │  A2ABackend    │           │
│              │ (HTTP forward) │           │
└──────────────┼────────────────┼───────────┘
               │                │
               ▼                ▼
   ┌──────────────────┐  ┌──────────────────┐
   │  a2-claude       │  │  a2-codex        │
   │  (Claude SDK)    │  │  (OpenAI SDK)    │
   │                  │  │                  │
   │  /.well-known/   │  │  /.well-known/   │
   │  agent.json      │  │  agent.json      │
   │  / (A2A)         │  │  / (A2A)         │
   │  /health         │  │  /health         │
   │  /metrics        │  │  /metrics        │
   └──────────────────┘  └──────────────────┘
```

### nyx-agent Components

**`main.py`** — The entrypoint. Constructs the `MessageBus`, `AgentExecutor`, `HeartbeatRunner`, `JobRunner`,
`TaskRunner`, `TriggerRunner`, `ContinuationRunner`, `WebhookRunner`, and A2A HTTP server, then runs all of them
concurrently via `asyncio.gather`. A `_guarded` wrapper catches crashes in any background task and restarts it with a
delay.

**`bus.py`** — An async `asyncio.Queue`-backed message bus. Deduplicates in-flight messages by `kind` — if a heartbeat
message is already in-flight, a second heartbeat is dropped rather than queued.

**`heartbeat.py`** — Watches `HEARTBEAT.md` for changes via `awatch`. On each heartbeat interval, enqueues a heartbeat
message on the bus. The executor forwards the heartbeat prompt to the backend named in `routing.heartbeat`.

**`jobs.py`** — Reads `*.md` files from the `jobs/` directory. Each file has YAML frontmatter defining a cron
`schedule`. Fires on schedule by enqueuing messages on the bus. Routed via `routing.job`.

**`tasks.py`** — Reads `*.md` files from the `tasks/` directory. Each file has calendar frontmatter (`days`,
`window-start`, `window-duration`, etc.). Fires within the defined window. Routed via `routing.task`.

**`triggers.py`** — Reads `*.md` files from the `triggers/` directory and serves a `POST /triggers/{endpoint}` HTTP
route for each. Dispatches the request payload as a prompt immediately (202 response). Routed via `routing.trigger`.

**`continuations.py`** — Reads `*.md` files from the `continuations/` directory. After any named upstream (job, task,
trigger, a2a, or another continuation) completes, fires a follow-up prompt. Enables prompt chaining. Routed via
`routing.continuation`.

**`webhooks.py`** — Reads `*.md` files from the `webhooks/` directory. After any prompt completes, evaluates all
subscriptions against three filters (`notify-when`, `notify-on-kind`, `notify-on-response`). Fires matching
subscriptions as async fire-and-forget HTTP POST tasks.

**`executor.py`** — Receives `BusMessage` objects from the bus, resolves the target backend from `routing.*`, and calls
`backend.run_query(prompt, session_id, is_new)`. On completion, calls `on_prompt_completed()` which notifies the
`ContinuationRunner` and `WebhookRunner`.

**`backends/a2a.py`** — Implements `AgentBackend.run_query` by constructing an A2A `message/send` JSON-RPC payload and
forwarding it to the backend URL. The backend URL can be overridden per-backend via an environment variable
(`A2A_URL_<ID_UPPERCASED>`), enabling Kubernetes sidecar, separate pod, or Docker Compose deployments without config
file changes.

**`metrics_proxy.py`** — Fetches `/metrics` from each configured backend and injects a `backend="<id>"` label on every
Prometheus sample line. The nyx-agent `/metrics` endpoint merges its own metrics with all backend metrics, providing a
single scrape target for the full deployment.

### Backend Components (a2-claude, a2-codex, a2-gemini)

All three backends share identical structure and API surface; they differ only in their LLM SDK.

**`main.py`** — Builds the A2A `AgentCard` from the mounted `agent.md` file (via `AGENT_MD` env var), wires the
`AgentExecutor` and task store (`SqliteTaskStore` when `TASK_STORE_PATH` is set, `InMemoryTaskStore` otherwise), and
serves the full Starlette application with routes for `/.well-known/agent.json`, `/` (A2A), `/health`, and `/metrics`.

**`executor.py`** — Implements the A2A `AgentExecutor` interface. Manages session continuity using the session ID passed
in the A2A request metadata. Writes `conversation.jsonl` to the mounted logs directory.

**`metrics.py`** — Prometheus metric definitions with `a2_*` prefix. `a2-claude` exposes a superset including tool call,
context window, and MCP metrics; `a2-codex` and `a2-gemini` expose the common `a2_*` set.

---

## Configuration Model

Agent identity and behavior are entirely file-based. No identity is baked into any image.

### nyx-agent config files

| File                 | Location              | Purpose                                                             |
| -------------------- | --------------------- | ------------------------------------------------------------------- |
| `AGENTS.md`          | `.nyx/`               | Behavioral guidance (served as CLAUDE.md and AGENTS.md in backends) |
| `agent-card.md`      | `.nyx/`               | A2A identity — description text served in agent card                |
| `backend.yaml`       | `.nyx/`               | Backend definitions and routing                                     |
| `HEARTBEAT.md`       | `.nyx/`               | Heartbeat schedule and prompt                                       |
| `jobs/*.md`          | `.nyx/jobs/`          | Scheduled jobs — cron frontmatter                                   |
| `tasks/*.md`         | `.nyx/tasks/`         | Calendar tasks — days/window frontmatter                            |
| `triggers/*.md`      | `.nyx/triggers/`      | Inbound HTTP trigger definitions                                    |
| `continuations/*.md` | `.nyx/continuations/` | Continuation definitions — fires on upstream completion             |
| `webhooks/*.md`      | `.nyx/webhooks/`      | Outbound webhook subscriptions                                      |

### Backend config files

| File       | Location                   | Purpose                                               |
| ---------- | -------------------------- | ----------------------------------------------------- |
| `agent.md` | `<name>/a2-claude/`        | Identity injected into the Claude backend at startup  |
| `agent.md` | `<name>/a2-codex/`         | Identity injected into the Codex backend at startup   |
| `agent.md` | `<name>/a2-gemini/`        | Identity injected into the Gemini backend at startup  |
| `memory/`  | `<name>/a2-claude/memory/` | Persistent markdown memory files for Claude backend   |
| `memory/`  | `<name>/a2-codex/memory/`  | Persistent markdown memory files for Codex backend    |
| `memory/`  | `<name>/a2-gemini/memory/` | JSON session history for Gemini backend (`sessions/`) |

### Key environment variables

**nyx-agent:**

| Variable                   | Default                         | Description                                                                                 |
| -------------------------- | ------------------------------- | ------------------------------------------------------------------------------------------- |
| `AGENT_NAME`               | `nyx-agent`                     | Agent display name (e.g. `iris`)                                                            |
| `AGENT_HOST`               | `0.0.0.0`                       | Interface to bind                                                                           |
| `AGENT_PORT`               | `8000`                          | HTTP port                                                                                   |
| `BACKEND_CONFIG_PATH`      | `/home/agent/.nyx/backend.yaml` | Path to backend routing config                                                              |
| `METRICS_ENABLED`          | _(unset)_                       | Enable Prometheus `/metrics`                                                                |
| `METRICS_AUTH_TOKEN`       | _(unset)_                       | Bearer token required to access `/metrics`                                                  |
| `METRICS_CACHE_TTL`        | `15`                            | Seconds to cache aggregated backend metrics between scrapes                                 |
| `CONVERSATIONS_AUTH_TOKEN` | _(unset)_                       | Bearer token required to access `/conversations` and `/trace`                               |
| `PROXY_AUTH_TOKEN`         | _(unset)_                       | Bearer token required to access `/proxy/{agent_name}`                                       |
| `TRIGGERS_AUTH_TOKEN`      | _(unset)_                       | Bearer token for inbound trigger requests (fallback when no per-trigger HMAC secret is set) |
| `CORS_ALLOW_ORIGINS`       | `*`                             | Comma-separated allowed CORS origins; defaults to `*` (logs a warning)                      |
| `TASK_STORE_PATH`          | _(unset)_                       | Path for SQLite A2A task store; defaults to in-memory                                       |
| `WORKER_MAX_RESTARTS`      | `5`                             | Consecutive crash limit before a critical worker marks the agent not-ready                  |
| `A2A_URL_<ID>`             | _(unset)_                       | Per-backend URL override (e.g. `A2A_URL_IRIS_A2_CLAUDE`)                                    |

**Backends (a2-claude / a2-codex / a2-gemini):**

| Variable                   | Default                                | Description                                                                  |
| -------------------------- | -------------------------------------- | ---------------------------------------------------------------------------- |
| `AGENT_NAME`               | `a2-claude` / `a2-codex` / `a2-gemini` | Backend instance name (e.g. `iris-a2-claude`)                                |
| `AGENT_OWNER`              | _(same as `AGENT_NAME`)_               | Named agent this backend belongs to (e.g. `iris`); used in metric labels     |
| `AGENT_ID`                 | `claude` / `codex` / `gemini`          | Backend slot identifier; used in metric labels                               |
| `AGENT_URL`                | `http://localhost:8080/`               | Public A2A endpoint URL reported in agent card                               |
| `AGENT_MD`                 | `/home/agent/agent.md`                 | Path to mounted identity file                                                |
| `BACKEND_PORT`             | `8080`                                 | HTTP port the backend listens on (internal)                                  |
| `METRICS_ENABLED`          | _(unset)_                              | Enable Prometheus `/metrics`                                                 |
| `CONVERSATIONS_AUTH_TOKEN` | _(unset)_                              | Bearer token required to access `/conversations` and `/trace`                |
| `TASK_STORE_PATH`          | _(unset)_                              | Path for SQLite A2A task store; defaults to in-memory                        |
| `WORKER_MAX_RESTARTS`      | `5`                                    | Consecutive crash limit before a critical worker marks the backend not-ready |

---

## Communication Layer

### A2A Protocol

Agents communicate via the A2A protocol (HTTP/JSON-RPC). External callers always target the **nyx agent** by its
hostname/port. nyx reads the `routing.a2a` entry from `backend.yaml` and forwards the request unchanged to the
configured backend. The backend session ID matches the session ID provided by the external caller, preserving
conversation continuity across turns.

Each nyx-agent exposes:

- `/.well-known/agent.json` — agent card for discovery
- `/` — task execution endpoint (`message/send`)

Each backend exposes the same A2A surface plus:

- `/health` — health check endpoint
- `/metrics` — Prometheus metrics endpoint

### Internal Message Bus (nyx-agent)

All internal work — heartbeat ticks, job/task fires, trigger dispatches, continuation fires, and A2A-inbound tasks —
flows through the `MessageBus`. The bus serializes execution: one message processed at a time, deduplicated by kind.
This prevents concurrent outbound backend calls from the same nyx-agent process.

---

## Port Assignments

| Agent       | nyx-agent | a2-claude | a2-codex | a2-gemini |
| ----------- | --------- | --------- | -------- | --------- |
| iris        | 8000      | 8010      | 8011     | 8012      |
| nova        | 8001      | 8020      | 8021     | 8022      |
| kira        | 8002      | 8030      | 8031     | 8032      |
| bob         | 8099      | 8090      | 8091     | 8092      |
| fred        | 8096      | 8086      | —        | —         |
| ui (active) | 3002      | —         | —        | —         |
| ui (test)   | 3001      | —         | —        | —         |

Backend containers all listen on port 8080 internally; host port mappings are as above.

---

## Issue and Skill Layer

### GitHub Issue Taxonomy

| Label     | Created by       | Worked by  | Purpose                                                                    |
| --------- | ---------------- | ---------- | -------------------------------------------------------------------------- |
| `bug`     | `bug-discovery`  | `bug-fix`  | Defect — code that is broken or behaves incorrectly                        |
| `risk`    | `risk-discovery` | `risk-fix` | Code quality issue — works today but fragile, insecure, or likely to break |
| `gap`     | `gap-discovery`  | `gap-fix`  | Missing capability — functionality the system should have but does not     |
| `feature` | humans / agents  | —          | Intentional enhancement requested by stakeholders                          |

### Feature Pipeline

Features are a planned issue type. The feature skill family is not yet built out.

### Develop Loop

The `develop` skill runs a continuous improvement cycle across all issue types:

```text
Phase 1–4:   bug discovery → refinement → approval → fix
Phase 5–8:   risk discovery → refinement → approval → fix
Phase 9–12:  gap discovery → refinement → approval → fix
```

---

## Deployment

### Local

Build all four images and bring up the active environment:

```bash
docker build -f agent/Dockerfile -t nyx-agent:latest .
docker build -f a2-claude/Dockerfile -t a2-claude:latest .
docker build -f a2-codex/Dockerfile -t a2-codex:latest .
docker build -f a2-gemini/Dockerfile -t a2-gemini:latest .
docker compose -f docker-compose.active.yml up -d
```

Port assignments per agent:

| Agent | nyx-agent | a2-claude | a2-codex | a2-gemini |
| ----- | --------- | --------- | -------- | --------- |
| iris  | 8000      | 8010      | 8011     | 8012      |
| nova  | 8001      | 8020      | 8021     | 8022      |
| kira  | 8002      | 8030      | 8031     | 8032      |

### Kubernetes (Target)

All infrastructure decisions are evaluated against Kubernetes compatibility:

- Health probes follow the three-probe model (`/health/start`, `/health/live`, `/health/ready`) for nyx-agent; `/health`
  for backend containers
- Configuration injected via env vars and mounted `ConfigMap`/`Secret` volumes
- Backend URL configurable via `A2A_URL_<ID>` env var — supports sidecar (`http://localhost:8080`), separate pod
  (`http://a2-claude-svc:8080`), or Compose service DNS (`http://iris-a2-claude:8080`) without config file changes
- Stateless containers at the nyx-agent layer (all state lives in backends)
- Standard HTTP endpoints suitable for `Service` and `Ingress`

A Helm chart is planned. A Kubernetes Operator (declarative agent lifecycle via CRDs) is under consideration.

---

## Architectural Patterns

### Patterns in Use

**nyx as pure infrastructure.** nyx-agent owns the scheduling and relay layer; LLM execution is the sole responsibility
of backend containers. This separation allows each layer to evolve independently and enables swapping LLM backends
without touching the scheduler.

**File-based configuration over compiled-in identity.** A new agent is a new directory with mounted files — not a new
image build. The same image serves any number of identities.

**Named routing over round-robin.** `backend.yaml` routes each concern (a2a, heartbeat, job, task, trigger,
continuation) to a named backend id. Routing is deterministic and explicit — no load-balancing or dynamic selection.

**Per-backend URL override.** The `A2A_URL_<ID>` env var allows the same `backend.yaml` config file to work across
Docker Compose, Kubernetes sidecars, and separate pod deployments.

**Message bus serialization.** All work flows through a single async queue per nyx-agent process. Prevents concurrent
outbound backend calls, enforces deduplication, and provides a single instrumentation point for latency and throughput.

**Guarded restart loop.** Every background task (heartbeat, jobs, tasks, triggers, continuations, webhooks, bus worker)
runs inside `_guarded()` — a crash-restart wrapper that logs the failure, increments a metric, and restarts after a
delay. No task can take down the nyx-agent process.

**Skill documents as workflow.** Agent behavior is expressed in markdown skill files, not hardcoded logic. Skills are
hot-swappable without rebuilding the image or restarting the container.

**Theme/slice feature decomposition.** Large features are broken into themes (logical phases) and slices (discrete work
units within a theme). No new theme begins until all slices of the current theme are closed.

### Patterns to Evaluate

The following patterns represent potential architectural directions. Each should be evaluated as an architectural change
proposal before becoming a feature issue:

**Plan-before-code execution mode.** OpenHands v1.5.0 and Devin both enforce a two-phase pattern: read-only planning →
execution. The Claude Agent SDK supports `permission_mode="plan"` natively. Applicable to jobs or tasks with high blast
radius.

**In-process custom tools.** The Claude Agent SDK's `@tool()` decorator and `create_sdk_mcp_server()` factory allow
defining tools as plain Python functions inside the harness process — no external MCP server.

**Programmatic subagent definitions.** `AgentDefinition` in `ClaudeAgentOptions` allows defining specialized subagents
programmatically without file-based configuration.

**Hooks system.** The SDK's `HookMatcher` API registers Python callbacks on `PreToolUse`, `PostToolUse`, `Stop`,
`SessionStart`, etc. `PreToolUse` supports `updatedInput` — rewriting tool arguments before execution.

**Structured shared memory.** Competitors use structured persistent memory with semantic search. This project uses flat
markdown files per backend. SQLite FTS5 with LLM-powered summarization is the strongest reference.

**Auto-generated skills.** Hermes Agent writes a new skill document after completing a complex task — a closed learning
loop from execution to capability accumulation.

**Declarative policy engine.** A file-based policy DSL (JSON/YAML) evaluated before every tool call would add guardrails
without requiring Python code changes.

**Webhook-to-trigger chaining.** Outbound webhooks can POST directly to a nyx-agent trigger endpoint, enabling
self-contained prompt chains without external infrastructure. A completed job response can fire a webhook that triggers
a second prompt on the same or a different agent.

---

## backend.yaml Reference

`backend.yaml` lives in `.nyx/` and controls which backend handles each concern. It has a top-level `backend:` key
containing an `agents:` list and a `routing:` block.

**Minimal single-backend config:**

```yaml
backend:
  agents:
    - id: claude
      url: http://iris-a2-claude:8080

  routing:
    default: claude
```

**Multi-backend config with per-concern routing and model overrides:**

```yaml
backend:
  agents:
    - id: claude
      url: http://iris-a2-claude:8080
      model: claude-opus-4-6

    - id: codex
      url: http://iris-a2-codex:8080
      model: gpt-5.1-codex

    - id: gemini
      url: http://iris-a2-gemini:8080

  routing:
    default:
      agent: claude
      model: claude-opus-4-6
    a2a:
      agent: claude
      model: claude-opus-4-6
    heartbeat:
      agent: claude
      model: claude-opus-4-6
    job:
      agent: claude
      model: claude-opus-4-6
    task:
      agent: claude
      model: claude-opus-4-6
    trigger:
      agent: claude
      model: claude-opus-4-6
    continuation:
      agent: claude
      model: claude-opus-4-6
```

Routing values can be a plain agent ID string (`default: claude`) or an object with `agent:` and optional `model:`
fields. Model resolution order: per-message override → routing entry model → per-backend config model.

The `url` for any backend can be overridden at deploy time via an environment variable named
`A2A_URL_<ID_UPPERCASED_WITH_UNDERSCORES>` — for example, `A2A_URL_IRIS_A2_CLAUDE`. This lets the same `backend.yaml`
work across Docker Compose, Kubernetes, and local sidecar deployments without modification.

---

## Relationship to Other Docs

| Document                                             | Purpose                                                |
| ---------------------------------------------------- | ------------------------------------------------------ |
| [product-vision.md](product-vision.md)               | Target audience, design principles, deployment roadmap |
| [competitive-landscape.md](competitive-landscape.md) | Competitor research, gap analysis, research themes     |
| [prompts/README.md](prompts/README.md)               | Prompt type reference (heartbeat, jobs, tasks, etc.)   |
| `README.md`                                          | Quickstart and technical reference                     |
| `AGENTS.md`                                          | Canonical repo instructions for all coding agents      |
