import { computed, onScopeDispose, ref, watch } from "vue";
import type { ComputedRef } from "vue";
import { storeToRefs } from "pinia";
import { useHealth, type HealthState } from "./useHealth";
import { useTimelineStore } from "../stores/timeline";
import type { EventEnvelope } from "./useEventStream";

// Global alert surface (#1108, rewired in #1110 phase 2). Two sources:
//
//   1. Polling fallback — useHealth's tri-state (harness / team reachability).
//      Kept as the offline signal: when the timeline SSE is down we still
//      need "can we even reach the cluster?" detection.
//
//   2. Timeline event stream — the harness /events/stream feed surfaced
//      via the timeline Pinia store. Derives per-event triggers (webhook
//      failures, hook deny-rate spikes, agent lifecycle stops, stream
//      reconnect markers) so operators see incidents as they happen rather
//      than waiting for the next poll tick.
//
// De-duplication: every trigger uses a stable key; re-firing the same key
// within DEDUPE_WINDOW_MS updates the existing alert instead of toasting
// again. Dismissal clears the key for the session. Keys match the task
// spec (webhook.<agent>.<name>.<host>, hook-deny.<backend>,
// lifecycle.<agent>, stream-gap).

export type AlertSeverity = "info" | "warning" | "error";

export interface Alert {
  id: string;
  severity: AlertSeverity;
  title: string;
  detail: string;
  count?: number;
  // Internal timestamp used for de-dupe — exposed so the banner could
  // show "last updated Xs ago" in a future iteration, but also so tests
  // can observe that a duplicate trigger updated the count rather than
  // spawning a new alert.
  firstSeenAt?: number;
  lastSeenAt?: number;
}

// Severity ordering for picking the "active" alert when multiple fire.
const SEVERITY_RANK: Record<AlertSeverity, number> = {
  error: 3,
  warning: 2,
  info: 1,
};

// Re-firing the same key within this window updates the existing entry.
// After it passes, the next occurrence re-arms as a fresh alert.
const DEDUPE_WINDOW_MS = 5 * 60 * 1000;

// Rolling window + thresholds for the hook deny-rate detector.
const HOOK_DENY_WINDOW_MS = 5 * 60 * 1000;
const HOOK_DENY_FIRE_THRESHOLD = 10;
const HOOK_DENY_RESET_THRESHOLD = 5;

// How long the stream must stay disconnected before we surface the
// "live activity feed unavailable" info alert.
const STREAM_DOWN_GRACE_MS = 30_000;

// Module-scope singletons so every consumer of useAlerts sees the same
// state. Cleared on full page reload (new session = new triage window).
const dismissedAlertIds = ref<Set<string>>(new Set());
const activeAlerts = ref<Map<string, Alert>>(new Map());

// Rolling window of hook-deny timestamps per backend (ms since epoch).
const hookDenyWindow = new Map<string, number[]>();
// Tracks whether the deny-rate alert is currently "armed" (fired) for a
// backend so we can auto-clear when the count drops below the reset
// threshold, matching the hysteresis described in the task spec.
const hookDenyArmed = new Set<string>();

// Tracks agents currently in the `stopped` state so a subsequent `started`
// event auto-resolves the lifecycle alert.
const lifecycleStopped = new Set<string>();

// Rolling-window "current time" for hook-deny bookkeeping. We use the
// latest observed event timestamp (max of wall clock and event.ts) so
// tests + replays anchored to a historical ts don't get scythed by a
// future-dated wall clock — and so out-of-order events don't accidentally
// drain the window from the past.
let latestObservedStampMs = 0;

function observeStamp(ms: number): void {
  if (ms > latestObservedStampMs) latestObservedStampMs = ms;
}

function currentStampMs(): number {
  // Prefer the latest event-time we've observed — the deny-window logic
  // is anchored to event timestamps so the reconciliation sweep must use
  // the same clock, or a stale wall-clock in tests / replays would scythe
  // an otherwise-valid window.
  return latestObservedStampMs || now();
}

// Monotonic cursor into the timeline store's ring — we only process
// events past this index each tick so a reactive burst doesn't re-score
// the whole ring.
let lastProcessedEventId = "";

// Marker for the stream-outage timer so only one timer is ever live.
let streamDownTimer: ReturnType<typeof setTimeout> | null = null;

