import { computed, ref } from "vue";
import type { ComputedRef } from "vue";
import { useHealth, type HealthState } from "./useHealth";

// Global alert surface (#1108). Subscribes to useHealth's derived team
// state and emits a single active alert describing the worst-currently-
// known condition. Consumers (AlertBanner component) render the alert
// banner; this composable also tracks dismissed alerts so a user who
// acknowledges "harness-down" during triage doesn't see the banner
// re-appear on every health refresh — the banner only re-surfaces when
// the state changes to a *different* severity.

export type AlertSeverity = "info" | "warning" | "error";

export interface Alert {
  id: string;
  severity: AlertSeverity;
  title: string;
  detail: string;
}

// Module-scope so every mount of useAlerts shares the same dismissal
// set for the current session. Cleared on full page reload, which is
// the right semantic — a new session is a new triage window.
const dismissedAlertIds = ref<Set<string>>(new Set());

function alertFor(state: HealthState, detail: string): Alert | null {
  if (state === "err") {
    return {
      id: `health:err:${detail}`,
      severity: "error",
      title: "All agents unreachable",
      detail: detail || "The harness fan-out is failing for every team member.",
    };
  }
  if (state === "partial") {
    return {
      id: `health:partial:${detail}`,
      severity: "warning",
      title: "Team degraded",
      detail: detail || "One or more agents are failing their health probe.",
    };
  }
  if (state === "empty") {
    return {
      id: "health:empty",
      severity: "info",
      title: "No agents configured",
      detail: "The dashboard is running but no agents are listed in the team directory.",
    };
  }
  return null;
}

export interface UseAlertsApi {
  active: ComputedRef<Alert | null>;
  dismiss(id: string): void;
}

export function useAlerts(): UseAlertsApi {
  const { state, detail } = useHealth();

  const active = computed<Alert | null>(() => {
    const candidate = alertFor(state.value, detail.value);
    if (!candidate) return null;
    if (dismissedAlertIds.value.has(candidate.id)) return null;
    return candidate;
  });

  function dismiss(id: string): void {
    if (dismissedAlertIds.value.has(id)) return;
    // Create a fresh Set so Vue's reactivity picks the change up — Sets
    // aren't deeply reactive when mutated in-place.
    const next = new Set(dismissedAlertIds.value);
    next.add(id);
    dismissedAlertIds.value = next;
  }

  return { active, dismiss };
}

// Test hook — unit tests reset the module singleton between cases.
export function __resetUseAlerts(): void {
  dismissedAlertIds.value = new Set();
}
