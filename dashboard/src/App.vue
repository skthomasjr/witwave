<script setup lang="ts">
import { RouterLink, RouterView } from "vue-router";

// App shell. Simple button-style nav matching the legacy ui/ pattern —
// compact, dark-surface, one entry per view. Reintroduce PrimeVue Menubar
// when the view count grows enough to need overflow management (#470).

interface NavItem {
  label: string;
  to: { name: string };
}

const navItems: NavItem[] = [{ label: "Team", to: { name: "team" } }];
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

.version {
  color: var(--nyx-dim);
  font-size: 11px;
}

.app-main {
  flex: 1;
  overflow: hidden;
}
</style>
