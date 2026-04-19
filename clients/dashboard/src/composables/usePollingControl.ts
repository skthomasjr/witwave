import { ref, computed } from "vue";
import type { ComputedRef, Ref } from "vue";

// Global auto-refresh pause + tab-visibility gating (#1107).
//
// Every polling composable (useTeam/useMetrics/useAgentFanout) wraps its
// setInterval tick in a predicate that consults this singleton: the tick
// is skipped while polling is paused by the user OR while the tab is
// hidden. Single source of truth keeps the header toggle in the App
// shell consistent across every view.
//
// Module-level so every consumer sees the same paused ref; persisted to
// localStorage so the user's preference survives reload. Visibility is
// tracked via a single document-level listener installed lazily on the
// first subscription.

const STORAGE_KEY = "nyx.polling.paused";

function readPersisted(): boolean {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === "true";
  } catch {
    return false;
  }
}

function writePersisted(val: boolean): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, val ? "true" : "false");
  } catch {
    // Quota exceeded / private mode — silently ignore. The in-memory
    // value still drives the feature for this tab.
  }
}

const pausedRef = ref<boolean>(readPersisted());
const visibleRef = ref<boolean>(
  typeof document === "undefined" ? true : !document.hidden,
);

// The visibility listener is intentionally long-lived: installed once at
// module scope on the first use and never removed. A single listener on
// `document.visibilitychange` is trivial in cost (a ref write per tab
// flip), and the refcount approach it replaces had a leak — multiple
// `usePollingControl()` callers during a single mount could bump the
// count without symmetric unmounts, leaving the listener reattached or
// detached out of sync with reality. (#1161)
let visibilityListenerInstalled = false;
function onVisibilityChange(): void {
  visibleRef.value = !document.hidden;
}
function installVisibilityListener(): void {
  if (typeof document === "undefined") return;
  if (visibilityListenerInstalled) return;
  document.addEventListener("visibilitychange", onVisibilityChange);
  visibilityListenerInstalled = true;
}

export interface UsePollingControlApi {
  paused: Ref<boolean>;
  visible: Ref<boolean>;
  // Effective polling state: skip this tick when true.
  shouldSkipTick: ComputedRef<boolean>;
  toggle(): void;
  setPaused(val: boolean): void;
}

export function usePollingControl(): UsePollingControlApi {
  // Permanently installed on first use — no refcount, no teardown hook.
  // See the note above installVisibilityListener. (#1161)
  installVisibilityListener();
  return {
    paused: pausedRef,
    visible: visibleRef,
    shouldSkipTick: computed(() => pausedRef.value || !visibleRef.value),
    toggle() {
      pausedRef.value = !pausedRef.value;
      writePersisted(pausedRef.value);
    },
    setPaused(val: boolean) {
      pausedRef.value = val;
      writePersisted(val);
    },
  };
}

// Non-component getter for pollers that run at module scope (e.g. the
// shared useTeam singleton). Does NOT install the visibility listener —
// callers that need it should call ensureVisibilityListenerInstalled()
// or mount a component that calls usePollingControl().
export function pollingShouldSkipTick(): boolean {
  return pausedRef.value || !visibleRef.value;
}

export function ensureVisibilityListenerInstalled(): void {
  installVisibilityListener();
}

// Test hook — unit tests reset this between cases.
export function __resetPollingControl(): void {
  pausedRef.value = false;
  visibleRef.value = true;
  // The visibility listener is intentionally long-lived; tests may
  // exercise the install path but we don't detach here. (#1161)
}
