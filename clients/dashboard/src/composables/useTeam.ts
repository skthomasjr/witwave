import { onMounted, onUnmounted, ref } from "vue";
import { apiGet, ApiError } from "../api/client";
import type { Agent, TeamMember, TeamResponse } from "../types/team";
import {
  ensureVisibilityListenerInstalled,
  pollingShouldSkipTick,
} from "./usePollingControl";

// Team state — discovery + per-agent fan-out.
//
// Architecture note (#470): the dashboard pod owns routing. Discovery hits
// the dashboard nginx's local /api/team (Helm-rendered member list, no
// agent call). Each member's full card set comes from /api/agents/<name>/
// agents, routed directly to that agent's harness — so a single agent
// outage only affects its own card, never the whole view.
//
// Shared poller (#742): historically every composable that wanted team
// state invoked ``useTeam`` and stood up its own timer + directory fetch
// + fan-out. A two-composable page (TeamView + useHealth) already meant
// double the network traffic; the automation view's ~five composables
// pushed that to ~25 requests per 5s interval on a 3-agent team. The
// module-level singleton below polls the shared directory exactly once
// per keyed ``intervalMs`` and every composable subscribes to the same
// reactive refs. A reference count drives start/stop so no timer runs
// when no view is mounted.

interface TeamDirectoryEntry {
  name: string;
  url: string;
}

export interface UseTeamOptions {
  intervalMs?: number;
  // Per-member timeout (ms) so one unreachable agent cannot stall the
  // whole fan-out and stop the dashboard refreshing (#743). Default
  // 5000ms; override per-caller if a specific view needs more slack.
  memberTimeoutMs?: number;
  // Timeout for the /team discovery call itself (#743). Default 5000ms.
  directoryTimeoutMs?: number;
}

// Shared singleton state. All composable subscribers share these refs so
// Vue's reactivity propagates a single fetch's result to every view
// without re-issuing the network call.
const sharedMembers = ref<TeamResponse>([]);
const sharedError = ref<string>("");
const sharedLoading = ref<boolean>(true);

// Poller configuration — the effective interval is min(intervalMs) across
// *all* active subscribers so a later subscriber that needs tighter
// cadence can always win (#891). Timeouts pick the MAX across subscribers
// (most conservative) so a later view mounting with a tight timeout
// doesn't cause spurious AbortError on a healthy but busy agent (#951).
// All three values restart whenever the derived aggregate changes.
let currentMemberTimeoutMs = 5000;
let currentDirectoryTimeoutMs = 5000;

// Token-keyed subscriber map. The previous implementation keyed register/
// deregister off the raw `intervalMs` numeric value, which meant two
// subscribers sharing the default `5000` could not be distinguished —
// unregistering either one removed the same entry and the refcount
// skewed. Each `startShared` call now mints a fresh `Symbol()` token,
// and `unregisterSubscriber` deletes by token so every subscribe/
// unsubscribe pair is accounted for exactly once. (#1239)
interface SubscriberEntry {
  intervalMs: number;
  memberTimeoutMs: number;
  directoryTimeoutMs: number;
}
const subscribers = new Map<symbol, SubscriberEntry>();
let effectiveIntervalMs = 5000;
let subscriberCount = 0;
let pollerTimer: ReturnType<typeof setInterval> | null = null;
let pollerAborter: AbortController | null = null;

function recomputeEffectiveInterval(): number {
  if (subscribers.size === 0) return 5000;
  let min = Number.POSITIVE_INFINITY;
  for (const s of subscribers.values()) {
    if (s.intervalMs < min) min = s.intervalMs;
  }
  return Number.isFinite(min) ? min : 5000;
}

function recomputeTimeouts(): void {
  if (subscribers.size === 0) {
    currentMemberTimeoutMs = 5000;
    currentDirectoryTimeoutMs = 5000;
    return;
  }
  let maxMember = 0;
  let maxDir = 0;
  for (const s of subscribers.values()) {
    if (s.memberTimeoutMs > maxMember) maxMember = s.memberTimeoutMs;
    if (s.directoryTimeoutMs > maxDir) maxDir = s.directoryTimeoutMs;
  }
  currentMemberTimeoutMs = maxMember > 0 ? maxMember : 5000;
  currentDirectoryTimeoutMs = maxDir > 0 ? maxDir : 5000;
}

async function fetchMember(
  entry: TeamDirectoryEntry,
  signal: AbortSignal,
  memberTimeoutMs: number,
): Promise<TeamMember> {
  try {
    const agents = await apiGet<Agent[]>(
      `/agents/${encodeURIComponent(entry.name)}/agents`,
      { signal, timeoutMs: memberTimeoutMs },
    );
    return { name: entry.name, url: entry.url, agents };
  } catch (e) {
    if ((e as { name?: string }).name === "AbortError") throw e;
    const msg = e instanceof ApiError ? e.message : (e as Error).message;
    return { name: entry.name, url: entry.url, agents: [], error: msg };
  }
}

