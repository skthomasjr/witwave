# Architecture

_Last updated: 2026-04-08_

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
│   │   │   ├── backends.yaml  # Backend routing config (type: a2a)
│   │   │   ├── HEARTBEAT.md   # Proactive heartbeat schedule
│   │   │   ├── agenda/        # Scheduled work items (*.md)
│   │   │   └── skills/        # Agent-local skill documents
│   │   ├── .claude/           # Claude Code config
│   │   │   ├── mcp.json
│   │   │   └── settings.json
│   │   ├── .codex/            # Codex config
│   │   │   └── config.toml
│   │   ├── logs/              # nyx-agent logs
│   │   ├── a2-claude/         # Claude backend instance
│   │   │   ├── agent.md       # Backend identity (mounted at startup)
│   │   │   ├── logs/          # conversation.log, trace.jsonl
│   │   │   └── memory/        # Persistent markdown memory files
│   │   └── a2-codex/          # Codex backend instance
│   │       ├── agent.md
│   │       ├── logs/
│   │       └── memory/
│   ├── nova/                  # Same structure as iris/
│   └── kira/                  # Same structure as iris/
└── test/                      # Test agents
    ├── manifest.json
    ├── bob/                   # Same structure as active agents
    └── tom/

agent/                         # nyx-agent source (router/scheduler)
├── Dockerfile
├── main.py                    # Entrypoint — wires all components and runs the event loop
├── executor.py                # Routes A2A requests to the configured backend
├── bus.py                     # Internal async message bus (deduplication, backpressure)
├── heartbeat.py               # Heartbeat scheduler — drives proactive agent behavior
├── agenda.py                  # Agenda scheduler — executes scheduled work items
├── metrics.py                 # Prometheus metric definitions (agent_* prefix)
├── utils.py                   # Shared utilities (frontmatter parser, etc.)
└── backends/
    ├── base.py                # AgentBackend abstract base class
    ├── a2a.py                 # A2ABackend — forwards requests to a remote A2A agent
    ├── claude.py              # ClaudeBackend — local Claude Agent SDK (legacy fallback)
    ├── codex.py               # CodexBackend — local Codex CLI (legacy fallback)
    └── config.py              # Backend config loader; supports type: a2a, claude, codex

a2-claude/                     # Claude backend source
├── Dockerfile
├── main.py                    # A2A server entrypoint
├── executor.py                # Claude Agent SDK executor; owns session state and logging
├── metrics.py                 # Prometheus metric definitions (a2_* prefix)
└── requirements.txt

a2-codex/                      # Codex backend source
├── Dockerfile
├── main.py                    # A2A server entrypoint
├── executor.py                # OpenAI Agents SDK executor; owns session state and logging
├── metrics.py                 # Prometheus metric definitions (a2_* prefix, parity with a2-claude)
└── requirements.txt

ui/                            # Web UI

.claude/
└── skills/                    # Local Claude Code skills (user-invokable slash commands)
    ├── deploy/                # Build images and manage Docker Compose environments
    ├── develop/               # Continuous improvement loop
    ├── evaluate-bugs/         # Find bugs → file issues
    ├── evaluate-features/     # Translate feature proposals → type/feature work items
    ├── evaluate-gaps/         # Find enhancement opportunities → file issues
    ├── evaluate-risks/        # Find risks → file issues
    ├── evaluate-skills/       # Find skill bugs → file issues
    ├── github-issue/          # GitHub Issue management (create, list, claim, close)
    ├── plan-features/         # Research competitive landscape → file feature proposals
    ├── remote/                # Send prompts to running agents via A2A
    ├── work-bugs/             # Work type/bug issues
    ├── work-features/         # Work type/feature issues
    ├── work-gaps/             # Work type/enhancement issues
    ├── work-risks/            # Work type/reliability and type/code-quality issues
    └── work-skills/           # Work type/skill issues

docs/
├── architecture.md            # This document
├── competitive-landscape.md   # Competitor research and gap analysis
└── product-vision.md          # Target audience, design principles, deployment roadmap

.github/
└── ISSUE_TEMPLATE/
    ├── feature.md             # Broad feature proposal template (label: feature)
    ├── task.md                # Implementation task template (label: type/*)
    └── question.md            # Question template

docker-compose.active.yml      # Active environment (iris, nova, kira + backends + ui)
docker-compose.test.yml        # Test environment (bob, tom + backends + ui)
AGENTS.md                      # Canonical repo instructions for all coding agents
CLAUDE.md                      # Claude Code compatibility shim → AGENTS.md
```

---

## Runtime Architecture

### Overview

Each named agent is a cluster of containers:

1. **nyx-agent** — the infrastructure layer. Receives external A2A requests, fires heartbeats, runs agenda items, and
   forwards all LLM work to a configured backend. Owns no LLM itself.
2. **a2-claude** (per agent) — a standalone A2A server backed by the Claude Agent SDK. Owns session state, memory,
   and conversation logging.
3. **a2-codex** (per agent) — a standalone A2A server backed by the OpenAI Agents SDK. Same interface as a2-claude.

```
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

