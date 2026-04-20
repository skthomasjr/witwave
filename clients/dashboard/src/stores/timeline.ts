import { defineStore } from "pinia";
import { computed, ref, watch } from "vue";
import { useTeam } from "../composables/useTeam";
import {
  useEventStream,
  type EventEnvelope,
  type UseEventStreamOptions,
  type UseEventStreamReturn,
} from "../composables/useEventStream";
// Circular with `useAlerts` (which imports `useTimelineStore` for its
// watchers) — ES modules resolve this fine as long as the binding is
// only dereferenced at call time, not at top-level of module eval.
// `__resetUseAlerts` is exported directly from the module, so importing
// the function reference here is safe. (#1238)
import { __resetUseAlerts } from "../composables/useAlerts";

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
//
// -----------------------------------------------------------------------
// Fanout model (phase 1.5, #1110)
//
// In a fleet deployment the dashboard faces N agents, each with its own
// harness and its own `/events/stream` SSE endpoint. Rather than run a
// single stream against one "lead" harness, we open one
// `useEventStream` per team member (discovered via `useTeam().members`)
// and merge their envelopes into a single authoritative ring ordered by
// `ts`. The per-agent stream URL follows the existing proxy convention
// `/api/agents/<name>/events/stream`, which nginx / vite dev-proxy
// forwards to that agent's harness.
//
// Stream lifecycle is reactive: streams are opened when a member enters
// the team directory and closed when a member leaves, so adding / removing
// agents at runtime does not require a page reload.
//
// Auth sharing: every agent stream uses the same bearer pulled from
// `__WITWAVE_CONFIG__.harnessBearerToken`. Single-secret deployments are the
// common case today; per-agent tokens can be layered in later without
// touching the merge path (add a `tokenFor(name)` hook, flow it into
// `openStreamFor`, done).
//
// Agent-id tagging: harness-wide events carry `agent_id: null`. At
// merge-time we stamp the originating member name onto those envelopes
// so `filterByAgent` behaves uniformly across scoped and global events
// — the view never has to special-case "which harness did this null-
// scoped event come from".
//
// Override path: if a caller passes `opts.url` to `start()` we skip
// fanout entirely and run a single stream against that URL. This
// preserves the phase-1 single-harness behaviour that test fixtures
// (and `values-test.yaml` single-agent setups) rely on.
// -----------------------------------------------------------------------

const DEFAULT_RING_SIZE = 1000;
// Relative URL — nginx / dev proxy forwards to the harness at /events/stream.
// Used only when the override `opts.url` path is taken; the fanout path
// derives per-agent URLs from `useTeam().members`.
const DEFAULT_EVENTS_URL = "/events/stream";

// Runtime-injected operator config (mirrors the #1061 traceApiUrl pattern).
// Set via the dashboard nginx ConfigMap so operators can inject:
//   - harnessBearerToken: the harness CONVERSATIONS_AUTH_TOKEN value
//   - timelineEventsUrl : override the single-stream URL when a caller
//                         passes `opts.url` (fanout path ignores this).
declare global {
  interface Window {
    __WITWAVE_CONFIG__?: {
      harnessBearerToken?: string;
      timelineEventsUrl?: string;
      basePath?: string;
    } & Record<string, unknown>;
  }
}

// Resolve the deployment base path prefix. Dashboard instances deployed
// under a sub-path (e.g. /witwave) need every proxy URL to share that
// prefix. (#1163)
function resolveBasePath(): string {
  if (typeof window === "undefined") return "";
  const cfg = window.__WITWAVE_CONFIG__;
  if (!cfg) return "";
  const bp = cfg.basePath;
  if (typeof bp !== "string" || bp.length === 0) return "";
  // Normalise: no trailing slash; ensure leading slash.
  let out = bp;
  if (!out.startsWith("/")) out = "/" + out;
  if (out.endsWith("/")) out = out.slice(0, -1);
  return out;
}

function resolveBearerToken(explicit?: string): string | undefined {
  if (explicit) return explicit;
  if (typeof window === "undefined") return undefined;
  const cfg = window.__WITWAVE_CONFIG__;
  if (!cfg) return undefined;
  const tok = cfg.harnessBearerToken;
  return typeof tok === "string" && tok.length > 0 ? tok : undefined;
}

