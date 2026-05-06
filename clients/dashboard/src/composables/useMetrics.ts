import { computed, onMounted, onUnmounted, ref, watch, type Ref } from "vue";
import { apiGet, ApiError } from "../api/client";
import { mergeFamilies, parseProm, type FamilyMap } from "../utils/prometheus";
import { pollingShouldSkipTick } from "./usePollingControl";

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

// Per-agent /metrics response size cap (#1065). A single agent with a
// histogram cardinality blowup can otherwise drag every dashboard tab
// into OOM/CPU soft-lock. 2 MiB is well above what a healthy backend
// emits (the claude backend's own /metrics is <<200 KiB at steady state)
// but comfortably below the soft-lock threshold in Chrome/Firefox. Kept
// at module scope so a future unit test can reference the limit.
const METRICS_MAX_BYTES = 2 * 1024 * 1024;

async function fetchText(url: string, signal: AbortSignal, timeoutMs?: number): Promise<string> {
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
    if (typeof AnyAbortSignal.any === "function" && typeof AnyAbortSignal.timeout === "function") {
      // #1540: AbortSignal.timeout() allocates an internal timer that
      // can't be cancelled from the outside, but the combined signal
      // returned by AbortSignal.any can be dropped from our retained
      // closures on cleanup so the merged chain is eligible for GC
      // once the fetch settles. Previously `cleanup` stayed a no-op
      // and the merged signal + its listener hookups were retained
      // until the AbortSignal.timeout's internal timer fired, bloating
      // heap on long-lived tabs doing many short fetches.
      const timeoutSignal = AnyAbortSignal.timeout(timeoutMs);
      effectiveSignal = AnyAbortSignal.any([signal, timeoutSignal]);
      cleanup = () => {
        // Null out our local reference so the combined signal and its
        // listener chain are GC-eligible as soon as fetch settles.
        // The timeout signal's internal timer will still fire on its
        // schedule (no public clear), but the reference graph rooted
        // here won't retain the merged signal past this call.
        effectiveSignal = signal;
      };
    } else {
      const controller = new AbortController();
      // If the outer signal is already aborted before we wire up the
      // listener, the listener never fires and the request proceeds as
      // if the caller hadn't cancelled. Synchronously propagate the
      // abort so callers that tear down the fan-out mid-tick actually
      // cancel the next fetch. (#1241)
      if (signal.aborted) {
        controller.abort();
        effectiveSignal = controller.signal;
      } else {
        const outerListener = () => controller.abort();
        signal.addEventListener("abort", outerListener);
        const timer = setTimeout(() => controller.abort(), timeoutMs);
        cleanup = () => {
          clearTimeout(timer);
          signal.removeEventListener("abort", outerListener);
        };
        effectiveSignal = controller.signal;
      }
      // #1313: the caller's catch handler uses `signal.aborted` (the
      // OUTER signal passed in here) to distinguish outer-cancel from
      // timeout-cancel. The timer aborts the INNER `controller` only,
      // so this distinction remains correct regardless of whether
      // AbortSignal.any was used above — documented for future edits.
    }
  }
  try {
    const resp = await fetch(url, { signal: effectiveSignal });
    if (!resp.ok) throw new ApiError(resp.status, `HTTP ${resp.status}`);
    // Byte-ceiling the response (#1065). Two-stage check: Content-Length
    // when the server provides one (fast reject), then streaming read
    // for chunked/un-typed responses. Overshooting the cap aborts the
    // body stream and surfaces an ApiError instead of silently
    // truncating — loud failure beats a corrupted parse.
    const cl = resp.headers?.get?.("Content-Length");
    if (cl != null) {
      const n = Number.parseInt(cl, 10);
      if (Number.isFinite(n) && n > METRICS_MAX_BYTES) {
        throw new ApiError(resp.status, `metrics response too large (${n} > ${METRICS_MAX_BYTES} bytes)`);
      }
    }
    // Stream so we can enforce the cap even when Content-Length is absent
    // or lies. Falls back to resp.text() on runtimes without body.getReader
    // (notably the vitest/jsdom mocks used in tests).
    const reader = resp.body?.getReader?.();
    if (!reader) {
      const txt = await resp.text();
      if (txt.length > METRICS_MAX_BYTES) {
        throw new ApiError(resp.status, `metrics response too large (>${METRICS_MAX_BYTES} bytes)`);
      }
      return txt;
    }
    const chunks: Uint8Array[] = [];
    let total = 0;
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      if (!value) continue;
      total += value.byteLength;
      if (total > METRICS_MAX_BYTES) {
        try {
          await reader.cancel();
        } catch {
          // ignore
        }
        throw new ApiError(resp.status, `metrics response too large (>${METRICS_MAX_BYTES} bytes)`);
      }
      chunks.push(value);
    }
    // Decode concatenated chunks as UTF-8.
    const merged = new Uint8Array(total);
    let offset = 0;
    for (const c of chunks) {
      merged.set(c, offset);
      offset += c.byteLength;
    }
    return new TextDecoder("utf-8").decode(merged);
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
  const intervalRef: Ref<number> = typeof intervalSource === "number" ? ref(intervalSource) : intervalSource;

  function clearTimer(): void {
    if (timer !== null) {
      clearInterval(timer);
      timer = null;
    }
  }

  function installTimer(ms: number): void {
    clearTimer();
    if (ms > 0) {
      timer = setInterval(() => {
        // #1107: skip ticks while paused or tab hidden. Keeps the timer
        // running so resume is immediate on next tick.
        if (pollingShouldSkipTick()) return;
        void refresh();
      }, ms);
    }
  }

  async function refresh(): Promise<void> {
    aborter?.abort();
    const localAborter = new AbortController();
    aborter = localAborter;
    const signal = localAborter.signal;
    try {
      const directory = await apiGet<TeamDirectoryEntry[]>("/team", {
        signal,
        timeoutMs: directoryTimeoutMs,
      });
      // #1002: Each per-agent fetch settles independently. Previously an
      // AbortError from one member's per-request timeout was rethrown and
      // bubbled up through Promise.all, cancelling the entire refresh and
      // discarding every successful sibling. A slow member now produces a
      // row with `error` set, while successful siblings still render.
      // Only a true outer-signal abort (caller-triggered, e.g. unmount or
      // a superseding refresh) short-circuits the cycle — see the
      // `signal.aborted` check after the fan-out.
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
            const isAbort = (e as { name?: string }).name === "AbortError";
            // Only a caller-driven outer abort should propagate. A
            // per-member timeout (merged via AbortSignal.any) also
            // surfaces as AbortError but the outer signal is still live,
            // so treat it as a fetch failure for this row only.
            if (isAbort && signal.aborted) throw e;
            const message = isAbort
              ? `timeout after ${memberTimeoutMs}ms`
              : e instanceof ApiError
                ? e.message
                : (e as Error).message;
            // Throttled: warn once per agent per outage, not every poll tick.
            if (!warnedAgents.has(entry.name)) {
              warnedAgents.add(entry.name);
              console.warn(`[useMetrics] /metrics failed for agent "${entry.name}": ${message}`);
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
      // Identity check (#744): stale aborter → silently drop the
      // rejection so a burst of refreshes doesn't flip the error
      // banner or degrade-badge state on the still-running cycle.
      if (aborter !== localAborter || (e as { name?: string }).name === "AbortError") {
        return;
      }
      error.value = e instanceof ApiError ? e.message : (e as Error).message;
    } finally {
      if (aborter === localAborter) loading.value = false;
    }
  }

  const merged = computed<FamilyMap>(() => mergeFamilies(perAgent.value.map((p) => p.families)));

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
