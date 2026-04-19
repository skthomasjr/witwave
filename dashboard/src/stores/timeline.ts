import { defineStore } from "pinia";
import { computed, ref, watch } from "vue";
import {
  useEventStream,
  type EventEnvelope,
  type UseEventStreamOptions,
  type UseEventStreamReturn,
} from "../composables/useEventStream";

// Pinia store wrapping the harness `/events/stream` SSE feed (#1110).
//
// Why a store and not a straight composable call from the view:
//   - Connection survives view unmount (e.g. navigate to Team then back
//     to Timeline keeps the feed uninterrupted).
//   - Selectors (filterByType / filterByAgent / search) are module-level
//     so the AlertBanner (phase 2) can subscribe without re-mounting the
//     stream.
//
// The store keeps a separate, larger ring (default 1000) than the
// composable's in-memory default (500). The composable is the live
// tail; the store is the fleet-visible buffer that feeds the view.

const DEFAULT_RING_SIZE = 1000;
// Relative URL — nginx / dev proxy forwards to the harness at /events/stream.
// Phase 1 targets a single harness (first agent in the deployment). Multi-
// agent fanout (one stream per team member, merged client-side) is a
// phase-1.5 follow-up tracked at the end of this module.
const DEFAULT_EVENTS_URL = "/events/stream";

// Runtime-injected operator config (mirrors the #1061 traceApiUrl pattern).
// Set via the dashboard nginx ConfigMap so operators can inject:
//   - harnessBearerToken: the harness CONVERSATIONS_AUTH_TOKEN value
//   - timelineEventsUrl : override the default /events/stream (e.g. when
//                         the dashboard pod sits behind its own ingress).
declare global {
  interface Window {
    __NYX_CONFIG__?: {
      harnessBearerToken?: string;
      timelineEventsUrl?: string;
    } & Record<string, unknown>;
  }
}

function resolveBearerToken(explicit?: string): string | undefined {
  if (explicit) return explicit;
  if (typeof window === "undefined") return undefined;
  const cfg = window.__NYX_CONFIG__;
  if (!cfg) return undefined;
  const tok = cfg.harnessBearerToken;
  return typeof tok === "string" && tok.length > 0 ? tok : undefined;
}

function resolveEventsUrl(explicit?: string): string {
  if (explicit) return explicit;
  if (typeof window !== "undefined") {
    const cfg = window.__NYX_CONFIG__;
    if (cfg && typeof cfg.timelineEventsUrl === "string" && cfg.timelineEventsUrl) {
      return cfg.timelineEventsUrl;
    }
  }
  return DEFAULT_EVENTS_URL;
}

export interface TimelineStoreInitOptions extends UseEventStreamOptions {
  ringSize?: number;
  url?: string;
}

// Expose the stream handle for tests so they can push events through a
// fake fetch without touching private store internals.
let activeStream: UseEventStreamReturn | null = null;
let activeWatcherStop: (() => void) | null = null;

function clampRing(size: number | undefined): number {
  if (!size || !Number.isFinite(size) || size <= 0) return DEFAULT_RING_SIZE;
  return Math.floor(size);
}

