# AGENTS.md

This file provides guidance to Claude Code (https://claude.ai/code) and Codex (https://openai.com/codex/) when working
with code in this repository.

## Repo Root

The repo root is referred to as `<repo-root>`. For this environment, `<repo-root>` is the directory containing this
file.

## Skills

Skills under `.claude/skills/` are mounted directly into each backend container — `claude` at
`/home/agent/.claude/skills/` and `codex` at `/home/agent/.codex/skills/`. Agents always have the same skills as
the local Claude Code session — no per-agent copying required.

## Agent Identity

The acting agent is referred to as `<agent-name>`. For containerized workers, `<agent-name>` is the value of the
`AGENT_NAME` environment variable (e.g. `iris`, `nova`, `kira`). When running as a local session (Claude Code, Codex,
or otherwise), `AGENT_NAME` is not set — in that case, `<agent-name>` is `local-agent`.

## Working with Claude Code and Codex

- Do not run `git commit` unless explicitly asked.
- Do not run `git push` unless explicitly asked.

## Project Overview

witwave is a multi-container autonomous agent platform. Each named agent (iris, nova, kira, …) consists of:

- A **harness** container — the infrastructure layer (A2A relay, heartbeat scheduler, job scheduler). It owns
  no LLM itself; it forwards all work to a backend.
- One or more **backend** containers (`claude`, `codex`, `gemini`) — the LLM execution layer. Each backend is a full A2A
  server that manages its own sessions, memory, conversation logs, and Prometheus metrics.
- Zero or more **MCP** containers (`mcp-kubernetes`, `mcp-helm`, …) — the tool layer. Each MCP server exposes a
  focused set of cluster- or system-level capabilities to backends over the Model Context Protocol. All entries
  under `tools/` are equal MCP components; backends opt into them via their own MCP configuration.

Multiple named agents can collaborate as a team via the A2A protocol, but the named agent (witwave + its backends) is the
deployable unit. MCP components are shared infrastructure — one deployment typically serves every agent in the
cluster rather than being replicated per agent.

## Architecture

### harness (router / scheduler)

Each named agent runs a containerized instance of the `harness` image. harness is the infrastructure layer:

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

Prompts can land in `.witwave/{jobs,tasks,triggers,continuations,webhooks}/` (or at `HEARTBEAT.md`) via two paths:

1. **gitSync materialisation** — a gitSync sidecar rsyncs `.md` files from a repo.
2. **`WitwavePrompt` CR** (operator-only) — declarative Kubernetes resource that binds one prompt to one or more `WitwaveAgent`s;
   the operator reconciles a ConfigMap per `(WitwavePrompt, agent)` pair that mounts at the same path. See
   `operator/README.md#the-witwaveprompt-resource` and `operator/config/samples/witwave_v1alpha1_witwaveprompt.yaml`.

harness retains no LLM of its own. All conversation state, session continuity, memory, and conversation logging
live in the backend container.

Every container in the stack exposes `/metrics` on a **dedicated port** (9000 by default, set via `METRICS_PORT`
env / `metrics.port` chart value / `WitwaveAgentSpec.MetricsPort` CRD field) separate from the app listener (#643).
The split lets NetworkPolicy and auth posture diverge between app traffic and monitoring scrapes. The shared
helper `shared/metrics_server.py` implements both the asyncio-task listener (harness + backends) and the
daemon-thread variant (FastMCP-hosted MCP tools).

### Backend containers

Three backend types exist, each implemented as a standalone A2A server:

- **`claude`** — Claude Agent SDK backend. Source in `backends/claude/`. Image: `claude:latest`.
- **`codex`** — OpenAI Agents SDK (Codex) backend. Source in `backends/codex/`. Image: `codex:latest`.
- **`gemini`** — Google Gemini backend (google-genai SDK). Source in `backends/gemini/`. Image: `gemini:latest`.

Each backend:

- Exposes `/.well-known/agent.json` for A2A discovery
- Exposes `/` as the A2A JSON-RPC task endpoint
- Exposes `/health` for health checks
- Exposes `/metrics` for Prometheus scraping (when `METRICS_ENABLED` is set)
- Exposes `/conversations`, `/trace`, `/mcp`, and `/api/traces[/<id>]` guarded by the same bearer token
  (`CONVERSATIONS_AUTH_TOKEN`) — parity across all three backends (#510, #516, #518). An empty token logs a
  startup warning (#517) and the shared guard refuses to serve protected endpoints unless the operator opts in
  with `CONVERSATIONS_AUTH_DISABLED=true` (documented escape hatch for local dev, loud startup log)
- On `/mcp`, `session_id` is routed through `shared/session_binding.derive_session_id` with a bearer-token
  fingerprint before lookup/insert — parity across all three backends (#867 claude, #929 codex, #935 gemini,
  #941 shared path). When `SESSION_ID_SECRET` is set the bound ID is HMAC-derived from the caller identity so
  one caller cannot hijack another's session. Leave unset only in single-tenant dev
- Manages its own session state, conversation log (`conversation.jsonl`), and memory (`/memory/`)
- Receives behavioral instructions via a mounted file (`CLAUDE.md` for claude, `AGENTS.md` for codex, `GEMINI.md` for gemini) and A2A identity via a mounted `agent-card.md`

Each named agent has its own dedicated backend instances. For example, iris has `iris-claude`, `iris-codex`, and `iris-gemini`.

### MCP components

Tool capabilities are delivered as MCP servers. Every subdirectory under `tools/` is an MCP component and is
treated equally regardless of what it wraps. Current MCP components:

- **`mcp-kubernetes`** — Kubernetes API access via the official Python client. Source in `tools/kubernetes/`.
  Image: `mcp-kubernetes:latest`.
- **`mcp-helm`** — Helm release management via the `helm` CLI (Helm has no Python/REST API). Source in
  `tools/helm/`. Image: `mcp-helm:latest`.
- **`mcp-prometheus`** — PromQL query surface wrapping the standard Prometheus HTTP API (#853). Source in
  `tools/prometheus/`. Image: `mcp-prometheus:latest`. Grafana / Loki / OTel surfaces are tracked as
  follow-ups.

Each MCP component:

- Runs a long-lived HTTP server on port **`8000`** using FastMCP's `streamable-http` transport (#644). The
  container `EXPOSE`s 8000 and Kubernetes addresses it via a per-tool Service (`<release>-mcp-<tool>:8000`).
  A `/health` endpoint is also served on the same port.
- Enforces bearer-token auth via the shared `shared/mcp_auth.py` middleware: when `MCP_TOOL_AUTH_TOKEN` is
  unset or empty and `MCP_TOOL_AUTH_DISABLED` is not explicitly set, the server refuses requests. Set
  `MCP_TOOL_AUTH_DISABLED=true` to acknowledge no-auth mode for local dev (startup log is loud).
- Speaks the Model Context Protocol (not A2A) and is consumed by backends via their MCP configuration
  (`mcp.json` under `.claude/`, `.codex/`, or `.gemini/` — all three backends share the same wire format).
  Entries point at the tool's Service URL, not at a local binary:

  ```json
  {
    "mcpServers": {
      "kubernetes": { "url": "http://witwave-mcp-kubernetes:8000" }
    }
  }
  ```

  Codex additionally reads `.codex/config.toml` for built-in tool enablement flags; that file is unrelated to
  MCP server wiring.
- Targets only the cluster where it is deployed; auth is in-cluster ServiceAccount + RBAC, not arbitrary
  kubeconfigs. The chart ships a least-privilege default ClusterRole/Binding per tool when
  `mcpTools.<name>.rbac.create: true` (default). Override with `mcpTools.<name>.serviceAccountName` to reuse
  an out-of-band SA. Pin the image immutably via `mcpTools.<name>.image.digest` in production (#855); toggle
  the in-pod projected SA token per-tool via `mcpTools.<name>.automountServiceAccountToken` (three-state,
  #856) to coexist with IRSA / workload-identity setups.
- Is independently deployable and **shared across all agents** in a cluster — one Deployment + Service per
  enabled tool. Opt in per tool in the chart's `mcpTools` block (`kubernetes.enabled: true`,
  `helm.enabled: true`); disabled by default.

Add a new MCP component by creating `tools/<name>/` with a `Dockerfile`, `server.py`, and `requirements.txt`
(server must call `mcp.run(transport="streamable-http", host="0.0.0.0", port=8000)`), add an `EXPOSE 8000` to
the Dockerfile, then register a `mcp-<name>:latest` build in the [Building Images](#building-images) section
and a `mcpTools.<name>` block in `charts/witwave/values.yaml`. Tag related issues/PRs with the `mcp` GitHub label.

### Routing configuration

`backend.yaml` (in `.witwave/`) controls which backend handles each concern:

```yaml
backend:
  agents:
    - id: claude
      url: http://localhost:8010
      model: claude-opus-4-7

    - id: codex
      url: http://localhost:8011
      model: gpt-5.1-codex

    - id: gemini
      url: http://localhost:8012

  routing:
    default:
      agent: claude
      model: claude-opus-4-7
    a2a:
      agent: claude
      model: claude-opus-4-7
    heartbeat:
      agent: claude
      model: claude-opus-4-7
    job:
      agent: claude
      model: claude-opus-4-7
    task:
      agent: claude
      model: claude-opus-4-7
    trigger:
      agent: claude
      model: claude-opus-4-7
    continuation:
      agent: claude
      model: claude-opus-4-7
```

Routing values can be a plain agent ID string or an object with `agent:` and optional `model:` fields.
Model resolution order: per-message override → routing entry model → per-backend config model.

The `url` field can be overridden at deploy time via the environment variable
`A2A_URL_<ID_UPPERCASED_WITH_UNDERSCORES>` (e.g. `A2A_URL_IRIS_CLAUDE`). This enables the same config file to
work with Docker Compose service DNS, Kubernetes service DNS, or localhost sidecars without modification.

### Agent configuration layout

Agent identity and behavior are file-based — nothing is baked into images.

```text
.agents/active/<name>/
├── agent-card.md            # A2A identity description text (mounted into all containers at /home/agent/agent-card.md)
├── .witwave/                    # Runtime config (mounted into harness)
│   ├── backend.yaml         # Backend selection and routing
│   ├── HEARTBEAT.md         # Proactive heartbeat schedule and prompt
│   ├── jobs/                # Scheduled job definitions (*.md with cron frontmatter)
│   ├── tasks/               # Scheduled task definitions (*.md with calendar frontmatter)
│   ├── triggers/            # Inbound HTTP trigger definitions (*.md with endpoint frontmatter)
│   ├── continuations/       # Continuation definitions (*.md with continues-after frontmatter)
│   └── webhooks/            # Outbound webhook subscriptions (*.md with url frontmatter)
├── .claude/                 # Claude backend config (mounted into claude)
│   ├── CLAUDE.md            # Behavioral instructions / system prompt
│   ├── agent-card.md        # A2A identity description text
│   ├── mcp.json             # MCP server configuration
│   ├── hooks.yaml           # Optional PreToolUse/PostToolUse extension rules (#467)
│   ├── settings.json        # Claude Code settings
│   └── skills/              # Skill definitions (*.md)
├── .codex/                  # Codex backend config (mounted into codex)
│   ├── AGENTS.md            # Behavioral instructions / system prompt
│   ├── agent-card.md        # A2A identity description text
│   └── config.toml
├── .gemini/                 # Gemini backend config (mounted into gemini)
│   ├── GEMINI.md            # Behavioral instructions / system prompt
│   └── agent-card.md        # A2A identity description text
├── logs/                    # harness logs (runtime, not committed)
├── claude/               # Claude backend instance for this agent
│   ├── logs/                # Backend conversation.jsonl + tool-activity.jsonl (runtime, not committed)
│   └── memory/              # Backend persistent memory (runtime, not committed)
├── codex/                # Codex backend instance for this agent
│   ├── logs/
│   └── memory/
└── gemini/               # Gemini backend instance for this agent
    ├── logs/
    └── memory/              # Includes sessions/ subdir for JSON session history
```

## Project Structure

```text
.agents/
├── active/                  # Active (production-like) agents: iris, nova, kira
│   ├── manifest.json        # Registry of all agents in this deployment
│   └── <name>/              # Per-agent directory (see layout above)
└── test/                    # Test agents: bob, fred (deployed); jack, luke (scaffolds only, see port table below)
    ├── manifest.json
    └── <name>/

harness/                     # harness source (router/scheduler)
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

backends/claude/                   # Claude backend source
├── Dockerfile
├── main.py                  # A2A server entrypoint
├── executor.py              # Claude Agent SDK executor; owns sessions, logging, hooks (#467)
├── hooks.py                 # PreToolUse/PostToolUse policy engine + baseline deny rules (#467)
├── metrics.py               # Prometheus metrics (superset of backends/codex and backends/gemini; adds tool, context, MCP, hooks metrics)
└── requirements.txt

backends/codex/                    # Codex backend source
├── Dockerfile
├── main.py                  # A2A server entrypoint
├── executor.py              # OpenAI Agents SDK executor; owns sessions and logging
├── metrics.py               # Prometheus metrics (common a2_* set; subset of claude)
└── requirements.txt

backends/gemini/                   # Gemini backend source
├── Dockerfile
├── main.py                  # A2A server entrypoint
├── executor.py              # google-genai SDK executor; owns sessions and logging
├── metrics.py               # Prometheus metrics (common a2_* set; subset of claude)
└── requirements.txt

tools/                       # MCP components (one directory per server)
├── kubernetes/              # mcp-kubernetes — Kubernetes API via Python client
├── helm/                    # mcp-helm — Helm CLI wrapper + helm-diff
└── prometheus/              # mcp-prometheus — PromQL / series / labels queries

clients/                     # User-facing interfaces to the platform
├── dashboard/               # Vue 3 + Vite + PrimeVue web UI
└── ww/                      # Go + cobra CLI (ships via Homebrew tap)

charts/                      # Helm charts
├── witwave/                     # witwave Helm chart (deploys agents to Kubernetes)
└── witwave-operator/            # witwave-operator Helm chart (deploys the WitwaveAgent controller)

operator/                    # Kubernetes operator (Go) — reconciles WitwaveAgent CRDs

shared/                      # Python modules shared across harness + backends + MCP tools
                             #   otel.py, metrics_server.py, log_utils.py,
                             #   hook_events.py, session_binding.py, redact.py,
                             #   event_schema.py, session_stream.py, …

docs/                        # Canonical reference docs
├── architecture.md          # High-level system diagram + component relationships
└── events/                  # Event stream wire contract (schema + envelope rules)
    ├── README.md
    └── events.schema.json
```

## Building Images

```bash
# harness (router/scheduler)
docker build -f harness/Dockerfile -t harness:latest .

# Claude backend
docker build -f backends/claude/Dockerfile -t claude:latest .

# Codex backend
docker build -f backends/codex/Dockerfile -t codex:latest .

# Gemini backend
docker build -f backends/gemini/Dockerfile -t gemini:latest .

# Kubernetes MCP tool
docker build -f tools/kubernetes/Dockerfile -t mcp-kubernetes:latest .

# Helm MCP tool
docker build -f tools/helm/Dockerfile -t mcp-helm:latest .

# Prometheus MCP tool (#853)
docker build -f tools/prometheus/Dockerfile -t mcp-prometheus:latest .

# Dashboard image — optional; only built/pushed when dashboard.enabled=true in the chart.
docker build -f clients/dashboard/Dockerfile -t dashboard:latest .

# git-sync helper image (upstream git-sync + rsync). Built from the `helpers/`
# folder alongside any future pod-level helper images (sidecar or init).
docker build -f helpers/git-sync/Dockerfile -t git-sync:latest helpers/git-sync
```

## Running Locally

```bash
docker build -f harness/Dockerfile -t harness:latest . \
  && docker build -f backends/claude/Dockerfile -t claude:latest . \
  && docker build -f backends/codex/Dockerfile -t codex:latest . \
  && docker build -f backends/gemini/Dockerfile -t gemini:latest . \
  && docker build -f tools/kubernetes/Dockerfile -t mcp-kubernetes:latest . \
  && docker build -f tools/helm/Dockerfile -t mcp-helm:latest . \
  && helm upgrade --install witwave ./charts/witwave -f ./charts/witwave/values-test.yaml -n witwave --create-namespace
```

## Interacting with Agents

Use the `/remote` skill to interact with running agents. Always target the **witwave agent by name** — witwave routes the
request internally to its configured backend. Never target backend services directly.

| Agent | Harness | claude | codex | gemini |
| ----- | ------- | ------ | ----- | ------ |
| iris  | 8000    | 8010   | 8011  | 8012   |
| nova  | 8001    | 8010   | 8011  | 8012   |
| kira  | 8002    | 8010   | 8011  | 8012   |
| bob   | 8099    | 8090   | 8091  | 8092   |
| fred  | 8098    | 8089   | —     | —      |

Active agents (iris/nova/kira) each run in their own pod with their own
localhost, so the backend ports are uniform across them (8010/8011/8012).
The harness port differs per agent only because multiple active agents may
share a host via `hostPort`/`NodePort` mappings. Test agents (bob/fred)
still use agent-unique backend ports because they're deployed together in
`values-test.yaml` with `hostPort` exposed on the same host.

Test agents `jack` (codex-only) and `luke` (gemini-only) exist as filesystem
scaffolds under `.agents/test/` but are not wired into
`charts/witwave/values-test.yaml` yet. Port assignments will land when they're
deployed.

The `/remote` skill derives the session ID automatically from the current Claude Code session. Pass it explicitly only
when you need to target a specific session.

## Memory

Each backend manages its own memory under `.agents/<env>/<name>/<backend>/memory/` (e.g.
`.agents/active/iris/claude/memory/`). For `claude` and `codex`, memory files are markdown documents. For `gemini`, conversation history is stored as JSON in `memory/sessions/`. Memory files are not committed to source control. harness has no memory layer of its own.

## Metrics landscape

All three backends share a common `backend_*` metric baseline so cross-backend dashboards can union on
`(agent, agent_id, backend)` without backend-specific series names. **Claude is the superset** — any
metric that exists on one backend exists on claude; peers fill in placeholders where a series doesn't
apply (e.g. codex's `backend_sdk_subprocess_spawn_duration_seconds` is a zero-value placeholder because
the Agents SDK runs in-process). Look at each backend's `metrics.py` for the live catalog; look at
`charts/witwave/dashboards/*.json` for the rendered Grafana views; look at
`charts/witwave/templates/prometheusrule.yaml` for the default alert set.

Harness, operator, and MCP tool metrics use their own prefixes (`harness_*`, `witwaveagent_*`,
`witwaveprompt_*`, `mcp_*`) and are documented in the same per-service `metrics.py` files.

## Conventions

### Auth + secret posture

- Every protected endpoint uses `Authorization: Bearer <token>` headers. Two harness-scope tokens split
  by purpose: `CONVERSATIONS_AUTH_TOKEN` (read / observe) and `ADHOC_RUN_AUTH_TOKEN` (trigger actions).
  Backends reuse `CONVERSATIONS_AUTH_TOKEN` for their `/conversations` / `/api/traces` /
  `/api/sessions/<id>/stream` paths. `CONVERSATIONS_AUTH_DISABLED=true` is the documented
  local-dev escape hatch; startup logs a loud warning when it's set.
- Session IDs on `/mcp` are HMAC-bound to the caller via `shared/session_binding.derive_session_id`
  when `SESSION_ID_SECRET` is set. `SESSION_ID_SECRET_PREV` provides a rotation window — writes always
  use the current-secret id; reads probe `[current, prev]` and emit a one-shot WARN on prev-hit so
  operators know when they can drop the prev secret.
- Secret-backed tokens never land in chart-rendered ConfigMaps. The dashboard injects its harness
  bearer via a Secret → env → nginx `envsubst` → `sub_filter` chain; the literal token lives only in
  the pod's `/etc/nginx/conf.d/default.conf`, not anywhere `kubectl get cm` can reach.

### Redaction + logging

- `shared/redact.py` is the single redaction path. It uses a merge-spans algorithm so
  `redact_text(redact_text(x)) == redact_text(x)` is guaranteed (idempotent by construction). UUID
  and OTel trace/span shapes are shielded before pattern substitution and restored after. The generic
  high-entropy catch-all is gated behind `LOG_REDACT_HIGH_ENTROPY=true` — don't enable it on trace
  pipelines without understanding the false-positive surface.
- Event envelopes carry `session_id_hash` (SHA-256 prefix 12 chars) instead of raw session ids on any
  stream that crosses trust boundaries (`/events/stream`, session drill-down streams). Raw session ids
  appear only in URL paths where the caller already has them.

### MCP transport posture

- `mcp_command_allowlist` gates every stdio MCP entry against `MCP_ALLOWED_COMMANDS` /
  `MCP_ALLOWED_COMMAND_PREFIXES` / `MCP_ALLOWED_CWD_PREFIXES`. Rejections increment
  `backend_mcp_command_rejected_total{reason}`. The default allow-list is pruned to
  `mcp-kubernetes, mcp-helm, mcp-prometheus, uv, uvx`; `mcp_command_args_safe()` rejects positional
  scripts (`*.py`, `*.js`, `*.sh`, `-` stdin) unless their cwd falls under the allow-listed prefix.
- MCP tool containers enforce bearer auth via `shared/mcp_auth.py`. Set `MCP_TOOL_AUTH_TOKEN` in
  deployment; `MCP_TOOL_AUTH_DISABLED=true` is the local-dev escape hatch (startup logs a warning).
- Read-only mode: `MCP_READ_ONLY=true` (or `MCP_HELM_READ_ONLY` / `MCP_KUBERNETES_READ_ONLY`) refuses
  mutating tools while keeping query tools available. Useful during cluster maintenance windows.

### Operator RBAC

- Operator runs with a split RBAC surface: `rbac.secretsWrite=false` on `charts/witwave-operator` drops
  the Secret write verbs while keeping reads, letting operators use pre-provisioned Secrets via
  `existingSecret`. Credential Secrets carry `app.kubernetes.io/component: credentials` and are
  dual-checked (label + `IsControlledBy`) before any update or delete — the operator never touches
  user-created Secrets.

## Reference pointers

AGENTS.md is deliberately high-level. For specifics, go to the source of truth:

- **Chart values + env var reference** — `charts/witwave/values.yaml` and `charts/witwave-operator/values.yaml`
  carry inline comments on every field. Service-specific env vars are declared in each Dockerfile's
  `ENV` directives. Env vars added in later fix cycles that aren't yet surfaced as chart values
  (tracked by #1416 — documented but needs values plumbing):
  `SESSION_STREAM_MAX_PER_CALLER`, `CONVERSATION_STREAM_{QUEUE_MAX,RING_MAX,GRACE_SEC,KEEPALIVE_SEC}`,
  `WEBHOOK_RETRY_BYTES_PER_SUB`, `WEBHOOK_ALLOW_LOOPBACK_HOSTS`,
  `A2A_SESSION_CONTEXT_CACHE_MAX`, `TASKS_SHUTDOWN_DRAIN_TIMEOUT`,
  `JOBS_SHUTDOWN_DRAIN_TIMEOUT`, `CONTINUATIONS_SHUTDOWN_DRAIN_TIMEOUT`,
  `MCP_HELM_REPO_URL_ALLOWLIST`, `MCP_HELM_ALLOW_ANY_REPO`,
  `MCP_K8S_READ_SECRETS_DISABLED`, `MCP_PROM_MAX_RESPONSE_BYTES`.
  All read via `os.environ.get(...)` at startup; no hot-reload.
- **Metric catalog** — per-service `metrics.py`; rendered dashboards at `charts/witwave/dashboards/`;
  default alert thresholds at `charts/witwave/templates/prometheusrule.yaml`.
- **Event stream wire contract** — `docs/events/README.md` + `docs/events/events.schema.json`.
- **HTTP route surface** — each service's `main.py` route declarations; harness
  `/.well-known/agent-runs.json` for runtime discovery of ad-hoc-run endpoints.
- **Architecture decisions + historical context** — `git log -- <file>` on the area you're looking
  at; GitHub issue tracker (component labels match this file's taxonomy: `harness, claude, codex,
  gemini, dashboard, operator, charts, mcp, cli` + cross-cutting).
- **Operator CRD reference** — `operator/api/v1alpha1/*_types.go` Go types + generated CRD schemas
  under `operator/config/crd/bases/`; sample manifests in `operator/config/samples/`.
