import { onUnmounted, ref, watchEffect } from "vue";
import { apiGet } from "../api/client";

// useRouting — fetches the harness's /routing reflection (#638) for one
// agent so the chat selector (#597) can honor backend.yaml defaults
// instead of falling back to the first backend in an arbitrary order.
//
// Response shape (see harness/main.py routing_handler):
//   {
//     "default": "iris-a2-claude",
//     "default_routing": {"agent": "...", "model": "..."} | null,
//     "routing": {
//       "a2a":         {"agent": "...", "model": "..."} | null,
//       "heartbeat":   ...,
//       "job":         ...,
//       "task":        ...,
//       "trigger":     ...,
//       "continuation": ...
//     }
//   }
//
// Absent or unparseable routing is non-fatal — we return null and the
// caller falls back to its prior default (usually the first backend).

export interface RoutingEntry {
  agent: string;
  model: string | null;
}

export interface RoutingResponse {
  default: string | null;
  default_routing: RoutingEntry | null;
  routing: {
    a2a: RoutingEntry | null;
    heartbeat: RoutingEntry | null;
    job: RoutingEntry | null;
    task: RoutingEntry | null;
    trigger: RoutingEntry | null;
    continuation: RoutingEntry | null;
  };
}

export function useRouting(agentName: () => string) {
  const routing = ref<RoutingResponse | null>(null);
  const error = ref<string>("");
  let aborter: AbortController | null = null;

  async function load() {
    aborter?.abort();
    aborter = new AbortController();
    const name = agentName();
    if (!name) {
      routing.value = null;
      return;
    }
    try {
      routing.value = await apiGet<RoutingResponse>(
        `/agents/${encodeURIComponent(name)}/routing`,
        { signal: aborter.signal, timeoutMs: 10_000 },
      );
      error.value = "";
    } catch (e) {
      // Routing reflection is best-effort; absence falls back to
      // the caller's prior default. Swallow AbortError.
      if ((e as { name?: string })?.name === "AbortError") return;
      routing.value = null;
      error.value = e instanceof Error ? e.message : String(e);
    }
  }

  // Re-fetch whenever agentName changes (mount, switch-agent).
  watchEffect(load);

  onUnmounted(() => aborter?.abort());

  // Resolve the default backend for a given kind, falling back to
  // routing.default, then null. Callers typically use kind="a2a" since
  // the dashboard chat maps to the A2A entrypoint.
  function defaultBackendFor(
    kind: keyof RoutingResponse["routing"] = "a2a",
  ): string | null {
    const r = routing.value;
    if (!r) return null;
    return r.routing[kind]?.agent ?? r.default ?? null;
  }

  return {
    routing,
    error,
    load,
    defaultBackendFor,
  };
}