async function sharedRefresh(): Promise<void> {
  pollerAborter?.abort();
  const localAborter = new AbortController();
  pollerAborter = localAborter;
  const signal = localAborter.signal;
  try {
    const directory = await apiGet<TeamDirectoryEntry[]>("/team", {
      signal,
      timeoutMs: currentDirectoryTimeoutMs,
    });
    const resolved = await Promise.all(
      directory.map((entry) =>
        fetchMember(entry, signal, currentMemberTimeoutMs),
      ),
    );
    if (signal.aborted) return;
    sharedMembers.value = resolved;
    sharedError.value = "";
  } catch (e) {
    // Identity check (#744): when the active aborter has moved on,
    // this rejection belongs to the OLD refresh cycle — stay silent
    // regardless of the specific error, so bursty refreshes don't
    // surface spurious AbortError toasts or 'degraded' badges.
    if (
      pollerAborter !== localAborter ||
      (e as { name?: string }).name === "AbortError"
    ) {
      return;
    }
    sharedError.value =
      e instanceof ApiError ? e.message : (e as Error).message;
  } finally {
    // Only clear loading for the currently-active cycle. A stale
    // refresh completing late should not flip the spinner off if a
    // newer one is still in flight.
    if (pollerAborter === localAborter) sharedLoading.value = false;
  }
}

function startShared(
  intervalMs: number,
  memberTimeoutMs: number,
  directoryTimeoutMs: number,
): symbol {
  // Register this subscriber's interval + timeouts and recompute the
  // effective (min) cadence + (max) timeouts. A later subscriber that
  // needs a tighter interval than the current one restarts the timer
  // (#891); a later subscriber with a tighter timeout is ignored so
  // healthy-but-busy agents aren't aborted mid-poll (#951).
  const token = Symbol("useTeamSubscriber");
  subscribers.set(token, { intervalMs, memberTimeoutMs, directoryTimeoutMs });
  recomputeTimeouts();

  const newEffective = recomputeEffectiveInterval();

  if (pollerTimer === null) {
    effectiveIntervalMs = newEffective;
    // Ensure the global visibility listener exists so tab-hidden gating
    // takes effect on the shared poller even if no component has yet
    // mounted usePollingControl() (#1107).
    ensureVisibilityListenerInstalled();
    void sharedRefresh();
    pollerTimer = setInterval(() => {
      // #1107: skip the tick (but don't tear the timer down) when the
      // user has paused auto-refresh or the tab is hidden. An explicit
      // refresh() call still fires via the manual path.
      if (pollingShouldSkipTick()) return;
      void sharedRefresh();
    }, effectiveIntervalMs);
    return token;
  }

  if (newEffective !== effectiveIntervalMs) {
    clearInterval(pollerTimer);
    effectiveIntervalMs = newEffective;
    pollerTimer = setInterval(() => {
      // #1107: skip the tick (but don't tear the timer down) when the
      // user has paused auto-refresh or the tab is hidden. An explicit
      // refresh() call still fires via the manual path.
      if (pollingShouldSkipTick()) return;
      void sharedRefresh();
    }, effectiveIntervalMs);
  }
  return token;
}

function stopShared(): void {
  if (pollerTimer !== null) {
    clearInterval(pollerTimer);
    pollerTimer = null;
  }
  pollerAborter?.abort();
  pollerAborter = null;
}

function unregisterSubscriber(token: symbol): void {
  if (!subscribers.delete(token)) return;
  recomputeTimeouts();

  if (subscribers.size === 0 || pollerTimer === null) {
    return;
  }
  const newEffective = recomputeEffectiveInterval();
  if (newEffective !== effectiveIntervalMs) {
    clearInterval(pollerTimer);
    effectiveIntervalMs = newEffective;
    pollerTimer = setInterval(() => {
      // #1107: skip the tick (but don't tear the timer down) when the
      // user has paused auto-refresh or the tab is hidden. An explicit
      // refresh() call still fires via the manual path.
      if (pollingShouldSkipTick()) return;
      void sharedRefresh();
    }, effectiveIntervalMs);
  }
}

// Test/shutdown hook — unit tests reset the singleton between cases so
// timers from the previous suite can't leak into the next. Not part of
// the stable surface consumed by views.
export function __resetSharedTeamPoller(): void {
  stopShared();
  subscriberCount = 0;
  subscribers.clear();
  effectiveIntervalMs = 5000;
  currentMemberTimeoutMs = 5000;
  currentDirectoryTimeoutMs = 5000;
  sharedMembers.value = [];
  sharedError.value = "";
  sharedLoading.value = true;
}

export function useTeam(opts: UseTeamOptions = {}) {
  const intervalMs = opts.intervalMs ?? 5000;
  const memberTimeoutMs = opts.memberTimeoutMs ?? 5000;
  const directoryTimeoutMs = opts.directoryTimeoutMs ?? 5000;

  // Each `useTeam()` call owns its own subscription token. Previously
  // register/deregister were keyed off the `intervalMs` numeric value,
  // which meant two concurrent subscribers sharing the default 5000
  // produced duplicated entries in parallel arrays and unregister
  // removed the wrong slot — skewing the refcount after repeated
  // mount/unmount cycles. (#1239)
  let token: symbol | null = null;

  onMounted(() => {
    subscriberCount += 1;
    token = startShared(intervalMs, memberTimeoutMs, directoryTimeoutMs);
  });

  onUnmounted(() => {
    subscriberCount = Math.max(0, subscriberCount - 1);
    if (subscriberCount === 0) {
      // Last subscriber leaving: tear everything down AND clear the
      // subscriber map so the poller starts cleanly on next mount.
      stopShared();
      subscribers.clear();
      effectiveIntervalMs = 5000;
      currentMemberTimeoutMs = 5000;
      currentDirectoryTimeoutMs = 5000;
      token = null;
    } else if (token !== null) {
      // Recompute min(interval) / max(timeouts). If this subscriber was
      // the tightest-cadence one, the poller should relax (#891); if it
      // was the longest-timeout one, timeouts contract back (#951).
      unregisterSubscriber(token);
      token = null;
    }
  });

  return {
    members: sharedMembers,
    error: sharedError,
    loading: sharedLoading,
    refresh: sharedRefresh,
  };
}
