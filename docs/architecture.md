# Architecture

Last updated: 2026-04-16

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
│   │   ├── .nyx/              # Runtime config (mounted into nyx-harness)
│   │   │   ├── agent-card.md  # A2A identity description (nyx agent card)
│   │   │   ├── backend.yaml   # Backend routing config
│   │   │   ├── HEARTBEAT.md   # Proactive heartbeat schedule
│   │   │   ├── jobs/          # Scheduled jobs (*.md, cron frontmatter)
│   │   │   ├── tasks/         # Calendar tasks (*.md, days/window frontmatter)
│   │   │   ├── triggers/      # Inbound HTTP trigger definitions (*.md)
│   │   │   ├── continuations/ # Continuation definitions (*.md)
│   │   │   └── webhooks/      # Outbound webhook subscriptions (*.md)
│   │   ├── .claude/           # Claude backend config (mounted into a2-claude)
│   │   │   ├── CLAUDE.md      # Behavioral instructions / system prompt
│   │   │   ├── agent-card.md  # A2A identity description (Claude backend)
│   │   │   ├── mcp.json       # MCP server configuration
│   │   │   └── settings.json  # Claude Code settings
│   │   ├── .codex/            # Codex backend config (mounted into a2-codex)
│   │   │   ├── AGENTS.md      # Behavioral instructions / system prompt
│   │   │   ├── agent-card.md  # A2A identity description (Codex backend)
│   │   │   └── config.toml
│   │   ├── .gemini/           # Gemini backend config (mounted into a2-gemini)
│   │   │   ├── GEMINI.md      # Behavioral instructions / system prompt
│   │   │   └── agent-card.md  # A2A identity description (Gemini backend)
│   │   ├── logs/              # nyx-harness logs
│   │   ├── a2-claude/         # Claude backend instance
│   │   │   ├── logs/          # conversation.jsonl
│   │   │   └── memory/        # Persistent markdown memory files
│   │   ├── a2-codex/          # Codex backend instance
│   │   │   ├── logs/
│   │   │   └── memory/
│   │   └── a2-gemini/         # Gemini backend instance
│   │       ├── logs/
│   │       └── memory/        # Includes sessions/ subdir for JSON session history
│   ├── nova/                  # Same structure as iris/
│   └── kira/                  # Same structure as iris/
└── test/                      # Test agents
    ├── manifest.json
    ├── bob/                   # Same structure as active agents
    └── fred/

harness/                       # nyx-harness source (router/scheduler)
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

backends/a2-claude/                     # Claude backend source
├── Dockerfile
├── main.py                    # A2A server entrypoint
├── executor.py                # Claude Agent SDK executor; owns session state and logging
├── metrics.py                 # Prometheus metric definitions (a2_* prefix; superset with tool/MCP metrics)
├── sqlite_task_store.py       # SQLite-backed A2A task store (used when TASK_STORE_PATH is set)
└── requirements.txt

backends/a2-codex/                      # Codex backend source
├── Dockerfile
├── main.py                    # A2A server entrypoint
├── executor.py                # OpenAI Agents SDK executor; owns session state and logging
├── computer.py                # PlaywrightComputer — headless Chromium browser implementation
├── metrics.py                 # Prometheus metric definitions (a2_* prefix)
├── sqlite_task_store.py       # SQLite-backed A2A task store (used when TASK_STORE_PATH is set)
└── requirements.txt

backends/a2-gemini/                     # Gemini backend source
├── Dockerfile
├── main.py                    # A2A server entrypoint
├── executor.py                # google-genai SDK executor; owns session state and logging
├── metrics.py                 # Prometheus metric definitions (a2_* prefix)
├── sqlite_task_store.py       # SQLite-backed A2A task store (used when TASK_STORE_PATH is set)
└── requirements.txt

dashboard/                     # Vue 3 + Vite + PrimeVue web interface

operator/                      # Go/Kubebuilder Kubernetes operator

git-sync/                      # Internal git-sync image (upstream git-sync + rsync for correct incremental sync)
└── Dockerfile                 # Adds rsync to the upstream git-sync image

