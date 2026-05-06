# prometheus MCP tool

MCP server that exposes PromQL queries against a Prometheus deployment reachable from the cluster where this tool runs
(#853).

## Implementation

Thin wrapper over the standard Prometheus HTTP API — five handlers that map 1:1 onto the upstream endpoints. No PromQL
is rewritten on the way through; the tool is purely an authenticated bridge so agents can ask questions of the metrics
fabric without a direct network path.

- `query` → `GET /api/v1/query`
- `query_range` → `GET /api/v1/query_range`
- `series` → `GET /api/v1/series`
- `labels` → `GET /api/v1/labels`
- `label_values` → `GET /api/v1/label/<name>/values`

Grafana, Loki, and the OTel collector surface are deferred to separate follow-up tools — this server's remit is
read-only PromQL only.

## Auth

Point `PROMETHEUS_URL` at the in-cluster Prometheus Service (e.g.
`http://prom-kube-prometheus-stack-prometheus.monitoring:9090`). The scheme must be `http://` or `https://`; the server
refuses startup otherwise.

Optional `PROMETHEUS_BEARER_TOKEN` is forwarded as `Authorization: Bearer <token>` on every request when the Prometheus
endpoint is itself auth-gated (e.g. when scraping through a reverse proxy). Leave unset when the in-cluster Prometheus
Service is open to the pod network.

Callers into this MCP tool still need the shared bearer-token check from `shared/mcp_auth.py` (`MCP_TOOL_AUTH_TOKEN`);
that's independent of the upstream Prometheus credential.

## RBAC

**No Kubernetes RBAC needed for reads.** Unlike `mcp-kubernetes` and `mcp-helm`, this tool doesn't touch the apiserver —
it only talks to the Prometheus HTTP endpoint. ServiceAccount-level permissions on the pod itself are irrelevant to what
queries return.

If you run this alongside a Prometheus deployment that enforces per-tenant auth (e.g. Cortex / Mimir), bake that
tenant's bearer token into `PROMETHEUS_BEARER_TOKEN` and scope the token upstream.

## Environment variables

| Variable                      | Default           | Purpose                                                                                     |
| ----------------------------- | ----------------- | ------------------------------------------------------------------------------------------- |
| `PROMETHEUS_URL`              | _(required)_      | Base URL for the Prometheus HTTP API. Must be `http://` or `https://`.                      |
| `PROMETHEUS_BEARER_TOKEN`     | _(unset)_         | Optional bearer token for auth-gated Prometheus endpoints.                                  |
| `MCP_PROM_MAX_RESPONSE_BYTES` | `1048576` (1 MiB) | Per-query cap on serialised response size. Queries exceeding the cap return an error row.   |
| `MCP_RESPONSE_MAX_BYTES`      | `8388608` (8 MiB) | Cross-tool response cap (shared-parity envelope enforced after the per-query cap).          |
| `MCP_SUBPROCESS_TIMEOUT_SEC`  | `30`              | HTTP timeout for each Prometheus call. Prometheus query budgets are already short.          |
| `MCP_TOOL_AUTH_TOKEN`         | _(required)_      | Shared bearer token callers must present. `MCP_TOOL_AUTH_DISABLED=true` is the dev opt-out. |
| `MCP_PORT`                    | `8000`            | Listener port (FastMCP streamable-http). Kubernetes Service addresses this port.            |
| `OTEL_ENABLED`                | _(unset)_         | When set, each handler opens `mcp.handler` + `prom.api.call` spans (#637).                  |
| `OTEL_SERVICE_NAME`           | `mcp-prometheus`  | OTel resource service name override.                                                        |
| `METRICS_ENABLED`             | _(unset)_         | When set, the `/metrics` listener on the dedicated metrics port is started.                 |

## Tools

| Tool           | Description                                                                           |
| -------------- | ------------------------------------------------------------------------------------- |
| `query`        | Instant PromQL query (`/api/v1/query`). Optional `time=<rfc3339\|unix>`.              |
| `query_range`  | Range PromQL query (`/api/v1/query_range`). Takes `start`, `end`, `step`.             |
| `series`       | Match list via `/api/v1/series`. Takes `match[]` selectors (repeatable) + time range. |
| `labels`       | All label names (`/api/v1/labels`), optionally filtered by `match[]` + time range.    |
| `label_values` | Values for a specific label (`/api/v1/label/<name>/values`) + same filters.           |

Every handler opens an `mcp.handler` SERVER span and wraps the HTTP call in a `prom.api.call` child span with
`prom.endpoint` and `prom.query` attributes so cross-service traces stitch cleanly with the `/trace` view on each
backend.

## Response caps

Responses that would exceed `MCP_PROM_MAX_RESPONSE_BYTES` are truncated before the outer `MCP_RESPONSE_MAX_BYTES`
envelope is checked. Both caps apply to the serialised JSON, not the parsed Python object. Callers see an explicit
truncation row rather than a silent-drop — downstream charting code can treat it as an error signal and retry with a
narrower time window or a heavier `step`.
