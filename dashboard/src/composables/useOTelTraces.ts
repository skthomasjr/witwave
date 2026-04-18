import { computed, onMounted, onUnmounted, ref } from "vue";
import type { ComputedRef } from "vue";

// Thin Jaeger-HTTP-API client for the dashboard OTel trace viewer (#632).
//
// Targets Jaeger's /api/traces endpoints, which Tempo also implements for
// the list + get-by-id shapes we rely on here. The trace backend URL is not
// the dashboard's /api/* path — it's a fully separate URL the operator
// points at (e.g. http://nyx-jaeger-query.observability:16686). Operators
// typically front that URL with their own auth proxy; we intentionally do
// not send credentials from this client. See dashboard/README.md.
//
// Resolution order for the base URL:
//   1. window.__NYX_CONFIG__.traceApiUrl   (runtime override, nginx-injected)
//   2. import.meta.env.VITE_TRACE_API_URL  (build-time)
// Returns null if neither is set so the view can render a "not configured"
// empty state instead of blasting out 404s.

export interface JaegerSpanRef {
  refType: string;
  traceID: string;
  spanID: string;
}

export interface JaegerTagOrLog {
  key: string;
  type?: string;
  value: unknown;
}

export interface JaegerSpan {
  traceID: string;
  spanID: string;
  operationName: string;
  references?: JaegerSpanRef[];
  startTime: number; // microseconds since epoch
  duration: number; // microseconds
  tags?: JaegerTagOrLog[];
  logs?: unknown[];
  processID: string;
  warnings?: unknown;
}

export interface JaegerProcess {
  serviceName: string;
  tags?: JaegerTagOrLog[];
}

export interface JaegerTrace {
  traceID: string;
  spans: JaegerSpan[];
  processes: Record<string, JaegerProcess>;
  warnings?: unknown;
}

export interface JaegerResponse<T> {
  data: T;
  total?: number;
  limit?: number;
  offset?: number;
  errors?: unknown;
}

export interface TraceListRow {
  traceID: string;
  // Derived from the minimum span start time in the trace.
  startTime: number; // microseconds since epoch
  // Derived from the root span duration (or trace end − start if no root).
  duration: number; // microseconds
  spanCount: number;
  // A best-effort service label for the row (the service of the root span,
  // or the first process when no root is present).
  rootService: string;
  rootOperation: string;
}

interface NyxRuntimeConfig {
  traceApiUrl?: string;
}

// Validate a candidate trace backend URL via `new URL()` and a protocol
// allow-list (#740). A mis-set runtime config that pointed at, say,
// `javascript:` or `file:` would otherwise be sent to `fetch()` and leak
// traceIds/service names (or worse, be coerced by a credulous browser).
// Returns the trimmed URL string when safe, null when not. Kept exported
// at module scope so a future unit test can cover it directly.
function validateTraceBaseUrl(raw: string | undefined | null): string | null {
  if (typeof raw !== "string" || raw.length === 0) return null;
  const trimmed = raw.replace(/\/+$/, "");
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    // eslint-disable-next-line no-console
    console.warn(
      "[nyx] traceApiUrl rejected: not a valid absolute URL:",
      trimmed,
    );
    return null;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    // eslint-disable-next-line no-console
    console.warn(
      `[nyx] traceApiUrl rejected: protocol ${parsed.protocol} not in {http:, https:}`,
      trimmed,
    );
    return null;
  }
  return trimmed;
}

function resolveBaseUrl(): string | null {
  // Runtime override wins so operators can change the backend without a
  // rebuild. Kept optional: when nginx hasn't injected anything we fall
  // through to the build-time env, and then to null — which triggers
  // the in-cluster fallback below.
  const runtime = (window as unknown as { __NYX_CONFIG__?: NyxRuntimeConfig })
    .__NYX_CONFIG__;
  if (runtime && typeof runtime.traceApiUrl === "string" && runtime.traceApiUrl) {
    const safe = validateTraceBaseUrl(runtime.traceApiUrl);
    if (safe !== null) return safe;
    // Fall through to build-time when the runtime value is rejected.
  }
  const env = (import.meta as unknown as { env?: Record<string, string | undefined> })
    .env;
  const build = env?.VITE_TRACE_API_URL;
  if (build && build.length > 0) {
    const safe = validateTraceBaseUrl(build);
    if (safe !== null) return safe;
  }
  return null;
}

