import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { effectScope, nextTick } from "vue";

// Unit tests for useTheme (#1106). The composable drives the header
// toggle button and the `data-theme` attribute on <html>, so stale
// state leaks between cases if the module-level refs aren't reset.
// Each spec resets localStorage + the module state and re-imports
// useTheme with a fresh module cache.

function freshImport() {
  vi.resetModules();
  return import("../../src/composables/useTheme");
}

// The default jsdom localStorage in this env does not implement the
// Storage interface methods (the node `--localstorage-file` flag is
// unset and jsdom's polyfill is missing). Install a tiny Map-backed
// stub before each test so the composable's getItem/setItem calls
// succeed and assertions can read back the stored value.
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

function run<T>(fn: () => T): T {
  const scope = effectScope();
  try {
    return scope.run(fn) as T;
  } finally {
    scope.stop();
  }
}

// jsdom ships a no-op matchMedia by default; supply a controllable
// stub so the "auto follows prefers-color-scheme" assertion is
// deterministic.
interface StubMQ {
  matches: boolean;
  addEventListener: () => void;
  removeEventListener: () => void;
  addListener: () => void;
  removeListener: () => void;
  media: string;
  onchange: null;
  dispatchEvent: () => boolean;
}

function installMatchMedia(prefersLight: boolean): void {
  const mq: StubMQ = {
    matches: prefersLight,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    media: "(prefers-color-scheme: light)",
    onchange: null,
    dispatchEvent: () => true,
  };
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    writable: true,
    value: (_q: string) => mq,
  });
}

describe("useTheme", () => {
  beforeEach(() => {
    installLocalStorage();
    document.documentElement.removeAttribute("data-theme");
    installMatchMedia(false);
  });

  afterEach(() => {
    installLocalStorage();
    document.documentElement.removeAttribute("data-theme");
  });

  it("returns the localStorage value when set, else auto", async () => {
    window.localStorage.setItem("witwave.theme", "light");
    const mod = await freshImport();
    const { theme } = run(() => mod.useTheme());
    expect(theme.value).toBe("light");
    // Module load should apply the persisted choice to <html>.
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");

    // Fresh module, no stored value → auto.
    installLocalStorage();
    document.documentElement.removeAttribute("data-theme");
    const mod2 = await freshImport();
    const { theme: theme2 } = run(() => mod2.useTheme());
    expect(theme2.value).toBe("auto");
  });

  it("setTheme writes to localStorage and applies data-theme to documentElement", async () => {
    const mod = await freshImport();
    const api = run(() => mod.useTheme());

    api.setTheme("dark");
    await nextTick();
    expect(window.localStorage.getItem("witwave.theme")).toBe("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");

    api.setTheme("light");
    await nextTick();
    expect(window.localStorage.getItem("witwave.theme")).toBe("light");
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");

    // Invalid value is ignored — current theme preserved.
    // Cast through unknown because the type signature refuses bad
    // strings at compile time; the runtime guard is what we're
    // exercising here.
    (api.setTheme as unknown as (v: string) => void)("not-a-theme");
    await nextTick();
    expect(window.localStorage.getItem("witwave.theme")).toBe("light");
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
  });

  it("in auto mode, matches prefers-color-scheme", async () => {
    // OS advertises light.
    installMatchMedia(true);
    const mod = await freshImport();
    const api = run(() => mod.useTheme());
    expect(api.theme.value).toBe("auto");
    expect(api.resolved.value).toBe("light");
    // Module-load side-effect should have applied the resolved value.
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");

    // OS advertises dark — reset modules so the module-scope ref
    // re-reads matchMedia on next import.
    document.documentElement.removeAttribute("data-theme");
    installMatchMedia(false);
    const mod2 = await freshImport();
    const api2 = run(() => mod2.useTheme());
    expect(api2.theme.value).toBe("auto");
    expect(api2.resolved.value).toBe("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
  });
});
