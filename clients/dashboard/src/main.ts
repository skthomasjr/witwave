import { createApp } from "vue";
import { createPinia } from "pinia";
import PrimeVue from "primevue/config";
import Aura from "@primevue/themes/aura";
import "primeicons/primeicons.css";
import "./styles/tokens.css";

import App from "./App.vue";
import { router } from "./router";
import { i18n } from "./i18n";

const app = createApp(App);
// Pinia hosts the cross-view selection store (#748). Keep install
// order before router/PrimeVue so stores resolve from any guard.
app.use(createPinia());
// vue-i18n (#819). Loaded before router so redirect-path guards can
// resolve translated labels if they ever need to.
app.use(i18n);
app.use(router);
app.use(PrimeVue, {
  theme: {
    preset: Aura,
    options: {
      // Theme toggle (#1106) — align PrimeVue's dark-mode selector with
      // the `[data-theme="dark"]` attribute written by `useTheme`. When
      // the user hasn't picked a theme we leave the attribute unset and
      // rely on the `prefers-color-scheme: light` media query to drive
      // the OS-following "auto" state.
      darkModeSelector: '[data-theme="dark"]',
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

// Rolling token-bucket throttle (#1060). A tight-loop error in a
// component would otherwise flood console/log shippers and can itself
// recurse through the unhandledrejection handler. We keep a per-key
// (kind+message+stack-head) bucket capped at BUCKET_MAX entries within
// BUCKET_WINDOW_MS; once exceeded, occurrences are rolled up into a
// single suppression note. After STACK_DROP_AFTER stacks for the same
// key we stop forwarding the stack payload to cap per-event size.
const BUCKET_WINDOW_MS = 60_000;
const BUCKET_MAX = 10;
const STACK_DROP_AFTER = 3;
const KEY_CACHE_MAX = 256;
interface BucketState {
  count: number;
  stackSeen: number;
  windowStart: number;
  suppressed: number;
}
const errorBuckets = new Map<string, BucketState>();
let inLogger = false;

function stackHead(stack: string | undefined): string {
  if (!stack) return "";
  const nl = stack.indexOf("\n");
  const first = nl === -1 ? stack : stack.slice(0, nl);
  const after = nl === -1 ? "" : stack.slice(nl + 1, nl + 200);
  return (first.slice(0, 120) + "|" + after.slice(0, 120)).trim();
}

function bucketKey(
  kind: DashboardClientError["kind"],
  message: string,
  stack: string | undefined,
): string {
  return kind + "::" + (message || "").slice(0, 200) + "::" + stackHead(stack);
}

function logClientError(payload: DashboardClientError): void {
  if (inLogger) {
    return;
  }
  inLogger = true;
  try {
    const key = bucketKey(payload.kind, payload.message, payload.stack);
    const now = Date.now();
    let bucket = errorBuckets.get(key);
    if (!bucket || now - bucket.windowStart > BUCKET_WINDOW_MS) {
      if (bucket && bucket.suppressed > 0) {
        try {
          console.error(
            "[dashboard.client_error.suppressed] " +
              JSON.stringify({
                key,
                suppressed: bucket.suppressed,
                windowMs: BUCKET_WINDOW_MS,
              }),
          );
        } catch {
          /* ignore */
        }
      }
      bucket = { count: 0, stackSeen: 0, windowStart: now, suppressed: 0 };
      errorBuckets.set(key, bucket);
      if (errorBuckets.size > KEY_CACHE_MAX) {
        errorBuckets.clear();
        errorBuckets.set(key, bucket);
      }
    }
    bucket.count += 1;
    if (bucket.count > BUCKET_MAX) {
      bucket.suppressed += 1;
      return;
    }
    const out: DashboardClientError = { ...payload };
    if (bucket.stackSeen >= STACK_DROP_AFTER) {
      delete out.stack;
    } else if (out.stack) {
      bucket.stackSeen += 1;
    }
    try {
      console.error("[dashboard.client_error] " + JSON.stringify(out));
    } catch {
      console.error("[dashboard.client_error] (non-serialisable payload)");
    }
  } catch {
    // Sink must never throw up the stack.
  } finally {
    inLogger = false;
  }
}

// Exposed for tests only.
export const __errorSinkInternals = {
  reset(): void {
    errorBuckets.clear();
    inLogger = false;
  },
  snapshot(): Map<string, BucketState> {
    return new Map(errorBuckets);
  },
  BUCKET_MAX,
  STACK_DROP_AFTER,
  BUCKET_WINDOW_MS,
  logClientError,
};

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
