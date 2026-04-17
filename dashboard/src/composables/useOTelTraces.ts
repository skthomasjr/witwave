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

function resolveBaseUrl(): string | null {
  // Runtime override wins so operators can change the backend without a
  // rebuild. Kept optional: when nginx hasn't injected anything we fall
  // through to the build-time env.
  const runtime = (window as unknown as { __NYX_CONFIG__?: NyxRuntimeConfig })
    .__NYX_CONFIG__;
  if (runtime && typeof runtime.traceApiUrl === "string" && runtime.traceApiUrl) {
    return runtime.traceApiUrl.replace(/\/+$/, "");
  }
  const env = (import.meta as unknown as { env?: Record<string, string | undefined> })
    .env;
  const build = env?.VITE_TRACE_API_URL;
  if (build && build.length > 0) return build.replace(/\/+$/, "");
  return null;
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
  const configured: ComputedRef<boolean> = computed(() => baseUrl !== null);

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
    if (!baseUrl) return;
    listAborter?.abort();
    listAborter = new AbortController();
    const signal = listAborter.signal;
    listLoading.value = true;
    try {
      const params = new URLSearchParams();
      params.set("limit", String(limit.value));
      if (service.value) params.set("service", service.value);
      const resp = await jaegerFetch<JaegerResponse<JaegerTrace[]>>(
        baseUrl,
        `/api/traces?${params.toString()}`,
        signal,
        timeoutMs,
      );
      if (signal.aborted) return;
      const rows = (resp.data ?? [])
        .map(summariseTrace)
        // Newest-first by start time — matches the other dashboard lists.
        .sort((a, b) => b.startTime - a.startTime);
      list.value = rows;
      listError.value = "";
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") return;
      listError.value = (e as Error).message || "trace list failed";
    } finally {
      listLoading.value = false;
    }
  }

  async function loadDetail(traceId: string): Promise<void> {
    if (!baseUrl) {
      detailError.value = "tracing not configured";
      return;
    }
    detailAborter?.abort();
    detailAborter = new AbortController();
    const signal = detailAborter.signal;
    detailLoading.value = true;
    detailError.value = "";
    try {
      const resp = await jaegerFetch<JaegerResponse<JaegerTrace[]>>(
        baseUrl,
        `/api/traces/${encodeURIComponent(traceId)}`,
        signal,
        timeoutMs,
      );
      if (signal.aborted) return;
      detail.value = resp.data?.[0] ?? null;
      if (!detail.value) detailError.value = `trace ${traceId} not found`;
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") return;
      detailError.value = (e as Error).message || "trace detail failed";
      detail.value = null;
    } finally {
      detailLoading.value = false;
    }
  }

  function clearDetail(): void {
    detailAborter?.abort();
    detail.value = null;
    detailError.value = "";
    detailLoading.value = false;
  }

  onMounted(() => {
    if (!baseUrl) return;
    void refreshList();
    timer = setInterval(() => void refreshList(), intervalMs);
  });

  onUnmounted(() => {
    if (timer !== null) clearInterval(timer);
    listAborter?.abort();
    detailAborter?.abort();
  });

  return {
    configured,
    baseUrl,
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
