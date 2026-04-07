# Architecture

_Last updated: 2026-04-07_

---

## Purpose

This document describes the current architecture of the autonomous agent platform — how the runtime is structured, how agents are configured and deployed, how they communicate, and how the skill and issue layers are organized. It also captures known architectural patterns from the competitive landscape and serves as the reference for evaluating large structural changes.

When a proposed change is architectural in nature — a new runtime primitive, a significant repo restructuring, a new protocol layer, a shift in deployment model — it should be discussed here first before becoming a `feature` issue.

---

## Repository Structure

```text
.agents/
└── active/                    # Live autonomous agents
    ├── manifest.json          # Registry of all agents in this deployment
    ├── iris/
    │   ├── .nyx/              # Runtime config
    │   │   ├── agent-card.md  # A2A identity description
    │   │   ├── backends.yaml  # Backend selection (claude, codex)
    │   │   ├── HEARTBEAT.md   # Proactive heartbeat schedule
    │   │   └── agenda/        # Scheduled work items (*.md)
    │   ├── .claude/           # Claude Code config
    │   │   ├── CLAUDE.md      # Agent-specific behavioral guidance
    │   │   ├── skills/        # Agent-local skills
    │   │   └── memory/        # Persistent markdown memory files
    │   ├── .codex/            # Codex config
    │   └── logs/
    │       └── conversation.log  # Per-run JSONL conversation log
    ├── nova/                  # Same structure as iris/
    └── kira/                  # Same structure as iris/

agent/
├── Dockerfile                 # nyx-agent image — no identity baked in
├── main.py                    # Entrypoint — wires all components and runs the event loop
├── executor.py                # Bridges A2A requests and the Claude Agent SDK
├── bus.py                     # Internal async message bus (deduplication, backpressure)
├── heartbeat.py               # Heartbeat scheduler — drives proactive agent behavior
├── agenda.py                  # Agenda scheduler — executes scheduled work items
├── metrics.py                 # Prometheus metric definitions (70+ metrics)
├── utils.py                   # Shared utilities (frontmatter parser, etc.)
└── backends/
    ├── base.py                # AgentBackend abstract base class
    ├── claude.py              # ClaudeSDKClient — wraps the Claude Agent SDK
    ├── codex.py               # CodexBackend — wraps the Codex CLI
    └── config.py              # Backend configuration and selection

.claude/
└── skills/                    # Local Claude Code skills (user-invokable slash commands)
    ├── develop/               # Continuous improvement loop
    ├── evaluate-bugs/         # Find bugs → file issues
    ├── evaluate-features/     # Translate feature proposals → type/feature work items
    ├── evaluate-gaps/         # Find enhancement opportunities → file issues
    ├── evaluate-risks/        # Find risks → file issues
    ├── evaluate-skills/       # Find skill bugs → file issues
    ├── github-issue/          # GitHub Issue management (create, list, claim, close)
    ├── plan-features/         # Research competitive landscape → file feature proposals
    ├── redeploy/              # Rebuild image and recreate containers
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

docker-compose.active.yml      # Local multi-agent deployment (active agents)
docker-compose.test.yml        # Test agent deployment (bob)
AGENTS.md                      # Canonical repo instructions for all coding agents
CLAUDE.md                      # Claude Code compatibility shim → AGENTS.md
```

---

## Runtime Architecture

### Overview

Each agent is a containerized instance of the `nyx-agent` image. The image contains the runtime; identity and behavior are injected via mounted files and environment variables. All agents are identical at the image level.

```
┌─────────────────────────────────────────────────────────┐
│                      nyx-agent container                │
│                                                         │
│  ┌──────────┐   ┌──────────┐   ┌─────────────────────┐ │
│  │ Heartbeat│   │  Agenda  │   │   A2A HTTP Server   │ │
│  │ Scheduler│   │ Scheduler│   │ (Starlette + uvicorn)│ │
│  └────┬─────┘   └────┬─────┘   └──────────┬──────────┘ │
│       │              │                     │             │
│       └──────────────┴─────────────────────┘            │
│                      │                                  │
│               ┌──────▼──────┐                           │
│               │ Message Bus │  (async, deduplicated)    │
│               └──────┬──────┘                           │
│                      │                                  │
│               ┌──────▼──────┐                           │
│               │  Executor   │  (AgentExecutor)          │
│               └──────┬──────┘                           │
│                      │                                  │
│               ┌──────▼──────┐                           │
│               │   Backend   │  (Claude SDK / Codex)    │
│               └─────────────┘                           │
│                                                         │
│  /health/start   /health/live   /health/ready           │
│  /metrics        /.well-known/agent.json                │
└─────────────────────────────────────────────────────────┘
```

