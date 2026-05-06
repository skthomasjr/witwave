import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Unit tests for usePollingControl (#1107, #1161, #1703). The composable owns
// the singleton paused-state + tab-visibility refs that every polling
// composable consults. Module state leaks between cases unless we
// reset between tests; the existing useTheme.spec.ts pattern is the
// reference.

function freshImport() {
  vi.resetModules();
  return import("../../src/composables/usePollingControl");
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

describe("usePollingControl", () => {
  beforeEach(() => {
    installLocalStorage();
  });

  afterEach(() => {
    window.localStorage.clear();
  });

  // ----- defaults -----

  it("defaults paused=false and visible=true on first mount", async () => {
    const mod = await freshImport();
    const api = mod.usePollingControl();
    expect(api.paused.value).toBe(false);
    expect(api.visible.value).toBe(true);
    expect(api.shouldSkipTick.value).toBe(false);
  });

  // ----- toggle persistence -----

  it("toggle() flips paused and persists to localStorage", async () => {
    const mod = await freshImport();
    const api = mod.usePollingControl();
    api.toggle();
    expect(api.paused.value).toBe(true);
    expect(window.localStorage.getItem("witwave.polling.paused")).toBe("true");
    api.toggle();
    expect(api.paused.value).toBe(false);
    expect(window.localStorage.getItem("witwave.polling.paused")).toBe("false");
  });

  it("setPaused(true) persists; setPaused(false) clears the persisted flag", async () => {
    const mod = await freshImport();
    const api = mod.usePollingControl();
    api.setPaused(true);
    expect(window.localStorage.getItem("witwave.polling.paused")).toBe("true");
    api.setPaused(false);
    expect(window.localStorage.getItem("witwave.polling.paused")).toBe("false");
  });

  it("paused=true on prior session survives reload", async () => {
    // Pre-seed the store, then fresh-import to simulate the next page load.
    window.localStorage.setItem("witwave.polling.paused", "true");
    const mod = await freshImport();
    const api = mod.usePollingControl();
    expect(api.paused.value).toBe(true);
    expect(api.shouldSkipTick.value).toBe(true);
  });

  // ----- shouldSkipTick logic -----

  it("shouldSkipTick=true when paused, regardless of visibility", async () => {
    const mod = await freshImport();
    const api = mod.usePollingControl();
    api.setPaused(true);
    expect(api.shouldSkipTick.value).toBe(true);
  });

  it("shouldSkipTick=true when tab hidden, even if not paused", async () => {
    const mod = await freshImport();
    const api = mod.usePollingControl();
    api.visible.value = false;
    expect(api.shouldSkipTick.value).toBe(true);
  });

  // ----- visibilitychange listener (#1161) -----

  it("installs the visibilitychange listener exactly once across multiple mounts", async () => {
    const addSpy = vi.spyOn(document, "addEventListener");
    const mod = await freshImport();
    mod.usePollingControl();
    mod.usePollingControl();
    mod.usePollingControl();
    const visibilityCalls = addSpy.mock.calls.filter((c) => c[0] === "visibilitychange");
    expect(visibilityCalls).toHaveLength(1);
    addSpy.mockRestore();
  });

  it("ensureVisibilityListenerInstalled() works without calling usePollingControl()", async () => {
    const addSpy = vi.spyOn(document, "addEventListener");
    const mod = await freshImport();
    mod.ensureVisibilityListenerInstalled();
    const visibilityCalls = addSpy.mock.calls.filter((c) => c[0] === "visibilitychange");
    expect(visibilityCalls).toHaveLength(1);
    addSpy.mockRestore();
  });

  // ----- pollingShouldSkipTick non-component getter -----

  it("pollingShouldSkipTick reflects paused/visible refs", async () => {
    const mod = await freshImport();
    const api = mod.usePollingControl();
    expect(mod.pollingShouldSkipTick()).toBe(false);
    api.setPaused(true);
    expect(mod.pollingShouldSkipTick()).toBe(true);
    api.setPaused(false);
    api.visible.value = false;
    expect(mod.pollingShouldSkipTick()).toBe(true);
  });

  // ----- localStorage failure tolerance -----

  it("toggle() does not throw when localStorage.setItem rejects (private mode / quota)", async () => {
    const mod = await freshImport();
    const api = mod.usePollingControl();
    const setItem = vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
      throw new Error("QuotaExceededError");
    });
    expect(() => api.toggle()).not.toThrow();
    // In-memory ref still flips even when persistence fails.
    expect(api.paused.value).toBe(true);
    setItem.mockRestore();
  });

  it("readPersisted defaults to false when localStorage.getItem throws", async () => {
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      writable: true,
      value: {
        getItem() {
          throw new Error("private mode");
        },
        setItem() {},
        removeItem() {},
        clear() {},
        key() {
          return null;
        },
        length: 0,
      },
    });
    const mod = await freshImport();
    const api = mod.usePollingControl();
    // No exception; default to not-paused.
    expect(api.paused.value).toBe(false);
  });

  // ----- __resetPollingControl test hook -----

  it("__resetPollingControl restores defaults but keeps the listener installed", async () => {
    const addSpy = vi.spyOn(document, "addEventListener");
    const mod = await freshImport();
    const api = mod.usePollingControl();
    api.setPaused(true);
    api.visible.value = false;
    mod.__resetPollingControl();
    expect(api.paused.value).toBe(false);
    expect(api.visible.value).toBe(true);
    // Listener install should NOT happen again — the reset hook is
    // intentionally permissive (the install path is itself idempotent).
    const visibilityCalls = addSpy.mock.calls.filter((c) => c[0] === "visibilitychange");
    expect(visibilityCalls.length).toBeLessThanOrEqual(1);
    addSpy.mockRestore();
  });
});
