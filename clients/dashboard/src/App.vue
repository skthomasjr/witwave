<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { RouterLink, RouterView } from "vue-router";
import { useHealth } from "./composables/useHealth";
import { usePollingControl } from "./composables/usePollingControl";
import { useTheme } from "./composables/useTheme";
import AlertBanner from "./components/AlertBanner.vue";

// App shell. Simple button-style nav matching the legacy ui/ pattern —
// compact, dark-surface, one entry per view. Status dot in the header
// aggregates per-agent health across the team (#543): green when every
// member is reachable, amber when some are failing, red when all are
// down, gray during the first fan-out probe.

const { t } = useI18n();

interface NavItem {
  labelKey: string;
  to: { name: string };
}

const navSchema: NavItem[] = [
  { labelKey: "nav.team", to: { name: "team" } },
  // The previous Jobs / Tasks / Triggers / Webhooks / Continuations /
  // Heartbeat tabs collapsed into a single card-grouped Automation view
  // so the nav bar has room to breathe. Legacy routes redirect — see
  // router.ts for the full list.
  { labelKey: "nav.automation", to: { name: "automation" } },
  { labelKey: "nav.conversations", to: { name: "conversations" } },
  { labelKey: "nav.trace", to: { name: "trace" } },
  { labelKey: "nav.otelTraces", to: { name: "otel-traces" } },
  { labelKey: "nav.metrics", to: { name: "metrics" } },
  { labelKey: "nav.timeline", to: { name: "timeline" } },
];

const navItems = computed(() => navSchema.map((n) => ({ label: t(n.labelKey), to: n.to })));

const { state, detail } = useHealth();
// Global pause toggle (#1107). Setting `paused=true` tells every polling
// composable to skip its tick; the tab-visibility guard auto-pauses when
// the tab isn't visible so background tabs don't spam fan-out requests.
const { paused, visible, toggle: togglePolling } = usePollingControl();

// Theme toggle (#1106). Cycles through auto → light → dark; the
// composable also resolves the effective palette so the button icon
// can reflect the currently-applied appearance in auto mode.
const { theme, resolved: resolvedTheme, cycleTheme } = useTheme();

const themeIcon = computed(() => {
  if (theme.value === "auto") return "pi pi-desktop";
  return resolvedTheme.value === "light" ? "pi pi-sun" : "pi pi-moon";
});

const themeTitle = computed(() => {
  if (theme.value === "auto") {
    return t("theme.autoTitle", { resolved: resolvedTheme.value });
  }
  if (theme.value === "light") return t("theme.lightTitle");
  return t("theme.darkTitle");
});

const themeLabel = computed(() => {
  if (theme.value === "auto") return t("theme.autoLabel");
  if (theme.value === "light") return t("theme.lightLabel");
  return t("theme.darkLabel");
});
</script>

