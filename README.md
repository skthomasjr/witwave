# autonomous-agent

A platform for building persistent, self-directed AI agents that can work autonomously on software projects — including
improving themselves.

The primary use case is autonomous software development: agents that can triage issues, implement features, fix bugs,
evaluate their own work, and iterate — continuously and without human intervention. The same platform can be pointed at
any software project, not just this one.

Agents are currently bootstrapped manually using AI CLI tools (Claude Code, Codex). The long-term goal is for the
agents to take over their own development cycle: evaluating the codebase, proposing improvements, implementing them,
and shipping — closing the loop without a human in the hot path.

---

Built on the [A2A protocol](https://a2a-protocol.org). Each named agent is a pair of containers: a **nyx-agent**
infrastructure layer (A2A relay, heartbeat scheduler, agenda scheduler) and one or more **backend** containers that do
the actual LLM work (Claude Agent SDK via `a2-claude`, OpenAI Agents SDK via `a2-codex`).

Multiple agents can collaborate as a team, but the named agent (nyx + its backends) is the deployable unit.

## How It Works

Each agent:

- Runs as a nyx-agent container that receives A2A requests, fires heartbeats, and runs agenda items
- Forwards all LLM work to a dedicated backend container (`a2-claude` or `a2-codex`)
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
```

## Agent Structure

Active agents are defined under `.agents/active/`. Each named agent has its own directory containing nyx config,
backend instances, logs, and memory.

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
├── .nyx/              # Runtime config (agent-card.md, backends.yaml, HEARTBEAT.md, agenda/)
├── .claude/           # Claude Code config (settings.json, mcp.json)
├── .codex/            # Codex config (config.toml)
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
```

## Routing Configuration

Each agent's `backends.yaml` (under `.nyx/`) controls where nyx routes each type of work:

```yaml
backends:
  - id: iris-a2-claude
    type: a2a
    url: http://iris-a2-claude:8080

  - id: iris-a2-codex
    type: a2a
    url: http://iris-a2-codex:8080

routing:
  default: iris-a2-claude    # fallback backend when no per-concern override matches
  a2a: iris-a2-claude        # handles incoming A2A requests
  heartbeat: iris-a2-claude  # handles heartbeat-triggered work
  agenda: iris-a2-claude     # handles agenda task execution
```

## Adding an Agent

1. Copy an existing agent directory:

   ```bash
   cp -r .agents/active/iris .agents/active/<name>
   ```

2. Update `.agents/active/<name>/.nyx/agent-card.md` with the agent's identity and role

3. Update `.agents/active/<name>/a2-claude/agent.md` (and `a2-codex/agent.md`) with backend identity

4. Update `.agents/active/<name>/.nyx/backends.yaml` with the new agent's backend service names and URLs

5. Add the agent and its backends to `docker-compose.active.yml` using the next available ports

6. Update the port table in `AGENTS.md`

7. Start the agent and its backends:

   ```bash
   docker compose -f docker-compose.active.yml up -d <name> <name>-a2-claude <name>-a2-codex
   ```

## Communication

Agents communicate over the [A2A protocol](https://a2a-protocol.org) via JSON-RPC. Each nyx agent exposes:

- `/.well-known/agent.json` — agent card (identity and capabilities)
- `/` — A2A JSON-RPC endpoint (`message/send`)
- `GET /health/start` — startup probe: 200 once ready, 503 while initializing
- `GET /health/live` — liveness probe: always 200 with `{"status": "ok", "agent": ..., "uptime_seconds": ...}`
- `GET /health/ready` — readiness probe: 200/`{"status": "ready"}` or 503/`{"status": "starting"}`

Each backend container additionally exposes:

- `GET /health` — health check: `{"status": "ok", "agent": ..., "uptime_seconds": ...}`
- `GET /metrics` — Prometheus metrics (when `METRICS_ENABLED` is set)

## Memory

Each backend agent manages its own memory at `.agents/<env>/<name>/<backend>/memory/`. Memory files are markdown
documents written and read by the backend at runtime. They are not committed to source control. nyx-agent has no
memory layer of its own.

## Authentication

| Service    | Method             | Environment variable                                         |
|------------|--------------------|--------------------------------------------------------------|
| a2-claude  | Claude Max (OAuth) | `CLAUDE_CODE_OAUTH_TOKEN`                                    |
| a2-claude  | Anthropic API key  | `ANTHROPIC_API_KEY`                                          |
| a2-codex   | OpenAI API key     | `OPENAI_API_KEY`                                             |
| a2-gemini  | Gemini API key     | `GEMINI_API_KEY` or `GOOGLE_API_KEY`                         |

## Configuration

### nyx-agent environment variables

| Variable               | Default    | Description                                              |
|------------------------|------------|----------------------------------------------------------|
| `AGENT_NAME`           | `nyx-agent`| Agent display name (e.g. `iris`)                         |
| `AGENT_PORT`           | `8000`     | HTTP port the nyx agent listens on                       |
| `BACKENDS_CONFIG_PATH` | `/home/agent/.nyx/backends.yaml` | Path to the backends config file     |
| `METRICS_ENABLED`      | _(unset)_  | Set to any non-empty value to expose `/metrics`          |

### Backend (a2-claude / a2-codex / a2-gemini) environment variables

| Variable          | Default               | Description                                             |
|-------------------|-----------------------|---------------------------------------------------------|
| `AGENT_NAME`      | `a2-claude`/`a2-codex`| Backend instance name (e.g. `iris-a2-claude`)           |
| `AGENT_URL`       | `http://localhost:8080/` | Public A2A endpoint URL for the agent card            |
| `AGENT_MD`        | `/home/agent/agent.md`| Path to the identity file mounted into the container    |
| `BACKEND_PORT`    | `8080`                | HTTP port the backend listens on (internal)             |
| `METRICS_ENABLED` | _(unset)_             | Set to any non-empty value to expose `/metrics`         |

## Metrics

When `METRICS_ENABLED` is set, Prometheus metrics are served at `/metrics` on both nyx-agent and backend containers.

Backend containers (`a2-claude`, `a2-codex`) expose `a2_*`-prefixed metrics with parity across both implementations.
nyx-agent exposes `agent_*`-prefixed infrastructure metrics (bus, heartbeat, agenda, sessions, etc.).
