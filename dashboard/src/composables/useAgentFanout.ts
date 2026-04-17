import { onMounted, onUnmounted, ref } from "vue";
import { apiGet, ApiError } from "../api/client";

// Generic per-agent fan-out + polling. Hits /api/agents/<name>/<endpoint> for
// every team member in parallel, tags each item with its source agent name,
// and merges into a single flat array. Polling cancels in-flight requests on
// unmount, keeping the dashboard's network profile tidy when the user
// navigates between views.

interface TeamDirectoryEntry {
  name: string;
  url: string;
}

export interface AgentSourced {
  _agent: string;
}

export interface UseAgentFanoutOptions {
  endpoint: string;
  intervalMs?: number;
  query?: Record<string, string | undefined>;
  // When true, individual agent failures do not set the overall error —
  // items from reachable agents still render. Default true for list views.
  tolerateIndividualErrors?: boolean;
}

export function useAgentFanout<T>(opts: UseAgentFanoutOptions) {
  const intervalMs = opts.intervalMs ?? 5000;
  const tolerateIndividualErrors = opts.tolerateIndividualErrors ?? true;

  const items = ref<(T & AgentSourced)[]>([]);
  const error = ref<string>("");
  const loading = ref<boolean>(true);

  let timer: ReturnType<typeof setInterval> | null = null;
  let aborter: AbortController | null = null;

  async function fetchOne(
    member: TeamDirectoryEntry,
    signal: AbortSignal,
  ): Promise<(T & AgentSourced)[]> {
    try {
      const raw = await apiGet<T | T[]>(
        `/agents/${encodeURIComponent(member.name)}/${opts.endpoint}`,
        { signal, query: opts.query },
      );
      const arr = Array.isArray(raw) ? raw : [raw];
      return arr.map((item) => ({ ...(item as T), _agent: member.name }));
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") throw e;
      if (!tolerateIndividualErrors) throw e;
      return [];
    }
  }

  async function refresh(): Promise<void> {
    aborter?.abort();
    aborter = new AbortController();
    const signal = aborter.signal;
    try {
      const directory = await apiGet<TeamDirectoryEntry[]>("/team", { signal });
      const perAgent = await Promise.all(
        directory.map((entry) => fetchOne(entry, signal)),
      );
      if (signal.aborted) return;
      items.value = perAgent.flat();
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
    timer = setInterval(() => void refresh(), intervalMs);
  });

  onUnmounted(() => {
    if (timer !== null) clearInterval(timer);
    aborter?.abort();
  });

  return { items, error, loading, refresh };
}
