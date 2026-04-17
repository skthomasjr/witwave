import { onMounted, onUnmounted, ref, unref, watch } from "vue";
import type { Ref } from "vue";
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

type QueryRecord = Record<string, string | undefined>;

// Accept a plain record, a ref/computed of one, or a zero-arg getter so
// consumers can make the query reactive (dropdown-driven limits, etc.).
// Passing a plain object keeps the previous call-site shape working.
export type QueryInput = QueryRecord | Ref<QueryRecord> | (() => QueryRecord);

export interface UseAgentFanoutOptions {
  endpoint: string;
  intervalMs?: number;
  query?: QueryInput;
  // When true, individual agent failures do not set the overall error —
  // items from reachable agents still render. Default true for list views.
  tolerateIndividualErrors?: boolean;
}

function resolveQuery(q: QueryInput | undefined): QueryRecord | undefined {
  if (q === undefined) return undefined;
  if (typeof q === "function") return (q as () => QueryRecord)();
  return unref(q as QueryRecord | Ref<QueryRecord>);
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
        { signal, query: resolveQuery(opts.query) },
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

  // When a reactive query (ref/computed/getter) is supplied, re-fetch on
  // change so dropdown-driven params (e.g. limit) take effect immediately
  // rather than waiting for the next poll tick. Plain-object queries have no
  // reactive dependencies, so the watcher simply never fires.
  if (opts.query !== undefined) {
    watch(
      () => resolveQuery(opts.query),
      () => void refresh(),
      { deep: true },
    );
  }

  onUnmounted(() => {
    if (timer !== null) clearInterval(timer);
    aborter?.abort();
  });

  return { items, error, loading, refresh };
}
