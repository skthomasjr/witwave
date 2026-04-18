/**
 * Locale-aware formatters (#827).
 *
 * Centralises `Intl.DateTimeFormat` / `Intl.NumberFormat` construction so
 * dashboard views render dates and counts consistently, and are ready to
 * pick up a user-selected locale once the i18n framework (#819) lands.
 * Until then the formatters follow the browser's runtime locale.
 *
 * Keep the surface intentionally small — call-sites care about
 * "short time", "medium date", and "grouped integer". Expand as real
 * views need more shapes.
 */

// Browser-negotiated locale. Components can pass an explicit override
// when the app-level locale gets threaded through (#819).
function resolveLocale(override?: string): string | string[] | undefined {
  if (override && override.length > 0) return override;
  if (typeof navigator !== "undefined" && Array.isArray(navigator.languages) && navigator.languages.length > 0) {
    return navigator.languages as string[];
  }
  return undefined;
}

/** Short time (hours:minutes) formatted for the resolved locale. */
export function formatShortTime(ts: number | Date, locale?: string): string {
  const d = ts instanceof Date ? ts : new Date(ts);
  return new Intl.DateTimeFormat(resolveLocale(locale), {
    hour: "numeric",
    minute: "2-digit",
  }).format(d);
}

/** Medium date-time (YYYY-MM-DD HH:mm:ss-ish, locale-aware). */
export function formatDateTime(ts: number | Date, locale?: string): string {
  const d = ts instanceof Date ? ts : new Date(ts);
  return new Intl.DateTimeFormat(resolveLocale(locale), {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(d);
}

/** ISO-8601 string suitable for a `<time datetime="...">` attribute. */
export function toIsoDateTime(ts: number | Date): string {
  const d = ts instanceof Date ? ts : new Date(ts);
  return d.toISOString();
}

/** Grouped integer (e.g. "1,234") in the resolved locale. */
export function formatInteger(n: number, locale?: string): string {
  return new Intl.NumberFormat(resolveLocale(locale), {
    maximumFractionDigits: 0,
  }).format(n);
}
