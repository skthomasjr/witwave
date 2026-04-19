import { ref, computed, watch } from "vue";
import type { ComputedRef, Ref } from "vue";

// Theme toggle (#1106). Three states:
//   - "light"  — force the light palette
//   - "dark"   — force the dark palette (legacy default)
//   - "auto"   — follow the OS `prefers-color-scheme` media query
//
// The resolved palette is applied via `data-theme="light"|"dark"` on
// <html> so tokens.css can swap variables with a top-level attribute
// selector. The user choice is persisted in localStorage under
// `nyx.theme`; absence of a stored value defaults to "auto" so new
// visitors respect whatever their OS advertises.
//
// Module-level refs keep every composable consumer consistent: the
// header toggle button, any settings surface, and (eventually) a
// screen-reader announcement share a single source of truth.

export type ThemeChoice = "light" | "dark" | "auto";
export type ResolvedTheme = "light" | "dark";

const STORAGE_KEY = "nyx.theme";
const VALID: ReadonlySet<ThemeChoice> = new Set(["light", "dark", "auto"]);

function readPersisted(): ThemeChoice {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (raw && VALID.has(raw as ThemeChoice)) {
      return raw as ThemeChoice;
    }
  } catch {
    // localStorage blocked — fall through to auto.
  }
  return "auto";
}

function writePersisted(val: ThemeChoice): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, val);
  } catch {
    // Quota exceeded / private mode — silently ignore; the in-memory
    // value still drives the feature for this tab.
  }
}

function prefersLight(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return false;
  }
  try {
    return window.matchMedia("(prefers-color-scheme: light)").matches;
  } catch {
    return false;
  }
}

const choiceRef = ref<ThemeChoice>(readPersisted());
const systemPrefersLightRef = ref<boolean>(prefersLight());

// Singleton OS listener — installed the first time any consumer mounts
// the composable. Using addEventListener so we can remove cleanly when
// tests reset state.
let mqList: MediaQueryList | null = null;
let mqHandler: ((evt: MediaQueryListEvent) => void) | null = null;
function ensureSystemListener(): void {
  if (mqList !== null) return;
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return;
  }
  try {
    mqList = window.matchMedia("(prefers-color-scheme: light)");
    mqHandler = (evt) => {
      systemPrefersLightRef.value = evt.matches;
    };
    // Modern browsers implement addEventListener; the older
    // addListener shim is kept for Safari < 14.
    if (typeof mqList.addEventListener === "function") {
      mqList.addEventListener("change", mqHandler);
    } else if (typeof (mqList as MediaQueryList).addListener === "function") {
      (mqList as MediaQueryList).addListener(mqHandler);
    }
  } catch {
    mqList = null;
    mqHandler = null;
  }
}

function applyToDocument(resolved: ResolvedTheme): void {
  if (typeof document === "undefined") return;
  const el = document.documentElement;
  if (!el) return;
  el.setAttribute("data-theme", resolved);
}

function resolveChoice(
  choice: ThemeChoice,
  systemLight: boolean,
): ResolvedTheme {
  if (choice === "light") return "light";
  if (choice === "dark") return "dark";
  return systemLight ? "light" : "dark";
}

// Apply once at module load so the document reflects the stored choice
// before the Vue app mounts — avoids a flash of wrong theme.
applyToDocument(resolveChoice(choiceRef.value, systemPrefersLightRef.value));

// Install the OS listener at module load so an OS-level toggle during
// page load / before the first component mounts doesn't get dropped.
// Previously we deferred this until `useTheme()` was called, which is
// first-mount-time in practice and misses anyone flipping their OS
// theme in the few hundred ms between module eval and the header
// rendering. (#1162)
ensureSystemListener();

// Keep documentElement in sync whenever the choice OR the OS preference
// changes. Module-level watch so the side effect fires regardless of
// which component happens to instantiate the composable.
watch(
  [choiceRef, systemPrefersLightRef],
  ([choice, systemLight]) => {
    applyToDocument(resolveChoice(choice, systemLight));
  },
  { flush: "sync" },
);

export interface UseThemeApi {
  theme: Ref<ThemeChoice>;
  resolved: ComputedRef<ResolvedTheme>;
  setTheme(choice: ThemeChoice): void;
  cycleTheme(): void;
}

export function useTheme(): UseThemeApi {
  ensureSystemListener();
  return {
    theme: choiceRef,
    resolved: computed(() =>
      resolveChoice(choiceRef.value, systemPrefersLightRef.value),
    ),
    setTheme(choice: ThemeChoice) {
      if (!VALID.has(choice)) return;
      choiceRef.value = choice;
      writePersisted(choice);
    },
    cycleTheme() {
      const order: ThemeChoice[] = ["auto", "light", "dark"];
      const idx = order.indexOf(choiceRef.value);
      const next = order[(idx + 1) % order.length];
      choiceRef.value = next;
      writePersisted(next);
    },
  };
}

// Test-only hook — unit tests reset module state between cases.
export function __resetThemeForTests(): void {
  try {
    window.localStorage.removeItem(STORAGE_KEY);
  } catch {
    // ignore
  }
  if (mqList && mqHandler) {
    try {
      if (typeof mqList.removeEventListener === "function") {
        mqList.removeEventListener("change", mqHandler);
      } else if (typeof (mqList as MediaQueryList).removeListener === "function") {
        (mqList as MediaQueryList).removeListener(mqHandler);
      }
    } catch {
      // ignore
    }
  }
  mqList = null;
  mqHandler = null;
  choiceRef.value = "auto";
  systemPrefersLightRef.value = prefersLight();
  if (typeof document !== "undefined" && document.documentElement) {
    document.documentElement.removeAttribute("data-theme");
  }
}
