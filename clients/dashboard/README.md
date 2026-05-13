# witwave dashboard

Vue 3 + Vite + PrimeVue + Vitest dashboard for the witwave autonomous agent platform (#470). Browser talks to the
dashboard pod, which fans out to each agent's harness directly via nginx per-agent routes — no single-harness front
door, no cross-agent fan-out inside any one agent's pod.

## Views

| View          | What it shows                                                                                                                                                                                                                                                                                                                                                                         |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Team          | Agent cards with per-backend health bubbles + a chat panel on selection (send + history load are timeout-bounded with a cancel button, #535)                                                                                                                                                                                                                                          |
| Automation    | Unified view collapsing Jobs, Tasks, Triggers, Webhooks, Continuations, and Heartbeat into card sections (#automation-v1). The legacy paths — `/jobs`, `/tasks`, `/triggers`, `/webhooks`, `/continuations`, `/heartbeat` — all redirect to `/automation` so existing bookmarks keep working                                                                                          |
| Conversations | Aggregated conversation log with agent/role/search/limit filters                                                                                                                                                                                                                                                                                                                      |
| Tool Trace    | Unified tool-activity feed across the team — tool_use rows paired with tool_result (duration/status) and folded with any matching tool_audit row (response preview, matched hook rule, `denied` status). Standalone audit rows surface hook-blocked calls that never produced a tool_use. Filter by agent, tool, status, or event type. The legacy `/tool-audit` path redirects here. |
| OTel Traces   | Distributed trace viewer (#632): recent traces + span-tree drawer from an operator-configured Jaeger/Tempo HTTP API. `/otel-traces/:traceId` deep-links straight into the detail drawer                                                                                                                                                                                               |
| Metrics       | Label-breakdown bar/doughnut charts from each agent's /metrics                                                                                                                                                                                                                                                                                                                        |
| Timeline      | Live event-stream feed from `/events/stream` SSE — per-agent or aggregate, with reconnection + `Last-Event-ID` resume                                                                                                                                                                                                                                                                 |

## Development

Per-agent routes need a team list at dev time too, not just in production. Pass `VITE_TEAM` as JSON and port-forward
each agent's harness:

```bash
# Terminal 1: port-forward each agent's harness
kubectl port-forward -n witwave-test svc/bob 8099:8000 &
kubectl port-forward -n witwave-test svc/fred 8098:8000 &

# Terminal 2: run the dev server with the team list
cd dashboard
VITE_TEAM='[{"name":"bob","url":"http://localhost:8099"},{"name":"fred","url":"http://localhost:8098"}]' \
  npm run dev
```

Open <http://localhost:5173>. `/api/team` serves the inline directory; `/api/agents/<name>/...` proxies to each entry.

## OTel trace backend (`VITE_TRACE_API_URL`)

The `OTel Traces` view (#632) queries an external Jaeger or Tempo query API directly — it does **not** route through
`/api/*`. Point the dashboard at that backend with one of:

- **Build time:** set `VITE_TRACE_API_URL` before `npm run build` (e.g.
  `VITE_TRACE_API_URL=http://witwave-jaeger-query.observability:16686 npm run build`).
- **Runtime:** have nginx or your platform inject `window.__WITWAVE_CONFIG__ = { traceApiUrl: "…" }` into `index.html`
  at startup. The runtime override wins if both are present.

Endpoints the view calls (standard Jaeger v1 query-service shape, which Tempo also exposes):

| Purpose             | Request                                            |
| ------------------- | -------------------------------------------------- |
| List recent traces  | `GET <base>/api/traces?limit=<N>[&service=<name>]` |
| Load a single trace | `GET <base>/api/traces/<traceID>`                  |

When neither `VITE_TRACE_API_URL` nor `window.__WITWAVE_CONFIG__.traceApiUrl` is set, the view renders a clear "tracing
not configured" empty state and makes no network calls. The referenced Helm values land with `charts/witwave`
`observability.tracing` (feature #634).

The view intentionally does **not** attach authentication headers. Operators who run Jaeger/Tempo on a non-public
network typically front it with their own auth proxy (oauth2-proxy, ingress auth, etc.). If your deployment uses the
Tempo Grafana-compatible API and hits a shape mismatch the Jaeger-compatible routes didn't cover, open a narrower
follow-up issue rather than bloating this view.

The existing `Trace` view (#592) is unrelated — it reads the harness `/trace` JSONL feed for per-agent tool events. Both
views coexist; OTel Traces is for distributed request flow spanning harness + backends + operator.

## Testing

```bash
npm run test         # one-shot unit specs (vitest)
npm run test:watch   # interactive vitest
```

Vitest + `@vue/test-utils` in jsdom. Smoke specs for `TeamView`, `ChatPanel`, and `JobsView` cover the list / chat /
fan-out patterns; add one per new view with the same shape.

### End-to-end (Playwright, #818)

`@playwright/test` is scaffolded for full-browser smoke coverage. Tests live under `tests/e2e/` and mock the `/api/*`
surface with `page.route()` so the suite does **not** require a live cluster.

```bash
cd dashboard
npx playwright install chromium   # first run only — downloads browser binary
npm run test:e2e
```

`playwright.config.ts` auto-starts `npm run build && npm run preview -- --port 4173` before the suite so the specs
always target a deterministic production bundle. Override with `PLAYWRIGHT_BASE_URL=<url>` to point at an
externally-hosted dev server instead (e.g. during iterative UI work).

The scaffold ships with three smoke flows: shell nav, team list rendering against a mocked `/api/team`, and a legacy
`/jobs` → `/automation` redirect. CI gating and broader flow coverage (conversations drawer, chat send/cancel, degraded
banner) are follow-up.

## Production build

```bash
npm run build
```

Produces `dist/`, which the Dockerfile copies into `/usr/share/nginx/html`. The image's baseline `nginx.conf` returns
404 for `/api/*`; the Helm chart (charts/witwave/templates/configmap-dashboard-nginx.yaml) mounts a ConfigMap over
`/etc/nginx/templates/` at deploy time with per-agent routes templated from `.Values.agents`.

The baseline `nginx.conf` also sets `Content-Security-Policy`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy`
response headers so the SPA ships with sensible browser-side hardening out of the box — operators who terminate TLS
elsewhere and strip headers should replicate these at the edge.

## Accessibility baselines

- `aria-live` announcements on load / error / empty states in list and chat views.
- `aria-pressed` on toggle-style buttons (filter pills, view switchers).
- `focus-visible` styling on every interactive element.
- Skip-to-main-content link on the app shell.
- Debounced search input on the Conversations view to cut screen-reader chatter.
- Structured client-error sink (`src/utils/clientErrors.ts`) so errors surface one at a time, with context, rather than
  as a toast storm.
- Per-member timeouts on fan-out composables (`useAgentFanout`, `useHealth`, `useTeam`) so a single slow harness doesn't
  stall the whole view.
- `_read_jsonl` tail-read so `Conversations` / `Tool Trace` stream the last N entries without reading the whole file
  into memory on large logs.
- **Automated a11y smoke (#970):** `vitest-axe` is wired into `tests/setup/axe.ts` and picked up by every `npm run test`
  run. `tests/unit/a11y.spec.ts` exercises a handful of key components (`AgentCard`, `AlertBanner`, `PromptCard`,
  `AgentList`) through axe-core and asserts zero violations. The `color-contrast` and `region` rules are disabled for
  isolated component mounts — re-enable them once a full-page harness lands. To extend coverage, mount the view, call
  `const results = await axe(wrapper.element, runRules);` and assert `expect(results).toHaveNoViolations()`.

## Internationalisation (#819)

`vue-i18n` is bootstrapped in `src/i18n/index.ts` and installed in `src/main.ts` before the router. English
(`src/i18n/locales/en.json`) is the only locale shipped today; the plumbing is in place so follow-up passes can add
additional locales without touching component source.

Locale resolution order at startup:

1. `VITE_LOCALE` build-time env (e.g. `VITE_LOCALE=en npm run build`).
2. `window.__WITWAVE_CONFIG__.locale` runtime injection (helm/configmap-driven deploy).
3. Browser `navigator.language` (first two chars).
4. Fallback `en`.

Consumer pattern inside a component:

```ts
import { useI18n } from "vue-i18n";
const { t } = useI18n();
// …
// {{ t("nav.team") }}
```

Pluralisation / interpolation use vue-i18n's native syntax — e.g. `t("team.pinned", { count: 3 })` resolves against
`"pinned": "{count} pinned"` in `en.json`.

`App.vue` is the reference migration (nav labels + header status copy). Remaining views still contain hardcoded English
strings — extract them key-by-key under the same `nav.*` / `status.*` / `<view>.*` scheme and land additional locale
files as `src/i18n/locales/<code>.json` when translation arrives.

## Runtime-config validation (`traceApiUrl`)

`VITE_TRACE_API_URL` / `window.__WITWAVE_CONFIG__.traceApiUrl` is validated at runtime — empty, non-URL, or
non-`http(s)` values are rejected with a clear message in the OTel Traces view rather than silently issuing malformed
requests.

## Deployment

- **Helm (cluster-wide dashboard):** set `dashboard.enabled: true` in `charts/witwave` values. Renders one dashboard
  that knows about every enabled agent via the ConfigMap described above.
- **Operator (per-agent dashboard):** set `spec.dashboard.enabled: true` on a `WitwaveAgent` CR. Operator renders a
  Deployment + Service + ConfigMap scoped to the one agent. Only that agent is visible from that dashboard.

## Directory layout

```text
clients/dashboard/
├── package.json           # npm scripts + deps (vue, vue-router, primevue, chart.js)
├── vite.config.ts         # dev server + VITE_TEAM-driven per-agent proxies
├── tsconfig.json          # strict TS for .ts + .vue
├── index.html             # SPA entry
├── nginx.conf             # image baseline (SPA + 404 on /api/*)
├── Dockerfile             # build (node) → serve (nginx-unprivileged) multi-stage
├── src/
│   ├── main.ts            # createApp + PrimeVue Aura theme
│   ├── App.vue            # shell: brand, nav, header status dot
│   ├── router.ts          # vue-router routes for every view
│   ├── types/             # typed contracts: team, chat, scheduler
│   ├── api/client.ts      # apiGet / apiPost + ApiError
│   ├── utils/             # markdown.ts (marked+DOMPurify), prometheus.ts
│   ├── composables/       # useTeam, useChat, useAgentFanout, useMetrics, useHealth
│   ├── components/        # AgentCard, AgentList, BackendBubble, AgentDetail, ChatPanel, ListView
│   └── views/             # TeamView, AutomationView, ConversationsView, TraceView, OTelTracesView, MetricsView, TimelineView
└── tests/unit/            # Vitest smoke specs
```