// ── In-cluster source (#otel-in-cluster) ────────────────────────────────
//
// When neither the runtime nor build-time trace URL is set, fall back
// to the harness's own in-memory span store at
// /api/agents/<name>/api/traces. The harness's `shared/otel.py`
// captures every span the OTLP exporter would send and serves them in
// the Jaeger v1 query shape this composable already consumes.
//
// Fans out across every agent in the team directory so a cluster-wide
// view merges harness + backend spans regardless of which pod started
// the trace. Identical trace_ids from different agents are merged so
// cross-pod distributed traces render as one.

interface TeamDirectoryEntry {
  name: string;
  url: string;
}

async function fetchTeam(signal: AbortSignal): Promise<TeamDirectoryEntry[]> {
  const resp = await fetch("/api/team", { signal });
  if (!resp.ok) return [];
  try {
    return (await resp.json()) as TeamDirectoryEntry[];
  } catch {
    return [];
  }
}

async function inClusterFetchList(
  path: string,
  signal: AbortSignal,
  timeoutMs: number,
): Promise<JaegerResponse<JaegerTrace[]>> {
  const team = await fetchTeam(signal);
  if (!team.length) return { data: [], total: 0 };
  // Extract the caller's ?limit= from the path so each per-agent
  // result can be capped to the newest K traces before returning to
  // the merge step (#746). Without the cap, a deep retention buffer
  // on one agent dominates the merged result and every poll copies
  // every span across the wire.
  // Also parsed into `totalLimit` (#895) so the merged list is
  // re-sliced to the caller's requested limit — previously 3 agents
  // with limit=10 each returned up to 30 rows in the outer list.
  let perAgentLimit = 500;
  let totalLimit: number | null = null;
  try {
    const qIdx = path.indexOf("?");
    if (qIdx >= 0) {
      const params = new URLSearchParams(path.slice(qIdx + 1));
      const l = params.get("limit");
      if (l !== null) {
        const n = Number.parseInt(l, 10);
        if (Number.isFinite(n) && n > 0) {
          perAgentLimit = n;
          totalLimit = n;
        }
      }
    }
  } catch {
    // keep default cap
  }
  // Honour timeoutMs per-agent so a single stalled pod does not hang the
  // whole fan-out. AbortSignal.any combines the caller's abort with a
  // per-call AbortSignal.timeout (#680).
  const perAgent = await Promise.all(
    team.map(async (m) => {
      const perCallSignal =
        typeof (AbortSignal as { any?: unknown }).any === "function"
          ? (AbortSignal as unknown as {
              any: (signals: AbortSignal[]) => AbortSignal;
            }).any([signal, AbortSignal.timeout(timeoutMs)])
          : signal;
      try {
        const r = await fetch(
          `/api/agents/${encodeURIComponent(m.name)}${path}`,
          { signal: perCallSignal },
        );
        if (!r.ok) return [] as JaegerTrace[];
        const body = (await r.json()) as JaegerResponse<JaegerTrace[]>;
        const traces = body.data ?? [];
        // Cap per-agent to newest K (#746). Uses the earliest span
        // start time as the trace start so the cap is consistent
        // with the final sort in refreshList.
        if (traces.length > perAgentLimit) {
          traces.sort((a, b) => {
            const sa = Math.min(...a.spans.map((s) => s.startTime ?? 0));
            const sb = Math.min(...b.spans.map((s) => s.startTime ?? 0));
            return sb - sa;
          });
          return traces.slice(0, perAgentLimit);
        }
        return traces;
      } catch {
        return [] as JaegerTrace[];
      }
    }),
  );
  // Merge traces that share a traceID (cross-pod distributed traces).
  const byTid = new Map<string, JaegerTrace>();
  for (const list of perAgent) {
    for (const t of list) {
      const existing = byTid.get(t.traceID);
      if (!existing) {
        byTid.set(t.traceID, { ...t });
      } else {
        // Combine span lists; dedupe by spanID so if two agents
        // happen to carry the same span (rare) we keep one.
        const seen = new Set(existing.spans.map((s) => s.spanID));
        for (const s of t.spans) if (!seen.has(s.spanID)) existing.spans.push(s);
        existing.processes = { ...(existing.processes ?? {}), ...(t.processes ?? {}) };
      }
    }
  }
  let merged = [...byTid.values()];
  // Re-slice the merged list to the caller's requested limit (#895).
  // Sort by earliest span start time (newest first) to keep the cap
  // consistent with the per-agent cap above and with refreshList's
  // final sort order.
  if (totalLimit !== null && merged.length > totalLimit) {
    merged.sort((a, b) => {
      const sa = Math.min(...a.spans.map((s) => s.startTime ?? 0));
      const sb = Math.min(...b.spans.map((s) => s.startTime ?? 0));
      return sb - sa;
    });
    merged = merged.slice(0, totalLimit);
  }
  return { data: merged, total: merged.length };
}

