# a2-claude

a2-claude is the Claude backend for the autonomous agent platform. It is a standalone A2A server that wraps the Claude
Agent SDK, managing its own sessions, conversation logs, trace logs, and Prometheus metrics.

## What it does

a2-claude receives A2A JSON-RPC requests (forwarded by nyx-agent), runs them through Claude via the Claude Agent SDK
CLI, streams back the response, and logs everything to JSONL files.

Each named agent that uses Claude gets its own dedicated instance of this image (e.g. `iris-a2-claude`,
`bob-a2-claude`). Instances are completely isolated — separate sessions, logs, memory, and metrics.

## Key features

**Session continuity** — Sessions are tracked in an in-process LRU cache keyed by session ID. Resuming a session carries
conversation history forward. The SDK handles context window management; a2-claude monitors usage and warns at 90%
utilization.

**MCP server support** — Loads MCP server definitions from a mounted `mcp.json` file. The file is hot-reloaded on each
request, so MCP servers can be added or reconfigured without restarting the container.

**Tool tracing** — Every `tool_use` and `tool_result` event is captured from the SDK stream and written to `trace.jsonl`
alongside summary response events. This gives full visibility into what tools Claude called and what they returned.

**Model override** — The model used for a given request can be overridden via `metadata.model` in the A2A message.
Resolution order: per-message metadata → routing config model → default model in `backend.yaml`.

**Agent identity** — Claude's system prompt is loaded from `/home/agent/.claude/CLAUDE.md`.
The agent's name, personality, and behavioral constraints all live there. The file is hot-reloaded on change — updating
`CLAUDE.md` takes effect for the next request without restarting the container.

**Metrics** — Exposes a superset of the common `a2_*` Prometheus metrics, plus Claude-specific metrics: context window
token counts, context exhaustion events, tool call counts, MCP tool usage, and time-to-first-message.

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

| File                   | Purpose                                                      |
| ---------------------- | ------------------------------------------------------------ |
| `main.py`              | A2A server entrypoint; registers routes and starts uvicorn   |
| `executor.py`          | Claude Agent SDK executor; session cache, streaming, logging |
| `metrics.py`           | Prometheus metric definitions                                |
| `sqlite_task_store.py` | SQLite-backed task store (used when TASK_STORE_PATH is set)  |
| `requirements.txt`     | Python dependencies                                          |
| `Dockerfile`           | Container image definition                                   |

## Secrets

Create a Kubernetes secret with the required credentials before deploying:

```bash
kubectl create secret generic <agent>-claude-secrets \
  --from-literal=ANTHROPIC_API_KEY=sk-ant-... \
  --namespace nyx
```

For Claude Max (OAuth), use `CLAUDE_CODE_OAUTH_TOKEN` instead of `ANTHROPIC_API_KEY`.

Reference the secret in your Helm values:

```yaml
backends:
  - name: claude
    envFrom:
      - secretRef:
          name: <agent>-claude-secrets
```

## Runtime

a2-claude mounts:

- `CLAUDE.md` — agent identity (system prompt), at `/home/agent/.claude/CLAUDE.md`
- `mcp.json` — MCP server configuration (optional)
- `logs/conversation.jsonl` — conversation log file (must pre-exist as a file)
- `logs/trace.jsonl` — trace log file (must pre-exist as a file)
- `memory/` — persistent memory directory

Key environment variables: `AGENT_NAME` (instance name), `AGENT_OWNER` (named agent, e.g. `iris`), `AGENT_ID` (backend
slot id, e.g. `claude`), `AGENT_URL`, `BACKEND_PORT`, `ANTHROPIC_API_KEY` (or `CLAUDE_CODE_OAUTH_TOKEN` for
Claude Max), `CLAUDE_MODEL` (model override), `METRICS_ENABLED`, `CONVERSATIONS_AUTH_TOKEN`, `TASK_STORE_PATH`,
`WORKER_MAX_RESTARTS`, `LOG_PROMPT_MAX_BYTES` (max bytes of prompt logged at INFO; default 200; set to 0 to suppress).
