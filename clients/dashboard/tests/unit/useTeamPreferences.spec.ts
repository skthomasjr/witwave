import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Unit tests for useTeamPreferences (#1109, #1703). The composable
// owns module-level pinned-agents + onlyDegraded refs persisted to
// localStorage via watch-effect. Tests reset the module + storage
// between cases following the useTheme.spec.ts pattern.

function freshImport() {
  vi.resetModules();
  return import("../../src/composables/useTeamPreferences");
}

function installLocalStorage(): void {
  const store = new Map<string, string>();
  const stub: Storage = {
    get length() {
      return store.size;
    },
    clear() {
      store.clear();
    },
    getItem(key: string) {
      return store.has(key) ? (store.get(key) as string) : null;
    },
    key(i: number) {
      return Array.from(store.keys())[i] ?? null;
    },
    removeItem(key: string) {
      store.delete(key);
    },
    setItem(key: string, value: string) {
      store.set(key, String(value));
    },
  };
  Object.defineProperty(window, "localStorage", {
    configurable: true,
    writable: true,
    value: stub,
  });
}

const PINS_KEY = "witwave.team.pinnedAgents";
const ONLY_DEGRADED_KEY = "witwave.team.onlyDegraded";

describe("useTeamPreferences", () => {
  beforeEach(() => {
    installLocalStorage();
  });

  afterEach(() => {
    window.localStorage.clear();
  });

  // ----- defaults -----

  it("defaults to empty pins + onlyDegraded=false on first mount", async () => {
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    expect(api.pinnedAgents.value).toEqual([]);
    expect(api.onlyDegraded.value).toBe(false);
    expect(api.isPinned("anything")).toBe(false);
  });

  // ----- togglePin idempotence -----

  it("togglePin adds an agent on first call", async () => {
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    api.togglePin("iris");
    expect(api.pinnedAgents.value).toEqual(["iris"]);
    expect(api.isPinned("iris")).toBe(true);
  });

  it("togglePin is idempotent: double-toggle returns to empty", async () => {
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    api.togglePin("iris");
    api.togglePin("iris");
    expect(api.pinnedAgents.value).toEqual([]);
    expect(api.isPinned("iris")).toBe(false);
  });

  it("togglePin preserves order across multiple distinct agents", async () => {
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    api.togglePin("iris");
    api.togglePin("nova");
    api.togglePin("kira");
    expect(api.pinnedAgents.value).toEqual(["iris", "nova", "kira"]);
  });

  it("togglePin un-pins from middle without disturbing other order", async () => {
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    api.togglePin("iris");
    api.togglePin("nova");
    api.togglePin("kira");
    api.togglePin("nova");
    expect(api.pinnedAgents.value).toEqual(["iris", "kira"]);
  });

  // ----- persistence round-trip -----

  it("pinnedAgents writes through to localStorage as JSON array", async () => {
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    api.togglePin("iris");
    api.togglePin("nova");
    // Watcher persists synchronously after Vue flushes; nextTick to be safe.
    await Promise.resolve();
    const raw = window.localStorage.getItem(PINS_KEY);
    expect(raw).toBeTruthy();
    expect(JSON.parse(raw as string)).toEqual(["iris", "nova"]);
  });

  it("onlyDegraded persists 'true'/'false' strings", async () => {
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    api.setOnlyDegraded(true);
    await Promise.resolve();
    expect(window.localStorage.getItem(ONLY_DEGRADED_KEY)).toBe("true");
    api.setOnlyDegraded(false);
    await Promise.resolve();
    expect(window.localStorage.getItem(ONLY_DEGRADED_KEY)).toBe("false");
  });

  it("readPins survives a fresh import (state persists across reloads)", async () => {
    window.localStorage.setItem(PINS_KEY, JSON.stringify(["iris", "kira"]));
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    expect(api.pinnedAgents.value).toEqual(["iris", "kira"]);
  });

  it("readOnlyDegraded survives a fresh import", async () => {
    window.localStorage.setItem(ONLY_DEGRADED_KEY, "true");
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    expect(api.onlyDegraded.value).toBe(true);
  });

  // ----- malformed persisted data -----

  it("readPins ignores non-array JSON (e.g. accidental object)", async () => {
    window.localStorage.setItem(PINS_KEY, JSON.stringify({ iris: true }));
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    expect(api.pinnedAgents.value).toEqual([]);
  });

  it("readPins ignores non-string array entries (e.g. corrupted entry)", async () => {
    window.localStorage.setItem(PINS_KEY, JSON.stringify(["iris", 42, null, "kira"]));
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    expect(api.pinnedAgents.value).toEqual(["iris", "kira"]);
  });

  it("readPins ignores garbage JSON without throwing", async () => {
    window.localStorage.setItem(PINS_KEY, "{not-valid-json");
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    expect(api.pinnedAgents.value).toEqual([]);
  });

  // ----- localStorage failure tolerance -----

  it("togglePin works in-memory even when localStorage.setItem throws", async () => {
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    const setItem = vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
      throw new Error("QuotaExceededError");
    });
    expect(() => api.togglePin("iris")).not.toThrow();
    // The watcher will fire and the write will throw, but in-memory state
    // still updates.
    expect(api.pinnedAgents.value).toEqual(["iris"]);
    setItem.mockRestore();
  });

  // ----- __resetTeamPreferences test hook -----

  it("__resetTeamPreferences clears both refs", async () => {
    const mod = await freshImport();
    const api = mod.useTeamPreferences();
    api.togglePin("iris");
    api.setOnlyDegraded(true);
    expect(api.pinnedAgents.value.length).toBeGreaterThan(0);
    expect(api.onlyDegraded.value).toBe(true);
    mod.__resetTeamPreferences();
    expect(api.pinnedAgents.value).toEqual([]);
    expect(api.onlyDegraded.value).toBe(false);
  });
});
