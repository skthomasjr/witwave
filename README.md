# witwave

A platform for building persistent, self-directed AI agents that can work autonomously on software projects — including
improving themselves.

The primary use case is autonomous software development: agents that can triage issues, implement features, fix bugs,
evaluate their own work, and iterate — continuously and without human intervention. The same platform can be pointed at
any software project, not just this one.

Agents are currently bootstrapped manually using AI CLI tools (Claude Code, Codex). The long-term goal is for the agents
to take over their own development cycle: evaluating the codebase, proposing improvements, implementing them, and
shipping — closing the loop without a human in the hot path.

**This project is also an experiment in AI-operated open source.** Every line of code here is written by AI. Every bug
is diagnosed and fixed by AI. Every issue is answered by AI. Every PR is opened, reviewed, and merged by AI. Humans file
issues and make strategic calls — that is the shape of participation. See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the
full model (including the current-state-vs-target breakdown), and [`docs/product-vision.md`](docs/product-vision.md) for
why this is a first-class project goal rather than a convention.

---

Built on the [A2A protocol](https://a2a-protocol.org). Each named agent is a set of containers: a **harness**
infrastructure layer (A2A relay, heartbeat scheduler, job scheduler) and one or more **backend agent** containers that
do the actual LLM work (Claude Agent SDK via `claude`, OpenAI Agents SDK via `codex`, Google Gemini SDK via `gemini`).
A fourth backend, `echo`, ships as a zero-dependency stub — it returns a canned response quoting the caller's prompt and
is the hello-world default for `ww agent create` when no API key is configured.

Multiple agents can collaborate as a team, but the named agent (harness + its backend agents) is the deployable unit.

## Agent Model

Three tiers to keep straight:

1. **A2A agent** — any server that publishes `/.well-known/agent.json`. The protocol's unit of identity. Both the
   harness and each backend agent qualify.
2. **Backend agent** — the LLM-wrapping worker. One image per LLM family (`claude`, `codex`, `gemini`), plus the
   zero-dependency `echo` stub. Each owns its own session state, memory, conversation log, and metrics, and is callable
   standalone over A2A.
3. **Named agent** — the deployable unit (`iris`, `nova`, `kira`, …). From outside it presents as a single A2A agent via
   the harness's endpoint. Inside, the harness orchestrates one or more backend agents using routing rules in
   `.witwave/backend.yaml`.

A named agent is both an agent **and** an orchestrator of sub-agents. Because the harness treats any A2A URL as a valid
dispatch target, peer named agents are reachable the same way local backend agents are — teams of named agents are just
agents all the way down.

The split of responsibilities:

- **Autonomy** (when and why work happens) lives in the harness: heartbeats, jobs, tasks, triggers, continuations,
  webhooks.
- **Intelligence** (what to say, what to do) lives in the backend agents: LLM SDK wrappers that turn prompts into
  responses.

Remove the harness and you have reactive LLM servers that only respond when called — not autonomous. Remove the backend
agents and you have a scheduler with nothing to dispatch to — no intelligence. Together they form an autonomous agent.

## Components

| Component          | Directory                  | Type                | Description                                                                                          |
| ------------------ | -------------------------- | ------------------- | ---------------------------------------------------------------------------------------------------- |
| **Harness**        | `harness/`                 | Orchestrator agent  | Scheduling, triggering, chaining, A2A relay. No LLM of its own.                                      |
| **Claude backend** | `backends/claude/`         | Backend agent       | Executes prompts via the Claude Agent SDK.                                                           |
| **Codex backend**  | `backends/codex/`          | Backend agent       | Executes prompts via the OpenAI Agents SDK. Supports web search and headless browser via Playwright. |
| **Gemini backend** | `backends/gemini/`         | Backend agent       | Executes prompts via the Google Gemini SDK.                                                          |
| **Echo backend**   | `backends/echo/`           | Backend agent       | Zero-dependency stub. Returns a canned response quoting the prompt. Hello-world default + reference. |
| **MCP tools**      | `tools/`                   | Tool infrastructure | `mcp-kubernetes`, `mcp-helm`, `mcp-prometheus` — shared MCP servers backends opt into.               |
| **Dashboard**      | `clients/dashboard/`       | Web client          | Vue 3 + PrimeVue web UI.                                                                             |
| **ww CLI**         | `clients/ww/`              | Client              | Go + cobra command-line interface (`brew install witwave-ai/homebrew-ww/ww`).                        |
| **Operator**       | `operator/`                | Kubernetes operator | Go controller that reconciles `WitwaveAgent` CRDs.                                                   |
| **Agent chart**    | `charts/witwave/`          | Deployment          | Helm chart that deploys witwave agents via templated manifests.                                      |
| **Operator chart** | `charts/witwave-operator/` | Deployment          | Helm chart that installs the operator + CRD.                                                         |

The harness routes work to backend agents but does no LLM execution itself. Client surfaces (dashboard + ww) provide
visibility and interaction; they don't participate in agent workflows. The operator and its chart are an alternative
install path to the agent chart; both target the same per-agent deployment shape.

## How It Works

Operational details that complement the Agent Model above:

- Each named agent has its own identity, memory, and configuration — none baked into the image. Behavioral instructions
  for each backend agent come from a mounted file (`CLAUDE.md` for claude, `AGENTS.md` for codex, `GEMINI.md` for
  gemini), and A2A identity comes from a mounted `agent-card.md`.
- Every container (harness and each backend agent) exposes `/health` for probes and `/metrics` for Prometheus on a
  dedicated port (9000 by default) alongside its A2A endpoint.

## Requirements

- Docker
- Docker Compose
- A Claude Code OAuth token (`claude setup-token`) or Anthropic API key (for `claude`)
- An OpenAI API key (for `codex`)
- A Gemini API key (for `gemini`)
- Nothing extra for `echo` — the stub backend runs without credentials or network access

## Container Images

Published images are available on GitHub Container Registry. Every image listed below is built and pushed automatically
on every release tag.

| Image            | Registry path                                     |
| ---------------- | ------------------------------------------------- |
| `harness`        | `ghcr.io/witwave-ai/images/harness:latest`        |
| `claude`         | `ghcr.io/witwave-ai/images/claude:latest`         |
| `codex`          | `ghcr.io/witwave-ai/images/codex:latest`          |
| `gemini`         | `ghcr.io/witwave-ai/images/gemini:latest`         |
| `echo`           | `ghcr.io/witwave-ai/images/echo:latest`           |
| `dashboard`      | `ghcr.io/witwave-ai/images/dashboard:latest`      |
| `operator`       | `ghcr.io/witwave-ai/images/operator:latest`       |
| `git-sync`       | `ghcr.io/witwave-ai/images/git-sync:latest`       |
| `mcp-kubernetes` | `ghcr.io/witwave-ai/images/mcp-kubernetes:latest` |
| `mcp-helm`       | `ghcr.io/witwave-ai/images/mcp-helm:latest`       |
| `mcp-prometheus` | `ghcr.io/witwave-ai/images/mcp-prometheus:latest` |

The `ww` CLI ships via Homebrew (the [witwave-ai/homebrew-ww](https://github.com/witwave-ai/homebrew-ww) tap) and as
standalone binaries on [GitHub Releases](https://github.com/witwave-ai/witwave/releases):

```bash
brew install witwave-ai/homebrew-ww/ww
```

`ww` checks for newer releases on startup and surfaces a one-line banner (configurable via
`ww config set update.mode ...`). To upgrade explicitly at any time:

```bash
ww update              # check + upgrade if newer
ww update --check      # check only
ww update --force      # run the upgrade unconditionally
```

Pull a specific image version with a semver tag, e.g. `ghcr.io/witwave-ai/images/harness:0.4.0`. The latest released tag
is visible on the [GitHub Releases](https://github.com/witwave-ai/witwave/releases) page; substitute it for the version
below.

## Helm Charts

Two Helm charts are published to GHCR alongside the images on every release tag. The fastest install for the
**operator** is the `ww` CLI — it embeds the chart so you don't need `helm` on PATH or any repo configured:

```bash
# Install `ww` then use it to install the operator.
brew install witwave-ai/homebrew-ww/ww
ww operator install                 # into witwave-system namespace
ww operator status                  # verify
```

See [clients/ww/README.md](clients/ww/README.md#operator-management) for the full `ww operator` surface.

For direct Helm installs (GitOps workflows, non-Homebrew environments, or the main agent chart which isn't yet
CLI-managed):

```bash
# Agent chart — deploys witwave agents directly via templated manifests.
helm install witwave oci://ghcr.io/witwave-ai/charts/witwave --version 0.5.6 --namespace witwave --create-namespace

# Operator chart — installs the witwave-operator controller and the WitwaveAgent CRD.
helm install witwave-operator oci://ghcr.io/witwave-ai/charts/witwave-operator --version 0.5.6 --namespace witwave-system --create-namespace
```

See [charts/witwave/README.md](charts/witwave/README.md) and
[charts/witwave-operator/README.md](charts/witwave-operator/README.md) for full installation instructions.

## Getting Started

### 1. Pull or build the images

Pull published images:

```bash
docker pull ghcr.io/witwave-ai/images/harness:latest
docker pull ghcr.io/witwave-ai/images/claude:latest
docker pull ghcr.io/witwave-ai/images/codex:latest
docker pull ghcr.io/witwave-ai/images/gemini:latest
docker pull ghcr.io/witwave-ai/images/echo:latest
```

Or build locally:

```bash
docker build -f harness/Dockerfile -t harness:latest .
docker build -f backends/claude/Dockerfile -t claude:latest .
docker build -f backends/codex/Dockerfile -t codex:latest .
docker build -f backends/gemini/Dockerfile -t gemini:latest .
docker build -f backends/echo/Dockerfile -t echo:latest .
```

### 2. Configure credentials

```bash
export CLAUDE_CODE_OAUTH_TOKEN=your-token-here
export OPENAI_API_KEY=your-key-here
export GEMINI_API_KEY=your-key-here
```

### 3. Start the agents

```bash
helm upgrade --install witwave ./charts/witwave -f ./charts/witwave/values-test.yaml -n witwave --create-namespace
```

### 4. Verify

```bash
# harness (router layer)
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

Active agents are defined under `.agents/active/`. Each named agent has its own directory containing witwave config,
backend instances, logs, and memory.

```text
.agents/
├── active/
│   ├── iris/          # Iris (witwave: 8000 | claude: 8010 | codex: 8011 | gemini: 8012)
│   ├── nova/          # Nova (witwave: 8001 | claude: 8010 | codex: 8011 | gemini: 8012)
│   └── kira/          # Kira (witwave: 8002 | claude: 8010 | codex: 8011 | gemini: 8012)
└── test/
    ├── bob/           # Bob  (witwave: 8099 | claude: 8090 | codex: 8091 | gemini: 8092)
    └── fred/          # Fred (witwave: 8098 | claude: 8089 — single-backend test agent)
```

Port numbers above are example assignments from the bundled `values-test.yaml` and the default `values.yaml` layout —
not hardcoded in any image. Each container reads its own port from an environment variable (`HARNESS_PORT`,
`BACKEND_PORT`, `METRICS_PORT`) and can be remapped per deployment via Helm values or the `WitwaveAgent` CRD.

Each agent directory contains:

```text
<agent>/
├── .witwave/              # Runtime config (agent-card.md, backend.yaml, HEARTBEAT.md, jobs/)
├── .claude/           # Claude backend config (CLAUDE.md, agent-card.md, mcp.json, settings.json)
├── .codex/            # Codex backend config (AGENTS.md, agent-card.md, config.toml)
├── .gemini/           # Gemini backend config (GEMINI.md, agent-card.md)
├── logs/              # harness logs (runtime, not committed)
├── claude/         # Claude backend instance
│   ├── logs/          # Conversation log (runtime, not committed)
│   └── memory/        # Persistent memory (runtime, not committed)
├── codex/          # Codex backend instance
│   ├── logs/
│   └── memory/
└── gemini/         # Gemini backend instance
    ├── logs/
    └── memory/
        └── sessions/  # JSON conversation history per session
```

## Routing Configuration

Each agent's `backend.yaml` (under `.witwave/`) controls where witwave routes each type of work:

```yaml
backend:
  agents:
    - id: claude
      url: http://localhost:8010

    - id: codex
      url: http://localhost:8011

    - id: gemini
      url: http://localhost:8012

  routing:
    default: claude # fallback backend when no per-concern override matches
    a2a: claude # handles incoming A2A requests
    heartbeat: claude # handles heartbeat-triggered work
    job: claude # handles job execution
    task: claude # handles task execution
    trigger: claude # handles inbound HTTP trigger requests
    continuation: claude # handles continuation-fired prompts
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
  - backend: "claude"
    model: "claude-opus-4-7"
  - backend: "codex*" # glob — matches any backend whose id starts with "codex"
  - backend: "claude"
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

| Backend  | Token source                                       |
| -------- | -------------------------------------------------- |
| `claude` | `get_context_usage()` after each assistant turn    |
| `codex`  | `event.data.usage.total_tokens` on response events |
| `gemini` | `chunk.usage_metadata.total_token_count` per chunk |

## Adding an Agent

1. Copy an existing agent directory:

   ```bash
   cp -r .agents/active/iris .agents/active/<name>
   ```

2. Update the agent's `agent-card.md` in `.witwave/` (mounted at `/home/agent/.witwave/agent-card.md`) with the agent's
   identity and role; update each backend's `agent-card.md` in `.claude/`, `.codex/`, and `.gemini/` if those
   directories are used

3. Update the backend instruction files: `CLAUDE.md` (at `/home/agent/.claude/CLAUDE.md`), `AGENTS.md` (at
   `/home/agent/.codex/AGENTS.md`), and `GEMINI.md` (at `/home/agent/.gemini/GEMINI.md`) with backend-specific
   behavioral instructions

4. Update `.agents/active/<name>/.witwave/backend.yaml` with the new agent's backend service names and URLs

5. Add the agent to `charts/witwave/values-test.yaml` (or your own overrides file) with its backends, config, and
   storage

6. Register the agent in `.agents/active/manifest.json`

7. Deploy:

   ```bash
   helm upgrade --install witwave ./charts/witwave -f ./charts/witwave/values-test.yaml -n witwave
   ```

## Communication

Agents communicate over the [A2A protocol](https://a2a-protocol.org) via JSON-RPC. Each witwave agent exposes:

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

Cross-agent views (`/team`, `/proxy/<name>`, `/conversations/<name>`, `/trace/<name>`) were retired in beta.46 — the
dashboard pod fans out directly to each agent and owns cross-agent routing (#470).

Each backend container additionally exposes:

- `GET /health/start` — startup probe: 200/`{"status": "ok"}` once the process has finished initial loads (`_ready`
  is True) and 503/`{"status": "starting"}` while still warming up. Mirrors the harness's `/health/start` so the
  three-probe model documented in `docs/product-vision.md` holds across the platform (#1686). K8s `startupProbe`
  should target this endpoint.
- `GET /health` — liveness check: 200/`{"status": "ok", "agent": ..., "uptime_seconds": ...}` once the process is
  up. Returns 200 even while initializing — does NOT flip to 503. Liveness-only by design (cycle-1 #1608, #1672); use
  the readiness endpoint below for gating LB rotation.
- `GET /health/ready` — readiness probe: 200 when fully ready, 503/`{"status": "starting"}` while initializing or in a
  boot-degraded state (claude #1608, codex+gemini #1672). Operators using K8s `readinessProbe` should point at
  `/health/ready`, not `/health`.
- `GET /metrics` — Prometheus metrics (when `METRICS_ENABLED` is set)
- `POST /mcp` — MCP JSON-RPC server (`initialize`, `tools/list`, `tools/call` with a single `ask_agent` tool); allows
  MCP hosts (Claude Desktop, Cursor, VS Code extensions) to invoke the agent as a tool without going through harness.
  **All three backends require a bearer token** (`CONVERSATIONS_AUTH_TOKEN`) on `/mcp` (#510, #516, #518); the shared
  token guard also gates `/conversations` and `/trace`. If the env var is left empty the backend logs a startup warning
  (#517) — set a non-empty token in production. The `session_id` attached to `/mcp` requests is routed through
  `shared/session_binding.derive_session_id` with a bearer-token fingerprint before lookup/insert on every backend (#867
  claude, #929 codex, #935 gemini, #941 shared path) so a caller cannot hijack another caller's session; set
  `SESSION_ID_SECRET` in production to HMAC-derive the bound ID.

## Memory

Each backend agent manages its own memory at `.agents/<env>/<name>/<backend>/memory/`. For `claude` and `codex`, memory
files are markdown documents. For `gemini`, conversation history is stored as JSON in `memory/sessions/`. Memory files
are not committed to source control. harness has no memory layer of its own.

## Authentication

| Service | Method             | Environment variable                 |
| ------- | ------------------ | ------------------------------------ |
| claude  | Claude Max (OAuth) | `CLAUDE_CODE_OAUTH_TOKEN`            |
| claude  | Anthropic API key  | `ANTHROPIC_API_KEY`                  |
| codex   | OpenAI API key     | `OPENAI_API_KEY`                     |
| gemini  | Gemini API key     | `GEMINI_API_KEY` or `GOOGLE_API_KEY` |

## Security

Protected endpoints use `Authorization: Bearer <token>` throughout. Two distinct harness tokens:

- **`CONVERSATIONS_AUTH_TOKEN`** — read / observe endpoints (`/conversations`, `/trace`, `/mcp`, `/api/traces`,
  `/events/stream`, `/api/sessions/<id>/stream`). Reused on the harness for inbound and on each backend for its own
  protected surface.
- **`ADHOC_RUN_AUTH_TOKEN`** — trigger-actions endpoints (`POST /jobs/<name>/run`, `/tasks/<name>/run`,
  `/triggers/<name>/run`, `/validate`).

Both are default-closed — the server refuses requests when the token is unset. `CONVERSATIONS_AUTH_DISABLED=true` is a
documented escape hatch for local dev; startup logs a loud warning when it's set.

Session IDs on multi-tenant surfaces are HMAC-bound to the caller via `SESSION_ID_SECRET`. Rotation uses a probe-list
window via `SESSION_ID_SECRET_PREV`: writes go to the current-secret derivation; reads probe `[current, prev]` and emit
a one-shot WARN on prev-hits so operators know when they can drop the prev secret.

MCP stdio entries are gated by a per-backend command allow-list (`MCP_ALLOWED_COMMANDS`, `MCP_ALLOWED_COMMAND_PREFIXES`,
`MCP_ALLOWED_CWD_PREFIXES`); rejections bump `backend_mcp_command_rejected_total{reason}`. Every MCP tool container
enforces its own bearer (`MCP_TOOL_AUTH_TOKEN`) via `shared/mcp_auth.py`. Outbound webhooks go through an SSRF-resistant
URL check that re-resolves the hostname at delivery time.

The witwave-operator chart runs with a split RBAC surface (`rbac.secretsWrite=false` drops Secret write verbs while
keeping reads). Credential Secrets are dual-checked (label + `IsControlledBy`) before any update/delete so the operator
never touches user-created Secrets.

See `AGENTS.md` → "Conventions" for the full auth / redaction / MCP / RBAC posture, `shared/redact.py` for the
conversation-log redaction rules (idempotent merge-spans with UUID / OTel-trace shielding), and each chart's
`values.yaml` for the full surface of security-affecting knobs.

## Configuration

### harness environment variables

| Variable                                    | Default                             | Description                                                                                                                                                                                                                              |
| ------------------------------------------- | ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `AGENT_NAME`                                | `witwave`                           | Agent display name (e.g. `iris`)                                                                                                                                                                                                         |
| `HARNESS_HOST`                              | `0.0.0.0`                           | Interface the harness binds to                                                                                                                                                                                                           |
| `HARNESS_PORT`                              | `8000`                              | HTTP port the harness listens on                                                                                                                                                                                                         |
| `HARNESS_URL`                               | `http://localhost:$HARNESS_PORT/`   | Public URL published on the A2A agent card                                                                                                                                                                                               |
| `BACKEND_CONFIG_PATH`                       | `/home/agent/.witwave/backend.yaml` | Path to the backend routing config file                                                                                                                                                                                                  |
| `METRICS_ENABLED`                           | _(unset)_                           | Set to any non-empty value to expose `/metrics`                                                                                                                                                                                          |
| `METRICS_PORT`                              | `9000`                              | Dedicated port the metrics listener binds to (split from the app port so NetworkPolicy + auth can differ, #643)                                                                                                                          |
| `METRICS_AUTH_TOKEN`                        | _(unset)_                           | Bearer token required to access `/metrics` (recommended in production)                                                                                                                                                                   |
| `METRICS_CACHE_TTL`                         | `15`                                | Seconds to cache aggregated backend metrics between scrapes                                                                                                                                                                              |
| `CONVERSATIONS_AUTH_TOKEN`                  | _(unset)_                           | Bearer token required to access `/conversations` and `/trace` (inbound)                                                                                                                                                                  |
| `BACKEND_CONVERSATIONS_AUTH_TOKEN`          | _(unset)_                           | Bearer token forwarded to backend `/conversations` and `/trace` endpoints (set if backends require auth)                                                                                                                                 |
| `TRIGGERS_AUTH_TOKEN`                       | _(unset)_                           | Bearer token required for inbound trigger requests (fallback when no per-trigger HMAC secret is set)                                                                                                                                     |
| `HOOK_EVENTS_AUTH_TOKEN`                    | _(unset)_                           | Canonical bearer token on `/internal/events/hook-decision` (bound to the metrics listener, #924). `HARNESS_EVENTS_AUTH_TOKEN` is a back-compat alias that logs a deprecation warning when used alone (#859). Unset = refuse (#712, #933) |
| `SESSION_ID_SECRET`                         | _(unset — permissive)_              | HMAC key for `shared/session_binding.derive_session_id` used on `/mcp` session-id binding across all three backends (#867/#929/#935/#941). Leave unset only in single-tenant dev; set to a 256-bit random value in production            |
| `ADHOC_RUN_AUTH_TOKEN`                      | _(unset)_                           | Bearer token required for `POST /jobs/<name>/run`, `/tasks/<name>/run`, `/triggers/<name>/run`; unset = refuse (#700)                                                                                                                    |
| `CORS_ALLOW_ORIGINS`                        | _(unset)_                           | Comma-separated list of allowed CORS origins; when unset, all cross-origin requests are denied (logs a warning)                                                                                                                          |
| `CORS_ALLOW_WILDCARD`                       | `false`                             | Explicit acknowledgement for `CORS_ALLOW_ORIGINS=*`; template refuses the wildcard otherwise (#701)                                                                                                                                      |
| `A2A_MAX_PROMPT_BYTES`                      | `1048576`                           | Reject inbound A2A prompts above this byte size at ingress; set to `0` to disable (#783)                                                                                                                                                 |
| `CONTINUATION_MAX_CONCURRENT_FIRES_GLOBAL`  | `0` (unlimited)                     | Hard cap on in-flight continuation fires across all items; protects against fan-out storms (#781)                                                                                                                                        |
| `TASK_STORE_PATH`                           | _(unset)_                           | Path for SQLite A2A task store; defaults to in-memory (state lost on restart)                                                                                                                                                            |
| `WORKER_MAX_RESTARTS`                       | `5`                                 | Consecutive crash limit before a critical worker marks the agent not-ready                                                                                                                                                               |
| `WEBHOOK_MAX_CONCURRENT_DELIVERIES`         | `50`                                | Maximum number of in-flight webhook delivery tasks across all subscriptions; deliveries beyond this cap are shed and counted                                                                                                             |
| `WEBHOOK_MAX_CONCURRENT_DELIVERIES_PER_SUB` | `10`                                | Per-subscription cap on concurrent in-flight deliveries; also settable per webhook via `max-concurrent-deliveries` frontmatter                                                                                                           |
| `WEBHOOK_EXTRACTION_TIMEOUT`                | `120`                               | Maximum seconds to wait for a single LLM extraction call inside a webhook delivery; prevents a slow backend from holding a delivery slot indefinitely                                                                                    |
| `WEBHOOK_URL_ALLOWED_HOSTS`                 | _(unset)_                           | Comma-separated `host` or `host:port` entries that are allowed to override the SSRF guard on private / loopback / reserved destinations (#524)                                                                                           |
| `JOBS_MAX_CONCURRENT`                       | `0` (unlimited)                     | Maximum number of jobs that may run concurrently; `0` disables the limit                                                                                                                                                                 |
| `TASKS_MAX_CONCURRENT`                      | `0` (unlimited)                     | Maximum number of tasks that may run concurrently; `0` disables the limit                                                                                                                                                                |
| `TASK_TIMEOUT_SECONDS`                      | `300`                               | Task timeout in seconds, applied to A2A backend requests                                                                                                                                                                                 |
| `MANIFEST_PATH`                             | `/home/agent/manifest.json`         | Path to the team manifest file listing all agents by name and URL                                                                                                                                                                        |
| `BACKENDS_READY_WARN_AFTER`                 | `120`                               | Seconds to wait before logging a warning that backends have not become healthy                                                                                                                                                           |
| `LOG_PROMPT_MAX_BYTES`                      | `200`                               | Maximum bytes of the prompt logged at INFO level; set to `0` to suppress prompt logging entirely                                                                                                                                         |
| `A2A_BACKEND_MAX_RETRIES`                   | `3`                                 | Maximum retry attempts for transient backend errors (429, 502, 503, 504, connection errors); must be >= 1                                                                                                                                |
| `A2A_BACKEND_RETRY_BACKOFF`                 | `1.0`                               | Base backoff in seconds for retry delay (exponential with jitter); multiplied by 2^attempt                                                                                                                                               |

### Backend (claude / codex / gemini) environment variables

| Variable                       | Default                   | Description                                                                                                                                          |
| ------------------------------ | ------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `AGENT_NAME`                   | `claude`/`codex`/`gemini` | Backend instance name (e.g. `iris-claude`)                                                                                                           |
| `AGENT_OWNER`                  | _(same as `AGENT_NAME`)_  | Named agent this backend belongs to (e.g. `iris`); used in metric labels                                                                             |
| `AGENT_ID`                     | `claude`/`codex`/`gemini` | Backend slot identifier (e.g. `claude`); used in metric labels                                                                                       |
| `AGENT_URL`                    | `http://localhost:8000/`  | Public A2A endpoint URL for the agent card                                                                                                           |
| `BACKEND_PORT`                 | `8000`                    | HTTP port the backend listens on (internal)                                                                                                          |
| `METRICS_ENABLED`              | _(unset)_                 | Set to any non-empty value to expose `/metrics`                                                                                                      |
| `METRICS_PORT`                 | `9000`                    | Dedicated port the metrics listener binds to (#643; same semantics as harness)                                                                       |
| `CONVERSATIONS_AUTH_TOKEN`     | _(unset — warn on empty)_ | Bearer token required to access `/conversations`, `/trace`, `/mcp`, and claude's `/api/traces[/<id>]` on all three backends (#510, #516, #517, #518) |
| `CONVERSATIONS_AUTH_DISABLED`  | _(unset)_                 | Explicit escape hatch to run without the auth guard; loud startup log for visibility (#718). Intended for local dev only.                            |
| `LOG_REDACT`                   | _(unset)_                 | When truthy, conversation and response logs redact user-prompt / agent-response content (#714)                                                       |
| `GEMINI_MAX_HISTORY_BYTES`     | _(gemini only)_           | Byte ceiling on the JSON session-history file gemini persists per session; older turns are truncated to fit                                          |
| `MCP_ALLOWED_COMMANDS`         | _(per-backend default)_   | Comma-separated allow-list of basenames for stdio entries parsed from `mcp.json`                                                                     |
| `MCP_ALLOWED_COMMAND_PREFIXES` | _(per-backend default)_   | Comma-separated allow-list of absolute-path prefixes for stdio entries                                                                               |
| `MCP_ALLOWED_CWD_PREFIXES`     | _(per-backend default)_   | Comma-separated allow-list of working-directory prefixes for stdio entries (rejections counted on `backend_mcp_command_rejected_total`)              |
| `TASK_STORE_PATH`              | _(unset)_                 | Path for SQLite A2A task store; defaults to in-memory (state lost on restart)                                                                        |
| `WORKER_MAX_RESTARTS`          | `5`                       | Consecutive crash limit before a critical worker marks the backend not-ready                                                                         |
| `LOG_PROMPT_MAX_BYTES`         | `200`                     | Maximum bytes of the prompt logged at INFO level; `0` suppresses prompt logging entirely                                                             |

## Metrics

When `METRICS_ENABLED` is set, Prometheus metrics are served at `/metrics` on a **dedicated port** (9000 by default,
configurable via `METRICS_PORT`) on every container. The metrics listener is split from the app listener so
NetworkPolicy and auth posture can diverge cleanly between app traffic and monitoring scrapes.

Each backend exposes `backend_*`-prefixed metrics; **claude is the superset** and peers track placeholders so
cross-backend PromQL joins don't lose label sets. Harness exposes `harness_*`-prefixed infrastructure metrics. The
harness `/metrics` endpoint also aggregates all backend `/metrics` endpoints with a `backend="<id>"` label injected per
sample, so a single scrape captures the full deployment.

For the full catalog, read each component's `metrics.py`. For the rendered view, see `charts/witwave/dashboards/`
(Grafana sidecar) and `charts/witwave/templates/prometheusrule.yaml` (default alerts).

```bash
curl -s http://localhost:9000/metrics | head
```

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

Two env vars control the feature, both set on the harness container:

| Variable               | Default | Description                                                                                             |
| ---------------------- | ------- | ------------------------------------------------------------------------------------------------------- |
| `PROMPT_ENV_ENABLED`   | unset   | Master toggle. When unset/false, prompt bodies pass through verbatim. Operators opt in.                 |
| `PROMPT_ENV_ALLOWLIST` | empty   | Comma-separated prefixes or globs (`WITWAVE_*,DEPLOY_*`). References outside the allowlist become `""`. |

Missing vars (and non-allowlisted references) are substituted with an empty string and a warning is logged once per
variable. For triggers specifically, interpolation is applied to the operator-authored `.md` body **only** — inbound
HTTP bodies are never interpolated, so callers who can hit the trigger endpoint cannot use the template engine to read
local env vars.

## Outbound Webhooks

Webhooks fire after a prompt completes. Each webhook subscription is a markdown file under `.witwave/webhooks/` with
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
  extraction-defined variables are **not** substituted in the URL field — env-derived URLs must be placed in a single
  env var and read via `url-env-var`. **Migration:** any webhook previously using `url: http://{{env.FOO}}/…` must
  switch to `url-env-var: FOO` — render fails loudly otherwise.
- Only `http` and `https` URLs are accepted. Schemes like `file://`, `gopher://`, `ftp://` are rejected.
- URLs whose host is a loopback / link-local / private / reserved IP literal (e.g. `127.0.0.1`, `169.254.169.254`,
  `10.0.0.5`) are rejected to prevent SSRF to cloud metadata endpoints and internal services. Operators can opt specific
  internal hosts into the allow-list via the `WEBHOOK_URL_ALLOWED_HOSTS` env var on harness (comma-separated `host` or
  `host:port` entries).

The markdown body is the POST payload. Use `{{variable}}` placeholders for substitution in the body and header values
(not in the URL — see above):

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

Prometheus metrics are opt-in per-agent; see `charts/witwave` values for `metrics.*`, `serviceMonitor.*`, and
`podMonitor.*`.

Distributed tracing (OpenTelemetry) is also opt-in and spans harness + backends + operator when enabled. The pod-side
SDK bootstraps already honour the standard OTel env vars (`shared/otel.py`, `operator/internal/tracing/otel.go`); the
Helm charts own the wiring end-to-end (#634):

- `charts/witwave` — `observability.tracing.enabled` + `observability.tracing.collector.enabled` deploys an in-cluster
  OpenTelemetry Collector and points every agent pod at it. Set `observability.tracing.endpoint` to forward to an
  out-of-band collector instead.
- `charts/witwave-operator` — matching `observability.tracing.*` block; wire the same endpoint to trace the reconciler
  alongside the agents.

See `charts/witwave/README.md` → "Enabling distributed tracing" for Jaeger and Tempo recipes.
