# a2-gemini

a2-gemini is the Google Gemini backend for the autonomous agent platform. It is a standalone A2A server that wraps the
Google `google-genai` SDK, managing its own sessions, conversation logs, trace logs, and Prometheus metrics.

## What it does

a2-gemini receives A2A JSON-RPC requests (forwarded by nyx-agent), runs them through a Gemini model via the
`google-genai` SDK, and logs everything to JSONL files.

Each named agent that uses Gemini gets its own dedicated instance of this image (e.g. `iris-a2-gemini`,
`bob-a2-gemini`). Instances are completely isolated — separate sessions, logs, and metrics.

## Key features

**Session history** — Conversation history is stored as JSON in `memory/sessions/`. Each session accumulates turns,
giving Gemini context across messages within the same session. An in-memory LRU cache (bounded by `MAX_SESSIONS`) tracks
active sessions; when a session is evicted, its JSON file is deleted from disk so storage does not grow without bound.

**Model override** — The model for a given request can be set via `metadata.model` in the A2A message. Resolution order:
per-message metadata → routing config model → `MODEL` environment variable.

**Agent identity** — The system prompt is loaded from `/home/agent/.gemini/GEMINI.md`. The agent's name and behavioral
constraints live there. The file is hot-reloaded on change — updating `GEMINI.md` takes effect for the next request
without restarting the container.

**Metrics** — Exposes the common `a2_*` Prometheus metrics: request count/latency, session starts/evictions, queue
depth, error counts, and execution duration.

## Endpoints

| Endpoint                      | Purpose                                                                                           |
| ----------------------------- | ------------------------------------------------------------------------------------------------- |
| `POST /`                      | A2A JSON-RPC task endpoint                                                                        |
| `GET /.well-known/agent.json` | A2A agent discovery                                                                               |
| `GET /health`                 | Health check                                                                                      |
| `GET /metrics`                | Prometheus metrics                                                                                |
| `GET /conversations`          | Conversation log (JSONL, filterable by `since`/`limit`)                                           |
| `GET /trace`                  | Trace log (JSONL, filterable by `since`/`limit`)                                                  |
| `POST /mcp`                   | MCP JSON-RPC server (`initialize`, `tools/list`, `tools/call`); exposes a single `ask_agent` tool |

## Key files

| File                   | Purpose                                                     |
| ---------------------- | ----------------------------------------------------------- |
| `main.py`              | A2A server entrypoint; registers routes and starts uvicorn  |
| `executor.py`          | Google Gemini SDK executor; session management, logging     |
| `metrics.py`           | Prometheus metric definitions                               |
| `sqlite_task_store.py` | SQLite-backed task store (used when TASK_STORE_PATH is set) |
| `requirements.txt`     | Python dependencies                                         |
| `Dockerfile`           | Container image definition                                  |

## Secrets

Create a Kubernetes secret with the required credentials before deploying:

```bash
kubectl create secret generic <agent>-gemini-secrets \
  --from-literal=GEMINI_API_KEY=... \
  --namespace nyx
```

`GOOGLE_API_KEY` is also accepted as an alternative to `GEMINI_API_KEY`.

Reference the secret in your Helm values:

```yaml
backends:
  - name: gemini
    envFrom:
      - secretRef:
          name: <agent>-gemini-secrets
```

## Runtime

a2-gemini mounts:

- `GEMINI.md` — agent identity (system prompt), at `/home/agent/.gemini/GEMINI.md`
- `logs/conversation.jsonl` — conversation log file (must pre-exist as a file)
- `logs/trace.jsonl` — trace log file (must pre-exist as a file)
- `memory/` — persistent memory and session history directory (`memory/sessions/` for JSON session files)

Key environment variables: `AGENT_NAME` (instance name), `AGENT_OWNER` (named agent, e.g. `iris`), `AGENT_ID` (backend
slot id, e.g. `gemini`), `AGENT_URL`, `BACKEND_PORT`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`), `GEMINI_MODEL`
(model override, default `gemini-2.5-pro`), `SESSION_STORE_DIR` (directory for session JSON files, default
`/home/agent/memory/sessions`), `MAX_SESSIONS` (LRU cache size, default `10000`), `METRICS_ENABLED`,
`CONVERSATIONS_AUTH_TOKEN`, `TASK_STORE_PATH`, `WORKER_MAX_RESTARTS`, `LOG_PROMPT_MAX_BYTES` (max bytes of prompt logged
at INFO; default 200; set to 0 to suppress).
