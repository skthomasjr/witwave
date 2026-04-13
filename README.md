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

Built on the [A2A protocol](https://a2a-protocol.org). Each named agent is a set of containers: a **nyx-agent**
infrastructure layer (A2A relay, heartbeat scheduler, job scheduler) and one or more **backend** containers that do the
actual LLM work (Claude Agent SDK via `a2-claude`, OpenAI Agents SDK via `a2-codex`, Google Gemini SDK via `a2-gemini`).

Multiple agents can collaborate as a team, but the named agent (nyx + its backends) is the deployable unit.

## Components

The platform has five components, each with its own source directory:

| Component          | Directory    | Type               | Description                                                                                                               |
| ------------------ | ------------ | ------------------ | ------------------------------------------------------------------------------------------------------------------------- |
| **Orchestrator**   | `agent/`     | Orchestrator agent | nyx-agent: the infrastructure and routing layer. Owns scheduling, triggering, chaining, and A2A relay. No LLM of its own. |
| **Claude backend** | `a2-claude/` | Backend agent      | Executes prompts via the Claude Agent SDK. Manages sessions, memory, conversation logs, and metrics.                      |
| **Codex backend**  | `a2-codex/`  | Backend agent      | Executes prompts via the OpenAI Agents SDK. Supports web search and headless browser via Playwright.                      |
| **Gemini backend** | `a2-gemini/` | Backend agent      | Executes prompts via the Google Gemini SDK. Manages sessions and conversation history.                                    |
| **UI**             | `ui/`        | Web interface      | Single-page app for monitoring metrics, browsing agents, viewing conversations, and chatting with agents.                 |

Each backend agent is a full A2A server. The orchestrator routes work to backends but does no LLM execution itself. The
UI provides visibility only — it does not participate in agent workflows.

## How It Works

Each agent:

- Runs as a nyx-agent container that receives A2A requests, fires heartbeats, and runs jobs
- Forwards all LLM work to a dedicated backend container (`a2-claude`, `a2-codex`, or `a2-gemini`)
- Exposes an [A2A (Agent-to-Agent)](https://a2a-protocol.org) interface for communication
- Has its own identity, memory, and configuration — none baked into the image

Backend containers:

- Are standalone A2A servers with their own session state, memory, and conversation logs
- Receive identity via a mounted `agent.md` file
- Expose `/health` and `/metrics` in addition to the A2A endpoints

## Requirements

- Docker
- Docker Compose
- A Claude Code OAuth token (`claude setup-token`) or Anthropic API key (for `a2-claude`)
- An OpenAI API key (for `a2-codex`)
- A Gemini API key (for `a2-gemini`)

## Getting Started

### 1. Build the images

```bash
docker build -f agent/Dockerfile -t nyx-agent:latest .
docker build -f a2-claude/Dockerfile -t a2-claude:latest .
docker build -f a2-codex/Dockerfile -t a2-codex:latest .
docker build -f a2-gemini/Dockerfile -t a2-gemini:latest .
```

### 2. Configure credentials

```bash
export CLAUDE_CODE_OAUTH_TOKEN=your-token-here
export OPENAI_API_KEY=your-key-here
export GEMINI_API_KEY=your-key-here
```

### 3. Start the agents

```bash
docker compose -f docker-compose.active.yml up -d
```

### 4. Verify

```bash
# nyx-agent (router layer)
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
├── .claude/           # Claude Code config (settings.json, mcp.json)
├── .codex/            # Codex config (config.toml)
├── .gemini/           # Gemini backend config (no extra config required)
├── logs/              # nyx-agent logs (runtime, not committed)
├── a2-claude/         # Claude backend instance
│   ├── agent.md       # Backend identity
│   ├── logs/          # Conversation log (runtime, not committed)
│   └── memory/        # Persistent memory (runtime, not committed)
├── a2-codex/          # Codex backend instance
│   ├── agent.md
│   ├── logs/
│   └── memory/
└── a2-gemini/         # Gemini backend instance
    ├── agent.md
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

## Adding an Agent

1. Copy an existing agent directory:

   ```bash
   cp -r .agents/active/iris .agents/active/<name>
   ```

2. Update `.agents/active/<name>/.nyx/agent-card.md` with the agent's identity and role

3. Update `.agents/active/<name>/a2-claude/agent.md` (and `a2-codex/agent.md` and `a2-gemini/agent.md`) with backend
   identity

4. Update `.agents/active/<name>/.nyx/backend.yaml` with the new agent's backend service names and URLs

5. Add the agent and its backends to `docker-compose.active.yml` using the next available ports

6. Register the agent in `.agents/active/manifest.json`

7. Update the port table in `AGENTS.md` and `README.md`

8. Start the agent and its backends:

   ```bash
   docker compose -f docker-compose.active.yml up -d <name> <name>-a2-claude <name>-a2-codex <name>-a2-gemini
   ```

## Communication

Agents communicate over the [A2A protocol](https://a2a-protocol.org) via JSON-RPC. Each nyx agent exposes:

- `/.well-known/agent.json` — agent card (identity and capabilities)
- `/` — A2A JSON-RPC endpoint (`message/send`)
- `GET /health/start` — startup probe: 200 once ready, 503 while initializing
- `GET /health/live` — liveness probe: always 200 with `{"status": "ok", "agent": ..., "uptime_seconds": ...}`
- `GET /health/ready` — readiness probe: 200/`{"status": "ready"}` or 503/`{"status": "starting"}`
- `GET /jobs` — structured snapshot of all registered scheduled jobs (name, cron, backend, running state)
- `GET /tasks` — structured snapshot of all registered scheduled tasks (name, days, window, running state)
- `GET /webhooks` — structured snapshot of all registered webhook subscriptions (name, url, filters, active deliveries)
- `GET /continuations` — structured snapshot of all registered continuation items (name, continues-after, filters, active fires)
- `GET /triggers` — structured snapshot of all registered inbound trigger endpoints (name, endpoint, description, session, backend, running state)

Each backend container additionally exposes:

- `GET /health` — health check: 200/`{"status": "ok", "agent": ..., "uptime_seconds": ...}` or
  503/`{"status": "starting"}` while initializing
- `GET /metrics` — Prometheus metrics (when `METRICS_ENABLED` is set)

## Memory

Each backend agent manages its own memory at `.agents/<env>/<name>/<backend>/memory/`. For `a2-claude` and `a2-codex`,
memory files are markdown documents. For `a2-gemini`, conversation history is stored as JSON in `memory/sessions/`.
Memory files are not committed to source control. nyx-agent has no memory layer of its own.

## Authentication

| Service   | Method             | Environment variable                 |
| --------- | ------------------ | ------------------------------------ |
| a2-claude | Claude Max (OAuth) | `CLAUDE_CODE_OAUTH_TOKEN`            |
| a2-claude | Anthropic API key  | `ANTHROPIC_API_KEY`                  |
| a2-codex  | OpenAI API key     | `OPENAI_API_KEY`                     |
| a2-gemini | Gemini API key     | `GEMINI_API_KEY` or `GOOGLE_API_KEY` |

## Configuration

### nyx-agent environment variables

| Variable                   | Default                         | Description                                                                                          |
| -------------------------- | ------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `AGENT_NAME`               | `nyx-agent`                     | Agent display name (e.g. `iris`)                                                                     |
| `AGENT_HOST`               | `0.0.0.0`                       | Interface to bind                                                                                    |
| `AGENT_PORT`               | `8000`                          | HTTP port the nyx agent listens on                                                                   |
| `BACKEND_CONFIG_PATH`      | `/home/agent/.nyx/backend.yaml` | Path to the backend routing config file                                                              |
| `METRICS_ENABLED`          | _(unset)_                       | Set to any non-empty value to expose `/metrics`                                                      |
| `METRICS_AUTH_TOKEN`       | _(unset)_                       | Bearer token required to access `/metrics` (recommended in production)                               |
| `METRICS_CACHE_TTL`        | `15`                            | Seconds to cache aggregated backend metrics between scrapes                                          |
| `CONVERSATIONS_AUTH_TOKEN`         | _(unset)_                       | Bearer token required to access `/conversations` and `/trace` (inbound)                              |
| `BACKEND_CONVERSATIONS_AUTH_TOKEN` | _(unset)_                       | Bearer token forwarded to backend `/conversations` and `/trace` endpoints (set if backends require auth) |
| `PROXY_AUTH_TOKEN`                 | _(unset)_                       | Bearer token required to access `/proxy/{agent_name}`                                                |
| `TRIGGERS_AUTH_TOKEN`      | _(unset)_                       | Bearer token required for inbound trigger requests (fallback when no per-trigger HMAC secret is set) |
| `CORS_ALLOW_ORIGINS`       | `*`                             | Comma-separated list of allowed CORS origins; defaults to `*` (logs a warning)                       |
| `TASK_STORE_PATH`          | _(unset)_                       | Path for SQLite A2A task store; defaults to in-memory (state lost on restart)                        |
| `WORKER_MAX_RESTARTS`              | `5`                             | Consecutive crash limit before a critical worker marks the agent not-ready                           |
| `WEBHOOK_MAX_CONCURRENT_DELIVERIES` | `50`                            | Maximum number of in-flight webhook delivery tasks; deliveries beyond this cap are shed and counted  |

### Backend (a2-claude / a2-codex / a2-gemini) environment variables

| Variable                   | Default                            | Description                                                                   |
| -------------------------- | ---------------------------------- | ----------------------------------------------------------------------------- |
| `AGENT_NAME`               | `a2-claude`/`a2-codex`/`a2-gemini` | Backend instance name (e.g. `iris-a2-claude`)                                 |
| `AGENT_OWNER`              | _(same as `AGENT_NAME`)_           | Named agent this backend belongs to (e.g. `iris`); used in metric labels      |
| `AGENT_ID`                 | `claude`/`codex`/`gemini`          | Backend slot identifier (e.g. `claude`); used in metric labels                |
| `AGENT_URL`                | `http://localhost:8080/`           | Public A2A endpoint URL for the agent card                                    |
| `AGENT_MD`                 | `/home/agent/agent.md`             | Path to the identity file mounted into the container                          |
| `BACKEND_PORT`             | `8080`                             | HTTP port the backend listens on (internal)                                   |
| `METRICS_ENABLED`          | _(unset)_                          | Set to any non-empty value to expose `/metrics`                               |
| `CONVERSATIONS_AUTH_TOKEN` | _(unset)_                          | Bearer token required to access `/conversations` and `/trace`                 |
| `TASK_STORE_PATH`          | _(unset)_                          | Path for SQLite A2A task store; defaults to in-memory (state lost on restart) |
| `WORKER_MAX_RESTARTS`      | `5`                                | Consecutive crash limit before a critical worker marks the backend not-ready  |

## Metrics

When `METRICS_ENABLED` is set, Prometheus metrics are served at `/metrics` on both nyx-agent and backend containers.

Backend containers (`a2-claude`, `a2-codex`, `a2-gemini`) expose `a2_*`-prefixed metrics. `a2-claude` exposes a superset
that includes tool call, context window, and MCP metrics; `a2-codex` and `a2-gemini` expose the common `a2_*` set.
nyx-agent exposes `agent_*`-prefixed infrastructure metrics (bus, heartbeat, job, sessions, webhooks, etc.). The
nyx-agent `/metrics` endpoint also aggregates all backend `/metrics` endpoints, injecting a `backend="<id>"` label on
each sample so a single scrape target captures the full deployment.

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

The markdown body is the POST payload. Use `{{variable}}` placeholders for substitution:

| Variable               | Value                                          |
| ---------------------- | ---------------------------------------------- |
| `{{kind}}`             | Prompt kind (`a2a`, `heartbeat`, `job:<name>`) |
| `{{session_id}}`       | Session/context ID                             |
| `{{source}}`           | Source name (job name, trigger endpoint, etc.) |
| `{{model}}`            | Model used for the prompt                      |
| `{{response_preview}}` | First 2048 chars of the response text          |
| `{{duration_seconds}}` | Prompt execution time in seconds               |

If the body is empty, a default JSON envelope is sent.
