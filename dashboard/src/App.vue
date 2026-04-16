<script setup lang="ts">
import { RouterLink, RouterView } from "vue-router";
</script>

<template>
  <div class="app-shell">
    <header class="app-header">
      <h1 class="brand">nyx dashboard</h1>
      <nav class="nav">
        <RouterLink to="/" class="nav-link">Team</RouterLink>
      </nav>
      <span class="version" data-testid="dashboard-version">v0.1.0-alpha</span>
    </header>
    <main class="app-main">
      <RouterView />
    </main>
  </div>
</template>

<style>
:root {
  --nyx-bg: #0f1115;
  --nyx-fg: #e6e8ef;
  --nyx-muted: #8a8f9c;
  --nyx-accent: #7c6cff;
}

body {
  margin: 0;
  background: var(--nyx-bg);
  color: var(--nyx-fg);
  font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
}

.app-shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.app-header {
  display: flex;
  align-items: center;
  gap: 1.5rem;
  padding: 0.75rem 1.5rem;
  border-bottom: 1px solid #1b1f2a;
  background: #0c0e13;
}

.brand {
  font-size: 1.1rem;
  margin: 0;
  font-weight: 600;
}

.nav {
  display: flex;
  gap: 1rem;
  flex: 1;
}

.nav-link {
  color: var(--nyx-muted);
  text-decoration: none;
  font-size: 0.95rem;
}

.nav-link.router-link-active {
  color: var(--nyx-fg);
}

.version {
  color: var(--nyx-muted);
  font-size: 0.85rem;
}

.app-main {
  flex: 1;
  padding: 1.5rem;
}
</style>
