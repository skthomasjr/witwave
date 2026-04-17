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
  const resp = await fetch(buildUrl(path, opts.query), { signal: opts.signal });
  if (!resp.ok) {
    throw new ApiError(resp.status, `HTTP ${resp.status}`);
  }
  return (await resp.json()) as T;
}

export async function apiPost<T, B = unknown>(
  path: string,
  body: B,
  opts: ApiRequestOptions = {},
): Promise<T> {
  const resp = await fetch(buildUrl(path, opts.query), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal: opts.signal,
  });
  if (!resp.ok) {
    throw new ApiError(resp.status, `HTTP ${resp.status}`);
  }
  return (await resp.json()) as T;
}
