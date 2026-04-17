# nyx dashboard

Vue 3 + Vite + PrimeVue + Vitest dashboard for the nyx autonomous agent platform (#470). Browser talks to the
dashboard pod, which fans out to each agent's harness directly via nginx per-agent routes — no single-harness front
door, no cross-agent fan-out inside any one agent's pod.

## Views

| View              | What it shows                                                                    |
| ----------------- | -------------------------------------------------------------------------------- |
| Team              | Agent cards with per-backend health bubbles + a chat panel on selection (send + history load are timeout-bounded with a cancel button, #535) |
| Calendar          | Conversation log plotted on a day/week/month grid (vue-cal)                      |
| Jobs              | Scheduled jobs across every agent, with search + refresh                         |
| Tasks             | Day/window-scheduled tasks across every agent                                    |
| Triggers          | Inbound HTTP triggers (endpoint, auth, enabled state)                            |
| Webhooks          | Outbound webhook subscriptions and delivery counts                               |
| Continuations     | Continues-after chains (jobs, triggers, other continuations)                     |
| Heartbeat         | Per-agent heartbeat schedule + backend + model                                   |
| Conversations     | Aggregated conversation log with agent/role/search/limit filters                 |
| Metrics           | Label-breakdown bar/doughnut charts from each agent's /metrics                   |

## Development

Per-agent routes need a team list at dev time too, not just in production. Pass `VITE_TEAM` as JSON and port-forward
each agent's harness:

```bash
# Terminal 1: port-forward each agent's harness
kubectl port-forward -n nyx svc/nyx-bob 8099:8099 &
kubectl port-forward -n nyx svc/nyx-fred 8098:8098 &

# Terminal 2: run the dev server with the team list
cd dashboard
VITE_TEAM='[{"name":"bob","url":"http://localhost:8099"},{"name":"fred","url":"http://localhost:8098"}]' \
  npm run dev
```

Open http://localhost:5173. `/api/team` serves the inline directory; `/api/agents/<name>/...` proxies to each entry.

## Testing

```bash
npm run test         # one-shot
npm run test:watch   # interactive
```

Vitest + `@vue/test-utils` in jsdom. Smoke specs for `TeamView`, `ChatPanel`, and `JobsView` cover the list /
chat / fan-out patterns; add one per new view with the same shape.

## Production build

```bash
npm run build
```

Produces `dist/`, which the Dockerfile copies into `/usr/share/nginx/html`. The image's baseline `nginx.conf`
returns 404 for `/api/*`; the Helm chart (charts/nyx/templates/configmap-dashboard-nginx.yaml) mounts a ConfigMap
over `/etc/nginx/templates/` at deploy time with per-agent routes templated from `.Values.agents`.

## Deployment

- **Helm (cluster-wide dashboard):** set `dashboard.enabled: true` in `charts/nyx` values. Renders one dashboard
  that knows about every enabled agent via the ConfigMap described above.
- **Operator (per-agent dashboard):** set `spec.dashboard.enabled: true` on a `NyxAgent` CR. Operator renders a
  Deployment + Service + ConfigMap scoped to the one agent. Only that agent is visible from that dashboard.

## Directory layout

```
dashboard/
├── package.json           # npm scripts + deps (vue, vue-router, primevue, chart.js, vue-cal)
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
│   └── views/             # TeamView, JobsView, TasksView, … (ten views)
└── tests/unit/            # Vitest smoke specs
```