**`main.py`** — The entrypoint. Constructs the `MessageBus`, `AgentExecutor`, `AgendaRunner`, and A2A HTTP server,
then runs all of them concurrently via `asyncio.gather`. A `_guarded` wrapper catches crashes in any background task
and restarts it with a delay.

**`bus.py`** — An async `asyncio.Queue`-backed message bus. Deduplicates in-flight messages by `kind` — if a
heartbeat message is already in-flight, a second heartbeat is dropped rather than queued.

**`heartbeat.py`** — Watches `HEARTBEAT.md` for changes via `awatch`. On each heartbeat interval, enqueues a
heartbeat message on the bus. The executor forwards the heartbeat prompt to the backend named in `routing.heartbeat`.

**`agenda.py`** — Reads `*.md` files from the `agenda/` directory. Each file has YAML frontmatter defining a cron
schedule. Fires agenda items on schedule by enqueuing messages on the bus. The executor forwards each item to the
backend named in `routing.agenda`.

**`executor.py`** — Receives `BusMessage` objects from the bus, resolves the target backend from `routing.*`, and
calls `backend.run_query(prompt, session_id, is_new)`. For `type: a2a` backends, this is an HTTP forward via
`A2ABackend`. Legacy local backends (`type: claude`, `type: codex`) remain available as a fallback during transition.

**`backends/a2a.py`** — Implements `AgentBackend.run_query` by constructing an A2A `message/send` JSON-RPC payload
and forwarding it to the backend URL. The backend URL can be overridden per-backend via an environment variable
(`A2A_URL_<ID_UPPERCASED>`), enabling Kubernetes sidecar, separate pod, or Docker Compose deployments without config
file changes.

### Backend Components (a2-claude and a2-codex)

Both backends share identical structure and API surface; they differ only in their LLM SDK.

**`main.py`** — Builds the A2A `AgentCard` from the mounted `agent.md` file (via `AGENT_MD` env var), wires the
`AgentExecutor` and `InMemoryTaskStore`, and serves the full Starlette application with routes for
`/.well-known/agent.json`, `/` (A2A), `/health`, and `/metrics`.

**`executor.py`** — Implements the A2A `AgentExecutor` interface. Manages session continuity using the session ID
passed in the A2A request metadata. Writes `conversation.log` and `trace.jsonl` to the mounted logs directory.

**`metrics.py`** — Prometheus metric definitions with `a2_*` prefix. Metric names and labels are kept at parity
across `a2-claude` and `a2-codex`.

---

## Configuration Model

Agent identity and behavior are entirely file-based. No identity is baked into any image.

### nyx-agent config files

| File            | Location       | Purpose                                                              |
| --------------- | -------------- | -------------------------------------------------------------------- |
| `AGENTS.md`     | `.nyx/`        | Behavioral guidance (served as CLAUDE.md and AGENTS.md in container) |
| `agent-card.md` | `.nyx/`        | A2A identity — description text served in agent card                 |
| `backends.yaml` | `.nyx/`        | Backend definitions and routing                                      |
| `HEARTBEAT.md`  | `.nyx/`        | Heartbeat schedule and prompt                                        |
| `agenda/*.md`   | `.nyx/agenda/` | Scheduled work items with cron frontmatter                           |
| `skills/`       | `.nyx/skills/` | Agent-local skill documents                                          |

### Backend config files

| File       | Location                   | Purpose                                              |
| ---------- | -------------------------- | ---------------------------------------------------- |
| `agent.md` | `<name>/a2-claude/`        | Identity injected into the Claude backend at startup |
| `agent.md` | `<name>/a2-codex/`         | Identity injected into the Codex backend at startup  |
| `memory/`  | `<name>/a2-claude/memory/` | Persistent markdown memory files for Claude backend  |
| `memory/`  | `<name>/a2-codex/memory/`  | Persistent markdown memory files for Codex backend   |

### backends.yaml schema

```yaml
backends:
  - id: <backend-id>       # Unique name; referenced in routing block
    type: a2a              # "a2a" for remote backends; "claude"/"codex" for local (legacy)
    url: http://<service>:8080   # Service URL; overridable via A2A_URL_<ID> env var
    default: true          # Mark exactly one backend as default

routing:
  a2a: <backend-id>        # Backend for incoming A2A requests
  heartbeat: <backend-id>  # Backend for heartbeat-triggered work
  agenda: <backend-id>     # Backend for agenda task execution
```

### Key environment variables

**nyx-agent:**

