# autonomous-agent

A platform for building persistent, self-directed AI agents that can work autonomously on software projects — including
improving themselves.

The primary use case is autonomous software development: agents that can triage issues, implement features, fix bugs,
evaluate their own work, and iterate — continuously and without human intervention. The same platform can be pointed at
any software project, not just this one.

Agents are currently bootstrapped manually using AI CLI tools (Claude Code, Codex). The long-term goal is for the agents
to take over their own development cycle: evaluating the codebase, proposing improvements, implementing them, and
shipping — closing the loop without a human in the hot path.

---

Built on the [A2A protocol](https://a2a-protocol.org). Each named agent is a set of containers: a **nyx-harness**
infrastructure layer (A2A relay, heartbeat scheduler, job scheduler) and one or more **backend** containers that do the
actual LLM work (Claude Agent SDK via `a2-claude`, OpenAI Agents SDK via `a2-codex`, Google Gemini SDK via `a2-gemini`).

Multiple agents can collaborate as a team, but the named agent (nyx + its backends) is the deployable unit.

## Components

The platform has eight components, each with its own source directory:

| Component          | Directory              | Type               | Description                                                                                                                 |
| ------------------ | ---------------------- | ------------------ | --------------------------------------------------------------------------------------------------------------------------- |
| **Orchestrator**   | `harness/`             | Orchestrator agent | nyx-harness: the infrastructure and routing layer. Owns scheduling, triggering, chaining, and A2A relay. No LLM of its own. |
| **Claude backend** | `backends/a2-claude/`           | Backend agent      | Executes prompts via the Claude Agent SDK. Manages sessions, memory, conversation logs, and metrics.                        |
| **Codex backend**  | `backends/a2-codex/`            | Backend agent      | Executes prompts via the OpenAI Agents SDK. Supports web search and headless browser via Playwright.                        |
| **Gemini backend** | `backends/a2-gemini/`           | Backend agent      | Executes prompts via the Google Gemini SDK. Manages sessions and conversation history.                                      |
| **Dashboard**      | `dashboard/`           | Web interface      | Vue 3 + PrimeVue app for monitoring metrics, browsing agents, viewing conversations, and chatting with agents.              |
| **Operator**       | `operator/`            | Kubernetes operator| Go controller (Operator SDK) that reconciles `NyxAgent` custom resources into the same workloads the Helm chart renders.     |
| **Agent chart**    | `charts/nyx/`          | Deployment         | Kubernetes Helm chart for deploying nyx agents via templated manifests (no CRDs).                                            |
| **Operator chart** | `charts/nyx-operator/` | Deployment         | Kubernetes Helm chart that installs the operator and the `NyxAgent` CRD.                                                     |

Each backend agent is a full A2A server. The orchestrator routes work to backends but does no LLM execution itself. The
dashboard provides visibility only — it does not participate in agent workflows. The operator and its chart are an alternative
install path to the agent chart; both target the same per-agent deployment shape.

## How It Works

Each agent:

- Runs as a nyx-harness container that receives A2A requests, fires heartbeats, and runs jobs
- Forwards all LLM work to a dedicated backend container (`a2-claude`, `a2-codex`, or `a2-gemini`)
- Exposes an [A2A (Agent-to-Agent)](https://a2a-protocol.org) interface for communication
- Has its own identity, memory, and configuration — none baked into the image

Backend containers:

- Are standalone A2A servers with their own session state, memory, and conversation logs
- Receive behavioral instructions via a backend-specific file (`CLAUDE.md` for a2-claude, `AGENTS.md` for a2-codex,
  `GEMINI.md` for a2-gemini) and A2A identity from a mounted `agent-card.md` file
- Expose `/health` and `/metrics` in addition to the A2A endpoints

## Requirements

- Docker
- Docker Compose
- A Claude Code OAuth token (`claude setup-token`) or Anthropic API key (for `a2-claude`)
- An OpenAI API key (for `a2-codex`)
- A Gemini API key (for `a2-gemini`)

## Container Images

Published images are available on GitHub Container Registry. Every image listed below is built and pushed
automatically on every release tag.

| Image            | Registry path                                     |
| ---------------- | ------------------------------------------------- |
| `nyx-harness`    | `ghcr.io/skthomasjr/images/nyx-harness:latest`    |
| `a2-claude`      | `ghcr.io/skthomasjr/images/a2-claude:latest`      |
| `a2-codex`       | `ghcr.io/skthomasjr/images/a2-codex:latest`       |
| `a2-gemini`      | `ghcr.io/skthomasjr/images/a2-gemini:latest`      |
| `dashboard`      | `ghcr.io/skthomasjr/images/dashboard:latest`      |
| `nyx-operator`   | `ghcr.io/skthomasjr/images/nyx-operator:latest`   |
| `git-sync`       | `ghcr.io/skthomasjr/images/git-sync:latest`       |
| `mcp-kubernetes` | `ghcr.io/skthomasjr/images/mcp-kubernetes:latest` |
| `mcp-helm`       | `ghcr.io/skthomasjr/images/mcp-helm:latest`       |

Pull a specific version with a semver tag, e.g. `ghcr.io/skthomasjr/images/nyx-harness:0.2.0-beta.37`.
The latest released tag is visible in the [GitHub Releases](https://github.com/skthomasjr/autonomous-agent/releases) page; substitute it for the version below.

## Helm Charts

Two Helm charts are published to GHCR alongside the images on every release tag:

```bash
# Agent chart — deploys nyx agents directly via templated manifests.
helm install nyx oci://ghcr.io/skthomasjr/charts/nyx --version 0.2.0-beta.37 --namespace nyx --create-namespace

# Operator chart — installs the nyx-operator controller and the NyxAgent CRD.
helm install nyx-operator oci://ghcr.io/skthomasjr/charts/nyx-operator --version 0.2.0-beta.37 --namespace nyx --create-namespace
```

See [charts/nyx/README.md](charts/nyx/README.md) and
[charts/nyx-operator/README.md](charts/nyx-operator/README.md) for full installation instructions.

## Getting Started

### 1. Pull or build the images

Pull published images:

```bash
docker pull ghcr.io/skthomasjr/images/nyx-harness:latest
docker pull ghcr.io/skthomasjr/images/a2-claude:latest
docker pull ghcr.io/skthomasjr/images/a2-codex:latest
docker pull ghcr.io/skthomasjr/images/a2-gemini:latest
```

Or build locally:

```bash
docker build -f harness/Dockerfile -t nyx-harness:latest .
docker build -f backends/a2-claude/Dockerfile -t a2-claude:latest .
docker build -f backends/a2-codex/Dockerfile -t a2-codex:latest .
docker build -f backends/a2-gemini/Dockerfile -t a2-gemini:latest .
```

### 2. Configure credentials

```bash
export CLAUDE_CODE_OAUTH_TOKEN=your-token-here
export OPENAI_API_KEY=your-key-here
export GEMINI_API_KEY=your-key-here
```

### 3. Start the agents

```bash
helm upgrade --install nyx ./charts/nyx -f ./charts/nyx/values-test.yaml -n nyx --create-namespace
```

### 4. Verify

```bash
# nyx-harness (router layer)
curl http://localhost:8000/.well-known/agent.json

# Claude backend for iris
curl http://localhost:8010/.well-known/agent.json
curl http://localhost:8010/health

# Codex backend for iris
curl http://localhost:8011/health

# Gemini backend for iris
curl http://localhost:8012/health
```

## Agent Structure

Active agents are defined under `.agents/active/`. Each named agent has its own directory containing nyx config, backend
instances, logs, and memory.

```text
.agents/
├── active/
│   ├── iris/          # Iris (nyx: 8000 | a2-claude: 8010 | a2-codex: 8011 | a2-gemini: 8012)
│   ├── nova/          # Nova (nyx: 8001 | a2-claude: 8020 | a2-codex: 8021 | a2-gemini: 8022)
│   └── kira/          # Kira (nyx: 8002 | a2-claude: 8030 | a2-codex: 8031 | a2-gemini: 8032)
└── test/
    └── bob/           # Bob  (nyx: 8099 | a2-claude: 8090 | a2-codex: 8091 | a2-gemini: 8092)
```

Each agent directory contains:

```text
<agent>/
├── .nyx/              # Runtime config (agent-card.md, backend.yaml, HEARTBEAT.md, jobs/)
├── .claude/           # Claude backend config (CLAUDE.md, agent-card.md, mcp.json, settings.json)
├── .codex/            # Codex backend config (AGENTS.md, agent-card.md, config.toml)
├── .gemini/           # Gemini backend config (GEMINI.md, agent-card.md)
├── logs/              # nyx-harness logs (runtime, not committed)
├── a2-claude/         # Claude backend instance
│   ├── logs/          # Conversation log (runtime, not committed)
│   └── memory/        # Persistent memory (runtime, not committed)
├── a2-codex/          # Codex backend instance
│   ├── logs/
│   └── memory/
└── a2-gemini/         # Gemini backend instance
    ├── logs/
    └── memory/
        └── sessions/  # JSON conversation history per session
```

## Routing Configuration

Each agent's `backend.yaml` (under `.nyx/`) controls where nyx routes each type of work:

```yaml
backend:
  agents:
    - id: iris-a2-claude
      url: http://iris-a2-claude:8080

    - id: iris-a2-codex
      url: http://iris-a2-codex:8080

    - id: iris-a2-gemini
      url: http://iris-a2-gemini:8080

  routing:
    default: iris-a2-claude # fallback backend when no per-concern override matches
    a2a: iris-a2-claude # handles incoming A2A requests
    heartbeat: iris-a2-claude # handles heartbeat-triggered work
    job: iris-a2-claude # handles job execution
    task: iris-a2-claude # handles task execution
    trigger: iris-a2-claude # handles inbound HTTP trigger requests
    continuation: iris-a2-claude # handles continuation-fired prompts
```

Routing values can be a plain agent ID string or an object with `agent:` and optional `model:` fields. Model resolution
order: per-message override → routing entry `model:` → per-backend config `model:`.

## Consensus Mode

Set `consensus:` in any prompt file's frontmatter to a list of backend entries. Each entry specifies a `backend` glob
pattern and an optional `model` override. The prompt is dispatched to every matched `(backend, model)` pair in parallel,
then the responses are aggregated:

- **Binary responses** (yes / no / agree / disagree variants): majority vote. The default backend breaks ties.
- **Freeform responses**: a synthesis prompt is dispatched to the default backend, which merges the collected responses
  into a single coherent answer.

```yaml
consensus:
  - backend: "iris-a2-claude"
    model: "claude-opus-4-6"
  - backend: "iris-a2-codex*" # glob — matches all codex backends
  - backend: "iris-a2-claude"
    model: "claude-haiku-4-5" # same backend, different model = two parallel calls
```

An empty list (the default when `consensus:` is omitted) disables consensus — the prompt is dispatched to the single
routing target. The same backend can appear twice with different models to compare outputs from different model sizes.

Use consensus mode for high-stakes decisions where you want more than one model family's perspective.

## Token Budget (max-tokens)

Set `max-tokens` in a job, task, or trigger frontmatter to cap cumulative token usage for that dispatch. When the
backend reports that usage has reached the limit, it stops processing and returns any partial response collected so far.
A `system` entry is written to `conversation.jsonl` recording how many tokens were consumed and what the limit was.

```yaml
---
name: daily-summary
schedule: "0 8 * * *"
max-tokens: 4000
---
Summarise the day's key events.
```

The value must be a positive integer. Invalid values are logged and ignored. The limit applies per-dispatch (not across
sessions), so each job/task/trigger invocation gets a fresh budget. All three backend types enforce it:

| Backend     | Token source                                       |
| ----------- | -------------------------------------------------- |
| `a2-claude` | `get_context_usage()` after each assistant turn    |
| `a2-codex`  | `event.data.usage.total_tokens` on response events |
| `a2-gemini` | `chunk.usage_metadata.total_token_count` per chunk |

## Adding an Agent

1. Copy an existing agent directory:

   ```bash
   cp -r .agents/active/iris .agents/active/<name>
   ```

2. Update the agent's `agent-card.md` in `.nyx/` (mounted at `/home/agent/.nyx/agent-card.md`) with the agent's identity
   and role; update each backend's `agent-card.md` in `.claude/`, `.codex/`, and `.gemini/` if those directories are
   used

3. Update the backend instruction files: `CLAUDE.md` (at `/home/agent/.claude/CLAUDE.md`), `AGENTS.md` (at
   `/home/agent/.codex/AGENTS.md`), and `GEMINI.md` (at `/home/agent/.gemini/GEMINI.md`) with backend-specific
   behavioral instructions

4. Update `.agents/active/<name>/.nyx/backend.yaml` with the new agent's backend service names and URLs

5. Add the agent to `charts/nyx/values-test.yaml` (or your own overrides file) with its backends, config, and storage

6. Register the agent in `.agents/active/manifest.json`

7. Deploy:

   ```bash
   helm upgrade --install nyx ./charts/nyx -f ./charts/nyx/values-test.yaml -n nyx
   ```

## Communication

Agents communicate over the [A2A protocol](https://a2a-protocol.org) via JSON-RPC. Each nyx agent exposes:

- `/.well-known/agent.json` — agent card (identity and capabilities)
- `/` — A2A JSON-RPC endpoint (`message/send`)
- `GET /health/start` — startup probe: 200 once ready, 503 while initializing
- `GET /health/live` — liveness probe: always 200 with `{"status": "ok", "agent": ..., "uptime_seconds": ...}`
- `GET /health/ready` — readiness probe: 200/`{"status": "ready"}`; 503/`{"status": "starting"}` while initializing;
  503/`{"status": "degraded"}` when a backend is unhealthy
- `GET /agents` — own agent card plus agent cards from all configured backends
- `GET /jobs` — structured snapshot of all registered scheduled jobs (name, cron, backend, running state)
- `GET /tasks` — structured snapshot of all registered scheduled tasks (name, days, window, running state)
- `GET /webhooks` — structured snapshot of all registered webhook subscriptions (name, url, filters, active deliveries)
- `GET /continuations` — structured snapshot of all registered continuation items (name, continues-after, filters,
  active fires)
- `GET /triggers` — structured snapshot of all registered inbound trigger endpoints (name, endpoint, description,
  session, backend, running state)
- `GET /heartbeat` — current heartbeat configuration from `HEARTBEAT.md`
- `GET /conversations` — merged conversation log from all backends
- `GET /trace` — merged trace log from all backends
- `GET /.well-known/agent-triggers.json` — discovery array of all enabled trigger descriptors

Cross-agent views (`/team`, `/proxy/<name>`, `/conversations/<name>`, `/trace/<name>`) were retired in beta.46 —
the dashboard pod fans out directly to each agent and owns cross-agent routing (#470).

Each backend container additionally exposes:

- `GET /health` — health check: 200/`{"status": "ok", "agent": ..., "uptime_seconds": ...}` or
  503/`{"status": "starting"}` while initializing
- `GET /metrics` — Prometheus metrics (when `METRICS_ENABLED` is set)
- `POST /mcp` — MCP JSON-RPC server (`initialize`, `tools/list`, `tools/call` with a single `ask_agent` tool); allows
  MCP hosts (Claude Desktop, Cursor, VS Code extensions) to invoke the agent as a tool without going through
  nyx-harness. **All three backends require a bearer token** (`CONVERSATIONS_AUTH_TOKEN`) on `/mcp` (#510, #516, #518);
  the shared token guard also gates `/conversations` and `/trace`. If the env var is left empty the backend logs a
  startup warning (#517) — set a non-empty token in production.

## Memory

Each backend agent manages its own memory at `.agents/<env>/<name>/<backend>/memory/`. For `a2-claude` and `a2-codex`,
memory files are markdown documents. For `a2-gemini`, conversation history is stored as JSON in `memory/sessions/`.
Memory files are not committed to source control. nyx-harness has no memory layer of its own.

## Authentication

| Service   | Method             | Environment variable                 |
| --------- | ------------------ | ------------------------------------ |
| a2-claude | Claude Max (OAuth) | `CLAUDE_CODE_OAUTH_TOKEN`            |
| a2-claude | Anthropic API key  | `ANTHROPIC_API_KEY`                  |
| a2-codex  | OpenAI API key     | `OPENAI_API_KEY`                     |
| a2-gemini | Gemini API key     | `GEMINI_API_KEY` or `GOOGLE_API_KEY` |

## Security

Three cross-cutting security gates land in this cycle. Each is configured via environment variables; all three ship
default-closed but with a warning when left unconfigured so an oversight is loud rather than silent.

### Backend `/mcp`, `/conversations`, `/trace` bearer auth

All three backends (`a2-claude`, `a2-codex`, `a2-gemini`) now require a bearer token on the `/mcp`, `/conversations`,
and `/trace` endpoints — **parity across backends** (#510, #516, #518). Set `CONVERSATIONS_AUTH_TOKEN` on every backend
container. If it is unset or empty the backend logs a startup warning (#517) and the shared guard in
`shared/conversations.py` refuses to serve the protected endpoints.

nyx-harness forwards inbound `/conversations` and `/trace` reads to the backends — set
`BACKEND_CONVERSATIONS_AUTH_TOKEN` on the harness to match so the aggregated reads continue to work. See the
environment variable tables below.

### Webhook URL allow-list and scheme guard (#524)

Outbound webhooks now go through an SSRF-resistant URL check. See [URL safety (#524)](#url-safety-524) under Outbound
Webhooks for the full rules. Migration: any webhook markdown that previously used `url: http://{{env.FOO}}/…` must be
rewritten to use `url-env-var: FOO` — `{{env.*}}` substitutions in the `url:` field are no longer honoured. Private /
loopback / link-local / reserved destinations can be explicitly opted in via the `WEBHOOK_URL_ALLOWED_HOSTS` env var
on nyx-harness (comma-separated `host` or `host:port` entries).

### Dashboard Ingress (#528)

The dashboard Ingress template is default-closed: `ingress.enabled=true` fails template render unless one of the
following is configured — chart-managed basic auth (`ingress.auth.enabled=true`, the default), an explicit escape
hatch (`ingress.auth.allowInsecure=true`), or a user-supplied auth annotation (nginx-ingress `auth-url` / `auth-signin`
or a traefik middleware). See [`charts/nyx/README.md`](charts/nyx/README.md) for the full `ingress.auth.*` values.

### Operator namespace-scoped RBAC (#532)

The `nyx-operator` chart supports a namespace-scoped deployment mode. Set `rbac.scope=namespace` and
`rbac.watchNamespaces: [...]` to install per-namespace Role/RoleBindings only (no ClusterRole/ClusterRoleBinding), and
pass the matching `--watch-namespaces` flag to restrict the controller-runtime cache. See
[`charts/nyx-operator/README.md`](charts/nyx-operator/README.md).

### Pod security (#541)

The agent chart's pod spec now sets `seccompProfile: RuntimeDefault` alongside the existing `runAsNonRoot` / `runAsUser`
settings, and the dashboard pod runs as non-root. This keeps the chart compatible with the Pod Security Standards
"restricted" profile out of the box.

## Configuration

### nyx-harness environment variables

| Variable                                    | Default                         | Description                                                                                                                                           |
| ------------------------------------------- | ------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `AGENT_NAME`                                | `nyx`                           | Agent display name (e.g. `iris`)                                                                                                                      |
| `AGENT_HOST`                                | `0.0.0.0`                       | Interface to bind                                                                                                                                     |
| `AGENT_PORT`                                | `8000`                          | HTTP port the nyx agent listens on                                                                                                                    |
| `BACKEND_CONFIG_PATH`                       | `/home/agent/.nyx/backend.yaml` | Path to the backend routing config file                                                                                                               |
| `METRICS_ENABLED`                           | _(unset)_                       | Set to any non-empty value to expose `/metrics`                                                                                                       |
| `METRICS_AUTH_TOKEN`                        | _(unset)_                       | Bearer token required to access `/metrics` (recommended in production)                                                                                |
| `METRICS_CACHE_TTL`                         | `15`                            | Seconds to cache aggregated backend metrics between scrapes                                                                                           |
| `CONVERSATIONS_AUTH_TOKEN`                  | _(unset)_                       | Bearer token required to access `/conversations` and `/trace` (inbound)                                                                               |
| `BACKEND_CONVERSATIONS_AUTH_TOKEN`          | _(unset)_                       | Bearer token forwarded to backend `/conversations` and `/trace` endpoints (set if backends require auth)                                              |
| `TRIGGERS_AUTH_TOKEN`                       | _(unset)_                       | Bearer token required for inbound trigger requests (fallback when no per-trigger HMAC secret is set)                                                  |
| `CORS_ALLOW_ORIGINS`                        | _(unset)_                       | Comma-separated list of allowed CORS origins; when unset, all cross-origin requests are denied (logs a warning)                                       |
| `TASK_STORE_PATH`                           | _(unset)_                       | Path for SQLite A2A task store; defaults to in-memory (state lost on restart)                                                                         |
| `WORKER_MAX_RESTARTS`                       | `5`                             | Consecutive crash limit before a critical worker marks the agent not-ready                                                                            |
| `WEBHOOK_MAX_CONCURRENT_DELIVERIES`         | `50`                            | Maximum number of in-flight webhook delivery tasks across all subscriptions; deliveries beyond this cap are shed and counted                          |
| `WEBHOOK_MAX_CONCURRENT_DELIVERIES_PER_SUB` | `10`                            | Per-subscription cap on concurrent in-flight deliveries; also settable per webhook via `max-concurrent-deliveries` frontmatter                        |
| `WEBHOOK_EXTRACTION_TIMEOUT`                | `120`                           | Maximum seconds to wait for a single LLM extraction call inside a webhook delivery; prevents a slow backend from holding a delivery slot indefinitely |
| `WEBHOOK_URL_ALLOWED_HOSTS`                 | _(unset)_                       | Comma-separated `host` or `host:port` entries that are allowed to override the SSRF guard on private / loopback / reserved destinations (#524)        |
| `JOBS_MAX_CONCURRENT`                       | `0` (unlimited)                 | Maximum number of jobs that may run concurrently; `0` disables the limit                                                                              |
| `TASKS_MAX_CONCURRENT`                      | `0` (unlimited)                 | Maximum number of tasks that may run concurrently; `0` disables the limit                                                                             |
| `TASK_TIMEOUT_SECONDS`                      | `300`                           | Task timeout in seconds, applied to A2A backend requests                                                                                              |
| `MANIFEST_PATH`                             | `/home/agent/manifest.json`     | Path to the team manifest file listing all agents by name and URL                                                                                     |
| `BACKENDS_READY_WARN_AFTER`                 | `120`                           | Seconds to wait before logging a warning that backends have not become healthy                                                                        |
| `LOG_PROMPT_MAX_BYTES`                      | `200`                           | Maximum bytes of the prompt logged at INFO level; set to `0` to suppress prompt logging entirely                                                      |
| `A2A_BACKEND_MAX_RETRIES`                   | `3`                             | Maximum retry attempts for transient backend errors (429, 502, 503, 504, connection errors); must be >= 1                                             |
| `A2A_BACKEND_RETRY_BACKOFF`                 | `1.0`                           | Base backoff in seconds for retry delay (exponential with jitter); multiplied by 2^attempt                                                            |

### Backend (a2-claude / a2-codex / a2-gemini) environment variables

| Variable                   | Default                            | Description                                                                              |
| -------------------------- | ---------------------------------- | ---------------------------------------------------------------------------------------- |
| `AGENT_NAME`               | `a2-claude`/`a2-codex`/`a2-gemini` | Backend instance name (e.g. `iris-a2-claude`)                                            |
| `AGENT_OWNER`              | _(same as `AGENT_NAME`)_           | Named agent this backend belongs to (e.g. `iris`); used in metric labels                 |
| `AGENT_ID`                 | `claude`/`codex`/`gemini`          | Backend slot identifier (e.g. `claude`); used in metric labels                           |
| `AGENT_URL`                | `http://localhost:8080/`           | Public A2A endpoint URL for the agent card                                               |
| `BACKEND_PORT`             | `8080`                             | HTTP port the backend listens on (internal)                                              |
| `METRICS_ENABLED`          | _(unset)_                          | Set to any non-empty value to expose `/metrics`                                          |
| `CONVERSATIONS_AUTH_TOKEN` | _(unset — warn on empty)_          | Bearer token required to access `/conversations`, `/trace`, and `/mcp` on all three backends (#510, #516, #517, #518) |
| `TASK_STORE_PATH`          | _(unset)_                          | Path for SQLite A2A task store; defaults to in-memory (state lost on restart)            |
| `WORKER_MAX_RESTARTS`      | `5`                                | Consecutive crash limit before a critical worker marks the backend not-ready             |
| `LOG_PROMPT_MAX_BYTES`     | `200`                              | Maximum bytes of the prompt logged at INFO level; `0` suppresses prompt logging entirely |

## Metrics

When `METRICS_ENABLED` is set, Prometheus metrics are served at `/metrics` on both nyx-harness and backend containers.

Backend containers (`a2-claude`, `a2-codex`, `a2-gemini`) expose `a2_*`-prefixed metrics. `a2-claude` exposes a superset
that includes tool call, context window, and MCP metrics; `a2-codex` also exposes tool-call and context-window metrics;
`a2-gemini` exposes context-window metrics. All three share the common `a2_*` baseline set.
nyx-harness exposes `agent_*`-prefixed infrastructure metrics (bus, heartbeat, job, sessions, webhooks, etc.). The
nyx-harness `/metrics` endpoint also aggregates all backend `/metrics` endpoints, injecting a `backend="<id>"` label on
each sample so a single scrape target captures the full deployment.

## Prompt env-var interpolation (#473)

Scheduler prompt bodies (`HEARTBEAT.md`, `jobs/*.md`, `tasks/*.md`, `triggers/*.md`, `continuations/*.md`) support
`{{env.VAR}}` interpolation so the same markdown can ship across dev / staging / prod without forking:

```yaml
# jobs/daily-status.md
---
schedule: "0 9 * * *"
---
Send a daily status update. Environment: {{env.DEPLOYMENT_ENV}}.
Dashboard: https://{{env.DASHBOARD_HOST}}/team.
```

Two env vars control the feature, both set on the nyx-harness container:

| Variable                 | Default   | Description                                                                                              |
| ------------------------ | --------- | -------------------------------------------------------------------------------------------------------- |
| `PROMPT_ENV_ENABLED`     | unset     | Master toggle. When unset/false, prompt bodies pass through verbatim. Operators opt in.                  |
| `PROMPT_ENV_ALLOWLIST`   | empty     | Comma-separated prefixes or globs (`NYX_*,DEPLOY_*`). References outside the allowlist become `""`.      |

Missing vars (and non-allowlisted references) are substituted with an empty string and a warning is logged once per
variable. For triggers specifically, interpolation is applied to the operator-authored `.md` body **only** — inbound
HTTP bodies are never interpolated, so callers who can hit the trigger endpoint cannot use the template engine to
read local env vars.

## Outbound Webhooks

Webhooks fire after a prompt completes. Each webhook subscription is a markdown file under `.nyx/webhooks/` with
frontmatter fields:

| Field                | Required | Description                                                                        |
| -------------------- | -------- | ---------------------------------------------------------------------------------- |
| `name`               | yes      | Subscription name (used in metrics labels)                                         |
| `url`                | yes\*    | POST target URL                                                                    |
| `url-env-var`        | yes\*    | Environment variable holding the URL (alternative to `url`)                        |
| `notify-when`        | no       | `always`, `on_success` (default), or `on_error`                                    |
| `notify-on-kind`     | no       | Glob list of prompt kinds to match (e.g. `a2a`, `job:*`, `heartbeat`); default `*` |
| `notify-on-response` | no       | Glob list of patterns matched against the response text; default `*`               |
| `secret`             | no       | HMAC secret — adds `X-Hub-Signature-256` header when set                           |
| `content-type`       | no       | `Content-Type` header; default `application/json`                                  |

\* Either `url` or `url-env-var` is required.

### URL safety (#524)

- The `url:` template may only reference the built-in variables listed below. `{{env.VAR}}` references and
  extraction-defined variables are **not** substituted in the URL field — env-derived URLs must be placed
  in a single env var and read via `url-env-var`. **Migration:** any webhook previously using
  `url: http://{{env.FOO}}/…` must switch to `url-env-var: FOO` — render fails loudly otherwise.
- Only `http` and `https` URLs are accepted. Schemes like `file://`, `gopher://`, `ftp://` are rejected.
- URLs whose host is a loopback / link-local / private / reserved IP literal (e.g. `127.0.0.1`,
  `169.254.169.254`, `10.0.0.5`) are rejected to prevent SSRF to cloud metadata endpoints and internal
  services. Operators can opt specific internal hosts into the allow-list via the
  `WEBHOOK_URL_ALLOWED_HOSTS` env var on nyx-harness (comma-separated `host` or `host:port` entries).

The markdown body is the POST payload. Use `{{variable}}` placeholders for substitution in the body and
header values (not in the URL — see above):

| Variable               | Value                                          |
| ---------------------- | ---------------------------------------------- |
| `{{agent}}`            | Agent name (e.g. `iris`)                       |
| `{{kind}}`             | Prompt kind (`a2a`, `heartbeat`, `job:<name>`) |
| `{{session_id}}`       | Session/context ID                             |
| `{{source}}`           | Source name (job name, trigger endpoint, etc.) |
| `{{model}}`            | Model used for the prompt                      |
| `{{success}}`          | `True` or `False`                              |
| `{{error}}`            | Error message, or empty string on success      |
| `{{response_preview}}` | First 2048 chars of the response text          |
| `{{duration_seconds}}` | Prompt execution time in seconds               |
| `{{timestamp}}`        | ISO 8601 UTC timestamp of delivery             |
| `{{delivery_id}}`      | UUID unique to this delivery attempt           |

If the body is empty, a default JSON envelope is sent.

## Observability

Prometheus metrics are opt-in per-agent; see `charts/nyx` values for `metrics.*`, `serviceMonitor.*`, and
`podMonitor.*`.

Distributed tracing (OpenTelemetry) is also opt-in and spans harness + backends + operator when enabled. The
pod-side SDK bootstraps already honour the standard OTel env vars (`shared/otel.py`,
`operator/internal/tracing/otel.go`); the Helm charts own the wiring end-to-end (#634):

- `charts/nyx` — `observability.tracing.enabled` + `observability.tracing.collector.enabled` deploys an in-cluster
  OpenTelemetry Collector and points every agent pod at it. Set `observability.tracing.endpoint` to forward to an
  out-of-band collector instead.
- `charts/nyx-operator` — matching `observability.tracing.*` block; wire the same endpoint to trace the reconciler
  alongside the agents.

See `charts/nyx/README.md` → "Enabling distributed tracing" for Jaeger and Tempo recipes.