.claude/
└── skills/                    # Local Claude Code skills (user-invokable slash commands)
    ├── develop.md             # Full autonomous development cycle (bugs → risks → gaps → features → docs)
    ├── docs-refine.md         # Review and update project documentation
    ├── docs-format.md         # Lint and format markdown documents (leaf skill)
    ├── skill-development.md   # Guide for creating and auditing skills
    ├── bug-discover.md        # Find bugs → file issues
    ├── bug-refine.md          # Analyze and order pending bugs
    ├── bug-approve.md         # Approve or defer pending bugs
    ├── bug-implement.md       # Fix approved bugs
    ├── bug-github-issues.md   # GitHub issue operations for bugs (leaf skill)
    ├── risk-discover.md       # Find risks → file issues
    ├── risk-refine.md         # Analyze and order pending risks
    ├── risk-approve.md        # Approve or defer pending risks
    ├── risk-implement.md      # Mitigate approved risks
    ├── risk-github-issues.md  # GitHub issue operations for risks (leaf skill)
    ├── gap-discover.md        # Find gaps → file issues
    ├── gap-refine.md          # Analyze and order pending gaps
    ├── gap-approve.md         # Approve or defer pending gaps
    ├── gap-implement.md       # Implement approved gaps
    ├── gap-github-issues.md   # GitHub issue operations for gaps (leaf skill)
    ├── feature-discover.md    # Derive features from ready requests
    ├── feature-refine.md      # Analyze and order pending features
    ├── feature-approve.md     # Approve or defer pending features
    ├── feature-implement.md   # Implement approved features
    ├── feature-github-issues.md # GitHub issue operations for features (leaf skill)
    ├── request-dialog.md      # Conversational request intake
    ├── request-discover.md    # Find open requests
    └── request-github-issues.md # GitHub issue operations for requests (leaf skill)

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

charts/                        # Helm charts
├── nyx/                       # nyx Helm chart (deploys agents to Kubernetes)
└── nyx-operator/              # Helm chart for the Kubernetes operator
AGENTS.md                      # Canonical repo instructions for all coding agents
CLAUDE.md                      # Claude Code compatibility shim → AGENTS.md
```

---

## Runtime Architecture

### Overview

Each named agent is a cluster of containers:

1. **nyx-harness** — the infrastructure layer. Receives external A2A requests, fires heartbeats, runs jobs/tasks,
   handles inbound triggers, fires outbound webhooks, and dispatches continuations. Owns no LLM itself.
2. **a2-claude** (per agent) — a standalone A2A server backed by the Claude Agent SDK. Owns session state, memory, and
   conversation logging.
3. **a2-codex** (per agent) — a standalone A2A server backed by the OpenAI Agents SDK. Same interface as a2-claude.
4. **a2-gemini** (per agent) — a standalone A2A server backed by the Google Gemini SDK. Same interface as a2-claude.

```text
External A2A caller
        │
        ▼
┌───────────────────────────────────────────┐
│               nyx-harness container          │
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

### nyx-harness Components

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
`backend.run_query(prompt, session_id, is_new)`. When `message.consensus` is a non-empty list of `ConsensusEntry`
objects, fans out to each matched `(backend, model)` pair in parallel and aggregates the responses (majority vote for
binary yes/no answers; synthesis pass via the default backend for freeform responses). The same backend can be targeted
twice with different models — each `(backend, model)` pair is a distinct call. On completion, calls
`on_prompt_completed()` which notifies the `ContinuationRunner` and `WebhookRunner`.

**`backends/a2a.py`** — Implements `AgentBackend.run_query` by constructing an A2A `message/send` JSON-RPC payload and
forwarding it to the backend URL. Retries transient errors (HTTP 429/502/503/504 and connection failures) up to
`A2A_BACKEND_MAX_RETRIES` times (default 3, must be >= 1) with exponential backoff. The backend URL can be overridden
per-backend via an environment variable (`A2A_URL_<ID_UPPERCASED>`), enabling Kubernetes sidecar, separate pod, or
Docker Compose deployments without config file changes.

**`metrics_proxy.py`** — Fetches `/metrics` from each configured backend and injects a `backend="<id>"` label on every
Prometheus sample line. The nyx-harness `/metrics` endpoint merges its own metrics with all backend metrics, providing a
single scrape target for the full deployment.

