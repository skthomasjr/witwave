import { createApp } from "vue";
import PrimeVue from "primevue/config";
import Aura from "@primevue/themes/aura";
import "primeicons/primeicons.css";
import "./styles/tokens.css";

import App from "./App.vue";
import { router } from "./router";

const app = createApp(App);
app.use(router);
app.use(PrimeVue, {
  theme: {
    preset: Aura,
    options: {
      darkModeSelector: ".p-dark",
    },
  },
});

// Structured client-error sink (#747). Every composable used to do
// `error.value = e.message` with no external sink, so operators had
// no way to distinguish outages from UI bugs without attaching
// devtools. The handlers below emit a single well-shaped
// console.error payload per incident so browser log forwarders
// (otel-browser, Sentry, structured console scrapers) can pick up
// the event. Intentionally NOT auto-POSTing to a server endpoint —
// that would need its own backend surface and opt-in; structured
// console output is the low-risk baseline.
interface DashboardClientError {
  kind: "vue" | "window" | "unhandledrejection";
  message: string;
  stack?: string;
  route?: string;
  componentName?: string;
  info?: string;
  ts: string;
}

function logClientError(payload: DashboardClientError): void {
  // Single entry, stringified JSON so log shippers can parse.
  try {
    console.error("[dashboard.client_error] " + JSON.stringify(payload));
  } catch {
    console.error("[dashboard.client_error] (non-serialisable payload)");
  }
}

app.config.errorHandler = (err, instance, info) => {
  const e = err as Error;
  logClientError({
    kind: "vue",
    message: e?.message ?? String(err),
    stack: e?.stack,
    route: typeof window !== "undefined" ? window.location.pathname : undefined,
    componentName: (instance as { $options?: { name?: string } } | null)
      ?.$options?.name,
    info,
    ts: new Date().toISOString(),
  });
};

if (typeof window !== "undefined") {
  window.addEventListener("error", (evt) => {
    const e = evt.error as Error | undefined;
    logClientError({
      kind: "window",
      message: e?.message ?? evt.message ?? "(unknown)",
      stack: e?.stack,
      route: window.location.pathname,
      ts: new Date().toISOString(),
    });
  });
  window.addEventListener("unhandledrejection", (evt) => {
    const reason = evt.reason;
    const e = reason instanceof Error ? reason : undefined;
    logClientError({
      kind: "unhandledrejection",
      message: e?.message ?? String(reason),
      stack: e?.stack,
      route: window.location.pathname,
      ts: new Date().toISOString(),
    });
  });
}

app.mount("#app");
