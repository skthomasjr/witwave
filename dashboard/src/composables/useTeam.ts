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
}

export function useTeam(opts: UseTeamOptions = {}) {
  const intervalMs = opts.intervalMs ?? 5000;

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
        { signal },
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
    aborter = new AbortController();
    const signal = aborter.signal;
    try {
      const directory = await apiGet<TeamDirectoryEntry[]>("/team", { signal });
      const resolved = await Promise.all(
        directory.map((entry) => fetchMember(entry, signal)),
      );
      if (signal.aborted) return;
      members.value = resolved;
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