async function inClusterFetchDetail(
  traceId: string,
  signal: AbortSignal,
  timeoutMs: number,
): Promise<JaegerResponse<JaegerTrace[]>> {
  // First try per-agent GET /api/agents/<name>/api/traces/<traceId>: the
  // harness in-memory store serves a single trace by ID even when it has
  // fallen out of the ring-buffer list view (#681). Only if no agent
  // returns it do we fall back to the broad list-scan below.
  const team = await fetchTeam(signal);
  if (team.length) {
    const byTid = new Map<string, JaegerTrace>();
    const perAgent = await Promise.all(
      team.map(async (m) => {
        const perCallSignal =
          typeof (AbortSignal as { any?: unknown }).any === "function"
            ? (AbortSignal as unknown as {
                any: (signals: AbortSignal[]) => AbortSignal;
              }).any([signal, AbortSignal.timeout(timeoutMs)])
            : signal;
        try {
          const r = await fetch(
            `/api/agents/${encodeURIComponent(m.name)}/api/traces/${encodeURIComponent(traceId)}`,
            { signal: perCallSignal },
          );
          if (!r.ok) return [] as JaegerTrace[];
          const body = (await r.json()) as JaegerResponse<JaegerTrace[]>;
          return body.data ?? [];
        } catch {
          return [] as JaegerTrace[];
        }
      }),
    );
    for (const list of perAgent) {
      for (const t of list) {
        if (t.traceID !== traceId) continue;
        const existing = byTid.get(t.traceID);
        if (!existing) {
          byTid.set(t.traceID, { ...t });
        } else {
          const seen = new Set(existing.spans.map((s) => s.spanID));
          for (const s of t.spans) if (!seen.has(s.spanID)) existing.spans.push(s);
          existing.processes = {
            ...(existing.processes ?? {}),
            ...(t.processes ?? {}),
          };
        }
      }
    }
    if (byTid.size > 0) {
      return { data: [...byTid.values()], total: byTid.size };
    }
    // #952: When we DID reach every known agent and none returned the
    // trace, the ring buffers have evicted it — scanning a 500-item list
    // per agent cannot recover it and just burns megabytes of payload
    // and harness CPU on a single user click. Surface "trace not found"
    // directly instead of falling through to the list-scan.
    return { data: [], total: 0 };
  }

  // Fallback: the list-scan. Only reached when we have no team roster
  // to fan the per-ID probe across (older implementations, first-paint
  // before /api/team resolves, or browsers without AbortSignal.any).
  // #952: Cap the fallback at 50 rather than 500 so a single click on a
  // stale URL cannot amplify into a multi-megabyte harness fan-out.
  const full = await inClusterFetchList(
    `/api/traces?limit=50`,
    signal,
    timeoutMs,
  );
  const match = (full.data ?? []).find((t) => t.traceID === traceId);
  return match ? { data: [match], total: 1 } : { data: [], total: 0 };
}

async function jaegerFetch<T>(
  baseUrl: string,
  path: string,
  signal: AbortSignal,
  timeoutMs: number,
): Promise<T> {
  // Combine caller abort + a local timeout. jsdom in vitest lacks
  // AbortSignal.any, so wire the timer manually.
  const ctrl = new AbortController();
  const onAbort = () => ctrl.abort((signal as AbortSignal).reason);
  if (signal.aborted) onAbort();
  else signal.addEventListener("abort", onAbort, { once: true });
  const timer = setTimeout(
    () => ctrl.abort(new DOMException("timeout", "TimeoutError")),
    timeoutMs,
  );
  try {
    const resp = await fetch(`${baseUrl}${path}`, { signal: ctrl.signal });
    if (!resp.ok) {
      throw new Error(`trace backend HTTP ${resp.status}`);
    }
    return (await resp.json()) as T;
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", onAbort);
  }
}