function resolveSingleEventsUrl(explicit?: string): string {
  if (explicit) return explicit;
  if (typeof window !== "undefined") {
    const cfg = window.__WITWAVE_CONFIG__;
    if (cfg && typeof cfg.timelineEventsUrl === "string" && cfg.timelineEventsUrl) {
      return cfg.timelineEventsUrl;
    }
  }
  return `${resolveBasePath()}${DEFAULT_EVENTS_URL}`;
}

// Per-agent stream URL — nginx proxies /api/agents/<name>/* straight to
// that agent's harness. Path parity with the rest of the dashboard's
// per-agent API surface. Honors `__WITWAVE_CONFIG__.basePath` so deployments
// under a sub-path (e.g. /witwave) get the correct proxy prefix. (#1163)
export function agentStreamUrl(name: string): string {
  return `${resolveBasePath()}/api/agents/${encodeURIComponent(name)}/events/stream`;
}

export interface TimelineStoreInitOptions extends UseEventStreamOptions {
  ringSize?: number;
  // When set, bypasses fanout and runs a single stream against this URL.
  // Preserves the phase-1 single-harness behaviour for tests + dev.
  url?: string;
}

// Per-agent stream handle. We track the stream return alongside the
// watcher teardown so a member leaving the team shuts both down cleanly.
interface AgentStreamHandle {
  name: string;
  stream: UseEventStreamReturn;
  stopWatcher: () => void;
  // Id of the last envelope we merged for this agent. Positional length
  // tracking broke down when the composable's ring evicted from the
  // left — the array shrinks, the cursor lands on what looks like a
  // "new" envelope, and we re-merged already-seen events. Iterating
  // forward from a matching id stays stable across evictions; if the
  // id is no longer present we fall back to a full scan with seenIds
  // dedup. (#1242)
  lastSeenEnvelopeId: string;
}

function clampRing(size: number | undefined): number {
  if (!size || !Number.isFinite(size) || size <= 0) return DEFAULT_RING_SIZE;
  return Math.floor(size);
}

// Parse an ISO timestamp to a millis number for ordered insert. Falls
// back to 0 on parse failure so a malformed `ts` lands at the oldest
// end rather than breaking insertion.
function tsMillis(ts: string): number {
  const n = Date.parse(ts);
  return Number.isFinite(n) ? n : 0;
}

// Binary-search the insert position for `candidate` in a ts-sorted
// `arr`. Returns the smallest index `i` such that `arr[i].ts >
// candidate.ts`. Ties append after equal-ts entries (stable arrival
// order within a millisecond).
function insertIndexByTs(arr: EventEnvelope[], candidate: EventEnvelope): number {
  const target = tsMillis(candidate.ts);
  let lo = 0;
  let hi = arr.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (tsMillis(arr[mid].ts) <= target) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return lo;
}

// Stamp the originating member name onto a harness-global envelope. We
// clone before mutating so the composable's own ref stays untouched —
// re-entering the merge path with the same envelope must not double-
// stamp (and must not race other subscribers of the composable's ref).
function tagEnvelope(env: EventEnvelope, member: string): EventEnvelope {
  if (env.agent_id !== null) return env;
  return { ...env, agent_id: member };
}

