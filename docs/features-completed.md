# Features Completed

Features move here once all their TODO items are marked `[x]`.

---

## F-002 — JSONL tool-use trace log

**Status:** implemented

**Summary:** Emits a structured per-event log (`logs/trace.jsonl`) capturing tool name, inputs, outputs, timestamps,
session ID, and message kind alongside the existing `conversation.log`. Implemented in `executor.py` via
`log_tool_event()` and `get_trace_logger()` with `RotatingFileHandler`.

---

## F-004 — MCP server configuration via `.claude/mcp.json`

**Status:** implemented

**Summary:** Per-agent opt-in MCP configuration loaded from `/home/agent/.claude/mcp.json`. Implemented in `executor.py`
via `_load_mcp_config()`, `mcp_config_watcher()` (a background `asyncio` task using `watchfiles`), and the
`_mcp_servers` module-level variable passed to `ClaudeAgentOptions` in `make_options()`. Hot-reload on file change.
Malformed files log a warning and fall back to the last valid config.

---

## F-005 — Agenda run checkpoint detection

**Status:** implemented

**Summary:** Stale checkpoint files from interrupted agenda runs are detected and logged as warnings on startup.
Implemented in `agenda.py`: `run_agenda_item()` writes `AGENDA_DIR/.checkpoints/<stem>.running.json` (start time, item
name, session ID) before firing the bus message and deletes it in the `finally` block. `AgendaRunner._scan()` checks for
stale checkpoint files on startup and emits `logger.warning` per interrupted item.

---

## F-006 — Delegate skill

**Status:** implemented

**Summary:** Per-agent delegate skill documents created at `.agents/<name>/.claude/skills/delegate/SKILL.md` for all
three agents. Instructs agents to read `~/manifest.json` for team URLs, construct an A2A `message/send` JSON-RPC payload
with the caller's session ID in metadata, POST via curl, and parse the response. Pure documentation change — no Python
code changes.

---

## F-007 — Kubernetes-compatible health endpoints

**Status:** implemented

**Summary:** Three distinct health probe endpoints added to `main.py`: `GET /health/start` (startup probe — 200 once
ready, 503 while initializing), `GET /health/live` (liveness probe — always 200 with agent name and uptime),
`GET /health/ready` (readiness probe — 200/`ready` or 503/`starting`). The `_ready` flag is set by
`_set_ready_when_started()` after `uvicorn.Server.started` is confirmed. Endpoints are handled by a lightweight
`Starlette` sub-app dispatched before the A2A app. Dockerfile `HEALTHCHECK` updated to `/health/live`.

---

## F-008 — Prometheus metrics endpoint

**Status:** implemented

**Summary:** Opt-in `/metrics` endpoint added to `main.py`, enabled via `METRICS_ENABLED=true` env var. Mounts
`prometheus_client.make_asgi_app()` and registers a single `agent_up` gauge set to `1.0` at startup. `prometheus-client`
added to Dockerfile pip dependencies. Zero overhead when disabled.

---

## F-011 — Context usage monitoring

**Status:** implemented

**Summary:** `run_query()` in `executor.py` was migrated from the stateless `query()` function to `ClaudeSDKClient`,
which exposes `get_context_usage()`. After each `AssistantMessage`, the method is called and the result is compared
against `CONTEXT_USAGE_WARN_THRESHOLD` (env var, float 0–1, default 0.9). When the threshold is exceeded, a `WARNING` is
emitted to the main logger and a structured JSONL `context_usage` entry (session ID, timestamp, percentage, totalTokens,
maxTokens, category breakdown) is written to the trace log. All logic is additive — no effect on the execution path,
session management, or metric counters.