function summariseTrace(trace: JaegerTrace): TraceListRow {
  // Root span = span whose references don't include a CHILD_OF/FOLLOWS_FROM
  // to another span in the same trace. For flame/waterfall purposes we
  // want a single representative row per trace.
  const ids = new Set(trace.spans.map((s) => s.spanID));
  let root: JaegerSpan | undefined;
  let minStart = Number.POSITIVE_INFINITY;
  for (const s of trace.spans) {
    if (s.startTime < minStart) minStart = s.startTime;
    const parented = (s.references ?? []).some(
      (r) => ids.has(r.spanID) && r.traceID === trace.traceID,
    );
    if (!parented && !root) root = s;
  }
  const rootStart = root?.startTime ?? minStart;
  const rootDuration = root?.duration ?? 0;
  const processes = trace.processes ?? {};
  const rootService = root
    ? processes[root.processID]?.serviceName ?? "unknown"
    : Object.values(processes)[0]?.serviceName ?? "unknown";
  const rootOperation = root?.operationName ?? trace.spans[0]?.operationName ?? "";
  return {
    traceID: trace.traceID,
    startTime: rootStart,
    duration: rootDuration,
    spanCount: trace.spans.length,
    rootService,
    rootOperation,
  };
}

export interface UseOTelTracesOptions {
  // Optional service filter applied to the list query. When unset, Jaeger
  // typically requires a service; we still issue the request and let the
  // backend return whatever default it wants to.
  service?: string;
  // Initial list size (Jaeger's `limit` query param).
  limit?: number;
  // Refresh poll for the list. Detail loads are on-demand only.
  intervalMs?: number;
  timeoutMs?: number;
}

