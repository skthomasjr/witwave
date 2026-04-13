# a2-gemini

a2-gemini is the Google Gemini backend for the autonomous agent platform. It is a standalone A2A server that wraps the Google `google-genai` SDK, managing its own sessions, conversation logs, trace logs, and Prometheus metrics.

## What it does

a2-gemini receives A2A JSON-RPC requests (forwarded by nyx-agent), runs them through a Gemini model via the `google-genai` SDK, and logs everything to JSONL files.

Each named agent that uses Gemini gets its own dedicated instance of this image (e.g. `iris-a2-gemini`, `bob-a2-gemini`). Instances are completely isolated — separate sessions, logs, and metrics.

## Key features

**Session history** — Conversation history is stored as JSON in `memory/sessions/`. Each session accumulates turns, giving Gemini context across messages within the same session.

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
| `executor.py` | Google Gemini SDK executor; session management, logging |
| `metrics.py` | Prometheus metric definitions |
| `sqlite_task_store.py` | SQLite-backed task store (used when TASK_STORE_PATH is set) |
| `requirements.txt` | Python dependencies |
| `Dockerfile` | Container image definition |

## Runtime

a2-gemini mounts:
- `agent.md` — agent identity (system prompt)
- `logs/conversation.jsonl` — conversation log file (must pre-exist as a file)
- `logs/trace.jsonl` — trace log file (must pre-exist as a file)
- `memory/` — persistent memory and session history directory (`memory/sessions/` for JSON session files)

Key environment variables: `AGENT_NAME` (instance name), `AGENT_OWNER` (named agent, e.g. `iris`), `AGENT_ID` (backend slot id, e.g. `gemini`), `AGENT_URL`, `AGENT_MD`, `BACKEND_PORT`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`), `GEMINI_MODEL` (model override, default `gemini-2.5-pro`), `METRICS_ENABLED`, `CONVERSATIONS_AUTH_TOKEN`, `TASK_STORE_PATH`, `WORKER_MAX_RESTARTS`.