### Backend Components (a2-claude, a2-codex, a2-gemini)

All three backends share identical structure and API surface; they differ only in their LLM SDK.

**`main.py`** — Builds the A2A `AgentCard` from the mounted `agent-card.md` file, wires the `AgentExecutor` and task
store (`SqliteTaskStore` when `TASK_STORE_PATH` is set, `InMemoryTaskStore` otherwise), and serves the full Starlette
application with routes for `/.well-known/agent.json`, `/` (A2A), `/health`, `/metrics`, and `/mcp` (MCP JSON-RPC
server).

**`executor.py`** — Implements the A2A `AgentExecutor` interface. Manages session continuity using the session ID passed
in the A2A request metadata. Writes `conversation.jsonl` to the mounted logs directory.

**`metrics.py`** — Prometheus metric definitions with `a2_*` prefix. `a2-claude` exposes a superset including tool call,
context window, and MCP metrics; `a2-codex` also exposes tool-call and context-window metrics; `a2-gemini` exposes
context-window metrics. All three share the common `a2_*` baseline set.

---

## Configuration Model

Agent identity and behavior are entirely file-based. No identity is baked into any image.

### nyx-harness config files

| File                 | Location              | Purpose                                                 |
| -------------------- | --------------------- | ------------------------------------------------------- |
| `agent-card.md`      | `.nyx/`               | A2A identity description for the nyx-harness agent card |
| `backend.yaml`       | `.nyx/`               | Backend definitions and routing                         |
| `HEARTBEAT.md`       | `.nyx/`               | Heartbeat schedule and prompt                           |
| `jobs/*.md`          | `.nyx/jobs/`          | Scheduled jobs — cron frontmatter                       |
| `tasks/*.md`         | `.nyx/tasks/`         | Calendar tasks — days/window frontmatter                |
| `triggers/*.md`      | `.nyx/triggers/`      | Inbound HTTP trigger definitions                        |
| `continuations/*.md` | `.nyx/continuations/` | Continuation definitions — fires on upstream completion |
| `webhooks/*.md`      | `.nyx/webhooks/`      | Outbound webhook subscriptions                          |

### Backend config files

| File            | Location                   | Purpose                                                             |
| --------------- | -------------------------- | ------------------------------------------------------------------- |
| `agent-card.md` | `/home/agent/.claude/`     | A2A identity (agent card description) for the Claude backend        |
| `agent-card.md` | `/home/agent/.codex/`      | A2A identity (agent card description) for the Codex backend         |
| `agent-card.md` | `/home/agent/.gemini/`     | A2A identity (agent card description) for the Gemini backend        |
| `CLAUDE.md`     | `/home/agent/.claude/`     | Behavioral instructions injected into the Claude backend at startup |
| `AGENTS.md`     | `/home/agent/.codex/`      | Behavioral instructions injected into the Codex backend at startup  |
| `GEMINI.md`     | `/home/agent/.gemini/`     | Behavioral instructions injected into the Gemini backend at startup |
| `memory/`       | `<name>/a2-claude/memory/` | Persistent markdown memory files for Claude backend                 |
| `memory/`       | `<name>/a2-codex/memory/`  | Persistent markdown memory files for Codex backend                  |
| `memory/`       | `<name>/a2-gemini/memory/` | JSON session history for Gemini backend (`sessions/`)               |

### Key environment variables

**nyx-harness:**

