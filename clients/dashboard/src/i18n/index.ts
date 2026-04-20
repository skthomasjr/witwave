import { createI18n } from "vue-i18n";
import en from "./locales/en.json";

// Dashboard i18n bootstrap (#819). Current state is "English-only, but
// wired end-to-end" — every string added through the `t()` function
// survives the eventual move to additional locales without needing a
// code refactor. Follow-up passes will sweep the rest of App.vue,
// views, and composables onto keys.
//
// Locale resolution order:
//   1. `VITE_LOCALE` build-time env (for CI builds or opinionated
//      deployments)
//   2. `window.__WITWAVE_CONFIG__.locale` runtime injection (for helm/
//      configmap-driven deploys)
//   3. Browser `navigator.language`
//   4. Fallback `en`
//
// Only locales present in `messages` are honoured; unknown values
// fall through to `en`.

export type SupportedLocale = "en";

const messages = { en } as const;

function detectLocale(): SupportedLocale {
  // Build-time env wins when explicitly set.
  const envLocale =
    typeof import.meta !== "undefined" &&
    typeof import.meta.env !== "undefined"
      ? (import.meta.env.VITE_LOCALE as string | undefined)
      : undefined;
  if (envLocale && envLocale in messages) {
    return envLocale as SupportedLocale;
  }
  if (typeof window !== "undefined") {
    const runtime = (window as unknown as {
      __WITWAVE_CONFIG__?: { locale?: string };
    }).__WITWAVE_CONFIG__?.locale;
    if (runtime && runtime in messages) {
      return runtime as SupportedLocale;
    }
    const nav = window.navigator?.language?.slice(0, 2);
    if (nav && nav in messages) {
      return nav as SupportedLocale;
    }
  }
  return "en";
}

export const i18n = createI18n({
  // legacy: false enables Composition API `useI18n()`.
  legacy: false,
  locale: detectLocale(),
  fallbackLocale: "en",
  messages,
  // Silence the "Not found key" warnings in production; dev keeps them.
  missingWarn:
    typeof import.meta !== "undefined" && import.meta.env?.DEV !== false,
  fallbackWarn:
    typeof import.meta !== "undefined" && import.meta.env?.DEV !== false,
});
