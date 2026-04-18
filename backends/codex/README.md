# codex

codex is the OpenAI/Codex backend for the autonomous agent platform. It is a standalone A2A server that wraps the
OpenAI Agents SDK, managing its own sessions, conversation logs, trace logs, and Prometheus metrics.

## What it does

codex receives A2A JSON-RPC requests (forwarded by harness), runs them through an OpenAI model via the Agents SDK
with streaming, and logs everything to JSONL files.

Each named agent that uses Codex gets its own dedicated instance of this image (e.g. `iris-codex`, `bob-codex`).
Instances are completely isolated — separate sessions, logs, and metrics.

## Key features

**Session persistence** — Sessions are stored in a SQLite database (`logs/codex_sessions.db`). Unlike in-memory caches,
sessions survive container restarts. The Agents SDK uses this to maintain conversation continuity across restarts.

**Streaming** — Uses `Runner.run_streamed()` with async event iteration. Response chunks are collected as they arrive
and assembled into the final text response.

**Tool support** — The agent is configured with `LocalShellTool` (shell command execution), `WebSearchTool` (web search
via OpenAI's search index), and optionally `ComputerTool` (headless browser via Playwright). Tools are enabled/disabled
via environment variables and `config.toml`.

**Headless browser** — When computer tool support is enabled, codex manages a Playwright Chromium instance via
`computer.py`. The browser is initialized lazily on first use and reused across tool calls within a session. Each
session gets its own **isolated browser context** (#522) — cookies, storage, and navigation history do not leak
between sessions, even though the underlying Chromium process is shared. Supports screenshot, click, scroll, type,
keypress, and drag operations.

**Model override** — The model for a given request can be set via `metadata.model` in the A2A message. Resolution order:
per-message metadata → routing config model → `MODEL` environment variable.

**Agent identity** — The system prompt is loaded from `/home/agent/.codex/AGENTS.md`. The agent's name and behavioral
constraints live there. The file is hot-reloaded on change — updating `AGENTS.md` takes effect for the next request
without restarting the container.

**MCP servers** — External tools can be wired in via `/home/agent/.codex/mcp.json` (override path with
`MCP_CONFIG_PATH`). Same wire format as the claude `mcp.json` — entries with a `command` field become
`MCPServerStdio` instances, entries with a `url` field become `MCPServerStreamableHttp` instances. Servers are
entered via `AsyncExitStack` per request and passed to `Agent(mcp_servers=[...])`, so MCP-provided tools coexist
with the built-in shell / web search / Playwright computer tools. The file is hot-reloaded on change. Three
metrics track config state: `backend_mcp_config_errors_total`, `backend_mcp_config_reloads_total`, `backend_mcp_servers_active`.

**Metrics** — Exposes the common `backend_*` Prometheus metrics: request count/latency, session starts/evictions,
queue depth, error counts, and execution duration. Tool-call metrics (`backend_sdk_tool_calls_total`,
`backend_sdk_tool_duration_seconds`, `backend_sdk_tool_errors_total`,
`backend_sdk_tool_calls_per_query{agent,agent_id,backend,model}` — `model` label aligned with claude/gemini in
#795, input/output size histograms), context-window metrics, SDK error classification
(`backend_sdk_errors_total`, `backend_sdk_result_errors_total`, `backend_sdk_client_errors_total`,
`backend_sdk_context_fetch_errors_total`), per-task noise metrics
(`backend_stderr_lines_per_task`, `backend_tasks_with_stderr_total`), retries (`backend_task_retries_total`),
MCP config metrics (`backend_mcp_config_errors_total`, `backend_mcp_config_reloads_total`,
`backend_mcp_servers_active`, `backend_mcp_command_rejected_total{reason}` — #720), streaming-chunk drops
(`backend_streaming_chunks_dropped_total` — #724), empty-prompt rejections (`backend_empty_prompts_total` —
#801), and a `backend_sdk_subprocess_spawn_duration_seconds` zero-value placeholder so cross-backend
dashboards carry the series (codex's OpenAI Agents SDK runs in-process so nothing literal is measured).
Hook denials are counted on the canonical cross-backend `backend_hooks_denials_total{tool,source,rule}`;
the legacy `backend_codex_hooks_denials_total{rule}` alias is retained through one release cycle and its
emission is now gated by `EMIT_DEPRECATED_HOOK_METRICS` (off by default, #940). `backend_hook_post_shed_total`
tracks hook.decision POSTs shed when the async dispatcher queue is saturated (#928). Peer-parity
placeholders for the rest of claude's hook metric family (`backend_hooks_warnings_total`,
`backend_hooks_config_*`, `backend_hooks_active_rules`, `backend_hooks_evaluations_total`,
`backend_tool_audit_entries_total`) register at zero until the non-shell hook path lands (#586 deferred).
`backend_session_history_save_errors_total` increments when the SQLite session store fails to initialize or
LRU eviction cleanup fails.

## Endpoints

| Endpoint                      | Purpose                                                                                           |
| ----------------------------- | ------------------------------------------------------------------------------------------------- |
| `POST /`                      | A2A JSON-RPC task endpoint                                                                        |
| `GET /.well-known/agent.json` | A2A agent discovery                                                                               |
| `GET /health`                 | Health check                                                                                      |
| `GET /metrics`                | Prometheus metrics                                                                                |
| `GET /conversations`          | Conversation log (JSONL, filterable by `since`/`limit`)                                           |
| `GET /trace`                  | Trace log (JSONL, filterable by `since`/`limit`)                                                  |
| `POST /mcp`                   | MCP JSON-RPC server (`initialize`, `tools/list`, `tools/call`); exposes a single `ask_agent` tool. Requires `Authorization: Bearer $CONVERSATIONS_AUTH_TOKEN` (#510). `session_id` is routed through `shared/session_binding.derive_session_id` with a bearer-token fingerprint before lookup/insert (#929/#941) for parity with claude and gemini. OTel spans now cover the `/mcp` request flow (#966). |

## Key files

| File                   | Purpose                                                            |
| ---------------------- | ------------------------------------------------------------------ |
| `main.py`              | A2A server entrypoint; registers routes and starts uvicorn         |
| `executor.py`          | OpenAI Agents SDK executor; session management, streaming, logging |
| `computer.py`          | PlaywrightComputer — headless Chromium browser implementation      |
| `metrics.py`           | Prometheus metric definitions                                      |
| `sqlite_task_store.py` | SQLite-backed task store (used when TASK_STORE_PATH is set)        |
| `requirements.txt`     | Python dependencies                                                |
| `Dockerfile`           | Container image definition                                         |

## Secrets

Create a Kubernetes secret with the required credentials before deploying:

```bash
kubectl create secret generic <agent>-codex-secrets \
  --from-literal=OPENAI_API_KEY=sk-... \
  --namespace nyx
```

Reference the secret in your Helm values:

```yaml
backends:
  - name: codex
    envFrom:
      - secretRef:
          name: <agent>-codex-secrets
```

## Runtime

codex mounts:

- `AGENTS.md` — agent identity (system prompt), at `/home/agent/.codex/AGENTS.md`
- `config.toml` — tool enablement flags (optional)
- `logs/conversation.jsonl` — conversation log file (must pre-exist as a file)
- `logs/tool-activity.jsonl` — trace log file (must pre-exist as a file)
- `memory/` — persistent memory directory

Key environment variables: `AGENT_NAME` (instance name), `AGENT_OWNER` (named agent, e.g. `iris`), `AGENT_ID` (backend
slot id, e.g. `codex`), `AGENT_URL`, `BACKEND_PORT`, `OPENAI_API_KEY`, `CODEX_MODEL` (model override,
default `gpt-5.1-codex`), `METRICS_ENABLED`, `CONVERSATIONS_AUTH_TOKEN`,
`CONVERSATIONS_AUTH_DISABLED` (explicit escape hatch for no-auth mode, #718), `LOG_REDACT` (conversation
redaction toggle, #714), `TASK_STORE_PATH`, `WORKER_MAX_RESTARTS`, `COMPUTER_USE_ENABLED` (activates
Playwright browser tool), `LOG_PROMPT_MAX_BYTES` (max bytes of prompt logged at INFO; default 200; set to 0
to suppress), `MCP_ALLOWED_COMMANDS` / `MCP_ALLOWED_COMMAND_PREFIXES` / `MCP_ALLOWED_CWD_PREFIXES` (stdio
MCP entry allow-list, #720; rejections counted on `backend_mcp_command_rejected_total{reason}`). Default
allow-list pruned to `mcp-kubernetes,mcp-helm,uv,uvx` with the absolute-path basename fallback removed (#862);
interpreter args are additionally vetted via `mcp_command_args_safe()` (#930).

## Tracing (OpenTelemetry)

When `OTEL_ENABLED=true` is set, codex emits a server span for every `execute()` call and continues any trace
propagated by harness via the `metadata.traceparent` field (#469). The OTLP/HTTP exporter reads the standard
`OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_SERVICE_NAME` / `OTEL_TRACES_SAMPLER` env vars. When `OTEL_ENABLED` is falsy
(default) the OTel call sites are no-ops. Bootstrap in `shared/otel.py` is shared with the harness and other backends.