| Variable                                    | Default                         | Description                                                                                                                    |
| ------------------------------------------- | ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `AGENT_NAME`                                | `nyx`                           | Agent display name (e.g. `iris`)                                                                                               |
| `AGENT_HOST`                                | `0.0.0.0`                       | Interface to bind                                                                                                              |
| `AGENT_PORT`                                | `8000`                          | HTTP port                                                                                                                      |
| `BACKEND_CONFIG_PATH`                       | `/home/agent/.nyx/backend.yaml` | Path to backend routing config                                                                                                 |
| `METRICS_ENABLED`                           | _(unset)_                       | Enable Prometheus `/metrics`                                                                                                   |
| `METRICS_AUTH_TOKEN`                        | _(unset)_                       | Bearer token required to access `/metrics`                                                                                     |
| `METRICS_CACHE_TTL`                         | `15`                            | Seconds to cache aggregated backend metrics between scrapes                                                                    |
| `CONVERSATIONS_AUTH_TOKEN`                  | _(unset)_                       | Bearer token required to access `/conversations` and `/trace` (inbound)                                                        |
| `BACKEND_CONVERSATIONS_AUTH_TOKEN`          | _(unset)_                       | Bearer token forwarded to backend `/conversations` and `/trace` endpoints (set if backends require auth)                       |
| `TRIGGERS_AUTH_TOKEN`                       | _(unset)_                       | Bearer token for inbound trigger requests (fallback when no per-trigger HMAC secret is set)                                    |
| `CORS_ALLOW_ORIGINS`                        | _(unset)_                       | Comma-separated allowed CORS origins; when unset, all cross-origin requests are denied (logs a warning)                        |
| `TASK_STORE_PATH`                           | _(unset)_                       | Path for SQLite A2A task store; defaults to in-memory                                                                          |
| `WORKER_MAX_RESTARTS`                       | `5`                             | Consecutive crash limit before a critical worker marks the agent not-ready                                                     |
| `WEBHOOK_MAX_CONCURRENT_DELIVERIES`         | `50`                            | Maximum number of in-flight webhook delivery tasks across all subscriptions                                                    |
| `WEBHOOK_MAX_CONCURRENT_DELIVERIES_PER_SUB` | `10`                            | Per-subscription cap on concurrent in-flight deliveries; also settable per webhook via `max-concurrent-deliveries` frontmatter |
| `WEBHOOK_EXTRACTION_TIMEOUT`                | `120`                           | Seconds to wait for a single LLM extraction call inside a webhook delivery                                                     |
| `JOBS_MAX_CONCURRENT`                       | `0` (unlimited)                 | Maximum number of jobs that may run concurrently; `0` disables the limit                                                       |
| `TASKS_MAX_CONCURRENT`                      | `0` (unlimited)                 | Maximum number of tasks that may run concurrently; `0` disables the limit                                                      |
| `TASK_TIMEOUT_SECONDS`                      | `300`                           | Task timeout in seconds, applied to A2A backend requests                                                                       |
| `MANIFEST_PATH`                             | `/home/agent/manifest.json`     | Path to the team manifest file listing all agents by name and URL                                                              |
| `BACKENDS_READY_WARN_AFTER`                 | `120`                           | Seconds to wait before logging a warning that backends have not become healthy                                                 |
| `LOG_PROMPT_MAX_BYTES`                      | `200`                           | Maximum bytes of the prompt logged at INFO level; `0` suppresses prompt logging entirely                                       |
| `A2A_BACKEND_MAX_RETRIES`                   | `3`                             | Maximum retry attempts for transient backend errors (429, 502, 503, 504, connection errors); must be >= 1                     |
| `A2A_BACKEND_RETRY_BACKOFF`                 | `1.0`                           | Base backoff in seconds for retry delay (exponential with jitter)                                                             |
| `A2A_URL_<ID>`                              | _(unset)_                       | Per-backend URL override (e.g. `A2A_URL_IRIS_A2_CLAUDE`)                                                                       |

**Backends (a2-claude / a2-codex / a2-gemini):**

| Variable                   | Default                                | Description                                                                  |
| -------------------------- | -------------------------------------- | ---------------------------------------------------------------------------- |
| `AGENT_NAME`               | `a2-claude` / `a2-codex` / `a2-gemini` | Backend instance name (e.g. `iris-a2-claude`)                                |
| `AGENT_OWNER`              | _(same as `AGENT_NAME`)_               | Named agent this backend belongs to (e.g. `iris`); used in metric labels     |
| `AGENT_ID`                 | `claude` / `codex` / `gemini`          | Backend slot identifier; used in metric labels                               |
| `AGENT_URL`                | `http://localhost:8080/`               | Public A2A endpoint URL reported in agent card                               |
| `BACKEND_PORT`             | `8080`                                 | HTTP port the backend listens on (internal)                                  |
| `METRICS_ENABLED`          | _(unset)_                              | Enable Prometheus `/metrics`                                                 |
| `CONVERSATIONS_AUTH_TOKEN` | _(unset)_                              | Bearer token required to access `/conversations` and `/trace`                |
| `TASK_STORE_PATH`          | _(unset)_                              | Path for SQLite A2A task store; defaults to in-memory                        |
| `WORKER_MAX_RESTARTS`      | `5`                                    | Consecutive crash limit before a critical worker marks the backend not-ready |
| `LOG_PROMPT_MAX_BYTES`     | `200`                                  | Max bytes of the prompt logged at INFO level; `0` suppresses it entirely     |