| Variable               | Default                          | Description                                              |
| ---------------------- | -------------------------------- | -------------------------------------------------------- |
| `AGENT_NAME`           | `nyx-agent`                      | Agent display name (e.g. `iris`)                         |
| `AGENT_PORT`           | `8000`                           | HTTP port                                                |
| `BACKENDS_CONFIG_PATH` | `/home/agent/.nyx/backends.yaml` | Path to backends config                                  |
| `METRICS_ENABLED`      | _(unset)_                        | Enable Prometheus `/metrics`                             |
| `A2A_URL_<ID>`         | _(unset)_                        | Per-backend URL override (e.g. `A2A_URL_IRIS_A2_CLAUDE`) |

**Backends (a2-claude / a2-codex):**

| Variable          | Default                  | Description                                    |
| ----------------- | ------------------------ | ---------------------------------------------- |
| `AGENT_NAME`      | `a2-claude` / `a2-codex` | Backend instance name (e.g. `iris-a2-claude`)  |
| `AGENT_URL`       | `http://localhost:8080/` | Public A2A endpoint URL reported in agent card |
| `AGENT_MD`        | `/home/agent/agent.md`   | Path to mounted identity file                  |
| `BACKEND_PORT`    | `8080`                   | HTTP port the backend listens on (internal)    |
| `METRICS_ENABLED` | _(unset)_                | Enable Prometheus `/metrics`                   |

---

## Communication Layer

### A2A Protocol

Agents communicate via the A2A protocol (HTTP/JSON-RPC). External callers always target the **nyx agent** by its
hostname/port. nyx reads the `routing.a2a` entry from `backends.yaml` and forwards the request unchanged to the
configured backend. The backend session ID matches the session ID provided by the external caller, preserving
conversation continuity across turns.

Each nyx-agent exposes:

- `/.well-known/agent.json` — agent card for discovery
- `/` — task execution endpoint (`message/send`)

Each backend exposes the same A2A surface plus:

- `/health` — health check endpoint
- `/metrics` — Prometheus metrics endpoint

### Internal Message Bus (nyx-agent)

All internal work — heartbeat ticks, agenda fires, A2A-inbound tasks — flows through the `MessageBus`. The bus
serializes execution: one message processed at a time, deduplicated by kind. This prevents concurrent outbound
backend calls from the same nyx-agent process.

---

## Port Assignments

| Agent       | nyx-agent | a2-claude | a2-codex |
| ----------- | --------- | --------- | -------- |
| iris        | 8000      | 8010      | 8011     |
| nova        | 8001      | 8020      | 8021     |
| kira        | 8002      | 8030      | 8031     |
| bob         | 8099      | 8090      | 8091     |
| tom         | 8098      | 8088      | 8089     |
| ui (active) | 3002      | —         | —        |
| ui (test)   | 3001      | —         | —        |

Backend containers all listen on port 8080 internally; host port mappings are as above.

---

## Issue and Skill Layer

### GitHub Issue Taxonomy

| Label               | Created by          | Worked by       | Purpose                                            |
| ------------------- | ------------------- | --------------- | -------------------------------------------------- |
| `feature`           | `plan-features`     | —               | Broad feature proposal; never implemented directly |
| `type/feature`      | `evaluate-features` | `work-features` | Feature implementation slice (theme/slice scoped)  |
| `type/bug`          | `evaluate-bugs`     | `work-bugs`     | Defect in source code                              |
| `type/reliability`  | `evaluate-risks`    | `work-risks`    | Reliability or operational risk                    |
| `type/code-quality` | `evaluate-risks`    | `work-risks`    | Code quality or maintainability risk               |
| `type/enhancement`  | `evaluate-gaps`     | `work-gaps`     | Missing capability or improvement opportunity      |
| `type/skill`        | `evaluate-skills`   | `work-skills`   | Bug in a skill document                            |
| `type/task`         | humans / agents     | varies          | General task                                       |

### Feature Pipeline

```
plan-features      → creates `feature` proposals (status/pending)
evaluate-features  → reads approved `feature` proposals
                   → creates `type/feature` implementation slices (theme + slice scoped)
                   → blocked until all current theme/slice issues are closed
work-features      → implements `type/feature` issues
```

### Develop Loop

The `/develop` skill runs a continuous improvement loop:

```
1. work-skills  → evaluate-skills   (restart from 1 if new issues)
2. work-bugs    → evaluate-bugs     (restart from 1 if new issues)
3. work-risks   → evaluate-risks    (restart from 1 if new issues)
4. evaluate-gaps → work-gaps        (once steps 1–3 clean)
5. evaluate-features → work-features
6. Report
```

---

## Deployment

### Local

Build all three images and bring up the active environment:

```bash
docker build -f agent/Dockerfile -t nyx-agent:latest .
docker build -f a2-claude/Dockerfile -t a2-claude:latest .
docker build -f a2-codex/Dockerfile -t a2-codex:latest .
docker compose -f docker-compose.active.yml up -d
```

