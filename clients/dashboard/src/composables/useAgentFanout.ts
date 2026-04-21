import { onMounted, onUnmounted, ref, unref, watch } from "vue";
import type { Ref } from "vue";
import { apiGet, ApiError } from "../api/client";
import { useTeam } from "./useTeam";
import { pollingShouldSkipTick } from "./usePollingControl";

// Generic per-agent fan-out + polling. Hits /api/agents/<name>/<endpoint> for
// every team member in parallel, tags each item with its source agent name,
// and merges into a single flat array. Polling cancels in-flight requests on
// unmount, keeping the dashboard's network profile tidy when the user
// navigates between views.
//
// Team directory (#1006): subscribe to the shared useTeam singleton rather
// than fetching /api/team per tick. A view mounting N fan-outs (e.g.
// AutomationView with 6) previously issued N × /api/team requests per
// interval — each fan-out had its own directory fetch, undoing #742. By
// reading the team list off the shared singleton's members ref, the
// dashboard issues exactly one /api/team per useTeam interval regardless
// of how many fan-outs are active.

interface TeamDirectoryEntry {
  name: string;
  url: string;
}

export interface AgentSourced {
  _agent: string;
}

// Aligned with useMetrics: map agentId -> error string for agents whose
// per-agent fetch failed during the most recent refresh. Successful agents
// are absent. Kept as a Record (not a Map) for easy template iteration.
export type PerAgentErrors = Record<string, string>;

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
  // #1538: Ref/computed that, when truthy, skips every poll tick (and
  // the mount-time refresh if already truthy at mount). Lets views
  // suspend the fanout during a stream drill-down without tearing the
  // composable instance down. Team-directory watcher still fires so
  // re-enabling reflects any agents that joined while paused.
  paused?: Ref<boolean> | (() => boolean);
}

function resolveQuery(q: QueryInput | undefined): QueryRecord | undefined {
  if (q === undefined) return undefined;
  if (typeof q === "function") return (q as () => QueryRecord)();
  return unref(q as QueryRecord | Ref<QueryRecord>);
}

interface FetchOneResult<T> {
  agent: string;
  items: (T & AgentSourced)[];
  error?: string;
}