<template>
  <div class="app-shell">
    <!-- Skip-to-main link (#822): first focusable element in tab order so
         keyboard users can jump past the nav rail. Hidden off-screen via
         tokens.css until it receives focus. -->
    <a href="#app-main" class="skip-to-main">Skip to main content</a>
    <header class="app-header">
      <h1 class="brand">witwave</h1>
      <nav class="app-nav">
        <RouterLink v-for="item in navItems" :key="item.label" :to="item.to" class="nav-link" active-class="is-active">
          {{ item.label }}
        </RouterLink>
      </nav>
      <!-- Screen readers hear the team transitioning online/degraded/offline
           via role=status + aria-live=polite (#820). Gated on same element
           so downstream test hooks stay valid. -->
      <span
        class="status"
        :class="`status-${state}`"
        :title="detail || state"
        data-testid="header-status"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        {{
          state === "connecting"
            ? t("status.connecting")
            : state === "ok"
              ? t("status.online")
              : state === "partial"
                ? t("status.degraded")
                : state === "empty"
                  ? t("status.noAgents")
                  : t("status.offline")
        }}
      </span>
      <button
        class="pause-toggle"
        type="button"
        :class="{ 'is-paused': paused, 'is-hidden': !visible }"
        :aria-pressed="paused"
        :title="
          paused
            ? 'Auto-refresh paused — click to resume'
            : visible
              ? 'Auto-refresh on — click to pause'
              : 'Auto-refresh paused (tab hidden)'
        "
        data-testid="header-pause-toggle"
        @click="togglePolling"
      >
        <i :class="paused || !visible ? 'pi pi-pause' : 'pi pi-play'" aria-hidden="true" />
        <span class="pause-label">
          {{ paused ? "paused" : !visible ? "hidden" : "live" }}
        </span>
      </button>
      <button
        class="theme-toggle"
        type="button"
        :title="themeTitle"
        :aria-label="themeTitle"
        data-testid="header-theme-toggle"
        @click="cycleTheme"
      >
        <i :class="themeIcon" aria-hidden="true" />
        <span class="theme-label">{{ themeLabel }}</span>
      </button>
      <span class="version" data-testid="dashboard-version">v0.1.0-alpha</span>
    </header>
    <AlertBanner />
    <main id="app-main" class="app-main" tabindex="-1">
      <RouterView />
    </main>
  </div>
</template>

<style>
.app-shell {
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
}

.app-header {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 0 18px;
  height: 46px;
  flex-shrink: 0;
  border-bottom: 1px solid var(--witwave-border);
  background: var(--witwave-surface);
}

.brand {
  font-size: 1rem;
  letter-spacing: 0.25em;
  color: var(--witwave-bright);
  margin: 0;
  font-weight: 600;
}

.app-nav {
  display: flex;
  gap: 2px;
  flex: 1;
  overflow-x: auto;
}

.nav-link {
  background: none;
  border: none;
  cursor: pointer;
  padding: 5px 13px;
  border-radius: var(--witwave-radius);
  color: var(--witwave-dim);
  font-family: var(--witwave-mono);
  font-size: 12px;
  letter-spacing: 0.04em;
  text-decoration: none;
  white-space: nowrap;
  transition:
    color 0.12s,
    background 0.12s;
}

.nav-link:hover {
  color: var(--witwave-text);
  background: var(--witwave-bg);
}

.nav-link.is-active {
  color: var(--witwave-bright);
  background: var(--witwave-bg);
}

.status {
  font-size: 11px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.status::before {
  content: "";
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--witwave-muted);
}

.status-ok {
  color: var(--witwave-dim);
}

.status-ok::before {
  background: var(--witwave-green);
}

.status-partial {
  color: var(--witwave-yellow);
}

.status-partial::before {
  background: var(--witwave-yellow);
}

.status-err {
  color: var(--witwave-red);
}

.status-err::before {
  background: var(--witwave-red);
}

.status-connecting {
  color: var(--witwave-dim);
}

.version {
  color: var(--witwave-dim);
  font-size: 11px;
}

.pause-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  background: none;
  border: 1px solid var(--witwave-border);
  border-radius: var(--witwave-radius);
  color: var(--witwave-dim);
  cursor: pointer;
  padding: 3px 8px;
  font-family: var(--witwave-mono);
  font-size: 11px;
  letter-spacing: 0.04em;
  transition:
    color 0.12s,
    background 0.12s,
    border-color 0.12s;
}

.pause-toggle:hover {
  color: var(--witwave-text);
  background: var(--witwave-bg);
}

.pause-toggle.is-paused {
  color: var(--witwave-yellow);
  border-color: var(--witwave-yellow);
}

.pause-toggle.is-hidden {
  color: var(--witwave-muted);
}

.pause-label {
  font-size: 11px;
}

.theme-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  background: none;
  border: 1px solid var(--witwave-border);
  border-radius: var(--witwave-radius);
  color: var(--witwave-dim);
  cursor: pointer;
  padding: 3px 8px;
  font-family: var(--witwave-mono);
  font-size: 11px;
  letter-spacing: 0.04em;
  transition:
    color 0.12s,
    background 0.12s,
    border-color 0.12s;
}

.theme-toggle:hover {
  color: var(--witwave-text);
  background: var(--witwave-bg);
}

.theme-label {
  font-size: 11px;
}

.app-main {
  flex: 1;
  overflow: hidden;
}
</style>
