// Thin fetch wrapper for the dashboard → harness API.
//
// All harness endpoints are reached through /api/*, which Vite proxies in dev
// (vite.config.ts) and nginx proxies in prod (dashboard/nginx.conf). Keeping
// the base path as a constant here means components never hard-code "/api".

const API_BASE = "/api";

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ApiRequestOptions {
  signal?: AbortSignal;
  query?: Record<string, string | undefined>;
  // Optional per-request timeout in milliseconds. When set, a timeout-only
  // AbortSignal is combined with any caller-supplied signal so hung fetches
  // reject deterministically instead of leaving callers (e.g. chat send)
  // stuck forever when the network or backend stalls mid-response (#535).
  timeoutMs?: number;
}

// Hand-combine two AbortSignals without relying on AbortSignal.any, which is
// still missing from some environments (notably older jsdom used by vitest).
// Returns a signal that aborts when either input aborts, plus a cleanup to
// detach listeners once the request settles.
function mergeSignals(
  a: AbortSignal | undefined,
  b: AbortSignal | undefined,
): { signal: AbortSignal | undefined; cleanup: () => void } {
  if (!a) return { signal: b, cleanup: () => {} };
  if (!b) return { signal: a, cleanup: () => {} };
  const controller = new AbortController();
  const forwardA = () => controller.abort((a as AbortSignal).reason);
  const forwardB = () => controller.abort((b as AbortSignal).reason);
  if (a.aborted) forwardA();
  else a.addEventListener("abort", forwardA, { once: true });
  if (b.aborted) forwardB();
  else b.addEventListener("abort", forwardB, { once: true });
  return {
    signal: controller.signal,
    cleanup: () => {
      a.removeEventListener("abort", forwardA);
      b.removeEventListener("abort", forwardB);
    },
  };
}

function buildTimeoutSignal(timeoutMs: number | undefined): {
  signal: AbortSignal | undefined;
  cleanup: () => void;
} {
  if (!timeoutMs || timeoutMs <= 0) return { signal: undefined, cleanup: () => {} };
  // AbortSignal.timeout is widely available, but fall back to a manual timer
  // in test environments that lack it so the default timeout is still honored.
  const AnyAbortSignal = AbortSignal as unknown as {
    timeout?: (ms: number) => AbortSignal;
  };
  if (typeof AnyAbortSignal.timeout === "function") {
    return { signal: AnyAbortSignal.timeout(timeoutMs), cleanup: () => {} };
  }
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new DOMException("timeout", "TimeoutError")), timeoutMs);
  return {
    signal: controller.signal,
    cleanup: () => clearTimeout(timer),
  };
}

function buildUrl(path: string, query?: ApiRequestOptions["query"]): string {
  if (!query) return `${API_BASE}${path}`;
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== "") params.set(k, v);
  }
  const qs = params.toString();
  return qs ? `${API_BASE}${path}?${qs}` : `${API_BASE}${path}`;
}

export async function apiGet<T>(path: string, opts: ApiRequestOptions = {}): Promise<T> {
  const timeout = buildTimeoutSignal(opts.timeoutMs);
  const merged = mergeSignals(opts.signal, timeout.signal);
  try {
    const resp = await fetch(buildUrl(path, opts.query), { signal: merged.signal });
    if (!resp.ok) {
      throw new ApiError(resp.status, `HTTP ${resp.status}`);
    }
    return (await resp.json()) as T;
  } finally {
    merged.cleanup();
    timeout.cleanup();
  }
}

export async function apiPost<T, B = unknown>(path: string, body: B, opts: ApiRequestOptions = {}): Promise<T> {
  const timeout = buildTimeoutSignal(opts.timeoutMs);
  const merged = mergeSignals(opts.signal, timeout.signal);
  try {
    const resp = await fetch(buildUrl(path, opts.query), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: merged.signal,
    });
    if (!resp.ok) {
      throw new ApiError(resp.status, `HTTP ${resp.status}`);
    }
    return (await resp.json()) as T;
  } finally {
    merged.cleanup();
    timeout.cleanup();
  }
}