export const useTimelineStore = defineStore("timeline", () => {
  const events = ref<EventEnvelope[]>([]);
  const connected = ref(false);
  const reconnecting = ref(false);
  const error = ref("");
  const ringSize = ref(DEFAULT_RING_SIZE);
  const started = ref(false);

  function start(opts: TimelineStoreInitOptions = {}): void {
    if (started.value) return;
    started.value = true;
    ringSize.value = clampRing(opts.ringSize);

    const url = resolveEventsUrl(opts.url);
    const token = resolveBearerToken(opts.token);
    // Let the composable hold a smaller live window — the store keeps
    // the authoritative ring. maxEvents on the composable trims rapid
    // in-memory growth between reactive flushes.
    const stream = useEventStream(url, {
      ...opts,
      token,
      maxEvents: Math.min(ringSize.value, opts.maxEvents ?? ringSize.value),
    });
    activeStream = stream;

    // Mirror connection state. Direct assignment to .value is fine —
    // the composable exposes refs whose values we copy through.
    activeWatcherStop = watch(
      [stream.events, stream.connected, stream.reconnecting, stream.error],
      ([ev, conn, rec, err]) => {
        connected.value = conn as boolean;
        reconnecting.value = rec as boolean;
        error.value = err as string;
        if (Array.isArray(ev)) {
          const arr = ev as EventEnvelope[];
          if (arr.length <= ringSize.value) {
            events.value = arr.slice();
          } else {
            events.value = arr.slice(arr.length - ringSize.value);
          }
        }
      },
      { immediate: true, deep: false },
    );
  }

  function stop(): void {
    if (activeWatcherStop) {
      activeWatcherStop();
      activeWatcherStop = null;
    }
    if (activeStream) {
      activeStream.close();
      activeStream = null;
    }
    started.value = false;
  }

  // Test-only injection. Lets specs feed events through the same
  // reactive pipeline without standing up a fake fetch + ReadableStream.
  function __pushForTest(envelope: EventEnvelope): void {
    const next = events.value.slice();
    next.push(envelope);
    if (next.length > ringSize.value) {
      next.splice(0, next.length - ringSize.value);
    }
    events.value = next;
  }

  function __resetForTest(): void {
    stop();
    events.value = [];
    connected.value = false;
    reconnecting.value = false;
    error.value = "";
    ringSize.value = DEFAULT_RING_SIZE;
  }

  // --- Selectors ---------------------------------------------------------

  // Returns events whose `type` is in the provided list. Empty list ⇒
  // pass-through so a "no filter selected" UI state doesn't require
  // callers to special-case.
  function filterByType(types: string[]): EventEnvelope[] {
    if (!types || types.length === 0) return events.value.slice();
    const set = new Set(types);
    return events.value.filter((e) => set.has(e.type));
  }

  function filterByAgent(agents: string[]): EventEnvelope[] {
    if (!agents || agents.length === 0) return events.value.slice();
    const set = new Set(agents);
    return events.value.filter((e) => {
      if (e.agent_id === null) return set.has("__global__");
      return set.has(e.agent_id);
    });
  }

  function search(q: string): EventEnvelope[] {
    const term = (q || "").trim().toLowerCase();
    if (!term) return events.value.slice();
    return events.value.filter((e) => {
      try {
        // Stringify the whole envelope so the search covers type, agent,
        // id, and every payload key/value. Keep the haystack lowercased
        // at query time — the ring is small enough (~1000 entries) that
        // per-query cost is acceptable; if that changes, cache per-event.
        return JSON.stringify(e).toLowerCase().includes(term);
      } catch {
        return false;
      }
    });
  }

  const eventCount = computed(() => events.value.length);

  return {
    // state
    events,
    connected,
    reconnecting,
    error,
    ringSize,
    eventCount,
    started,
    // lifecycle
    start,
    stop,
    // selectors
    filterByType,
    filterByAgent,
    search,
    // test helpers
    __pushForTest,
    __resetForTest,
  };
});

export type { EventEnvelope } from "../composables/useEventStream";

// -----------------------------------------------------------------------------
// TODO(#1110 phase 1.5) — multi-agent fanout
//
// Phase 1 targets a single harness (default /events/stream, or an override
// via __NYX_CONFIG__.timelineEventsUrl). For fleet deployments where the
// dashboard faces N agents with their own harnesses, replace `activeStream`
// with a Map<agentName, UseEventStreamReturn> indexed off `useTeam().members`.
// Add/remove streams as the team directory changes, tag each emitted event
// with the originating agent name at merge time, and preserve a single ring
// ordered by ts. The selectors (`filterByAgent`, etc.) already assume this
// per-agent labelling so the API surface doesn't need to change.
//
// Auth stays bearer-based; each per-agent stream can either share
// __NYX_CONFIG__.harnessBearerToken (single-secret deployments) or pull
// from a per-agent token map that the team directory carries.
// -----------------------------------------------------------------------------