Port assignments per agent:

| Agent | nyx-agent | a2-claude | a2-codex |
|-------|-----------|-----------|----------|
| iris  | 8000      | 8010      | 8011     |
| nova  | 8001      | 8020      | 8021     |
| kira  | 8002      | 8030      | 8031     |

### Kubernetes (Target)

All infrastructure decisions are evaluated against Kubernetes compatibility:

- Health probes follow the three-probe model (`/health/start`, `/health/live`, `/health/ready`) for nyx-agent;
  `/health` for backend containers
- Configuration injected via env vars and mounted `ConfigMap`/`Secret` volumes
- Backend URL configurable via `A2A_URL_<ID>` env var — supports sidecar (`http://localhost:8080`), separate pod
  (`http://a2-claude-svc:8080`), or Compose service DNS (`http://iris-a2-claude:8080`) without config file changes
- Stateless containers at the nyx-agent layer (all state lives in backends)
- Standard HTTP endpoints suitable for `Service` and `Ingress`

A Helm chart is planned. A Kubernetes Operator (declarative agent lifecycle via CRDs) is under consideration.

---

## Architectural Patterns

### Patterns in Use

**nyx as pure infrastructure.** nyx-agent owns the scheduling and relay layer; LLM execution is the sole
responsibility of backend containers. This separation allows each layer to evolve independently and enables swapping
LLM backends without touching the scheduler.

**File-based configuration over compiled-in identity.** A new agent is a new directory with mounted files — not a
new image build. The same image serves any number of identities.

**Named routing over round-robin.** `backends.yaml` routes each concern (a2a, heartbeat, agenda) to a named backend
id. Routing is deterministic and explicit — no load-balancing or dynamic selection.

**Per-backend URL override.** The `A2A_URL_<ID>` env var allows the same `backends.yaml` config file to work across
Docker Compose, Kubernetes sidecars, and separate pod deployments.

**Message bus serialization.** All work flows through a single async queue per nyx-agent process. Prevents concurrent
outbound backend calls, enforces deduplication, and provides a single instrumentation point for latency and throughput.

**Guarded restart loop.** Every background task (`heartbeat_runner`, `bus_worker`, `agenda_runner`) runs inside
`_guarded()` — a crash-restart wrapper that logs the failure, increments a metric, and restarts after a delay. No
task can take down the nyx-agent process.

**Skill documents as workflow.** Agent behavior is expressed in markdown skill files, not hardcoded logic. Skills are
hot-swappable without rebuilding the image or restarting the container.

**Theme/slice feature decomposition.** Large features are broken into themes (logical phases) and slices (discrete
work units within a theme). No new theme begins until all slices of the current theme are closed.

### Patterns to Evaluate

The following patterns represent potential architectural directions. Each should be evaluated as an architectural
change proposal before becoming a feature issue:

**Plan-before-code execution mode.** OpenHands v1.5.0 and Devin both enforce a two-phase pattern: read-only planning
→ execution. The Claude Agent SDK supports `permission_mode="plan"` natively. Applicable to agenda items with high
blast radius.

**In-process custom tools.** The Claude Agent SDK's `@tool()` decorator and `create_sdk_mcp_server()` factory allow
defining tools as plain Python functions inside the harness process — no external MCP server.

**Programmatic subagent definitions.** `AgentDefinition` in `ClaudeAgentOptions` allows defining specialized
subagents programmatically without file-based configuration.

**Hooks system.** The SDK's `HookMatcher` API registers Python callbacks on `PreToolUse`, `PostToolUse`, `Stop`,
`SessionStart`, etc. `PreToolUse` supports `updatedInput` — rewriting tool arguments before execution.

**Structured shared memory.** Competitors use structured persistent memory with semantic search. This project uses
flat markdown files per backend. SQLite FTS5 with LLM-powered summarization is the strongest reference.

**Auto-generated skills.** Hermes Agent writes a new skill document after completing a complex task — a closed
learning loop from execution to capability accumulation.

**Declarative policy engine.** A file-based policy DSL (JSON/YAML) evaluated before every tool call would add
guardrails without requiring Python code changes.

**Event-driven triggers.** An inbound HTTP trigger endpoint would bridge this project to external systems (GitHub PRs,
Slack messages, Jira tickets) without cron schedules.

---

## Relationship to Other Docs

| Document                                             | Purpose                                                |
| ---------------------------------------------------- | ------------------------------------------------------ |
| [product-vision.md](product-vision.md)               | Target audience, design principles, deployment roadmap |
| [competitive-landscape.md](competitive-landscape.md) | Competitor research, gap analysis, research themes     |
| `README.md`                                          | Quickstart and technical reference                     |
| `AGENTS.md`                                          | Canonical repo instructions for all coding agents      |