export function useAgentFanout<T>(opts: UseAgentFanoutOptions) {
  const intervalMs = opts.intervalMs ?? 5000;
  const tolerateIndividualErrors = opts.tolerateIndividualErrors ?? true;

  // Share the team directory with every other dashboard consumer (#1006).
  // useTeam is a ref-counted module-level singleton (see #742); subscribing
  // here does NOT add a second /api/team poller — it reuses the existing
  // one (or starts it exactly once if we're the first mount).
  const team = useTeam({ intervalMs });

  const items = ref<(T & AgentSourced)[]>([]);
  const perAgentErrors = ref<PerAgentErrors>({});
  const error = ref<string>("");
  const loading = ref<boolean>(true);
  // Stamped at the end of each successful refresh (including "degraded"
  // refreshes where only some agents responded — matches useMetrics
  // semantics). A failed top-level refresh leaves the previous timestamp in
  // place so the UI signals staleness rather than lying about freshness.
  const lastUpdated = ref<number | null>(null);

  // Throttles per-agent console.warn to once per sustained outage per agent.
  const warnedAgents = new Set<string>();

  let timer: ReturnType<typeof setInterval> | null = null;
  let aborter: AbortController | null = null;

  async function fetchOne(
    member: TeamDirectoryEntry,
    signal: AbortSignal,
  ): Promise<FetchOneResult<T>> {
    try {
      const raw = await apiGet<T | T[]>(
        `/agents/${encodeURIComponent(member.name)}/${opts.endpoint}`,
        { signal, query: resolveQuery(opts.query) },
      );
      const arr = Array.isArray(raw) ? raw : [raw];
      warnedAgents.delete(member.name);
      return {
        agent: member.name,
        items: arr.map((item) => ({ ...(item as T), _agent: member.name })),
      };
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") throw e;
      if (!tolerateIndividualErrors) throw e;
      const message = e instanceof ApiError ? e.message : (e as Error).message;
      if (!warnedAgents.has(member.name)) {
        warnedAgents.add(member.name);
        console.warn(
          `[useAgentFanout] /${opts.endpoint} failed for agent "${member.name}": ${message}`,
        );
      }
      return { agent: member.name, items: [], error: message };
    }
  }

  async function refresh(): Promise<void> {
    aborter?.abort();
    aborter = new AbortController();
    // Snapshot the aborter identity so the catch/finally branches can
    // verify this cycle is still the current one (#892). Without this
    // guard a late non-AbortError rejection from an older cycle could
    // overwrite the newer cycle's error.value, and loading.value would
    // flip off mid-newer-cycle. Matches the useTeam pattern from #744.
    const localAborter = aborter;
    const signal = aborter.signal;
    try {
      // Directory comes from the shared useTeam singleton (#1006), which
      // polls /api/team exactly once per effective interval regardless of
      // subscriber count. Derive the thin {name,url} shape locally.
      const directory: TeamDirectoryEntry[] = team.members.value.map((m) => ({
        name: m.name,
        url: m.url,
      }));
      const perAgent = await Promise.all(
        directory.map((entry) => fetchOne(entry, signal)),
      );
      if (signal.aborted) {
        // #1542: an aborted cycle must still clear stale per-agent
        // errors for agents that recovered before the abort fired.
        // Without this, a transient outage entry in perAgentErrors from
        // a previous cycle sticks across every aborted successor cycle
        // and the degraded banner never clears for a recovered agent.
        // We don't touch items.value or lastUpdated (those remain
        // owned by the last fully-completed cycle), but we do narrow
        // the error map to what the partial fetch observed.
        const partialErrs: PerAgentErrors = { ...perAgentErrors.value };
        for (const r of perAgent) {
          if (r.error !== undefined) {
            partialErrs[r.agent] = r.error;
          } else {
            delete partialErrs[r.agent];
          }
        }
        perAgentErrors.value = partialErrs;
        return;
      }
      items.value = perAgent.flatMap((r) => r.items);
      const errs: PerAgentErrors = {};
      for (const r of perAgent) {
        if (r.error) errs[r.agent] = r.error;
      }
      perAgentErrors.value = errs;
      lastUpdated.value = Date.now();
      error.value = "";
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") return;
      // Stale cycle — silently drop. A newer cycle owns the UI state.
      if (aborter !== localAborter) return;
      error.value = e instanceof ApiError ? e.message : (e as Error).message;
    } finally {
      if (aborter === localAborter) loading.value = false;
    }
  }

  const isPaused = (): boolean => {
    const p = opts.paused;
    if (p === undefined) return false;
    if (typeof p === "function") return Boolean(p());
    return Boolean(unref(p));
  };

  onMounted(() => {
    // #1538: don't fire a mount-time refresh when we're already paused
    // — ConversationsView instantiates the fanout at mount but flips
    // paused=true as soon as streamMode engages via the agent+session
    // filters, so running the first refresh would waste a round trip.
    if (!isPaused()) void refresh();
    timer = setInterval(() => {
      // #1107: skip ticks when polling is paused or the tab is hidden.
      if (pollingShouldSkipTick()) return;
      // #1538: skip when the consumer has suspended fanout (e.g.
      // per-session stream drill-down is active).
      if (isPaused()) return;
      void refresh();
    }, intervalMs);
  });

  // Re-fetch when the shared team directory changes (agent added/removed).
  // Key off a stable scalar (joined names) so identical-content updates
  // don't trigger spurious re-fetches.
  watch(
    () => team.members.value.map((m) => m.name).join("\u0000"),
    () => {
      void refresh();
    },
  );

  // When a reactive query (ref/computed/getter) is supplied, re-fetch on
  // change so dropdown-driven params (e.g. limit) take effect immediately
  // rather than waiting for the next poll tick. Plain-object queries have no
  // reactive dependencies, so the watcher simply never fires.
  //
  // #1063: key off a stable scalar (JSON of sorted entries) rather than the
  // raw object. A computed-returned QueryRecord has new identity on every
  // access, which combined with `deep: true` caused the watcher to fire on
  // mount even when nothing had changed — each useAgentFanout instance then
  // double-fetched on mount (once from onMounted, once from the watcher).
  // AutomationView constructs six fan-outs so the effect compounded. A
  // stable string key only fires when the serialised query *contents*
  // differ, so mount emits exactly one refresh() per instance.
  if (opts.query !== undefined) {
    const queryKey = (): string => {
      const q = resolveQuery(opts.query) ?? {};
      const keys = Object.keys(q).sort();
      const pairs: string[] = [];
      for (const k of keys) {
        const v = q[k];
        if (v === undefined) continue;
        pairs.push(`${k}=${v}`);
      }
      return pairs.join("&");
    };
    watch(queryKey, () => void refresh());
  }

  onUnmounted(() => {
    if (timer !== null) clearInterval(timer);
    aborter?.abort();
  });

  return { items, perAgentErrors, error, loading, lastUpdated, refresh };
}