---

## Communication Layer

### A2A Protocol

Agents communicate via the A2A protocol (HTTP/JSON-RPC). External callers always target the **nyx agent** by its
hostname/port. nyx reads the `routing.a2a` entry from `backend.yaml` and forwards the request unchanged to the
configured backend. The backend session ID matches the session ID provided by the external caller, preserving
conversation continuity across turns.

Each nyx-harness exposes:

- `/.well-known/agent.json` — agent card for discovery
- `/` — task execution endpoint (`message/send`)
- `GET /health/start` — startup probe: 200 once ready, 503 while initializing
- `GET /health/live` — liveness probe: always 200 with `{"status": "ok", "agent": ..., "uptime_seconds": ...}`
- `GET /health/ready` — readiness probe: 200/`{"status": "ready"}`; 503/`{"status": "starting"}` while initializing;
  503/`{"status": "degraded"}` when a backend is unhealthy
- `GET /agents` — own card plus agent cards from all configured backends
- `GET /jobs` — structured snapshot of registered scheduled jobs
- `GET /tasks` — structured snapshot of registered scheduled tasks
- `GET /webhooks` — structured snapshot of registered webhook subscriptions
- `GET /continuations` — structured snapshot of registered continuation items
- `GET /triggers` — structured snapshot of registered inbound trigger endpoints
- `GET /heartbeat` — current heartbeat configuration from `HEARTBEAT.md`
- `GET /conversations` — merged conversation log from all backends
- `GET /trace` — merged trace log from all backends
- `GET /.well-known/agent-triggers.json` — discovery array of all enabled trigger descriptors

Cross-agent aggregation (`/team`, `/proxy/<name>`, `/conversations/<name>`, `/trace/<name>`) was retired in beta.46 —
the dashboard pod fans out directly to each agent's endpoints and owns cross-agent routing (#470).

Each backend exposes the same A2A surface plus:

- `/health` — health check endpoint
- `/metrics` — Prometheus metrics endpoint
- `/mcp` — MCP JSON-RPC server (`initialize`, `tools/list`, `tools/call`) for MCP hosts (Claude Desktop, Cursor, VS Code
  extensions, etc.)

### Internal Message Bus (nyx-harness)

All internal work — heartbeat ticks, job/task fires, trigger dispatches, continuation fires, and A2A-inbound tasks —
flows through the `MessageBus`. The bus serializes execution: one message processed at a time, deduplicated by kind.
This prevents concurrent outbound backend calls from the same nyx-harness process.

---

## Port Assignments

| Agent       | nyx-harness | a2-claude | a2-codex | a2-gemini |
| ----------- | ----------- | --------- | -------- | --------- |
| iris        | 8000        | 8010      | 8011     | 8012      |
| nova        | 8001        | 8020      | 8021     | 8022      |
| kira        | 8002        | 8030      | 8031     | 8032      |
| bob         | 8099        | 8090      | 8091     | 8092      |
| fred        | 8098        | 8089      | —        | —         |
| ui (active) | 3002        | —         | —        | —         |
| ui (test)   | 3001        | —         | —        | —         |

Backend containers all listen on port 8080 internally; host port mappings are as above.

---

## Issue and Skill Layer

### GitHub Issue Taxonomy

| Label     | Created by      | Worked by           | Purpose                                                                    |
| --------- | --------------- | ------------------- | -------------------------------------------------------------------------- |
| `bug`     | `bug-discover`  | `bug-implement`     | Defect — code that is broken or behaves incorrectly                        |
| `risk`    | `risk-discover` | `risk-implement`    | Code quality issue — works today but fragile, insecure, or likely to break |
| `gap`     | `gap-discover`  | `gap-implement`     | Missing capability — functionality the system should have but does not     |
| `feature` | humans / agents | `feature-implement` | Intentional enhancement requested by stakeholders                          |

