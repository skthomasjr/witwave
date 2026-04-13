# a2-codex

a2-codex is the OpenAI/Codex backend for the autonomous agent platform. It is a standalone A2A server that wraps the OpenAI Agents SDK, managing its own sessions, conversation logs, trace logs, and Prometheus metrics.

## What it does

a2-codex receives A2A JSON-RPC requests (forwarded by nyx-agent), runs them through an OpenAI model via the Agents SDK with streaming, and logs everything to JSONL files.

Each named agent that uses Codex gets its own dedicated instance of this image (e.g. `iris-a2-codex`, `bob-a2-codex`). Instances are completely isolated — separate sessions, logs, and metrics.

## Key features

**Session persistence** — Sessions are stored in a SQLite database (`logs/codex_sessions.db`). Unlike in-memory caches, sessions survive container restarts. The Agents SDK uses this to maintain conversation continuity across restarts.

**Streaming** — Uses `Runner.run_streamed()` with async event iteration. Response chunks are collected as they arrive and assembled into the final text response.

**Tool support** — The agent is configured with `LocalShellTool` (shell command execution), `WebSearchTool` (web search via OpenAI's search index), and optionally `ComputerTool` (headless browser via Playwright). Tools are enabled/disabled via environment variables and `config.toml`.

**Headless browser** — When computer tool support is enabled, a2-codex manages a Playwright Chromium instance via `computer.py`. The browser is initialized lazily on first use and reused across tool calls within a session. Supports screenshot, click, scroll, type, keypress, and drag operations.

**Model override** — The model for a given request can be set via `metadata.model` in the A2A message. Resolution order: per-message metadata → routing config model → `MODEL` environment variable.

**Agent identity** — The system prompt is the contents of a mounted `agent.md` file. The agent's name and behavioral constraints live there.

**Metrics** — Exposes the common `a2_*` Prometheus metrics: request count/latency, session starts/evictions, queue depth, error counts, and execution duration.

## Endpoints

| Endpoint | Purpose |
|----------|---------|
| `POST /` | A2A JSON-RPC task endpoint |
| `GET /.well-known/agent.json` | A2A agent discovery |
| `GET /health` | Health check |
| `GET /metrics` | Prometheus metrics |
| `GET /conversations` | Conversation log (JSONL, filterable by `since`/`limit`) |
| `GET /trace` | Trace log (JSONL, filterable by `since`/`limit`) |

## Key files

| File | Purpose |
|------|---------|
| `main.py` | A2A server entrypoint; registers routes and starts uvicorn |
| `executor.py` | OpenAI Agents SDK executor; session management, streaming, logging |
| `computer.py` | PlaywrightComputer — headless Chromium browser implementation |
| `metrics.py` | Prometheus metric definitions |
| `sqlite_task_store.py` | SQLite-backed task store (used when TASK_STORE_PATH is set) |
| `requirements.txt` | Python dependencies |
| `Dockerfile` | Container image definition |

## Runtime

a2-codex mounts:
- `agent.md` — agent identity (system prompt)
- `config.toml` — tool enablement flags (optional)
- `logs/conversation.jsonl` — conversation log file (must pre-exist as a file)
- `logs/trace.jsonl` — trace log file (must pre-exist as a file)
- `memory/` — persistent memory directory

The `AGENT_NAME`, `AGENT_ID`, `OPENAI_API_KEY`, and `MODEL` environment variables configure identity and credentials. `METRICS_ENABLED=true` activates the Prometheus endpoint. `COMPUTER_USE_ENABLED=true` activates the Playwright browser tool.
