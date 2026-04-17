import { onMounted, onUnmounted, ref } from "vue";
import { apiGet, ApiError } from "../api/client";
import type { TeamResponse } from "../types/team";

// Polls the harness /team endpoint. The legacy ui/ polls periodically to keep
// backend-up/down dots current; we match that approach here (SSE can come
// later if needed — see #470). The first fetch's loading state is distinct
// from subsequent refreshes so the UI doesn't flash "Loading…" every tick.

export interface UseTeamOptions {
  intervalMs?: number;
}

export function useTeam(opts: UseTeamOptions = {}) {
  const intervalMs = opts.intervalMs ?? 5000;

  const members = ref<TeamResponse>([]);
  const error = ref<string>("");
  const loading = ref<boolean>(true);

  let timer: ReturnType<typeof setInterval> | null = null;
  let aborter: AbortController | null = null;

  async function refresh(): Promise<void> {
    aborter?.abort();
    aborter = new AbortController();
    try {
      const data = await apiGet<TeamResponse>("/team", { signal: aborter.signal });
      members.value = data;
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

  return { members, error, loading, refresh };
}
