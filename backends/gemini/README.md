# a2-gemini

a2-gemini is the Google Gemini backend for the autonomous agent platform. It is a standalone A2A server that wraps the
Google `google-genai` SDK, managing its own sessions, conversation logs, trace logs, and Prometheus metrics.

## What it does

a2-gemini receives A2A JSON-RPC requests (forwarded by harness), runs them through a Gemini model via the
`google-genai` SDK, and logs everything to JSONL files.

Each named agent that uses Gemini gets its own dedicated instance of this image (e.g. `iris-a2-gemini`,
`bob-a2-gemini`). Instances are completely isolated — separate sessions, logs, and metrics.

## Key features

**Session history** — Conversation history is stored as JSON in `memory/sessions/`. Each session accumulates turns,
giving Gemini context across messages within the same session. An in-memory LRU cache (bounded by `MAX_SESSIONS`) tracks
active sessions; when a session is evicted, its JSON file is deleted from disk so storage does not grow without bound.

**Model override** — The model for a given request can be set via `metadata.model` in the A2A message. Resolution order:
per-message metadata → routing config model → `MODEL` environment variable.

**Startup validation** — The Gemini API key (`GEMINI_API_KEY` or `GOOGLE_API_KEY`) is validated at startup in
`AgentExecutor.__init__`. If neither is set, the container fails to start immediately rather than surfacing the error
on the first request.

**Agent identity** — The system prompt is loaded from `/home/agent/.gemini/GEMINI.md`. The agent's name and behavioral
constraints live there. The file is hot-reloaded on change — updating `GEMINI.md` takes effect for the next request
without restarting the container.

**Metrics** — Exposes the common `a2_*` Prometheus metrics: request count/latency, session starts/evictions, queue
depth, error counts, and execution duration. Also includes context-window metrics (`a2_context_tokens`,
`a2_context_usage_percent`, `a2_context_exhaustion_total`, etc.) tracked via `usage_metadata.total_token_count` on
each response chunk, and SDK error classification metrics (`a2_sdk_errors_total`, `a2_sdk_result_errors_total`,
`a2_sdk_client_errors_total`) that distinguish connection-level failures from result-level errors and catch-all
exceptions.

## Endpoints

| Endpoint                      | Purpose                                                                                           |
| ----------------------------- | ------------------------------------------------------------------------------------------------- |
| `POST /`                      | A2A JSON-RPC task endpoint                                                                        |
| `GET /.well-known/agent.json` | A2A agent discovery                                                                               |
| `GET /health`                 | Health check                                                                                      |
| `GET /metrics`                | Prometheus metrics                                                                                |
| `GET /conversations`          | Conversation log (JSONL, filterable by `since`/`limit`)                                           |
| `GET /trace`                  | Trace log (JSONL, filterable by `since`/`limit`)                                                  |
| `POST /mcp`                   | MCP JSON-RPC server (`initialize`, `tools/list`, `tools/call`); exposes a single `ask_agent` tool. Requires `Authorization: Bearer $CONVERSATIONS_AUTH_TOKEN` (#516) |

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
`/home/agent/.gemini/memory/sessions`), `MAX_SESSIONS` (LRU cache size, default `10000`), `GEMINI_MAX_HISTORY_TURNS`
(maximum number of conversation turns to persist per session; older turns are dropped to keep file sizes bounded;
default `100`; set to `0` to disable truncation and keep full history), `METRICS_ENABLED`,
`CONVERSATIONS_AUTH_TOKEN`, `TASK_STORE_PATH`, `WORKER_MAX_RESTARTS`, `LOG_PROMPT_MAX_BYTES` (max bytes of prompt logged
at INFO; default 200; set to 0 to suppress).

## Tools / MCP

a2-gemini supports external tool invocation via the Model Context Protocol (MCP). The google-genai SDK's
experimental MCP-as-tool path accepts raw `mcp.ClientSession` objects in `GenerateContentConfig(tools=[...])`
and handles the entire function_call / function_response ping-pong via its Automatic Function Calling (AFC)
loop.

**Enable MCP by mounting an `mcp.json`** at `/home/agent/.gemini/mcp.json` (override with the `MCP_CONFIG_PATH`
environment variable). The file uses the same shape as a2-claude and a2-codex:

```json
{
  "mcpServers": {
    "kubernetes": {
      "command": "python",
      "args": ["/app/server.py"],
      "env": {"KUBECONFIG": "/home/agent/.kube/config"}
    }
  }
}
```

Only stdio transport is supported today (the `command` shape above). HTTP transport support can be added if a
use case emerges. Each configured server is started once at process startup, reused across requests, and
hot-reloaded on `mcp.json` changes — the lifespan-scoped stack pattern is shared with a2-codex (#526).

**AFC vs. hooks caveat.** google-genai's AFC runs the tool loop inside `generate_content`, so the #631 hooks
skeleton (PreToolUse policy enforcement) **cannot intercept MCP tool calls**. Tool invocations are observable
after the fact (via `tool_use` / `tool_result` rows on `trace.jsonl` and the
`a2_sdk_tool_calls_total` / `a2_sdk_tool_duration_seconds` metrics), but a rule like "deny tool X on input
pattern Y" cannot block the call because the decision point is inside the SDK. If policy enforcement is a
hard requirement, the #640 issue body documents the alternate design (disable AFC and hand-roll the
function-call dispatch loop); no operator toggle ships today.

## Tracing (OpenTelemetry)

When `OTEL_ENABLED=true` is set, a2-gemini emits a server span for every `execute()` call and continues any trace
propagated by harness via the `metadata.traceparent` field (#469). The OTLP/HTTP exporter reads the standard
`OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_SERVICE_NAME` / `OTEL_TRACES_SAMPLER` env vars. When `OTEL_ENABLED` is falsy
(default) the OTel call sites are no-ops. Bootstrap in `shared/otel.py` is shared with the harness and other backends.
