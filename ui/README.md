# nyx UI

The nyx UI is a single-page web application for monitoring and interacting with the autonomous agent platform. It is served by nginx from a Docker container and communicates with agents via their nyx-harness HTTP APIs.

## What it does

The UI provides five views into the running agent system:

**Metrics** — Real-time operational dashboard. Displays stat cards (uptime, active sessions, queue depth, error counts) and time-series charts powered by Chart.js. Data is polled from each agent's Prometheus `/metrics` endpoint on a configurable interval. Filterable by agent and backend.

**Agents** — Team browser and interactive chat. The left sidebar shows every agent in the team manifest with their backend status indicators (claude/codex/gemini availability). Clicking an agent opens a chat panel on the right where you can send A2A messages directly, select which backend handles the request, and override the model. Responses stream back into the thread in real time, with tool calls and results rendered inline.

**Conversations** — Merged conversation feed from all running backends. Shows user and agent turns interleaved with tool
trace events (tool_use, tool_result). Each row shows the agent, model, timestamp, and session ID. A toolbar dropdown
controls the number of entries loaded (100, 500, 1000, or 5000; defaults to 500). The selected limit is persisted in
`localStorage`. Supports filtering by agent, role, and tool visibility.

**Calendar** — Visual schedule of configured jobs and tasks. Renders in month, week, and day views. On activation, the
calendar fetches `GET /jobs` and `GET /tasks` from the agent and renders registered items as labeled entries in the
month view. Job items are shown in purple-accent; task items in violet. The view degrades gracefully if the endpoints
are unavailable.

**Triggers** — List of inbound HTTP trigger endpoints registered on the agent. Fetches `GET /triggers` on activation and
renders each trigger's endpoint, description, running state, assigned backend/model, and session ID. Supports text search
filtering and manual refresh.

## Key features

- **No build step** — Pure HTML + vanilla JS in a single `index.html` file. nginx serves it directly.
- **Dark theme** — CSS custom properties throughout; purple accent (`#7c6af7`), teal/green/yellow/red semantic colors.
- **Markdown rendering** — Agent responses are rendered as markdown via the Marked library.
- **Session continuity** — The chat panel derives a session ID from the current page session, so conversations with agents persist across page reloads within the same browser tab.
- **Model override** — The chat toolbar exposes a model input field. The selected model is passed to the backend via `message.metadata.model` in the A2A request.
- **Clear button** — The chat panel toolbar has a right-justified clear button that wipes the local thread display without touching any backend state.
- **Backend selection** — A dropdown in the chat toolbar lets you route a message to a specific backend (claude, codex, gemini) rather than the agent's configured default.

## Files

| File | Purpose |
|------|---------|
| `index.html` | The entire application — markup, styles, and JavaScript |
| `nginx.conf` | nginx configuration; serves `index.html` for all routes (SPA routing) |
| `Dockerfile` | Builds the nginx container image |

## Runtime

The UI container is stateless. It serves `index.html` over HTTP on port 80 and proxies nothing — all API calls go directly from the browser to the agent ports exposed by the Docker Compose network.

The `AGENT_BASE` constant at the top of `index.html` sets the base URL for all API calls. By default it points to the local nyx-harness port. Update this if the agent is running on a different host or port.

The nginx config sets permissive CORS headers by default so the UI can be opened from any origin during development.
Two environment variables let deployments tighten this for production:

- `UI_CORS_ALLOW_ORIGIN` — sets the `Access-Control-Allow-Origin` header on static-asset responses (default `"*"`).
- `UI_CONNECT_SRC` — scopes the CSP `connect-src` directive, restricting which origins the UI's browser-side JavaScript
  may contact via `fetch`/`XHR`/`WebSocket` (default `"*"`; tighten to e.g. `"'self' https://nyx.example.com"`).

The nginx config also emits `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, and
`Referrer-Policy: strict-origin-when-cross-origin` on every response as defence-in-depth against clickjacking, MIME
sniffing, and referrer leakage.
