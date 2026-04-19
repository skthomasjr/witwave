import { ref, watch } from "vue";

// TeamView UX preferences persisted to localStorage (#1109):
//   - pinned agents  — render pinned entries first, sorted by pin order
//   - onlyDegraded   — hide healthy agents; matches SRE "only trouble" view
//
// Two small module singletons so every mount of TeamView + AgentList
// shares the same state without prop drilling. Writes are debounced
// via the watch-effect pattern (set ref, watcher persists). Graceful
// degradation: if localStorage is unavailable (private mode, quota)
// we still drive the in-memory UI, we just don't persist.

const PINS_KEY = "nyx.team.pinnedAgents";
const ONLY_DEGRADED_KEY = "nyx.team.onlyDegraded";

function readPins(): string[] {
  try {
    const raw = window.localStorage.getItem(PINS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((v): v is string => typeof v === "string");
  } catch {
    return [];
  }
}

function readOnlyDegraded(): boolean {
  try {
    return window.localStorage.getItem(ONLY_DEGRADED_KEY) === "true";
  } catch {
    return false;
  }
}

const pinnedAgents = ref<string[]>(readPins());
const onlyDegraded = ref<boolean>(readOnlyDegraded());

// Persist-on-change. localStorage writes are synchronous but tiny; the
// happy path is O(pinCount) JSON.stringify.
watch(
  pinnedAgents,
  (v) => {
    try {
      window.localStorage.setItem(PINS_KEY, JSON.stringify(v));
    } catch {
      // ignore
    }
  },
  { deep: true },
);

watch(onlyDegraded, (v) => {
  try {
    window.localStorage.setItem(ONLY_DEGRADED_KEY, v ? "true" : "false");
  } catch {
    // ignore
  }
});

export function useTeamPreferences() {
  function isPinned(name: string): boolean {
    return pinnedAgents.value.includes(name);
  }

  function togglePin(name: string): void {
    const i = pinnedAgents.value.indexOf(name);
    if (i === -1) {
      pinnedAgents.value = [...pinnedAgents.value, name];
    } else {
      pinnedAgents.value = pinnedAgents.value.filter((n) => n !== name);
    }
  }

  function setOnlyDegraded(val: boolean): void {
    onlyDegraded.value = val;
  }

  return {
    pinnedAgents,
    onlyDegraded,
    isPinned,
    togglePin,
    setOnlyDegraded,
  };
}

// Test hook — reset the module-level state between cases.
export function __resetTeamPreferences(): void {
  pinnedAgents.value = [];
  onlyDegraded.value = false;
}
