# nyx dashboard

Vue 3 + Vite + PrimeVue + Vitest dashboard for the nyx autonomous agent platform. This is the **future** UI; the
current first-class UI remains the single-file `ui/index.html` app. The two ship side-by-side (#470) until the
dashboard reaches feature parity — at which point UI stays as the fallback and dashboard becomes primary.

## Status: scaffold

Only the Team view is implemented so far. The table below tracks parity against `ui/` and will be checked off as
each feature lands.

| Feature                          | `ui/`  | `dashboard/` |
| -------------------------------- | ------ | ------------ |
| Team list                        | ✓      | ✓ (placeholder) |
| Conversations viewer             | ✓      | —            |
| Trace viewer                     | ✓      | —            |
| Triggers / signed toggle         | ✓      | —            |
| Jobs + tasks viewer              | ✓      | —            |
| Webhooks + continuations viewer  | ✓      | —            |
| Heartbeat panel                  | ✓      | —            |
| Trigger send form                | ✓      | —            |
| Metrics aggregation panel        | ✓      | —            |
| Session continuity send/receive  | ✓      | —            |

## Development

```bash
cd dashboard
npm install
VITE_HARNESS_URL=http://localhost:8000 npm run dev
```

The Vite dev server listens on `:5173`; `/api/*` is proxied to `VITE_HARNESS_URL` (default `http://localhost:8000`)
so component code can talk to the harness without CORS gymnastics.

## Testing

```bash
npm run test         # one-shot
npm run test:watch   # interactive
```

Vitest + `@vue/test-utils` in jsdom. A smoke test for `TeamView` covers the happy path, error path, and empty
state — use it as the template for each new view.

## Production build

```bash
npm run build
```

Produces `dist/`, which the Dockerfile copies into `/usr/share/nginx/html`. The nginx layer serves the SPA with a
history-mode fallback and proxies `/api/*` to the harness (target from `HARNESS_URL` at deploy time).

## Deployment

The chart in `charts/nyx` adds a `dashboard` section (gated by `dashboard.enabled`, default `false`) that renders a
Deployment + Service per agent. The operator's `NyxAgent.spec.dashboard` mirrors this field so clusters that declare
agents via the CRD get the same coexistence model as the Helm path.

## Directory layout

```
dashboard/
├── package.json           # npm scripts + deps
├── vite.config.ts         # dev server + /api proxy, Vitest config
├── tsconfig.json          # strict TS for .ts + .vue
├── index.html             # SPA entry
├── nginx.conf             # prod static+proxy serving
├── Dockerfile             # build (node) → serve (nginx) multi-stage
├── src/
│   ├── main.ts            # createApp + PrimeVue theme (Aura)
│   ├── App.vue            # root layout shell
│   ├── router.ts          # vue-router history mode
│   └── views/
│       └── TeamView.vue   # first parity view (reads /api/team)
└── tests/unit/
    └── TeamView.spec.ts   # Vitest smoke test

```
