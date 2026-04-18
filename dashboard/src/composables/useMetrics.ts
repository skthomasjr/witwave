import { computed, onMounted, onUnmounted, ref, watch, type Ref } from "vue";
import { apiGet, ApiError } from "../api/client";
import { mergeFamilies, parseProm, type FamilyMap } from "../utils/prometheus";

// Polls /api/agents/<name>/metrics for each team member, parses the
// Prometheus text into per-agent FamilyMaps, and exposes a merged
// cluster-wide view. Kept snapshot-only (no history): the dashboard shows
// the current-moment distribution across labels, matching the legacy ui/
// semantics. For trend analysis point Grafana at /metrics.

interface TeamDirectoryEntry {
  name: string;
  url: string;
}

export interface AgentMetrics {
  agent: string;
  families: FamilyMap;
  // When set, this agent's /metrics endpoint failed to respond. `families`
  // is an empty map in that case so merged aggregates are unaffected.
  error?: string;
}

// Aligned with useAgentFanout: map agentId -> error string for agents whose
// per-agent fetch failed during the most recent refresh. Successful agents
// are absent. Kept as a Record (not a Map) for easy template iteration.
export type PerAgentErrors = Record<string, string>;

async function fetchText(
  url: string,
  signal: AbortSignal,
  timeoutMs?: number,
): Promise<string> {
  // Combine the outer abort signal with a per-request timeout (#743).
  // When AbortSignal.any is available we merge; otherwise we fall back
  // to a manual timer.
  let cleanup: () => void = () => {};
  let effectiveSignal: AbortSignal = signal;
  if (timeoutMs && timeoutMs > 0) {
    const AnyAbortSignal = AbortSignal as unknown as {
      any?: (signals: AbortSignal[]) => AbortSignal;
      timeout?: (ms: number) => AbortSignal;
    };
    if (
      typeof AnyAbortSignal.any === "function" &&
      typeof AnyAbortSignal.timeout === "function"
    ) {
      effectiveSignal = AnyAbortSignal.any([
        signal,
        AnyAbortSignal.timeout(timeoutMs),
      ]);
    } else {
      const controller = new AbortController();
      const outerListener = () => controller.abort();
      signal.addEventListener("abort", outerListener);
      const timer = setTimeout(() => controller.abort(), timeoutMs);
      cleanup = () => {
        clearTimeout(timer);
        signal.removeEventListener("abort", outerListener);
      };
      effectiveSignal = controller.signal;
    }
  }
  try {
    const resp = await fetch(url, { signal: effectiveSignal });
    if (!resp.ok) throw new ApiError(resp.status, `HTTP ${resp.status}`);
    return await resp.text();
  } finally {
    cleanup();
  }
}

export interface UseMetricsOptions {
  // Polling interval in milliseconds. Accepts a ref or plain number. A value
  // of 0 disables the auto-refresh timer entirely (manual refresh() still
  // works). Changes are observed: the timer is torn down and reinstalled
  // whenever the interval changes.
  intervalMs?: Ref<number> | number;
  // Per-agent metrics-fetch timeout (ms). Default 5000 so one stuck
  // backend never stalls the whole fan-out and stops the dashboard
  // refreshing (#743).
  memberTimeoutMs?: number;
  // Timeout for the /team directory fetch (ms). Default 5000.
  directoryTimeoutMs?: number;
}

export function useMetrics(options: UseMetricsOptions = {}) {
  const perAgent = ref<AgentMetrics[]>([]);
  const perAgentErrors = ref<PerAgentErrors>({});
  const error = ref<string>("");
  const loading = ref<boolean>(true);
  const lastUpdated = ref<number | null>(null);

  // Track which agents we've already warned about in the current outage so
  // devtools doesn't get spammed at every poll tick. Cleared for an agent as
  // soon as it recovers.
  const warnedAgents = new Set<string>();

  let timer: ReturnType<typeof setInterval> | null = null;
  let aborter: AbortController | null = null;

  const memberTimeoutMs = options.memberTimeoutMs ?? 5000;
  const directoryTimeoutMs = options.directoryTimeoutMs ?? 5000;
  const intervalSource = options.intervalMs ?? 5000;
  const intervalRef: Ref<number> =
    typeof intervalSource === "number" ? ref(intervalSource) : intervalSource;

  function clearTimer(): void {
    if (timer !== null) {
      clearInterval(timer);
      timer = null;
    }
  }

  function installTimer(ms: number): void {
    clearTimer();
    if (ms > 0) {
      timer = setInterval(() => void refresh(), ms);
    }
  }

  async function refresh(): Promise<void> {
    aborter?.abort();
    aborter = new AbortController();
    const signal = aborter.signal;
    try {
      const directory = await apiGet<TeamDirectoryEntry[]>("/team", {
        signal,
        timeoutMs: directoryTimeoutMs,
      });
      const results = await Promise.all(
        directory.map(async (entry): Promise<AgentMetrics> => {
          try {
            const text = await fetchText(
              `/api/agents/${encodeURIComponent(entry.name)}/metrics`,
              signal,
              memberTimeoutMs,
            );
            // Clear any prior warn-suppression so a future failure warns again.
            warnedAgents.delete(entry.name);
            return { agent: entry.name, families: parseProm(text) };
          } catch (e) {
            if ((e as { name?: string }).name === "AbortError") throw e;
            const message =
              e instanceof ApiError ? e.message : (e as Error).message;
            // Throttled: warn once per agent per outage, not every poll tick.
            if (!warnedAgents.has(entry.name)) {
              warnedAgents.add(entry.name);
              console.warn(
                `[useMetrics] /metrics failed for agent "${entry.name}": ${message}`,
              );
            }
            return {
              agent: entry.name,
              families: new Map() as FamilyMap,
              error: message,
            };
          }
        }),
      );
      if (signal.aborted) return;
      perAgent.value = results;
      const errs: PerAgentErrors = {};
      for (const r of results) {
        if (r.error) errs[r.agent] = r.error;
      }
      perAgentErrors.value = errs;
      lastUpdated.value = Date.now();
      error.value = "";
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") return;
      error.value = e instanceof ApiError ? e.message : (e as Error).message;
    } finally {
      loading.value = false;
    }
  }

  const merged = computed<FamilyMap>(() =>
    mergeFamilies(perAgent.value.map((p) => p.families)),
  );

  onMounted(() => {
    void refresh();
    installTimer(intervalRef.value);
  });

  watch(intervalRef, (ms) => {
    installTimer(ms);
  });

  onUnmounted(() => {
    clearTimer();
    aborter?.abort();
  });

  return {
    perAgent,
    perAgentErrors,
    merged,
    error,
    loading,
    lastUpdated,
    refresh,
  };
}
