# Event stream schema (#1110)

This directory is the contract between the harness event stream
(`GET /events/stream`) and any client consuming it — the web dashboard
today, a future CLI / iOS / Android client tomorrow. Schemas are
**internal** (not published as a public contract) but stable within
the repo.

## Wire format

Server-Sent Events (SSE). Each message is a JSON object serialised on a
single `data:` line. Every message carries a monotonic `id:` so
reconnecting clients can pass `Last-Event-ID` and receive missed events
from the server's in-memory ring.

```
event: job.fired
id: 4217
data: {"type":"job.fired","version":1,"id":"4217","ts":"2026-04-19T12:00:00.003Z","agent_id":"iris","payload":{"name":"daily-report","schedule":"0 9 * * *","duration_ms":420,"outcome":"success"}}

: keepalive

```

Keepalives (`: keepalive\n\n`) fire every 15s so intermediate proxies
don't close idle connections.

## Envelope

Every event has the same top-level envelope:

| Field       | Type                 | Notes                                                                      |
| ----------- | -------------------- | -------------------------------------------------------------------------- |
| `type`      | string               | Dotted event type, e.g. `job.fired`, `webhook.delivered`.                  |
| `version`   | integer              | Per-type schema version. Starts at `1`. Bumped on incompatible changes.    |
| `id`        | string               | Monotonic id within the harness process. Used for `Last-Event-ID` resume.  |
| `ts`        | string (RFC 3339)    | Event timestamp at the emitter, UTC, millisecond precision.                |
| `agent_id`  | string \| null       | Scoped event → agent name (e.g. `iris`). Harness-wide event → `null`.      |
| `payload`   | object               | Type-specific fields, validated against the corresponding schema below.    |

Unknown fields in an older client MUST be ignored (forward compat).
The server never emits a `type` its running config doesn't know how to
validate; validation is best-effort at emit time with a `WARN` + counter
bump on failure rather than a hard error.

## Versioning

- `version` is a per-`type` integer. Clients match on `type` + fall
  back on unknown `version` by treating the payload as the latest they
  understand.
- Additive changes to a payload do NOT bump the version (new optional
  field).
- Renaming / removing / retyping a payload field **does** bump the
  version. The server emits both the old and new versions side-by-side
  for one release cycle, so old clients keep working.

## Event types