### Develop Loop

The `develop` skill runs a continuous improvement cycle across all issue types:

```text
Phase 1–4:   bug discovery → refinement → approval → implementation
Phase 5–8:   risk discovery → refinement → approval → implementation
Phase 9–12:  gap discovery → refinement → approval → implementation
Phase 13–16: feature discovery → refinement → approval → implementation
Phase 17:    docs refinement
```

---

## Deployment

### Local

Build all four images and deploy with Helm:

```bash
docker build -f harness/Dockerfile -t nyx-harness:latest .
docker build -f backends/a2-claude/Dockerfile -t a2-claude:latest .
docker build -f backends/a2-codex/Dockerfile -t a2-codex:latest .
docker build -f backends/a2-gemini/Dockerfile -t a2-gemini:latest .
helm upgrade --install nyx ./charts/nyx -f ./charts/nyx/values-test.yaml -n nyx --create-namespace
```

Port assignments per agent:

| Agent | nyx-harness | a2-claude | a2-codex | a2-gemini |
| ----- | ----------- | --------- | -------- | --------- |
| iris  | 8000        | 8010      | 8011     | 8012      |
| nova  | 8001        | 8020      | 8021     | 8022      |
| kira  | 8002        | 8030      | 8031     | 8032      |

### Kubernetes (Target)

All infrastructure decisions are evaluated against Kubernetes compatibility:

- Health probes follow the three-probe model (`/health/start`, `/health/live`, `/health/ready`) for nyx-harness;
  `/health` for backend containers
- Configuration injected via env vars and mounted `ConfigMap`/`Secret` volumes
- Backend URL configurable via `A2A_URL_<ID>` env var — supports sidecar (`http://localhost:8080`), separate pod
  (`http://a2-claude-svc:8080`), or Compose service DNS (`http://iris-a2-claude:8080`) without config file changes
- Stateless containers at the nyx-harness layer (all state lives in backends)
- Standard HTTP endpoints suitable for `Service` and `Ingress`

A Helm chart is available at `charts/nyx/` and published to `oci://ghcr.io/skthomasjr/charts/nyx` on every release tag.
A Kubernetes Operator (declarative agent lifecycle via CRDs) is in development; its chart lives at
`charts/nyx-operator/`.

### git-sync Image

The Helm chart uses an internal git-sync image (`ghcr.io/skthomasjr/images/git-sync`) built from `git-sync/Dockerfile`.
This image adds `rsync` to the upstream git-sync base image, enabling `rsync --delete` for correct incremental directory
sync. Without rsync, upstream git-sync copies only changed files — deletions and deep directory removes are not
propagated. With rsync, the sync is fully correct: files and directories are added, modified, and deleted at all depths
to match the source exactly.

---

## Architectural Patterns

### Patterns in Use

**nyx as pure infrastructure.** nyx-harness owns the scheduling and relay layer; LLM execution is the sole
responsibility of backend containers. This separation allows each layer to evolve independently and enables swapping LLM
backends without touching the scheduler.

**File-based configuration over compiled-in identity.** A new agent is a new directory with mounted files — not a new
image build. The same image serves any number of identities.

**Named routing over round-robin.** `backend.yaml` routes each concern (a2a, heartbeat, job, task, trigger,
continuation) to a named backend id. Routing is deterministic and explicit — no load-balancing or dynamic selection.

**Per-backend URL override.** The `A2A_URL_<ID>` env var allows the same `backend.yaml` config file to work across
Docker Compose, Kubernetes sidecars, and separate pod deployments.

**Message bus serialization.** All work flows through a single async queue per nyx-harness process. Prevents concurrent
outbound backend calls, enforces deduplication, and provides a single instrumentation point for latency and throughput.

**Guarded restart loop.** Every background task (heartbeat, jobs, tasks, triggers, continuations, webhooks, bus worker)
runs inside `_guarded()` — a crash-restart wrapper that logs the failure, increments a metric, and restarts after a
delay. No task can take down the nyx-harness process.

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

**Webhook-to-trigger chaining.** Outbound webhooks can POST directly to a nyx-harness trigger endpoint, enabling
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
