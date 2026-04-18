<script setup lang="ts">
import { RouterLink, RouterView } from "vue-router";
import { useHealth } from "./composables/useHealth";

// App shell. Simple button-style nav matching the legacy ui/ pattern —
// compact, dark-surface, one entry per view. Status dot in the header
// aggregates per-agent health across the team (#543): green when every
// member is reachable, amber when some are failing, red when all are
// down, gray during the first fan-out probe.

interface NavItem {
  label: string;
  to: { name: string };
}

const navItems: NavItem[] = [
  { label: "Team", to: { name: "team" } },
  // The previous Jobs / Tasks / Triggers / Webhooks / Continuations /
  // Heartbeat tabs collapsed into a single card-grouped Automation view
  // so the nav bar has room to breathe. Legacy routes redirect — see
  // router.ts for the full list.
  { label: "Automation", to: { name: "automation" } },
  { label: "Conversations", to: { name: "conversations" } },
  { label: "Tool Trace", to: { name: "trace" } },
  { label: "Tool audit", to: { name: "tool-audit" } },
  { label: "Traces", to: { name: "otel-traces" } },
  { label: "Metrics", to: { name: "metrics" } },
];

const { state, detail } = useHealth();
</script>

<template>
  <div class="app-shell p-dark">
    <header class="app-header">
      <h1 class="brand">nyx</h1>
      <nav class="app-nav">
        <RouterLink
          v-for="item in navItems"
          :key="item.label"
          :to="item.to"
          class="nav-link"
          active-class="is-active"
        >
          {{ item.label }}
        </RouterLink>
      </nav>
      <span
        class="status"
        :class="`status-${state}`"
        :title="detail || state"
        data-testid="header-status"
      >
        {{
          state === "connecting"
            ? "connecting…"
            : state === "ok"
              ? "online"
              : state === "partial"
                ? "degraded"
                : "offline"
        }}
      </span>
      <span class="version" data-testid="dashboard-version">v0.1.0-alpha</span>
    </header>
    <main class="app-main">
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
  border-bottom: 1px solid var(--nyx-border);
  background: var(--nyx-surface);
}

.brand {
  font-size: 1rem;
  letter-spacing: 0.25em;
  color: var(--nyx-bright);
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
  border-radius: var(--nyx-radius);
  color: var(--nyx-dim);
  font-family: var(--nyx-mono);
  font-size: 12px;
  letter-spacing: 0.04em;
  text-decoration: none;
  white-space: nowrap;
  transition: color 0.12s, background 0.12s;
}

.nav-link:hover {
  color: var(--nyx-text);
  background: var(--nyx-bg);
}

.nav-link.is-active {
  color: var(--nyx-bright);
  background: var(--nyx-bg);
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
  background: var(--nyx-muted);
}

.status-ok {
  color: var(--nyx-dim);
}

.status-ok::before {
  background: var(--nyx-green);
}

.status-partial {
  color: var(--nyx-yellow);
}

.status-partial::before {
  background: var(--nyx-yellow);
}

.status-err {
  color: var(--nyx-red);
}

.status-err::before {
  background: var(--nyx-red);
}

.status-connecting {
  color: var(--nyx-dim);
}

.version {
  color: var(--nyx-dim);
  font-size: 11px;
}

.app-main {
  flex: 1;
  overflow: hidden;
}
</style>