Phase 1 ships eleven harness-emitted types; phase 3 (#1110) adds three
backend-emitted types that flow over the backend→harness event channel
(`POST /internal/events/publish`) and are fanned out on the same SSE
stream:

| Type                      | Emitted by              | Payload summary                                                             |
| ------------------------- | ----------------------- | --------------------------------------------------------------------------- |
| `job.fired`               | harness jobs scheduler  | `{name, schedule, duration_ms, outcome, error?}`                            |
| `task.fired`              | harness tasks scheduler | `{name, window, duration_ms, outcome, error?}`                              |
| `heartbeat.fired`         | harness heartbeat       | `{schedule, duration_ms, outcome, error?}`                                  |
| `continuation.fired`      | harness continuations   | `{name, upstream_kind, upstream_name, duration_ms, outcome, error?}`        |
| `trigger.fired`           | harness triggers        | `{name, endpoint, duration_ms, outcome, error?}`                            |
| `webhook.delivered`       | harness webhooks        | `{name, url_host, status_code, duration_ms}`                                |
| `webhook.failed`          | harness webhooks        | `{name, url_host, reason, duration_ms}`                                     |
| `hook.decision`           | bus.py (from backends)  | `{backend, session_id_hash, tool, decision, rule_id?}`                      |
| `a2a.request.received`    | harness A2A relay       | `{concern, model?}`                                                         |
| `a2a.request.completed`   | harness A2A relay       | `{concern, outcome, duration_ms}`                                           |
| `agent.lifecycle`         | harness or backends     | `{backend, event: started|stopped|config_reloaded|credential_rotated}`      |
| `conversation.turn`       | backends (claude/codex/gemini) | `{session_id_hash, role: user|assistant, content_bytes, model?}`     |
| `conversation.chunk`      | backends (claude/codex/gemini) | `{session_id_hash, role: user|assistant, seq, content, final}` — per-session drill-down stream only (not on `/events/stream`) |
| `tool.use`                | backends (claude/codex/gemini) | `{session_id_hash, tool, duration_ms, outcome: ok|error|denied, result_size_bytes?, error?}` |
| `trace.span`              | backends (claude/codex/gemini) | `{session_id_hash?, span_name, duration_ms, status: ok|error, service}` — only emitted for `{llm.request, shell, mcp.handler, backend.mcp.tools_call}` |

See `events.schema.json` for the full JSON Schema.

## What is NOT on this stream

- Per-token conversation chunks — drill-down surface, per-backend stream.
  Phase 3 emits a single `conversation.turn` summary event (content_bytes,
  not raw content) per turn; raw token streams remain per-backend.
  Phase 4 (#1110) surfaces them on a per-session endpoint,
  `GET /api/sessions/<session_id>/stream` on each backend — same SSE envelope,
  type `conversation.chunk`, scoped to one session and backend-local (no
  fan-out to harness). See below.
- Prometheus metrics — polled over `/metrics`, not event-shaped.
- Team / config / schedule snapshots — polled REST surfaces.

## Per-session backend stream (phase 4)

Each backend (`claude`, `codex`, `gemini`) additionally serves
`GET /api/sessions/<session_id>/stream` for real-time drill-down into a
single session. Wire format is identical to the harness event stream
(SSE, same envelope) but the published types are limited to
`conversation.chunk`, `conversation.turn`, `tool.use`, `trace.span`, and
the `stream.overrun` terminal envelope.

- Auth: `Authorization: Bearer <CONVERSATIONS_AUTH_TOKEN>`, same token
  used by `/conversations`, `/trace`, `/mcp`, `/api/traces`.
  `CONVERSATIONS_AUTH_DISABLED=true` acts as the documented local-dev
  escape hatch (loud startup warning).
- Scope: backend-local (per pod replica). No cross-pod session sharing
  — the same session ID routed to a different replica will not see
  events emitted on this replica.
- Ring: bounded by `CONVERSATION_STREAM_RING_MAX` (default 200) for
  `Last-Event-ID` resume.
- Backpressure: bounded per-subscriber queue
  `CONVERSATION_STREAM_QUEUE_MAX` (default 500); slow subscriber →
  terminal `stream.overrun` and close.
- Keepalive: `CONVERSATION_STREAM_KEEPALIVE_SEC` (default 15).
- Grace window: after the last subscriber disconnects, the broadcaster
  lingers for `CONVERSATION_STREAM_GRACE_SEC` seconds (default 60) so
  a brief reconnect can resume without losing the ring. After that the
  registry entry is evicted.

Payloads continue to use `session_id_hash` (SHA-256 prefix, 12 chars)
rather than the raw session id; the URL path carries the actual id
because the caller already knows it in order to construct the URL.

Phase 3 (#1110) added backend-emitted `conversation.turn`, `tool.use`,
and `trace.span` events over the backend→harness event channel
(`POST /internal/events/publish`, bearer-authed by
`HOOK_EVENTS_AUTH_TOKEN`). Raw per-token chunks and the full OTel span
tree are still drill-down surfaces on the per-backend endpoints.

## URL-host redaction

Webhook events carry `url_host` (bare hostname) rather than the full
URL. The host is sufficient for dashboards and avoids exposing path /
query / credential fragments if the webhook URL carries any. Full URLs
live in the existing `tool-activity.jsonl` audit path.

## session_id_hash

`hook.decision` events identify the session as a SHA-256 prefix (first
12 chars) rather than the raw derived session id. Dashboards rendering
activity per-session can still group by the hash without ever seeing
the HMAC-bound value. Full session_id stays in the per-backend
conversation stream (drill-down only, where auth already scopes access).

## Resume / reconnect

- Server keeps an in-memory ring of the last 1000 events per stream.
- Client reconnecting with `Last-Event-ID: <n>` receives all events
  with `id > n` still present in the ring.
- If the requested id is older than the ring's oldest, the server sends
  a single synthetic event `{"type":"stream.gap","version":1,...}` so
  the client knows it missed events and can re-fetch any stale REST
  snapshots to reconcile.

## Backpressure

- Each subscriber has a bounded async queue (default 1000 events).
- If the queue fills, the server emits `{"type":"stream.overrun",...}`,
  closes the connection, and bumps
  `harness_event_stream_overruns_total{subscriber}`.
- Client treats overrun as a reconnect signal with `Last-Event-ID`;
  if the missed span is larger than the ring, the gap event above
  fires.

## Auth

`Authorization: Bearer <harness_token>` on every request. No
query-param token fallback — clients that can't set headers natively
(browser `EventSource`) use the `fetch` + `ReadableStream` pattern
instead.