export function useOTelTraces(opts: UseOTelTracesOptions = {}) {
  const intervalMs = opts.intervalMs ?? 15000;
  const timeoutMs = opts.timeoutMs ?? 8000;
  const limit = ref<number>(opts.limit ?? 20);
  const service = ref<string>(opts.service ?? "");

  const baseUrl = resolveBaseUrl();
  // Configured when either an external Jaeger/Tempo URL is set OR the
  // in-cluster in-memory span store is available.
  const inClusterMode = baseUrl === null;
  // "configured" reports whether we have at least one trace source to
  // query. The previous expression `baseUrl !== null || inClusterMode`
  // was a tautology (#894): inClusterMode IS `baseUrl === null`, so
  // the disjunction was always true and any future consumer that
  // tried to use it as a polling guard would poll forever with no
  // source available.
  //
  // External URL path: configured iff resolveBaseUrl() succeeded, which
  // already runs validateTraceBaseUrl() (protocol + parse checks).
  // In-cluster path: configured only once the /api/team fan-out has
  // yielded at least one agent entry — otherwise the fallback branch
  // has nothing to query. The probe is recomputed on team refresh so
  // the flag goes true the first time a backend becomes reachable.
  const inClusterReachable = ref<boolean>(false);
  const configured: ComputedRef<boolean> = computed(
    () => baseUrl !== null || inClusterReachable.value,
  );
  // Probe: fetch /api/team; a non-empty response means the dashboard
  // can fan out to at least one agent's /api/traces endpoint. Errors
  // leave configured=false so consumers render a "not configured" empty
  // state instead of polling into the void.
  //
  // #1003: Previously this was one-shot — a cold-start glitch where the
  // dashboard loaded before pods became ready would pin
  // inClusterReachable=false forever. Now:
  //  (a) the probe's AbortController is bound to the component lifecycle
  //      (aborted in onUnmounted below) so it can't leak,
  //  (b) the probe auto-retries at PROBE_RETRY_MS after a failure until
  //      it succeeds once,
  //  (c) refreshList() flips inClusterReachable=true on the first
  //      successful list fetch in inClusterMode, so even without the
  //      probe the configured flag recovers,
  //  (d) retryProbe() is exposed for explicit user-driven retry.
  const PROBE_RETRY_MS = 60_000;
  let probeAborter: AbortController | null = null;
  let probeRetryTimer: ReturnType<typeof setTimeout> | null = null;

  async function runProbe(): Promise<void> {
    if (!inClusterMode) return;
    if (inClusterReachable.value) return;
    probeAborter?.abort();
    const localAborter = new AbortController();
    probeAborter = localAborter;
    try {
      const team = await fetchTeam(localAborter.signal);
      if (probeAborter !== localAborter) return;
      if (team.length > 0) {
        inClusterReachable.value = true;
        return;
      }
    } catch {
      // Fall through to schedule retry.
    }
    if (probeAborter !== localAborter) return;
    if (inClusterReachable.value) return;
    if (probeRetryTimer !== null) clearTimeout(probeRetryTimer);
    probeRetryTimer = setTimeout(() => {
      probeRetryTimer = null;
      void runProbe();
    }, PROBE_RETRY_MS);
  }

  function retryProbe(): void {
    // User-invoked: cancel any pending backoff and re-probe immediately.
    if (probeRetryTimer !== null) {
      clearTimeout(probeRetryTimer);
      probeRetryTimer = null;
    }
    void runProbe();
  }

  void runProbe();

  const list = ref<TraceListRow[]>([]);
  const listError = ref<string>("");
  const listLoading = ref<boolean>(false);
  const detail = ref<JaegerTrace | null>(null);
  const detailError = ref<string>("");
  const detailLoading = ref<boolean>(false);

  let timer: ReturnType<typeof setInterval> | null = null;
  let listAborter: AbortController | null = null;
  let detailAborter: AbortController | null = null;

  async function refreshList(): Promise<void> {
    listAborter?.abort();
    listAborter = new AbortController();
    // Snapshot aborter identity (#893): a late non-AbortError rejection
    // from a stale cycle must not overwrite a newer cycle's listError
    // or flip listLoading off while the newer cycle is still running.
    const localAborter = listAborter;
    const signal = listAborter.signal;
    listLoading.value = true;
    try {
      const params = new URLSearchParams();
      params.set("limit", String(limit.value));
      if (service.value) params.set("service", service.value);
      const resp = inClusterMode
        ? await inClusterFetchList(
            `/api/traces?${params.toString()}`,
            signal,
            timeoutMs,
          )
        : await jaegerFetch<JaegerResponse<JaegerTrace[]>>(
            baseUrl as string,
            `/api/traces?${params.toString()}`,
            signal,
            timeoutMs,
          );
      if (signal.aborted) return;
      if (listAborter !== localAborter) return;
      const rows = (resp.data ?? [])
        .map(summariseTrace)
        // Newest-first by start time — matches the other dashboard lists.
        .sort((a, b) => b.startTime - a.startTime);
      list.value = rows;
      listError.value = "";
      // #1003: recover from a stuck-false probe. If the list fetch
      // succeeded (even with empty data) in inClusterMode, the fan-out
      // is evidently reachable — flip configured=true so the UI stops
      // rendering the "not configured" empty state.
      if (inClusterMode && !inClusterReachable.value) {
        inClusterReachable.value = true;
      }
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") return;
      if (listAborter !== localAborter) return;
      listError.value = (e as Error).message || "trace list failed";
    } finally {
      if (listAborter === localAborter) listLoading.value = false;
    }
  }

  async function loadDetail(traceId: string): Promise<void> {
    detailAborter?.abort();
    detailAborter = new AbortController();
    // Snapshot aborter identity (#893): stale detail cycle must not
    // overwrite the newer cycle's detail/detailError/detailLoading.
    const localAborter = detailAborter;
    const signal = detailAborter.signal;
    detailLoading.value = true;
    detailError.value = "";
    try {
      const resp = inClusterMode
        ? await inClusterFetchDetail(traceId, signal, timeoutMs)
        : await jaegerFetch<JaegerResponse<JaegerTrace[]>>(
            baseUrl as string,
            `/api/traces/${encodeURIComponent(traceId)}`,
            signal,
            timeoutMs,
          );
      if (signal.aborted) return;
      if (detailAborter !== localAborter) return;
      detail.value = resp.data?.[0] ?? null;
      if (!detail.value) detailError.value = `trace ${traceId} not found`;
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") return;
      if (detailAborter !== localAborter) return;
      detailError.value = (e as Error).message || "trace detail failed";
      detail.value = null;
    } finally {
      if (detailAborter === localAborter) detailLoading.value = false;
    }
  }

  function clearDetail(): void {
    detailAborter?.abort();
    detail.value = null;
    detailError.value = "";
    detailLoading.value = false;
  }

  onMounted(() => {
    void refreshList();
    timer = setInterval(() => void refreshList(), intervalMs);
  });

  onUnmounted(() => {
    if (timer !== null) clearInterval(timer);
    listAborter?.abort();
    detailAborter?.abort();
    // #1003: probe controller + retry backoff must also be torn down
    // so neither a late-resolving fetchTeam() nor a pending timeout
    // touches refs after the component unmounts.
    probeAborter?.abort();
    if (probeRetryTimer !== null) {
      clearTimeout(probeRetryTimer);
      probeRetryTimer = null;
    }
  });

  return {
    configured,
    baseUrl,
    inClusterMode,
    limit,
    service,
    list,
    listError,
    listLoading,
    detail,
    detailError,
    detailLoading,
    refreshList,
    loadDetail,
    clearDetail,
    retryProbe,
  };
}

