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
}

async function fetchText(url: string, signal: AbortSignal): Promise<string> {
  const resp = await fetch(url, { signal });
  if (!resp.ok) throw new ApiError(resp.status, `HTTP ${resp.status}`);
  return await resp.text();
}

export interface UseMetricsOptions {
  // Polling interval in milliseconds. Accepts a ref or plain number. A value
  // of 0 disables the auto-refresh timer entirely (manual refresh() still
  // works). Changes are observed: the timer is torn down and reinstalled
  // whenever the interval changes.
  intervalMs?: Ref<number> | number;
}

export function useMetrics(options: UseMetricsOptions = {}) {
  const perAgent = ref<AgentMetrics[]>([]);
  const error = ref<string>("");
  const loading = ref<boolean>(true);
  const lastUpdated = ref<number | null>(null);

  let timer: ReturnType<typeof setInterval> | null = null;
  let aborter: AbortController | null = null;

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
      const directory = await apiGet<TeamDirectoryEntry[]>("/team", { signal });
      const results = await Promise.all(
        directory.map(async (entry): Promise<AgentMetrics | null> => {
          try {
            const text = await fetchText(
              `/api/agents/${encodeURIComponent(entry.name)}/metrics`,
              signal,
            );
            return { agent: entry.name, families: parseProm(text) };
          } catch {
            return null;
          }
        }),
      );
      if (signal.aborted) return;
      perAgent.value = results.filter((r): r is AgentMetrics => r !== null);
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

  return { perAgent, merged, error, loading, lastUpdated, refresh };
}
