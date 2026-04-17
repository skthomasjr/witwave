import { onMounted, onUnmounted, ref } from "vue";
import { apiGet, ApiError } from "../api/client";
import { parseProm, type Sample } from "../utils/prometheus";

// Fetches the metrics endpoint of each team member, parses the Prometheus
// text, and keeps a short in-memory history so the view can render small
// trend charts. The history window is bounded — this is a live dashboard,
// not a time-series DB. For real trend analysis point Grafana at /metrics.

const MAX_HISTORY = 60; // ~5 min at 5s cadence

interface TeamDirectoryEntry {
  name: string;
  url: string;
}

export interface MetricsSnapshot {
  _agent: string;
  ts: number;
  samples: Sample[];
}

async function fetchText(url: string, signal: AbortSignal): Promise<string> {
  const resp = await fetch(url, { signal });
  if (!resp.ok) throw new ApiError(resp.status, `HTTP ${resp.status}`);
  return await resp.text();
}

export function useMetrics() {
  const history = ref<MetricsSnapshot[]>([]);
  const error = ref<string>("");
  const loading = ref<boolean>(true);

  let timer: ReturnType<typeof setInterval> | null = null;
  let aborter: AbortController | null = null;

  async function refresh(): Promise<void> {
    aborter?.abort();
    aborter = new AbortController();
    const signal = aborter.signal;
    const ts = Date.now();
    try {
      const directory = await apiGet<TeamDirectoryEntry[]>("/team", { signal });
      const snaps = await Promise.all(
        directory.map(async (entry): Promise<MetricsSnapshot | null> => {
          try {
            const text = await fetchText(
              `/api/agents/${encodeURIComponent(entry.name)}/metrics`,
              signal,
            );
            return { _agent: entry.name, ts, samples: parseProm(text) };
          } catch {
            return null;
          }
        }),
      );
      if (signal.aborted) return;
      const fresh = snaps.filter((s): s is MetricsSnapshot => s !== null);
      history.value = [...history.value, ...fresh].slice(-MAX_HISTORY * directory.length);
      error.value = "";
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") return;
      error.value = e instanceof ApiError ? e.message : (e as Error).message;
    } finally {
      loading.value = false;
    }
  }

  onMounted(() => {
    void refresh();
    timer = setInterval(() => void refresh(), 5000);
  });

  onUnmounted(() => {
    if (timer !== null) clearInterval(timer);
    aborter?.abort();
  });

  return { history, error, loading, refresh };
}