// Helpers exported for the view.
export function formatMicros(micros: number): string {
  if (!Number.isFinite(micros) || micros <= 0) return "0 ms";
  const ms = micros / 1000;
  if (ms < 1) return `${micros} µs`;
  if (ms < 1000) return `${ms.toFixed(1)} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

export function formatStart(micros: number): string {
  if (!Number.isFinite(micros) || micros <= 0) return "";
  const d = new Date(micros / 1000);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString();
}

// Build a parent/child tree for the detail view. Jaeger's span graph is
// expressed via references[]; we treat the first CHILD_OF reference as the
// parent edge and fall back to FOLLOWS_FROM. Roots are spans with no
// matching parent in the trace.
export interface SpanNode {
  span: JaegerSpan;
  service: string;
  depth: number;
  children: SpanNode[];
  // Offset from the trace start, in microseconds, for the timeline bar.
  offsetMicros: number;
}

export function buildSpanTree(trace: JaegerTrace): {
  roots: SpanNode[];
  traceStart: number;
  traceEnd: number;
} {
  const byId = new Map<string, JaegerSpan>();
  for (const s of trace.spans) byId.set(s.spanID, s);
  const childrenOf = new Map<string, JaegerSpan[]>();
  const rootSpans: JaegerSpan[] = [];
  for (const s of trace.spans) {
    const parentRef = (s.references ?? []).find(
      (r) => byId.has(r.spanID) && r.traceID === trace.traceID,
    );
    if (!parentRef) {
      rootSpans.push(s);
      continue;
    }
    const arr = childrenOf.get(parentRef.spanID) ?? [];
    arr.push(s);
    childrenOf.set(parentRef.spanID, arr);
  }
  let traceStart = Number.POSITIVE_INFINITY;
  let traceEnd = 0;
  for (const s of trace.spans) {
    if (s.startTime < traceStart) traceStart = s.startTime;
    const end = s.startTime + s.duration;
    if (end > traceEnd) traceEnd = end;
  }
  if (!Number.isFinite(traceStart)) traceStart = 0;

  function build(span: JaegerSpan, depth: number): SpanNode {
    const kids = (childrenOf.get(span.spanID) ?? [])
      .slice()
      .sort((a, b) => a.startTime - b.startTime)
      .map((c) => build(c, depth + 1));
    return {
      span,
      service: trace.processes[span.processID]?.serviceName ?? "unknown",
      depth,
      children: kids,
      offsetMicros: span.startTime - traceStart,
    };
  }

  const roots = rootSpans
    .sort((a, b) => a.startTime - b.startTime)
    .map((s) => build(s, 0));
  return { roots, traceStart, traceEnd };
}

// Flatten the tree into a depth-first list suitable for a single <table> or
// flex-column render, preserving parent ordering.
export function flattenSpanTree(roots: SpanNode[]): SpanNode[] {
  const out: SpanNode[] = [];
  function walk(n: SpanNode): void {
    out.push(n);
    for (const c of n.children) walk(c);
  }
  for (const r of roots) walk(r);
  return out;
}

// Pull the subset of OTel attributes we want to surface inline on a span
// row. The rest are available in the raw tag list; rendering every tag
// would drown the UI. Keys match #469 / #630 / #637 conventions.
const HIGHLIGHT_TAG_KEYS = [
  "agent",
  "agent.name",
  "backend",
  "tool.name",
  "mcp.server",
  "model",
  "span.kind",
  "otel.status_code",
  "http.status_code",
];

export interface SpanHighlight {
  key: string;
  value: string;
}

export function highlightsForSpan(span: JaegerSpan): SpanHighlight[] {
  const tags = span.tags ?? [];
  const found: SpanHighlight[] = [];
  for (const k of HIGHLIGHT_TAG_KEYS) {
    const tag = tags.find((t) => t.key === k);
    if (tag === undefined) continue;
    const v = tag.value;
    if (v === undefined || v === null || v === "") continue;
    found.push({ key: k, value: String(v) });
  }
  return found;
}

export function statusForSpan(span: JaegerSpan): "ok" | "error" | "unset" {
  const tags = span.tags ?? [];
  const errTag = tags.find((t) => t.key === "error");
  if (errTag && (errTag.value === true || errTag.value === "true")) return "error";
  const statusTag = tags.find((t) => t.key === "otel.status_code");
  if (statusTag) {
    const v = String(statusTag.value).toUpperCase();
    if (v === "ERROR") return "error";
    if (v === "OK") return "ok";
  }
  return "unset";
}