// Guard: ensure the module-level event/stream watchers are wired up
// exactly once across the app lifetime, even if useAlerts() is called
// from multiple components.
let wiredUp = false;

function now(): number {
  return Date.now();
}

interface UpsertOptions {
  // Only when the caller explicitly asks for it does a re-arm outside
  // the dedupe window clear an existing dismissal. Without this flag a
  // dismissed alert stays dismissed regardless of how much time has
  // passed since the last firing. (#1158)
  unDismiss?: boolean;
}

function upsertAlert(next: Alert, opts: UpsertOptions = {}): void {
  const prev = activeAlerts.value.get(next.id);
  const merged: Alert = {
    ...next,
    firstSeenAt: prev?.firstSeenAt ?? next.firstSeenAt ?? now(),
    lastSeenAt: next.lastSeenAt ?? now(),
    count: prev ? (prev.count ?? 1) + 1 : next.count ?? 1,
  };

  if (prev) {
    const withinWindow =
      merged.lastSeenAt !== undefined &&
      prev.lastSeenAt !== undefined &&
      merged.lastSeenAt - prev.lastSeenAt <= DEDUPE_WINDOW_MS;
    if (withinWindow) {
      // De-dupe: refresh the existing entry in place. Don't bump the
      // dismissal — operators who dismissed it keep it hidden until the
      // window elapses.
      const copy = new Map(activeAlerts.value);
      copy.set(next.id, merged);
      activeAlerts.value = copy;
      return;
    }
    // Outside the window → treat as a fresh re-arm: reset firstSeenAt +
    // count. Respect any existing dismissal unless caller opts in via
    // `unDismiss: true`. Previously an unconditional wipe meant an
    // operator dismissal silently vanished once the dedupe window
    // elapsed. (#1158)
    merged.firstSeenAt = merged.lastSeenAt;
    merged.count = 1;
    if (opts.unDismiss && dismissedAlertIds.value.has(next.id)) {
      const dismissed = new Set(dismissedAlertIds.value);
      dismissed.delete(next.id);
      dismissedAlertIds.value = dismissed;
    }
  }

  const copy = new Map(activeAlerts.value);
  copy.set(next.id, merged);
  activeAlerts.value = copy;
}

function clearAlert(id: string): void {
  if (!activeAlerts.value.has(id)) return;
  const copy = new Map(activeAlerts.value);
  copy.delete(id);
  activeAlerts.value = copy;
  if (dismissedAlertIds.value.has(id)) {
    const dismissed = new Set(dismissedAlertIds.value);
    dismissed.delete(id);
    dismissedAlertIds.value = dismissed;
  }
}

// --- Health-poll fallback ---------------------------------------------------

