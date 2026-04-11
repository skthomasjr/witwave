# nyx UI

The nyx UI is a single-page web application for monitoring and interacting with the autonomous agent platform. It is served by nginx from a Docker container and communicates with agents via their nyx-agent HTTP APIs.

## What it does

The UI provides four views into the running agent system:

**Metrics** — Real-time operational dashboard. Displays stat cards (uptime, active sessions, queue depth, error counts) and time-series charts powered by Chart.js. Data is polled from each agent's Prometheus `/metrics` endpoint on a configurable interval. Filterable by agent and backend.

**Agents** — Team browser and interactive chat. The left sidebar shows every agent in the team manifest with their backend status indicators (claude/codex/gemini availability). Clicking an agent opens a chat panel on the right where you can send A2A messages directly, select which backend handles the request, and override the model. Responses stream back into the thread in real time, with tool calls and results rendered inline.

**Conversations** — Merged conversation feed from all running backends. Shows user and agent turns interleaved with tool trace events (tool_use, tool_result). Each row shows the agent, model, timestamp, and session ID. Supports filtering by agent and auto-refresh.

**Calendar** — Visual schedule of all configured jobs, tasks, and heartbeats. Renders in month, week, and day views. Each event is color-coded by type. Clicking an event shows the frontmatter and prompt body.

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

The `AGENT_BASE` constant at the top of `index.html` sets the base URL for all API calls. By default it points to the local nyx-agent port. Update this if the agent is running on a different host or port.

The nginx config sets permissive CORS headers so the UI can be opened from any origin during development.
