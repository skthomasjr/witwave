# nyx-agent

nyx-agent is the orchestration layer for the autonomous agent platform. It owns no LLM of its own — its job is to receive work, route it to the right backend, and coordinate everything around that: scheduling, triggering, chaining, webhooks, and observability.

## What it does

Every named agent (iris, nova, kira, …) runs one instance of this image alongside its backend containers. nyx-agent acts as the single entry point for all inbound requests and all scheduled work.

**A2A relay** — Receives A2A JSON-RPC requests and forwards them to the backend configured for the `a2a` routing slot. Returns the response verbatim. External callers always target nyx; they never talk to backends directly.

**Heartbeat scheduler** — Fires a prompt on a cron schedule defined in `HEARTBEAT.md`. Used to give an agent a regular opportunity to reflect, check in, or take proactive action.

**Job scheduler** — Reads `jobs/*.md` files with cron frontmatter and fires each on its schedule. Jobs are stateless — each run gets its own session unless a persistent session ID is configured.

**Task scheduler** — Reads `tasks/*.md` files with calendar-style frontmatter (days of week, time window, date range). Supports loop mode (repeated firing within a window), checkpoint recovery after restarts, and deterministic session IDs per agent+task+date.

**Trigger handler** — Serves `POST /triggers/{endpoint}` HTTP endpoints defined in `triggers/*.md`. Validates requests via HMAC-SHA256 (GitHub-style) or bearer token, dispatches the payload as a prompt to the backend, and returns 202 immediately.

**Continuation runner** — Reads `continuations/*.md` and fires a follow-up prompt whenever a named upstream (job, task, trigger, a2a, or another continuation) completes. Supports conditional firing (on success/error), substring matching on the response, and optional delay. Enables prompt chaining without hardcoded sequences.

**Webhook dispatcher** — Reads `webhooks/*.md` and delivers outbound HTTP notifications when work completes. Supports glob-based filtering, optional LLM extraction passes, HMAC signing, and retry with exponential backoff.

**Proxy endpoints** — Exposes `/proxy/{agent_name}` and `/conversations/{agent_name}` so the UI can target any team member by name and have the request routed through nyx's team manifest.

**Metrics** — Aggregates Prometheus metrics from all backends at `/metrics` and exposes its own scheduler/queue/routing metrics.

## Key files

| File | Purpose |
|------|---------|
| `main.py` | Starlette HTTP server; wires all routes and starts all background loops |
| `executor.py` | Core routing engine; maintains an LRU session cache; dispatches prompts to backends |
| `bus.py` | Async message queue with deduplication and backpressure |
| `heartbeat.py` | Periodic heartbeat loop |
| `jobs.py` | Cron-based job scheduler with checkpoint recovery |
| `tasks.py` | Calendar-based task scheduler with window/loop support |
| `triggers.py` | Inbound HTTP trigger registration and dispatch |
| `continuations.py` | Conditional follow-up chaining after upstream completion |
| `webhooks.py` | Outbound notification delivery |
| `backends/config.py` | Loads and hot-reloads `backend.yaml` |
| `backends/a2a.py` | Forwards requests to a remote A2A backend over HTTP/JSON-RPC |
| `metrics.py` | Prometheus metric definitions |
| `utils.py` | Frontmatter parser, duration parser, shared helpers |

## Configuration

All configuration is file-based and hot-reloaded — no restart required for most changes.

**`backend.yaml`** — Which backend handles each concern. Routing slots: `default`, `a2a`, `heartbeat`, `job`, `task`, `trigger`, `continuation`. Each slot can specify a backend ID and an optional model override.

**`HEARTBEAT.md`** — Cron schedule + prompt body for the heartbeat.

**`jobs/*.md`** — Frontmatter: `schedule` (cron), `session` (optional fixed ID), `agent`/`model` overrides. Body: the prompt.

**`tasks/*.md`** — Frontmatter: `days` (e.g. `mon-fri`), `start`/`end` time window, `loop`/`gap` for repeated firing, optional date range. Body: the prompt.

**`triggers/*.md`** — Frontmatter: `endpoint` (URL slug), `secret` (HMAC key), `agent`/`model` overrides. Body: system context prepended to the inbound payload.

**`continuations/*.md`** — Frontmatter: `continues-after` (upstream kind), `on` (success/error/any), `trigger-when` (substring match), `delay`. Body: the follow-up prompt.

**`webhooks/*.md`** — Frontmatter: `url` (template), `kind` (glob filter), `secret` (HMAC key), `extract` (prompt for LLM extraction pass). Body: webhook payload template.

## Runtime

nyx-agent is a Docker container. It mounts the agent's `.nyx/` directory for configuration and writes conversation and trace logs to individual files under `logs/`. The `MANIFEST_PATH` environment variable points to the team manifest (`manifest.json`), which lists all agents in the deployment by name and URL.
