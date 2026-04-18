import { onMounted, onUnmounted, ref } from "vue";
import { apiGet, ApiError } from "../api/client";
import type { Agent, TeamMember, TeamResponse } from "../types/team";

// Team state — discovery + per-agent fan-out.
//
// Architecture note (#470): the dashboard pod owns routing. Discovery hits
// the dashboard nginx's local /api/team (Helm-rendered member list, no
// agent call). Each member's full card set comes from /api/agents/<name>/
// agents, routed directly to that agent's harness — so a single agent
// outage only affects its own card, never the whole view.

interface TeamDirectoryEntry {
  name: string;
  url: string;
}

export interface UseTeamOptions {
  intervalMs?: number;
  // Per-member timeout (ms) so one unreachable agent cannot stall the
  // whole fan-out and stop the dashboard refreshing (#743). Default
  // 5000ms; override per-caller if a specific view needs more slack.
  memberTimeoutMs?: number;
  // Timeout for the /team discovery call itself (#743). Default 5000ms.
  directoryTimeoutMs?: number;
}

export function useTeam(opts: UseTeamOptions = {}) {
  const intervalMs = opts.intervalMs ?? 5000;
  const memberTimeoutMs = opts.memberTimeoutMs ?? 5000;
  const directoryTimeoutMs = opts.directoryTimeoutMs ?? 5000;

  const members = ref<TeamResponse>([]);
  const error = ref<string>("");
  const loading = ref<boolean>(true);

  let timer: ReturnType<typeof setInterval> | null = null;
  let aborter: AbortController | null = null;

  async function fetchMember(
    entry: TeamDirectoryEntry,
    signal: AbortSignal,
  ): Promise<TeamMember> {
    try {
      const agents = await apiGet<Agent[]>(
        `/agents/${encodeURIComponent(entry.name)}/agents`,
        { signal, timeoutMs: memberTimeoutMs },
      );
      return { name: entry.name, url: entry.url, agents };
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") throw e;
      const msg = e instanceof ApiError ? e.message : (e as Error).message;
      return { name: entry.name, url: entry.url, agents: [], error: msg };
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
      const resolved = await Promise.all(
        directory.map((entry) => fetchMember(entry, signal)),
      );
      if (signal.aborted) return;
      members.value = resolved;
      error.value = "";
    } catch (e) {
      // Identity check (#744): when the active aborter has moved on,
      // this rejection belongs to the OLD refresh cycle — stay silent
      // regardless of the specific error, so bursty refreshes don't
      // surface spurious AbortError toasts or 'degraded' badges.
      if (aborter !== localAborter || (e as { name?: string }).name === "AbortError") {
        return;
      }
      error.value = e instanceof ApiError ? e.message : (e as Error).message;
    } finally {
      // Only clear loading for the currently-active cycle. A stale
      // refresh completing late should not flip the spinner off if a
      // newer one is still in flight.
      if (aborter === localAborter) loading.value = false;
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
