<script setup lang="ts">
import { useRouter } from "vue-router";
import Menubar from "primevue/menubar";
import type { MenuItem } from "primevue/menuitem";

// App shell. Single nav item for now (Team) — other parity views (Jobs, Tasks,
// Triggers, Conversations, Metrics, …) get added to this list as they land.
// PrimeVue Menubar keeps the chrome consistent as we grow.

const router = useRouter();

const navItems: MenuItem[] = [
  {
    label: "Team",
    icon: "pi pi-users",
    command: () => router.push({ name: "team" }),
  },
];
</script>

<template>
  <div class="app-shell p-dark">
    <header class="app-header">
      <h1 class="brand">nyx</h1>
      <Menubar :model="navItems" class="app-nav" />
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
  flex: 1;
  background: transparent;
  border: none;
  padding: 0;
}

.app-nav :deep(.p-menubar-root-list) {
  gap: 2px;
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