// Stable ids per health state so dismissals persist across detail
// changes. Previously the id embedded the detail string, which meant a
// single char change in the detail (e.g. a new failing member name)
// created a new alert id and re-surfaced a dismissed entry. (#1159)
function healthAlertFor(state: HealthState, detail: string): Alert | null {
  if (state === "err") {
    return {
      id: "health:err",
      severity: "error",
      title: "All agents unreachable",
      detail: detail || "The harness fan-out is failing for every team member.",
    };
  }
  if (state === "partial") {
    return {
      id: "health:partial",
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

// --- Timeline-event triggers -----------------------------------------------

function handleWebhookFailed(event: EventEnvelope): void {
  const payload = event.payload ?? {};
  const reason = typeof payload.reason === "string" ? payload.reason : "";
  // Only the transport-level failures get loud — a 4xx from the peer is
  // already surfaced via webhook.delivered{status_code} panels.
  if (reason !== "timeout" && reason !== "exception") return;

  const name = typeof payload.name === "string" ? payload.name : "unknown";
  const urlHost = typeof payload.url_host === "string" ? payload.url_host : "unknown";
  const agent = event.agent_id ?? "harness";

  upsertAlert({
    id: `webhook.${agent}.${name}.${urlHost}`,
    severity: "warning",
    title: `Webhook ${name} failing on ${agent}`,
    detail: `Delivery to ${urlHost} failed (${reason}).`,
  });
}

function handleHookDecision(event: EventEnvelope): void {
  const payload = event.payload ?? {};
  const decision = typeof payload.decision === "string" ? payload.decision : "";
  if (decision !== "deny") return;

  const backend = typeof payload.backend === "string" ? payload.backend : "unknown";
  const ts = Date.parse(event.ts);
  const stamp = Number.isFinite(ts) ? ts : now();
  observeStamp(stamp);

  const window = hookDenyWindow.get(backend) ?? [];
  const cutoff = stamp - HOOK_DENY_WINDOW_MS;
  // Keep only timestamps within the rolling window.
  const pruned = window.filter((t) => t >= cutoff);
  pruned.push(stamp);
  hookDenyWindow.set(backend, pruned);

  const key = `hook-deny.${backend}`;

  if (hookDenyArmed.has(backend)) {
    // Already firing — once we fall back below the reset threshold, the
    // alert auto-clears so a future spike can re-arm.
    if (pruned.length < HOOK_DENY_RESET_THRESHOLD) {
      hookDenyArmed.delete(backend);
      clearAlert(key);
    }
    return;
  }

  if (pruned.length >= HOOK_DENY_FIRE_THRESHOLD) {
    hookDenyArmed.add(backend);
    upsertAlert({
      id: key,
      severity: "warning",
      title: `Hook denial spike on ${backend} backend`,
      detail: `${pruned.length} deny decisions in the last 5 minutes.`,
    });
  }
}

// Called on every watcher tick — the rolling window prunes lazily on
// arrival, but if deny events stop entirely the armed flag never clears.
// This pass sweeps each backend and auto-resolves when the window drains.
function reconcileHookDeny(): void {
  const cutoff = currentStampMs() - HOOK_DENY_WINDOW_MS;
  for (const [backend, stamps] of hookDenyWindow) {
    const pruned = stamps.filter((t) => t >= cutoff);
    if (pruned.length !== stamps.length) {
      hookDenyWindow.set(backend, pruned);
    }
    if (hookDenyArmed.has(backend) && pruned.length < HOOK_DENY_RESET_THRESHOLD) {
      hookDenyArmed.delete(backend);
      clearAlert(`hook-deny.${backend}`);
    }
  }
}

function handleAgentLifecycle(event: EventEnvelope): void {
  const payload = event.payload ?? {};
  const evt = typeof payload.event === "string" ? payload.event : "";
  const agent = event.agent_id ?? "unknown";
  const key = `lifecycle.${agent}`;

  if (evt === "stopped") {
    lifecycleStopped.add(agent);
    const backend = typeof payload.backend === "string" ? `${payload.backend} backend` : "backend";
    upsertAlert({
      id: key,
      severity: "error",
      title: `Agent ${agent} stopped`,
      detail: `${backend} lifecycle event reported a stop.`,
    });
    return;
  }

  if (evt === "started") {
    if (lifecycleStopped.has(agent)) {
      lifecycleStopped.delete(agent);
      clearAlert(key);
    }
  }
}

function handleStreamMarker(event: EventEnvelope): void {
  // Both stream.gap (server-synthesised after ring drop) and
  // stream.overrun (client-side marker surfaced by the composable)
  // indicate the in-tab history may have holes — operators care that
  // the live feed was briefly lossy, nothing more.
  if (event.type !== "stream.gap" && event.type !== "stream.overrun") return;
  upsertAlert({
    id: "stream-gap",
    severity: "info",
    title: "Event stream caught up after reconnect",
    detail: "The in-tab activity history may have holes — refresh the view to reconcile.",
  });
}

function dispatchEvent(event: EventEnvelope): void {
  switch (event.type) {
    case "webhook.failed":
      handleWebhookFailed(event);
      break;
    case "hook.decision":
      handleHookDecision(event);
      break;
    case "agent.lifecycle":
      handleAgentLifecycle(event);
      break;
    case "stream.gap":
    case "stream.overrun":
      handleStreamMarker(event);
      break;
    default:
      break;
  }
}

// --- Module wiring ----------------------------------------------------------

function wireOnce(): void {
  if (wiredUp) return;
  wiredUp = true;

  const store = useTimelineStore();
  const { events, connected, reconnecting } = storeToRefs(store);

  // Stream activity — process incremental tail only, so a watcher burst
  // doesn't re-score the whole ring.
  watch(
    events,
    (list) => {
      if (!Array.isArray(list) || list.length === 0) return;
      // Find the slice past the last id we handled. The ring is ordered
      // by arrival; if the cursor id is no longer present (ring evicted
      // it), fall back to processing the whole ring.
      let startIdx = 0;
      if (lastProcessedEventId) {
        const found = list.findIndex((e) => e.id === lastProcessedEventId);
        startIdx = found === -1 ? 0 : found + 1;
      }
      for (let i = startIdx; i < list.length; i += 1) {
        dispatchEvent(list[i]);
      }
      const tail = list[list.length - 1];
      if (tail) lastProcessedEventId = tail.id;
      // Each event arrival is a natural moment to sweep the hook-deny
      // window so stale armed flags clear even without new deny events.
      reconcileHookDeny();
    },
    { deep: false },
  );

  // Connection-down surface — only fires once the stream has been in a
  // non-connected state continuously for STREAM_DOWN_GRACE_MS.
  const scheduleStreamDown = (): void => {
    if (streamDownTimer) return;
    streamDownTimer = setTimeout(() => {
      streamDownTimer = null;
      if (!connected.value) {
        upsertAlert({
          id: "stream-down",
          severity: "info",
          title: "Live activity feed unavailable",
          detail: "Falling back to polling. Real-time event triggers will re-engage on reconnect.",
        });
      }
    }, STREAM_DOWN_GRACE_MS);
  };

  const clearStreamDown = (): void => {
    if (streamDownTimer) {
      clearTimeout(streamDownTimer);
      streamDownTimer = null;
    }
    clearAlert("stream-down");
  };

  watch(
    [connected, reconnecting],
    ([conn]) => {
      if (conn) {
        clearStreamDown();
        return;
      }
      // Post-guard: `conn` is false here, so we're always in the
      // stream-down scheduling path. The prior `if (rec || !conn)`
      // test had a dead `rec ||` half and was tautologically true once
      // the guard fell through. (#1160)
      scheduleStreamDown();
    },
    { immediate: true },
  );
}

// --- Public API -------------------------------------------------------------

export interface UseAlertsApi {
  active: ComputedRef<Alert | null>;
  alerts: ComputedRef<Alert[]>;
  dismiss(id: string): void;
}

export function useAlerts(): UseAlertsApi {
  wireOnce();
  const { state, detail } = useHealth();

  // Aggregate the event-driven alerts with the polling-derived fallback.
  // Health alerts don't live in `activeAlerts` because they're a pure
  // function of the reactive health state — they appear and disappear
  // naturally as that state changes, with no de-dupe semantics required.
  const alerts = computed<Alert[]>(() => {
    const list: Alert[] = [];

    // Event-driven first (most recent info).
    for (const alert of activeAlerts.value.values()) {
      if (dismissedAlertIds.value.has(alert.id)) continue;
      list.push(alert);
    }

    // Poll-derived fallback.
    const healthAlert = healthAlertFor(state.value, detail.value);
    if (healthAlert && !dismissedAlertIds.value.has(healthAlert.id)) {
      list.push(healthAlert);
    }

    // Sort by severity (error > warning > info). Within a severity keep
    // insertion order so the most recently fired of equal severity stays
    // visible (event alerts come before the poll fallback, which matches
    // the "something happened" vs "I'm not even connected" priority).
    list.sort((a, b) => SEVERITY_RANK[b.severity] - SEVERITY_RANK[a.severity]);
    return list;
  });

  const active = computed<Alert | null>(() => alerts.value[0] ?? null);

  function dismiss(id: string): void {
    if (dismissedAlertIds.value.has(id)) return;
    const next = new Set(dismissedAlertIds.value);
    next.add(id);
    dismissedAlertIds.value = next;
  }

  // Per-instance scope dispose is a no-op — module singletons survive
  // unmount on purpose — but register the hook so consumers inside a
  // setup() scope don't see "unregistered" warnings.
  onScopeDispose(() => {
    // intentionally empty; see above.
  });

  return { active, alerts, dismiss };
}

// Test hook — unit tests reset the module singletons between cases.
export function __resetUseAlerts(): void {
  dismissedAlertIds.value = new Set();
  activeAlerts.value = new Map();
  hookDenyWindow.clear();
  hookDenyArmed.clear();
  lifecycleStopped.clear();
  lastProcessedEventId = "";
  latestObservedStampMs = 0;
  if (streamDownTimer) {
    clearTimeout(streamDownTimer);
    streamDownTimer = null;
  }
  wiredUp = false;
}