export const useTimelineStore = defineStore("timeline", () => {
  const events = ref<EventEnvelope[]>([]);
  const connected = ref(false);
  const reconnecting = ref(false);
  const error = ref("");
  const ringSize = ref(DEFAULT_RING_SIZE);
  const started = ref(false);
  // Track which envelope ids we've already merged so we don't re-insert
  // on every tick — the composable's ref is append-only within its own
  // window, but ring eviction there can truncate from the left and the
  // merge path mustn't confuse "left-truncated" with "new".
  const seenIds = new Set<string>();

  // Per-store fanout state. Kept inside the setup closure (not module
  // scope) so a Pinia reset / multiple store instantiations don't share
  // stream handles. (#1155) A Map keyed by member name carries per-agent
  // streams; the override path uses `singleStream` instead.
  const agentStreams = new Map<string, AgentStreamHandle>();
  let singleStream: UseEventStreamReturn | null = null;
  let singleWatcherStop: (() => void) | null = null;
  let teamWatcherStop: (() => void) | null = null;
  let teamHandle: ReturnType<typeof useTeam> | null = null;
  // Cached token for fanout streams — resolved once at `start()` time.
  let sharedBearerToken: string | undefined;

  function trimRing(): void {
    const max = ringSize.value;
    if (events.value.length <= max) return;
    const drop = events.value.length - max;
    const removed = events.value.slice(0, drop);
    events.value = events.value.slice(drop);
    for (const e of removed) {
      seenIds.delete(e.id);
    }
  }

  function mergeBatch(batch: EventEnvelope[]): void {
    if (batch.length === 0) return;
    // Cheap path: if the batch is already newer than everything in the
    // ring (common for a single agent with monotonic ts), append in one
    // shot and skip binary search entirely.
    const lastExisting =
      events.value.length > 0
        ? tsMillis(events.value[events.value.length - 1].ts)
        : -Infinity;
    let allAppend = true;
    for (const env of batch) {
      if (tsMillis(env.ts) < lastExisting) {
        allAppend = false;
        break;
      }
    }

    if (allAppend) {
      const next = events.value.slice();
      for (const env of batch) {
        if (seenIds.has(env.id)) continue;
        seenIds.add(env.id);
        next.push(env);
      }
      events.value = next;
    } else {
      // Interleaved arrival — binary-search the insert slot per event.
      // N*log(M) in the worst case but the batch is typically ≤ a
      // handful of events per tick.
      const next = events.value.slice();
      for (const env of batch) {
        if (seenIds.has(env.id)) continue;
        seenIds.add(env.id);
        const idx = insertIndexByTs(next, env);
        next.splice(idx, 0, env);
      }
      events.value = next;
    }
    trimRing();
  }

  function recomputeAggregateState(): void {
    if (singleStream) {
      connected.value = singleStream.connected.value;
      reconnecting.value = singleStream.reconnecting.value;
      error.value = singleStream.error.value;
      return;
    }
    const handles = Array.from(agentStreams.values());
    if (handles.length === 0) {
      connected.value = false;
      reconnecting.value = false;
      error.value = "";
      return;
    }
    connected.value = handles.every((h) => h.stream.connected.value);
    reconnecting.value = handles.some((h) => h.stream.reconnecting.value);
    // First non-empty error across streams wins, prefixed with the
    // agent name so UX can attribute the failure.
    let firstError = "";
    for (const h of handles) {
      const e = h.stream.error.value;
      if (e) {
        firstError = `${h.name}: ${e}`;
        break;
      }
    }
    error.value = firstError;
  }

  function openStreamFor(name: string, baseOpts: UseEventStreamOptions): void {
    if (agentStreams.has(name)) return;
    const stream = useEventStream(agentStreamUrl(name), {
      ...baseOpts,
      token: sharedBearerToken,
      // Per-agent live window sized to the store's ring — the authoritative
      // ring downstream trims; this just prevents unbounded growth within
      // the composable between reactive flushes.
      maxEvents: Math.min(ringSize.value, baseOpts.maxEvents ?? ringSize.value),
    });
    const handle: AgentStreamHandle = {
      name,
      stream,
      stopWatcher: () => {},
      lastSeenEnvelopeId: "",
    };
    agentStreams.set(name, handle);

    handle.stopWatcher = watch(
      [stream.events, stream.connected, stream.reconnecting, stream.error],
      ([ev]) => {
        if (Array.isArray(ev)) {
          const arr = ev as EventEnvelope[];
          // Diff forward from the last id we merged. If the cursor id
          // is no longer in the composable's ring (left-eviction), fall
          // back to a full scan — `seenIds` dedups the merged batch so
          // re-submitting the whole array is safe. (#1242)
          let startAt = 0;
          if (handle.lastSeenEnvelopeId) {
            const idx = arr.findIndex(
              (e) => e.id === handle.lastSeenEnvelopeId,
            );
            startAt = idx === -1 ? 0 : idx + 1;
          }
          const fresh = arr.slice(startAt);
          if (arr.length > 0) {
            handle.lastSeenEnvelopeId = arr[arr.length - 1].id ?? "";
          }
          if (fresh.length > 0) {
            const tagged = fresh.map((env) => tagEnvelope(env, name));
            mergeBatch(tagged);
          }
        }
        recomputeAggregateState();
      },
      { immediate: true, deep: false },
    );
  }

  function closeStreamFor(name: string): void {
    const handle = agentStreams.get(name);
    if (!handle) return;
    try {
      handle.stopWatcher();
    } catch {
      // ignore — watcher teardown should not block stream close.
    }
    try {
      handle.stream.close();
    } catch {
      // ignore — close is best-effort.
    }
    agentStreams.delete(name);
  }

  function start(opts: TimelineStoreInitOptions = {}): void {
    if (started.value) return;
    started.value = true;
    ringSize.value = clampRing(opts.ringSize);

    // -- Override path: single stream, preserves phase-1 behaviour. -----
    if (opts.url) {
      const token = resolveBearerToken(opts.token);
      const stream = useEventStream(resolveSingleEventsUrl(opts.url), {
        ...opts,
        token,
        maxEvents: Math.min(ringSize.value, opts.maxEvents ?? ringSize.value),
      });
      singleStream = stream;

      // The single-stream path does not need ts-ordered merging (one
      // source, monotonic ts within it), but we still route through
      // `mergeBatch` so seenIds / ring trimming stay uniform. Events
      // keep their original agent_id — no tagging in override mode,
      // because there's no "originating member" identity for this URL.
      // Id-based cursor (parity with the fanout path) — see #1242.
      let lastSeenEnvelopeId = "";
      singleWatcherStop = watch(
        [stream.events, stream.connected, stream.reconnecting, stream.error],
        ([ev]) => {
          if (Array.isArray(ev)) {
            const arr = ev as EventEnvelope[];
            let startAt = 0;
            if (lastSeenEnvelopeId) {
              const idx = arr.findIndex((e) => e.id === lastSeenEnvelopeId);
              startAt = idx === -1 ? 0 : idx + 1;
            }
            const fresh = arr.slice(startAt);
            if (arr.length > 0) {
              lastSeenEnvelopeId = arr[arr.length - 1].id ?? "";
            }
            if (fresh.length > 0) mergeBatch(fresh);
          }
          recomputeAggregateState();
        },
        { immediate: true, deep: false },
      );
      return;
    }

    // -- Fanout path: one stream per team member. -----------------------
    sharedBearerToken = resolveBearerToken(opts.token);
    const baseOpts: UseEventStreamOptions = { ...opts };
    // Strip per-request token / url from the template — we inject
    // `token` per stream and URL is derived from the member name.
    delete baseOpts.token;

    teamHandle = useTeam();
    // Use a getter as the watch source so test mocks that hand back a
    // plain `{ value: [] }` instead of a real Ref still work — Vue
    // refuses non-ref/non-reactive objects as sources and we want the
    // fanout loop to be forgiving of both shapes. Unwrapping ourselves
    // also means we always hand the callback a plain array.
    const readMembers = (): readonly { name?: string }[] => {
      const raw = teamHandle?.members as unknown;
      if (Array.isArray(raw)) return raw as { name?: string }[];
      const inner = (raw as { value?: unknown })?.value;
      if (Array.isArray(inner)) return inner as { name?: string }[];
      return [];
    };
    teamWatcherStop = watch(
      readMembers,
      (list) => {
        const alive = new Set<string>();
        for (const m of list) {
          // A member without a reachable URL is still addressable via
          // the `/api/agents/<name>/` proxy — the proxy is a dashboard-
          // side concern, not a direct-to-harness one — so we key off
          // name alone and let the stream's own reconnect handle
          // transient unreachability.
          if (!m.name) continue;
          alive.add(m.name);
          if (!agentStreams.has(m.name)) {
            openStreamFor(m.name, baseOpts);
          }
        }
        // Close streams for members that have left the directory.
        for (const existing of Array.from(agentStreams.keys())) {
          if (!alive.has(existing)) {
            closeStreamFor(existing);
          }
        }
        recomputeAggregateState();
      },
      { immediate: true, deep: false },
    );
  }

  function stop(): void {
    if (singleWatcherStop) {
      singleWatcherStop();
      singleWatcherStop = null;
    }
    if (singleStream) {
      try {
        singleStream.close();
      } catch {
        // ignore
      }
      singleStream = null;
    }
    if (teamWatcherStop) {
      teamWatcherStop();
      teamWatcherStop = null;
    }
    teamHandle = null;
    for (const name of Array.from(agentStreams.keys())) {
      closeStreamFor(name);
    }
    started.value = false;
    recomputeAggregateState();
  }

  // Test-only injection. Lets specs feed events through the same
  // reactive pipeline without standing up a fake fetch + ReadableStream.
  function __pushForTest(envelope: EventEnvelope): void {
    mergeBatch([envelope]);
  }

  function __resetForTest(): void {
    stop();
    events.value = [];
    connected.value = false;
    reconnecting.value = false;
    error.value = "";
    ringSize.value = DEFAULT_RING_SIZE;
    seenIds.clear();
    // Belt-and-braces: stop() already tears these down, but tests that
    // exercise mid-setup failure paths may land here with stale state.
    // Fully clear the closure-local singletons. (#1155)
    agentStreams.clear();
    singleStream = null;
    singleWatcherStop = null;
    teamWatcherStop = null;
    teamHandle = null;
    sharedBearerToken = undefined;
    // Clear the alert module's event cursor in tandem. Resetting the
    // timeline store without resetting the alert module leaves
    // `lastProcessedEventId` pointing at an id that no longer exists
    // in the fresh ring, causing the next event batch to skip dispatch
    // entirely. (#1238)
    //
    // #1382: guard for the circular-import initialisation window.
    // If tree-shaking / SSR rehydration evaluates the modules in an
    // unexpected order, `__resetUseAlerts` may be `undefined` at the
    // point this code runs. Treat that as a soft no-op instead of a
    // TypeError so tests keep passing; in production the import order
    // is stable.
    if (typeof __resetUseAlerts === "function") {
      __resetUseAlerts();
    }
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

  // #1380: cache the lowercased JSON.stringify per envelope so a fast
  // typist doesn't pay O(ring × payload) per keystroke. WeakMap keyed
  // on the envelope object means the cache entry dies with the envelope.
  const searchIndex = new WeakMap<EventEnvelope, string>();

  function searchHaystack(e: EventEnvelope): string {
    const cached = searchIndex.get(e);
    if (cached !== undefined) return cached;
    let haystack = "";
    try {
      haystack = JSON.stringify(e).toLowerCase();
    } catch {
      haystack = "";
    }
    searchIndex.set(e, haystack);
    return haystack;
  }

  function search(q: string): EventEnvelope[] {
    const term = (q || "").trim().toLowerCase();
    if (!term) return events.value.slice();
    return events.value.filter((e) => searchHaystack(e).includes(term));
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
// Phase 1.5 landed (#1110)
//
// The store now opens one `useEventStream` per team member (discovered via
// `useTeam().members`) and merges their envelopes into a single ts-ordered
// ring. Members entering / leaving the directory open / close their streams
// reactively. The override path (`start({ url })`) still runs a single
// stream, so existing test fixtures and single-agent dev setups are
// unchanged.
//
// Future work:
//   - Per-agent bearer tokens. Today every stream shares
//     `__WITWAVE_CONFIG__.harnessBearerToken`. If a deployment rotates tokens
//     independently per agent, add a `tokenFor(name)` hook and flow it
//     through `openStreamFor`.
//   - Backpressure feedback. If one agent's stream produces events
//     dramatically faster than the others, the single ring could evict
//     a slow agent's history prematurely. Per-agent caps (ring allocated
//     by agent then merged at query time) would address that without
//     giving up the unified `events` ref.
// -----------------------------------------------------------------------------
