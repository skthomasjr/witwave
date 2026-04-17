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

export interface ApiGetOptions {
  signal?: AbortSignal;
}

export async function apiGet<T>(path: string, opts: ApiGetOptions = {}): Promise<T> {
  const resp = await fetch(`${API_BASE}${path}`, { signal: opts.signal });
  if (!resp.ok) {
    throw new ApiError(resp.status, `HTTP ${resp.status}`);
  }
  return (await resp.json()) as T;
}
