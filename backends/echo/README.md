# echo backend

Echo is a zero-dependency A2A backend that returns a canned response quoting
the caller's prompt. It serves two distinct purposes:

1. **Hello-world default for `ww agent create`.** New users can deploy a live
   agent with "access to a Kubernetes cluster and the CLI" as the only
   prerequisites — no API keys, no external services, no pre-existing Secrets.
2. **Reference implementation of the common backend contract.** When a new
   backend type is added (Ollama, Mistral, self-hosted, …), echo is the
   template to copy from. It demonstrates the A2A wiring, the dedicated-port
   metrics listener, the common `backend_*` metric baseline, and the pytest
   contract suite — all without the LLM-SDK coupling that the real backends
   carry.

## In scope (baseline every backend should implement)

- A2A JSON-RPC server (`POST /`, `GET /.well-known/agent-card.json`)
- `GET /health` with 503→200 lifecycle
- Dedicated `/metrics` listener on `METRICS_PORT` (default 9000), gated on
  `METRICS_ENABLED`
- Common `backend_*` metric baseline with `(agent, agent_id, backend)` labels:
  - Lifecycle: `backend_up`, `backend_info`, `backend_uptime_seconds`,
    `backend_startup_duration_seconds`, `backend_health_checks_total`
  - A2A request surface: `backend_a2a_requests_total{status}`,
    `backend_a2a_request_duration_seconds`,
    `backend_a2a_last_request_timestamp_seconds`
  - Prompt shape: `backend_prompt_length_bytes`, `backend_response_length_bytes`,
    `backend_empty_prompts_total`
- Empty-prompt guard with counter bump
- pytest suite that verifies agent card, health lifecycle, A2A contract, and
  metrics exposition

## Intentionally out of scope

These concerns are coupled to LLM semantics and don't belong in a reference:

- **MCP integration** — echo has no tools to call. Real backends wire MCP via
  `shared/mcp_auth.py` and their own executor-level MCP stack.
- **Hooks engine** — echo has no tool calls to gate. Claude's
  `backends/claude/hooks.py` is the reference for `PreToolUse`/`PostToolUse`.
- **Conversation persistence** — echo is stateless per request. Real backends
  wire `shared/conversations.py` + `conversation.jsonl` + `/conversations` +
  `/trace`.
- **Session binding HMAC** — multi-tenant isolation via
  `shared/session_binding.py`. Echo has no sessions to bind.
- **OTel tracing** — echo is too trivial to benefit from spans. Real backends
  wire `shared/otel.py` + `TraceparentASGIMiddleware`.
- **LLM-specific metrics** — SDK errors, context-window, session LRU, tool
  audit, context exhaustion. Declaring them as always-zero placeholders would
  muddy dashboards.

A checklist for backend authors copying from echo: start with everything echo
does, then add the categories from this list that apply to your backend. Don't
invert the relationship — do not remove anything echo does.

## Endpoints

- `GET /.well-known/agent-card.json` — A2A agent card (auto-served by the SDK)
- `POST /` — A2A JSON-RPC task endpoint
- `GET /health` — liveness probe (503 during startup, 200 once ready)
- `GET /metrics` — Prometheus exposition on `METRICS_PORT` when
  `METRICS_ENABLED` is set

## Environment

- `AGENT_NAME` — display name on the agent card (default `echo`)
- `AGENT_OWNER` — `agent` label on every metric series (default `AGENT_NAME`)
- `AGENT_ID` — `agent_id` label on every metric series
  (default `$HOSTNAME` or `echo`)
- `AGENT_HOST` — A2A listen host (default `0.0.0.0`)
- `BACKEND_PORT` — A2A listen port (default `8000`)
- `METRICS_PORT` — metrics listener port (default `9000`)
- `METRICS_ENABLED` — any truthy value enables the metrics listener
- `AGENT_URL` — public URL advertised on the agent card
  (default `http://localhost:$BACKEND_PORT/`)
- `AGENT_VERSION` — version string on the agent card + `backend_info`
  (default `0.1.0`)

## Build

```bash
docker build -f backends/echo/Dockerfile -t echo:latest .
```

## Test

```bash
pip install -r backends/echo/requirements.txt pytest httpx
pytest backends/echo/test_echo.py -v
```

The pytest suite uses Starlette's `TestClient` (httpx under the hood), so no
uvicorn binding or port allocation is needed — the suite runs the A2A server
in-process against an ASGI test harness.
