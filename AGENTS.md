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

autonomous-agent is a multi-container autonomous agent platform. Each named agent (iris, nova, kira, …) consists of:

- A **harness** container — the infrastructure layer (A2A relay, heartbeat scheduler, job scheduler). It owns
  no LLM itself; it forwards all work to a backend.
- One or more **backend** containers (`claude`, `codex`, `gemini`) — the LLM execution layer. Each backend is a full A2A
  server that manages its own sessions, memory, conversation logs, and Prometheus metrics.
- Zero or more **MCP** containers (`mcp-kubernetes`, `mcp-helm`, …) — the tool layer. Each MCP server exposes a
  focused set of cluster- or system-level capabilities to backends over the Model Context Protocol. All entries
  under `tools/` are equal MCP components; backends opt into them via their own MCP configuration.

Multiple named agents can collaborate as a team via the A2A protocol, but the named agent (nyx + its backends) is the
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

Prompts can land in `.nyx/{jobs,tasks,triggers,continuations,webhooks}/` (or at `HEARTBEAT.md`) via two paths:

1. **gitSync materialisation** — a gitSync sidecar rsyncs `.md` files from a repo.
2. **`NyxPrompt` CR** (operator-only) — declarative Kubernetes resource that binds one prompt to one or more `NyxAgent`s;
   the operator reconciles a ConfigMap per `(NyxPrompt, agent)` pair that mounts at the same path. See
   `operator/README.md#the-nyxprompt-resource` and `operator/config/samples/nyx_v1alpha1_nyxprompt.yaml`.

harness retains no LLM of its own. All conversation state, session continuity, memory, and conversation logging
live in the backend container.