### Components

**`main.py`** — The entrypoint. Constructs the `MessageBus`, `AgentExecutor`, `AgendaRunner`, and A2A HTTP server, then runs all of them concurrently via `asyncio.gather`. A `_guarded` wrapper catches crashes in any background task and restarts it with a delay. An event loop lag monitor observes scheduling delays as a Prometheus histogram.

**`bus.py`** — An async `asyncio.Queue`-backed message bus. Deduplicates in-flight messages by `kind` — if a heartbeat message is already in-flight, a second heartbeat is dropped rather than queued. Produces `BusMessage` objects with a `result` future that callers can await.

**`heartbeat.py`** — Watches `HEARTBEAT.md` for changes via `awatch`. On each heartbeat interval, enqueues a heartbeat message on the bus. Drives proactive agent behavior without external triggers.

**`agenda.py`** — Reads `*.md` files from the `agenda/` directory. Each file has YAML frontmatter defining a cron schedule, optional model override, enabled flag, and priority. The runner fires agenda items on schedule by enqueuing messages on the bus.

**`executor.py`** — The core bridge between the bus and the Claude Agent SDK (or Codex). Receives `BusMessage` objects, resolves the backend, constructs `ClaudeAgentOptions` (or equivalent), and calls `run_query`. Manages MCP config watchers that hot-reload tool configuration when mounted files change.

**`backends/`** — Pluggable backend abstraction. `base.py` defines `AgentBackend.run_query(prompt, session_id, is_new, model)`. `claude.py` wraps the Claude Agent SDK's `ClaudeSDKClient`. `codex.py` wraps the Codex CLI. Selection is driven by `backends.yaml` in the agent's `.nyx/` directory.

---

## Configuration Model

Agent identity and behavior are entirely file-based. No identity is baked into the image.

| File            | Location          | Purpose                                               |
|-----------------|-------------------|-------------------------------------------------------|
| `agent-card.md` | `.nyx/`           | A2A identity — description text served in agent card  |
| `backends.yaml` | `.nyx/`           | Backend selection and model configuration             |
| `HEARTBEAT.md`  | `.nyx/`           | Heartbeat schedule and prompt                         |
| `agenda/*.md`   | `.nyx/agenda/`    | Scheduled work items with cron frontmatter            |
| `CLAUDE.md`     | `.claude/`        | Behavioral guidance for Claude Code                   |
| `skills/`       | `.claude/skills/` | Agent-local skill documents                           |
| `memory/`       | `.claude/memory/` | Persistent markdown memory files                      |

Environment variables override or extend file-based config:

| Variable             | Purpose                                       |
|----------------------|-----------------------------------------------|
| `AGENT_NAME`         | Agent identity (`iris`, `nova`, `kira`)       |
| `AGENT_PORT`         | HTTP port                                     |
| `AGENT_URL`          | Public A2A endpoint URL                       |
| `AGENT_MODEL`        | Default model override                        |
| `METRICS_ENABLED`    | Enable Prometheus `/metrics` endpoint         |
| `ANTHROPIC_API_KEY`  | Claude API credentials                        |

---

## Communication Layer

### A2A Protocol

Agents communicate via the A2A protocol (HTTP/JSON-RPC). Each agent exposes:

- `/.well-known/agent.json` — agent card for discovery
- `/` — task execution endpoint

The `/remote` skill wraps A2A into a natural-language pattern for agent-to-agent delegation.

### Internal Message Bus

All internal work — heartbeat ticks, agenda fires, A2A-inbound tasks — flows through the `MessageBus`. The bus serializes execution: one message processed at a time, deduplicated by kind. This prevents concurrent SDK calls from the same agent process.

---

## Issue and Skill Layer

### GitHub Issue Taxonomy

| Label               | Created by          | Worked by       | Purpose                                             |
|---------------------|---------------------|-----------------|-----------------------------------------------------|
| `feature`           | `plan-features`     | —               | Broad feature proposal; never implemented directly  |
| `type/feature`      | `evaluate-features` | `work-features` | Feature implementation slice (theme/slice scoped)   |
| `type/bug`          | `evaluate-bugs`     | `work-bugs`     | Defect in source code                               |
| `type/reliability`  | `evaluate-risks`    | `work-risks`    | Reliability or operational risk                     |
| `type/code-quality` | `evaluate-risks`    | `work-risks`    | Code quality or maintainability risk                |
| `type/enhancement`  | `evaluate-gaps`     | `work-gaps`     | Missing capability or improvement opportunity       |
| `type/skill`        | `evaluate-skills`   | `work-skills`   | Bug in a skill document                             |
| `type/task`         | humans / agents     | varies          | General task                                        |

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

