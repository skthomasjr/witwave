import { onMounted, onUnmounted, ref } from "vue";
import { apiGet, ApiError } from "../api/client";
import type { Agent, TeamMember, TeamResponse } from "../types/team";

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
// cadence can always win (#891). The timer is restarted whenever the
// effective interval changes. Member/directory timeouts take the latest
// supplied value (still tight enough in practice).
let currentMemberTimeoutMs = 5000;
let currentDirectoryTimeoutMs = 5000;

// Multiset of requested interval values; we pick the min on every
// subscribe/unsubscribe so dropping a tight subscriber relaxes the
// cadence correctly.
const subscriberIntervals: number[] = [];
let effectiveIntervalMs = 5000;
let subscriberCount = 0;
let pollerTimer: ReturnType<typeof setInterval> | null = null;
let pollerAborter: AbortController | null = null;

function recomputeEffectiveInterval(): number {
  return subscriberIntervals.length === 0
    ? 5000
    : Math.min(...subscriberIntervals);
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
): void {
  // Register this subscriber's interval and recompute the effective
  // (min) cadence. A later subscriber that needs a tighter interval
  // than the current one restarts the timer (#891); one that requests
  // a looser interval leaves the timer unchanged.
  subscriberIntervals.push(intervalMs);
  currentMemberTimeoutMs = memberTimeoutMs;
  currentDirectoryTimeoutMs = directoryTimeoutMs;

  const newEffective = recomputeEffectiveInterval();

  if (pollerTimer === null) {
    effectiveIntervalMs = newEffective;
    void sharedRefresh();
    pollerTimer = setInterval(() => void sharedRefresh(), effectiveIntervalMs);
    return;
  }

  if (newEffective !== effectiveIntervalMs) {
    clearInterval(pollerTimer);
    effectiveIntervalMs = newEffective;
    pollerTimer = setInterval(() => void sharedRefresh(), effectiveIntervalMs);
  }
}

function stopShared(): void {
  if (pollerTimer !== null) {
    clearInterval(pollerTimer);
    pollerTimer = null;
  }
  pollerAborter?.abort();
  pollerAborter = null;
}

function unregisterSubscriber(intervalMs: number): void {
  const idx = subscriberIntervals.indexOf(intervalMs);
  if (idx !== -1) subscriberIntervals.splice(idx, 1);

  if (subscriberIntervals.length === 0 || pollerTimer === null) {
    return;
  }
  const newEffective = recomputeEffectiveInterval();
  if (newEffective !== effectiveIntervalMs) {
    clearInterval(pollerTimer);
    effectiveIntervalMs = newEffective;
    pollerTimer = setInterval(() => void sharedRefresh(), effectiveIntervalMs);
  }
}

// Test/shutdown hook — unit tests reset the singleton between cases so
// timers from the previous suite can't leak into the next. Not part of
// the stable surface consumed by views.
export function __resetSharedTeamPoller(): void {
  stopShared();
  subscriberCount = 0;
  subscriberIntervals.length = 0;
  effectiveIntervalMs = 5000;
  sharedMembers.value = [];
  sharedError.value = "";
  sharedLoading.value = true;
}

export function useTeam(opts: UseTeamOptions = {}) {
  const intervalMs = opts.intervalMs ?? 5000;
  const memberTimeoutMs = opts.memberTimeoutMs ?? 5000;
  const directoryTimeoutMs = opts.directoryTimeoutMs ?? 5000;

  onMounted(() => {
    subscriberCount += 1;
    startShared(intervalMs, memberTimeoutMs, directoryTimeoutMs);
  });

  onUnmounted(() => {
    subscriberCount = Math.max(0, subscriberCount - 1);
    if (subscriberCount === 0) {
      // Last subscriber leaving: tear everything down AND clear the
      // interval multiset so the poller starts cleanly on next mount.
      stopShared();
      subscriberIntervals.length = 0;
      effectiveIntervalMs = 5000;
    } else {
      // Recompute min — if this subscriber was the tightest, the poller
      // should relax its cadence accordingly (#891).
      unregisterSubscriber(intervalMs);
    }
  });

  return {
    members: sharedMembers,
    error: sharedError,
    loading: sharedLoading,
    refresh: sharedRefresh,
  };
}
