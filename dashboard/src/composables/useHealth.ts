import { computed } from "vue";
import { useTeam } from "./useTeam";

// Header status dot — aggregates per-agent health across the whole team
// (#543). Previously this polled the dashboard-local /api/team endpoint,
// which is served by the dashboard pod itself and cannot detect cluster
// outages. We now derive state from useTeam's per-member fan-out (each
// member carries its own `error` populated by /api/agents/<name>/agents),
// giving a tri-state indicator that reflects real cluster health rather
// than "the dashboard pod is up".
//
//   connecting — first fetch in flight, or we haven't seen any members yet
//   ok         — every team member's last probe succeeded
//   partial    — some members ok, some failed (tooltip lists failing names)
//   err        — every team member failed, or the directory itself failed
//   empty      — directory fetch succeeded but the configured team is empty;
//                terminal (#679) instead of latching to "connecting" forever
//
// Implementation note: we reuse useTeam rather than starting a second
// poller to avoid doubling dashboard network traffic. Soft-dependency on
// the per-agent error shape surfaced by #574.

export type HealthState = "connecting" | "ok" | "partial" | "err" | "empty";

export function useHealth(intervalMs = 10000) {
  const { members, error, loading } = useTeam({ intervalMs });

  const state = computed<HealthState>(() => {
    // Directory fetch itself failed (dashboard-local /api/team unreachable,
    // which in practice means the SPA also can't load — but handle it).
    if (error.value && members.value.length === 0) return "err";
    // First fan-out still in flight, or no members resolved yet.
    if (loading.value && members.value.length === 0) return "connecting";
    // Directory fetch succeeded and returned zero members — terminal
    // "empty" state so the dot doesn't latch to "connecting" forever
    // when the deployment has no configured team (#679).
    if (members.value.length === 0) return "empty";

    const failing = members.value.filter((m) => m.error);
    if (failing.length === 0) return "ok";
    if (failing.length === members.value.length) return "err";
    return "partial";
  });

  const detail = computed<string>(() => {
    if (state.value === "ok" || state.value === "connecting") return "";
    if (state.value === "empty") return "no agents configured";
    if (state.value === "err" && members.value.length === 0) {
      return error.value || "team directory unavailable";
    }
    const failing = members.value.filter((m) => m.error);
    if (failing.length === 0) return "";
    const names = failing.map((m) => m.name).join(", ");
    if (state.value === "err") return `all agents unreachable: ${names}`;
    return `failing: ${names}`;
  });

  return { state, detail };
}