```bash
docker build -f agent/Dockerfile -t nyx-agent:latest .
docker compose up -d
```

Three agents run locally:

| Agent | Port |
|-------|-----:|
| iris  | 8000 |
| nova  | 8001 |
| kira  | 8002 |

### Kubernetes (Target)

All infrastructure decisions are evaluated against Kubernetes compatibility:

- Health probes follow the three-probe model (`/health/start`, `/health/live`, `/health/ready`)
- Configuration injected via env vars and mounted `ConfigMap`/`Secret` volumes
- Stateless containers — horizontally scalable
- Standard HTTP endpoints suitable for `Service` and `Ingress`

A Helm chart is planned. A Kubernetes Operator (declarative agent lifecycle via CRDs) is under consideration.

---

## Architectural Patterns

### Patterns in Use

**File-based configuration over compiled-in identity.** A new agent is a new directory with mounted files — not a new image build. This enables the same image to serve any number of identities.

**Message bus serialization.** All work flows through a single async queue per agent process. Prevents concurrent SDK calls, enforces deduplication, and provides a single instrumentation point for latency and throughput metrics.

**Backend abstraction.** The `AgentBackend` interface decouples the executor from any specific LLM provider. Claude SDK and Codex are both supported today; new backends require only implementing `run_query`.

**Guarded restart loop.** Every background task (`heartbeat_runner`, `bus_worker`, `agenda_runner`) runs inside `_guarded()` — a crash-restart wrapper that logs the failure, increments a metric, and restarts after a delay. No task can take down the agent process.

**Skill documents as workflow.** Agent behavior is expressed in markdown skill files, not hardcoded logic. Skills are hot-swappable without rebuilding the image or restarting the container.

**Theme/slice feature decomposition.** Large features are broken into themes (logical phases) and slices (discrete work units within a theme). No new theme begins until all slices of the current theme are closed.

### Patterns to Evaluate

The following patterns are used by competitors and represent potential architectural directions. Each should be evaluated as an architectural change proposal before becoming a feature issue:

**Plan-before-code execution mode.** OpenHands v1.5.0 and Devin both enforce a two-phase pattern: read-only planning → execution. The Claude Agent SDK supports `permission_mode="plan"` natively. Applicable to agenda items with high blast radius.

**In-process custom tools.** The Claude Agent SDK's `@tool()` decorator and `create_sdk_mcp_server()` factory allow defining tools as plain Python functions inside the harness process — no external MCP server. Enables lightweight harness-native tools without operational overhead.

**Programmatic subagent definitions.** `AgentDefinition` in `ClaudeAgentOptions` allows defining specialized subagents programmatically (description, prompt, tools, maxTurns, skills, memory) without file-based configuration.

**Hooks system.** The SDK's `HookMatcher` API registers Python callbacks on `PreToolUse`, `PostToolUse`, `Stop`, `SessionStart`, etc. `PreToolUse` supports `updatedInput` — rewriting tool arguments before execution. Enables ACI-style guardrails at the harness layer.

**Structured shared memory.** Competitors (CrewAI, AutoGPT, Hermes Agent) use structured persistent memory with semantic search. This project uses flat markdown files. SQLite FTS5 with LLM-powered summarization (Hermes Agent pattern) or a unified structured memory class (CrewAI pattern) are the strongest reference implementations.

**Auto-generated skills.** Hermes Agent writes a new skill document after completing a complex task — a closed learning loop from execution to capability accumulation. The skill directory already exists; the gap is the post-task synthesis step.

**Declarative policy engine.** LangGraph 2.0 ships guardrail nodes as first-class primitives. Microsoft's Agent Governance Toolkit (MIT, April 2026) addresses all OWASP Agentic Top 10 risks with sub-millisecond enforcement. A file-based policy DSL (JSON/YAML) evaluated before every tool call would add this without requiring Python code changes.

**Event-driven triggers.** Devin reacts to GitHub PRs, Jira tickets, and Slack messages in addition to scheduled runs. An inbound HTTP trigger endpoint would bridge this project to external systems without cron schedules.

---

## Relationship to Other Docs

| Document                                              | Purpose                                               |
|-------------------------------------------------------|-------------------------------------------------------|
| [product-vision.md](product-vision.md)                | Target audience, design principles, deployment roadmap |
| [competitive-landscape.md](competitive-landscape.md)  | Competitor research, gap analysis, research themes    |
| `README.md`                                           | Quickstart and technical reference                    |
| `AGENTS.md`                                           | Canonical repo instructions for all coding agents     |