Every container in the stack exposes `/metrics` on a **dedicated port** (9000 by default, set via `METRICS_PORT`
env / `metrics.port` chart value / `NyxAgentSpec.MetricsPort` CRD field) separate from the app listener (#643).
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
- Exposes `/conversations`, `/trace`, `/mcp`, and claude's `/api/traces[/<id>]` guarded by the same bearer token
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
      "kubernetes": { "url": "http://nyx-mcp-kubernetes:8000" }
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
and a `mcpTools.<name>` block in `charts/nyx/values.yaml`. Tag related issues/PRs with the `mcp` GitHub label.

### Routing configuration

`backend.yaml` (in `.nyx/`) controls which backend handles each concern:

```yaml
backend:
  agents:
    - id: claude
      url: http://localhost:8010
      model: claude-opus-4-6

    - id: codex
      url: http://localhost:8011
      model: gpt-5.1-codex

    - id: gemini
      url: http://localhost:8012

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
├── .nyx/                    # Runtime config (mounted into harness)
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
└── test/                    # Test agents: bob
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
│   ├── Dockerfile
│   ├── server.py
│   └── requirements.txt
└── helm/                    # mcp-helm — Helm release management via the CLI
    ├── Dockerfile
    ├── server.py
    └── requirements.txt

dashboard/                   # Vue 3 + Vite + PrimeVue web interface
charts/                      # Helm charts
├── nyx/                     # nyx Helm chart (deploys agents to Kubernetes)
└── nyx-operator/            # nyx-operator Helm chart (deploys the NyxAgent controller)
operator/                    # Kubernetes operator (Go) — reconciles NyxAgent CRDs
shared/                      # Shared Python modules mounted into harness + backends + MCP tools
                             #   otel.py           — OpenTelemetry bootstrap and helpers (#469)
                             #   metrics_server.py — dedicated /metrics listener on :9000 (#643);
                             #                       asyncio-task variant for harness/backends,
                             #                       daemon-thread variant for MCP tools
                             #   log_utils.py      — structured log append helpers
                             #   exceptions.py, conversations.py
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
docker build -f dashboard/Dockerfile -t dashboard:latest .

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
  && helm upgrade --install nyx ./charts/nyx -f ./charts/nyx/values-test.yaml -n nyx --create-namespace
```

## Interacting with Agents

Use the `/remote` skill to interact with running agents. Always target the **nyx agent by name** — nyx routes the
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
`charts/nyx/values-test.yaml` yet. Port assignments will land when they're
deployed.

The `/remote` skill derives the session ID automatically from the current Claude Code session. Pass it explicitly only
when you need to target a specific session.

## Memory

Each backend manages its own memory under `.agents/<env>/<name>/<backend>/memory/` (e.g.
`.agents/active/iris/claude/memory/`). For `claude` and `codex`, memory files are markdown documents. For `gemini`, conversation history is stored as JSON in `memory/sessions/`. Memory files are not committed to source control. harness has no memory layer of its own.

## Metrics landscape

The three backends share a common `backend_*` metric baseline; per-backend extensions are aligned so
cross-backend dashboards can union on `(agent, agent_id, backend)` without backend-specific names.

- **claude (superset).** Adds `backend_hooks_denials_total` (canonical cross-backend deny counter, paired
  with the legacy `backend_hooks_blocked_total` alias through one release cycle), `backend_mcp_requests_total`
  / `backend_mcp_request_duration_seconds` (per-request `/mcp` observability, peer-parity with gemini),
  `backend_sqlite_task_store_lock_wait_seconds` (mirrors gemini's series so lock pressure can be diffed),
  `backend_empty_prompts_total`, `backend_stderr_lines_per_task`, `backend_tasks_with_stderr_total`,
  `backend_task_retries_total`, `backend_sdk_context_fetch_errors_total`,
  `backend_log_write_errors_by_logger_total`, and `backend_sdk_subprocess_spawn_duration_seconds`. All
  histograms declare explicit bucket tuples rather than relying on Prometheus defaults.
- **claude.** Adds `backend_webhook_timeout_total` (webhook delivery timeouts surfaced separately from
  generic errors), `backend_allowed_tools_reload_total` (count of allow-list hot-reloads), and
  `nyxprompt_status_patch_conflicts_total` continues to be tracked on the operator side.
- **codex.** Adds `backend_empty_prompts_total`, `backend_stderr_lines_per_task`,
  `backend_tasks_with_stderr_total`, `backend_task_retries_total`, `backend_sdk_context_fetch_errors_total`,
  `backend_log_write_errors_by_logger_total`, `backend_sdk_subprocess_spawn_duration_seconds`
  (zero-value placeholder — the Agents SDK runs in-process), `backend_hook_post_shed_total`
  (hook.decision POSTs shed when the async dispatcher queue is saturated, #928), and peer-parity
  placeholder hook metrics (`backend_hooks_warnings_total`, `backend_hooks_config_*`,
  `backend_hooks_active_rules`, `backend_hooks_evaluations_total`, `backend_tool_audit_entries_total`).
  The legacy `backend_codex_hooks_denials_total` alias is retained alongside the canonical
  `backend_hooks_denials_total` during migration; emission is now gated by `EMIT_DEPRECATED_HOOK_METRICS`
  (#940).
- **gemini.** Adds `backend_empty_prompts_total`, `backend_sdk_subprocess_spawn_duration_seconds`,
  `backend_sdk_tokens_per_query`, `backend_sdk_tool_call_input_size_bytes`,
  `backend_sdk_tool_result_size_bytes`, `backend_mcp_server_up`, and `backend_mcp_server_exits_total`
  (per-stdio-server liveness and exit reasons, #816). Hook counters carry a `source` label for
  baseline-vs-extension disambiguation, matching claude's schema.
- **Cross-backend alignment.** `backend_sdk_tool_calls_per_query` now carries the `model` label on every
  backend (#795). `backend_sdk_tool_calls_total` and `backend_sdk_tool_errors_total` share label schema
  `(agent, agent_id, backend, tool)`. Command allow-list rejections surface on every backend as
  `backend_mcp_command_rejected_total{reason}` (claude #711, codex #720, gemini #730).

## Environment variables added this cycle

Set on the **harness** container:

| Variable                                   | Purpose                                                                                                         |
| ------------------------------------------ | --------------------------------------------------------------------------------------------------------------- |
| `HOOK_EVENTS_AUTH_TOKEN`                   | Canonical bearer token required on the internal hook-decision endpoint (`/internal/events/hook-decision`) backends POST to. Unset = refuse (#712, #933). `HARNESS_EVENTS_AUTH_TOKEN` is kept as a back-compat alias and logs a deprecation warning when only the old name is set (#859). The endpoint was moved onto the dedicated metrics listener and enforces per-field length caps on the POST body (#924). |
| `ADHOC_RUN_AUTH_TOKEN`                     | Bearer token for `POST /heartbeat/run`, `/jobs/<name>/run`, `/tasks/<name>/run`, `/triggers/<name>/run`. Unset = refuse (#700). Ad-hoc run handlers now also honour the `backends_ready` warmup shield — calls during backend warmup return 503 instead of racing the executor (#925, #955). Advertised in `/.well-known/agent-runs.json` (#956). |
| `SESSION_ID_SECRET`                        | Server-side HMAC key for `shared/session_binding.derive_session_id`. When set, session IDs on `/mcp` are bound to the caller identity so a caller cannot hijack another caller's session (#867/#929/#935/#941). Leave unset only in single-tenant dev. |
| `CORS_ALLOW_WILDCARD`                      | Explicit ack for `CORS_ALLOW_ORIGINS=*`; template refuses the wildcard otherwise (#701).                        |
| `A2A_MAX_PROMPT_BYTES`                     | Reject A2A prompts above this byte size at ingress; default 1 MiB, `0` disables (#783).                         |
| `CONTINUATION_MAX_CONCURRENT_FIRES_GLOBAL` | Hard cap on in-flight continuation fires across all items; protects against fan-out storms (#781).              |

Set on the **backend** containers:

| Variable                                   | Purpose                                                                                                         |
| ------------------------------------------ | --------------------------------------------------------------------------------------------------------------- |
| `CONVERSATIONS_AUTH_DISABLED`              | Escape hatch to run without the `CONVERSATIONS_AUTH_TOKEN` guard; loud startup log for visibility (#718).       |
| `LOG_REDACT`                               | When truthy, conversation and response logs wrap user-prompt / agent-response content in redaction (#714). `_redact_manifest` returns a safe placeholder when an MCP manifest fails to parse (#918); `_looks_like_secret_key` skips common false-positives like `authMode` / `tokenAudience` (#920). |
| `LOG_TRACE_CONTENT_MAX_BYTES`              | Per-entry cap on `tool_result` content serialised into trace logs; prevents a pathological tool output from bloating `tool-activity.jsonl` (#939). |
| `EMIT_DEPRECATED_HOOK_METRICS`             | Codex-only. Gates emission of the legacy `backend_codex_hooks_denials_total` counter. Default off; set to `true` for one release cycle while dashboards migrate to `backend_hooks_denials_total` (#940). |
| `GEMINI_MAX_HISTORY_BYTES`                 | Byte ceiling on the JSON session history file gemini persists per session; older turns truncated to fit.        |
| `MCP_ALLOWED_COMMANDS` / `MCP_ALLOWED_COMMAND_PREFIXES` | Command allow-list for stdio MCP entries in `mcp.json`; configured per-backend (#711/#720/#730).   |
| `MCP_ALLOWED_CWD_PREFIXES`                 | CWD allow-list for stdio MCP entries; rejections counted on `backend_mcp_command_rejected_total{reason}`.       |

Set on **MCP tool** containers:

| Variable                                   | Purpose                                                                                                         |
| ------------------------------------------ | --------------------------------------------------------------------------------------------------------------- |
| `MCP_TOOL_AUTH_TOKEN`                      | Bearer token required on every MCP tool HTTP request; unset + `MCP_TOOL_AUTH_DISABLED` unset = refuse (#771).   |
| `MCP_TOOL_AUTH_DISABLED`                   | Explicit ack for running MCP tools with no auth (local dev).                                                    |

## Chart values added this cycle

- `cors.allowOrigins` + `cors.allowWildcard` (charts/nyx, #763/#701) — first-class harness CORS policy,
  validated by `values.schema.json`.
- `storage.retainOnUninstall` (#767) — annotates every chart-owned PVC with `helm.sh/resource-policy=keep`
  so `helm uninstall` leaves conversation logs / memory intact on clusters with delete-reclaim defaults.
- `mcpTools.<name>.image.digest` (#855) — immutable digest pin per MCP tool; when set the template renders
  `repository@<digest>` and ignores `tag`.
- `mcpTools.<name>.rbac.create` (#762) — default-on minimal baseline RBAC per MCP tool so enabling a tool
  doesn't 403 out of the box; set `create: false` + `serviceAccountName` to manage RBAC out-of-band.
- `mcpTools.<name>.automountServiceAccountToken` (#856) — three-state override for the in-pod SA token,
  for IRSA / workload-identity setups where the projected token should be suppressed.
- `rbac.secretsWrite` (charts/nyx-operator, #761) — toggles the Secret write verbs (`create`/`delete`/
  `patch`/`update`) on the operator Role/ClusterRole. Read verbs are always granted. Set to `false` when
  all backend credentials are pre-provisioned Secrets referenced via `existingSecret`.

## Endpoint additions

- **harness** — `GET /.well-known/agent-runs.json` discovery doc enumerates the ad-hoc run endpoints
  (`/jobs/<name>/run`, `/tasks/<name>/run`, `/triggers/<name>/run`) that are now guarded by
  `ADHOC_RUN_AUTH_TOKEN` (#700).
- **claude** — `GET /api/traces` and `GET /api/traces/<id>` now require `CONVERSATIONS_AUTH_TOKEN` (parity
  with the other protected endpoints).
- **MCP tool servers** — `GET /health` on every tool container for liveness probes and a bearer-auth
  middleware in front of every request.

## Security hardening

- **Webhook SSRF DNS pinning** — the URL guard in harness (#524) resolves webhook hostnames at delivery
  time and refuses to send to the resolved IP when it is private / loopback / link-local / reserved,
  catching DNS-rebind attacks that check the hostname once and then flip the A record.
- **MCP command + cwd allow-list** — every stdio entry parsed out of `mcp.json` is checked against
  `MCP_ALLOWED_COMMANDS` / `MCP_ALLOWED_COMMAND_PREFIXES` / `MCP_ALLOWED_CWD_PREFIXES` on all three
  backends; rejections increment `backend_mcp_command_rejected_total{reason}`. The default allow-list is
  pruned to `mcp-kubernetes,mcp-helm,uv,uvx` (#862) and the absolute-path basename fallback was removed —
  an entry like `/usr/local/bin/uvx` no longer passes solely because `uvx` is allow-listed; the full path
  must match a `MCP_ALLOWED_COMMAND_PREFIXES` entry. `mcp_command_args_safe()` additionally vets
  interpreter invocations (`python -c`, `node -e`, etc.) against an args deny-list so the allow-list
  cannot be bypassed via a permitted interpreter (#930).
- **Operator Secret RBAC split** — `rbac.secretsWrite=false` in charts/nyx-operator drops the Secret write
  verbs while keeping reads. Credential Secrets carry `app.kubernetes.io/component: credentials` and are
  dual-checked (label + `IsControlledBy`) before any update or delete so user-created Secrets are never
  touched.
- **PodMonitor teardown on operator-disable** — when `spec.enabled=false` on a NyxAgent, the reconciler
  now tears down the optional `PodMonitor` and `ServiceMonitor` CRs alongside everything else.

## Cycle 3 additions (#975–#1127)

### New environment variables

Harness:

| Variable                             | Purpose                                                                                                                          |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------- |
| `LOG_REDACT_HIGH_ENTROPY`            | Opt-in gate for the catch-all high-entropy redaction pattern; UUID/OTel trace/span IDs are shielded regardless (#1034).         |
| `PARSE_FRONTMATTER_MAX_FILE_BYTES`   | Per-file size cap (default 128 KiB) before `parse_frontmatter` reads a scheduler `.md` — defends against YAML-bomb surface (#1038). |

Backends:

| Variable                                   | Purpose                                                                                                     |
| ------------------------------------------ | ----------------------------------------------------------------------------------------------------------- |
| `OPENAI_API_KEY_FILE`                      | Codex-only. Path to a mounted file holding the OpenAI key; watched for change so rotation no longer requires pod restart (#728, parity with gemini #1057). |
| `GEMINI_API_KEY_FILE`                      | Gemini-only. Path to a mounted file holding the Gemini API key; watcher calls `_close_client` on change (#1057). |
| `GEMINI_AFC_HISTORY_SOFT_CAP_BYTES`        | Gemini soft ceiling on `chat.history` bytes during AFC ping-pong; raises `BudgetExceededError` when exceeded (default 2 MiB, #1058). |
| `SESSION_ID_SECRET_PREV`                   | Previous-generation HMAC key for `SESSION_ID_SECRET` rotation windows (#1042). When set and different from the current secret, `derive_session_id_candidates()` returns `[current, prev]` so backends probe both at lookup time; new writes always land on the current-secret id. Once `note_prev_secret_hit` stops firing across your session-retention window, remove this env var. `SESSION_PREV_HIT_WARN_REARM_EVERY` (default 500) controls the WARN re-arm cadence. |
| `SQLITE_TASK_STORE_BUSY_TIMEOUT_MS`        | Codex SqliteTaskStore busy-timeout tuning; replaces the previous process-wide asyncio.Lock with per-thread connections (#726). |
| `BROWSER_CONTEXT_MAX_IDLE_SECONDS`         | Codex BrowserPool idle-release sweep interval — prevents linear RSS growth under per-session Chromium caching (#1053). |
| `COMPUTER_MAX_CONTEXTS`                    | Codex hard cap on concurrent Chromium contexts, independent of the LRU session count (#1053).               |
| `ALLOWED_TOOLS`                            | Gemini allow-list scaffold mirrored from claude; wired with `backend_allowed_tools_reload_total` counter ahead of hand-rolled AFC retrofit (#1100). |

MCP tool containers:

| Variable                                    | Purpose                                                                                                     |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `MCP_SUBPROCESS_TIMEOUT_SEC`                | Unified subprocess / API-call timeout for mcp-helm + mcp-kubernetes; falls back to legacy `HELM_SUBPROCESS_TIMEOUT_SECONDS` when set (#778, #857). |
| `MCP_RESPONSE_MAX_BYTES`                    | Response-size cap applied to every query/list/get tool output (default 8 MiB, 0 disables, #778).            |
| `MCP_LOGS_TAIL_LINES_MAX`                   | mcp-kubernetes hard cap on `logs()` tail_lines (default 50 000, #778).                                      |
| `MCP_READ_ONLY` / `MCP_HELM_READ_ONLY` / `MCP_KUBERNETES_READ_ONLY` | Maintenance-mode switch — keeps read tools available while rejecting mutating tools with a clear error (#1123). |
| `MCP_AUDIT_LOG_PATH`                        | Durable JSONL audit sink path for privileged ops (`read_secret_value`, `apply`, `delete`, `install`, `upgrade`, `rollback`, `uninstall`) — independent of OTel (#1125). |
| `MCP_TOOL_BUDGET_<SERVER>_<TOOL>`           | Per-(server, tool) rolling call budget expressed as `N/<duration>` (s/m/h/ms); raises `CallBudgetExhausted` before concurrency-cap acquisition and bumps `mcp_tool_budget_exhausted_total` (#1124). |
| `HELM_VALUES_TMPDIR`                        | Override for the tmpfs/emptyDir path used by mcp-helm when falling back from `helm --values=-` stdin — lets operators target a memory-backed volume so values never touch disk (#1081). |
| `PROMETHEUS_URL` / `PROMETHEUS_BEARER_TOKEN` | Upstream endpoint + optional bearer for the new `mcp-prometheus` tool scaffold (#853).                     |

Operator:

| Variable          | Purpose                                                                                                     |
| ----------------- | ----------------------------------------------------------------------------------------------------------- |
| (annotation)      | `nyx.ai/reconcile-paused=true` on a NyxAgent short-circuits the reconcile before finalizer logic; deletions still proceed so paused agents remain removable (#1113). |
| (annotation)      | `nyx.ai/credentials-checksum` is now stamped on backend pod templates — a rotated `existingSecret` triggers a rolling restart via controlled checksum drift (#1114). |

### New chart values

- `podSecurity.readOnlyRootFilesystem` flipped to `true` by default; `charts/nyx/templates/_helpers.tpl` ships a
  `nyx.hardenedContainerSecurityContext` helper applied uniformly to every container + initContainer for
  PSS-restricted compliance (#1073).
- `mcpTools.<name>.rbac.clusterWide` (default `false`) — defaults to namespace-scoped `Role`+`RoleBinding`;
  set to `true` to revert to `ClusterRole`+`ClusterRoleBinding` (#1074).
- `mcpTools.<name>.networkPolicy.ingress.allowNyxAgents` — default ingress rule letting only pods carrying
  `app.kubernetes.io/part-of: nyx` reach MCP tool Services on `:8000` (#1074).
- `mcpTools.<name>.{nodeSelector,tolerations,affinity,topologySpreadConstraints,priorityClassName}` —
  scheduling values wired for mcp-tools pods (#1116).
- `mcpTools.prometheus.*` — toggle block for the new `mcp-prometheus` scaffold (disabled by default, #853).
- `serviceMesh.{enabled,type}` (both charts) — single knob rendering linkerd / istio / none sidecar-injection
  annotations uniformly across agents, mcp-tools, dashboard, and operator pods (#1121).
- `prometheusRule.alerts.{BackendDown,HookDenialSpike,McpAuthFailure,WebhookTimeout,LockWaitSaturation}` —
  opinionated default PrometheusRule template (disabled by default, individually toggleable, #1117).
- `dashboard.traceApiAllowCrossOrigin` + `dashboard.nginx.cspConnectSrc` — explicit opt-in required for
  cross-origin `traceApiUrl`; same-origin by default (#1061).
- `agents[].gitSyncs[].repo` now rejects `^https?://[^/]*:[^/]*@` at render time — credentials-in-URL are
  refused; use `existingSecret` + `GITSYNC_REPO` env injection (#1077).
- `values.schema.json` — `agents[].name` and `agents[].backends[].name` enforce DNS-1123
  `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` + `maxLength: 63` (#1023).

### New endpoints

- **harness** — `POST /validate` dry-run parses supplied frontmatter and returns structured errors without
  registering the item; bearer-gated by `ADHOC_RUN_AUTH_TOKEN` (#1088).
- **harness** — `/jobs`, `/tasks`, `/heartbeat`, `/continuations` snapshot responses now carry
  `next_fire` / `last_fire` / `last_success` fields (#1087).
- **harness** — `/.well-known/agent-runs.json` discovery doc extended with `/webhooks` and `/continuations`
  introspection surfaces (#1086).
- **MCP tool servers** — `GET /info` on both mcp-helm and mcp-kubernetes advertises image/SDK/tool versions,
  helm-diff plugin presence, feature flags, and the tool list; bearer-gated (#1122).
- **mcp-helm** — `diff_manifest(manifest, redact)` tool accepts raw YAML and reports what would change via
  `kubectl diff -f -` (#1127).

### New metrics

- `harness_hook_decision_dropped_total` (#1085), `harness_hook_decision_listener_errors_total{listener,error}`
  + `harness_hook_decision_listener_dup_rejects_total` (#1036).
- `harness_prompt_env_substitutions_total{var,result=hit|missing|denied}` (#1089).
- `backend_session_caller_cardinality` gauge — unique caller-identity hashes per backend; detects the
  single-tenant-token collapse where every caller maps to one identity (#1049).
- `backend_session_binding_fallback_total{reason}` — fires when `SESSION_ID_SECRET` is set but a request
  lacks `caller_identity` and falls back to legacy uuid5 (#1103).
- `backend_mcp_outbound_requests_total{server,result}` + `backend_mcp_outbound_request_duration_seconds` —
  backend-as-MCP-client observability of mcp-kubernetes / mcp-helm calls, separate from SDK-internal tools (#1104).
- `backend_streaming_chunks_dropped_total` — claude parity with codex #724 (#1091).
- `backend_sdk_info{sdk,version}` Info gauge across all three backends (#1092).
- `backend_hook_session_missing_total` — codex bump when `_current_session_id` ContextVar is empty (#1052).
- `backend_hooks_config_errors_total{reason='predicate_runtime'}` — codex bump on baseline-predicate
  exception previously silently swallowed (#1055).
- `backend_session_history_reset_total` — gemini WARN-path counter for silent save-history resets (#1000).
- `mcp_tool_budget_exhausted_total{server,tool}` (#1124).
- `mcp_k8s_token_reload_total` + `mcp_discovery_reload_total` — MCP-kubernetes SA token and CRD discovery
  cache refresh counters tied to the 401 / NoKindMatchError reload-once wrappers (#1082, #1083).
- `helm_subprocess_duration_seconds{command,outcome}` (#1126) and
  `k8s_api_call_duration_seconds{verb,resource,outcome}` (#1126) — inner-work histograms beside the existing
  handler-span duration.
- `nyxagent_credential_rotations_total{namespace,name}` (#1114), `nyxagent_leader{pod}` gauge (#1115),
  `nyxprompt_webhook_index_fallback_total` (#1069), `nyxagent_manifest_owner_ref_skipped_no_uid_total{namespace}` (#1016).

### Additional security hardening

- **REPLACE_ME guard covers `valueFrom` + `envFrom` + `credentials.existingSecret`** — not just inline
  `env[].value` (#1072). Denylist now matches `*REPLACE_ME*`, `*test*`, and `nyx-test-credentials`; a chart
  render against a test-shaped Secret reference fails.
- **Webhook-cert checksum annotation** on MutatingWebhookConfiguration / ValidatingWebhookConfiguration —
  rolls when `caBundle` / `existingSecret` / cert-manager config changes; deprecates `installCRDs` in favour
  of native Helm `crds/` directory (#1075).
- **Shell env denylist expanded** with `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` / `NO_PROXY` /
  `SSL_CERT_FILE` / `REQUESTS_CA_BUNDLE` / `CURL_CA_BUNDLE` / `SSLKEYLOGFILE` / `NODE_EXTRA_CA_CERTS` —
  closes prompt-injection-driven exfiltration path via transport-layer env overrides (#1054).
- **MCP command-args-safe script rejection** — positional `*.py` / `*.js` / `*.sh` rejected unless under
  `MCP_ALLOWED_CWD_PREFIXES`; bare `-` stdin scripts rejected. Allow-listed interpreter is no longer a
  near-universal entry point (#1046).
- **`diff()` redactor state-machine hardened** against unified-diff file-headers and hunk markers
  (`--- a/`, `+++ b/`, `@@`) that previously reset `in_secret=False` mid-Secret hunk (#1078, #1031, #1028).
- **SSA migration on key operator apply paths** — Deployment + Service reconcile use
  `client.Apply + FieldOwner("nyx-operator")` so field ownership is tracked cleanly and user/operator
  conflicts surface as explicit apply errors (#751).
- **`readyz` tied to webhook certwatcher** — operator pod isn't ready until `GetCertificate` returns a
  valid leaf (parses + checks `NotBefore` / `NotAfter`) (#758).

### New components

- **mcp-prometheus** — new MCP tool at `tools/prometheus/` wrapping the Prometheus HTTP API. Exposes
  `query`, `query_range`, `series`, `labels`, `label_values` via FastMCP streamable-http on `:8000`.
  Disabled by default in the chart; enable via `mcpTools.prometheus.enabled=true` (#853 scaffold; Grafana /
  Loki / OTel wrappers are follow-up).

### New operator CRD surfaces (scaffolded, not complete parity)

- `NyxAgentSpec.MCPTools` — scaffold CRD fields mirroring chart `mcpTools` (`Enabled`, `Image`,
  `Replicas` on each of `Kubernetes`, `Helm`); reconciler renders Deployment + Service per enabled tool
  via SSA (#830). Full-parity RBAC/scheduling/image.digest + delete path are follow-up.
- `NyxAgentSpec.NetworkPolicy` — scaffold fields (`Enabled`, `Ingress.{AllowDashboard, AllowSameNamespace,
  MetricsFrom, AdditionalFrom}`, `EgressOpen`) with pure `buildNetworkPolicy()` + 5 unit tests (#971).
  MCP-tool NetworkPolicies + explicit egress rules are follow-up.
- `NyxAgentSpec.Dashboard.Ingress` + `Auth.Mode` — fail-closed gate via `EvaluateDashboardIngressAuth`;
  emits `DashboardIngressAuthRequired` / `DashboardIngressUnauthenticated` events. Full Ingress + Secret
  render is follow-up (#831).
- `operator/cmd/plan` — new subcommand renders Deployment / Service / ConfigMaps / PVCs / HPA / PDB /
  dashboard / manifest CM for a given NyxAgent spec on stdout without applying (#1111).
