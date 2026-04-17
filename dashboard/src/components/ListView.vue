<script setup lang="ts" generic="T extends Record<string, unknown>">
import { computed, ref } from "vue";

// Generic "list with search + count + refresh" panel. Every parity view in
// Group A (Jobs, Tasks, Triggers, Webhooks, Continuations) collapses to a
// thin wrapper around this component. One table style, one search input,
// one count badge, one refresh button — consistent dashboard-wide.

interface Column<Row> {
  key: string;
  label: string;
  // Optional cell renderer. Returns either a string (rendered as text) or
  // an object { text, className } to style status-like cells.
  render?: (row: Row) => string | { text: string; class?: string };
  // Flag a column as "dim" (muted) vs "bright" (standout) visually.
  dim?: boolean;
  // Fixed width in px; columns without a width flex-fill.
  width?: number;
}

const props = defineProps<{
  title: string;
  items: T[];
  columns: Column<T>[];
  searchKeys: (keyof T)[];
  searchPlaceholder?: string;
  loading: boolean;
  error: string;
  emptyMessage?: string;
  // Optional map of agentId -> error string for agents whose per-agent fetch
  // failed in the most recent refresh. When non-empty, ListView renders a
  // compact "N agents degraded" badge in the toolbar with a tooltip listing
  // each failing agent. Keeps the current "tolerate individual errors"
  // behavior — the table still renders rows from reachable agents.
  perAgentErrors?: Record<string, string>;
  // Epoch-ms timestamp of the most recent successful refresh. When set,
  // ListView renders an "updated HH:MM:SS" label in the toolbar — matching
  // MetricsView's `updatedLabel`. Operators need this on every polled view
  // to tell a stale-looking count from a stalled poll, especially because
  // per-agent errors are tolerated silently by useAgentFanout.
  lastUpdated?: number | null;
}>();

const emit = defineEmits<{ (e: "refresh"): void }>();

const searchTerm = ref("");

const filtered = computed(() => {
  const q = searchTerm.value.trim().toLowerCase();
  if (!q) return props.items;
  return props.items.filter((row) =>
    props.searchKeys.some((k) =>
      String(row[k] ?? "")
        .toLowerCase()
        .includes(q),
    ),
  );
});

const degradedEntries = computed<[string, string][]>(() =>
  Object.entries(props.perAgentErrors ?? {}),
);

const degradedTooltip = computed(() =>
  degradedEntries.value
    .map(([agent, msg]) => `${agent}: ${msg}`)
    .join("\n"),
);

const updatedLabel = computed(() => {
  if (props.lastUpdated == null) return "";
  return `updated ${new Date(props.lastUpdated).toLocaleTimeString()}`;
});

function cellFor(row: T, col: Column<T>): { text: string; className: string } {
  if (col.render) {
    const out = col.render(row);
    if (typeof out === "string") return { text: out, className: "" };
    return { text: out.text, className: out.class ?? "" };
  }
  const v = row[col.key as keyof T];
  return { text: v == null ? "" : String(v), className: "" };
}
</script>

<template>
  <div class="list-view" :data-testid="`list-${title.toLowerCase()}`">
    <div class="toolbar">
      <h2 class="title">{{ title }}</h2>
      <input
        v-model="searchTerm"
        class="search"
        type="text"
        :placeholder="searchPlaceholder ?? `filter ${title.toLowerCase()}…`"
      />
      <span class="count">{{ filtered.length }} / {{ items.length }}</span>
      <span
        v-if="updatedLabel"
        class="ts"
        :data-testid="`list-${title.toLowerCase()}-updated`"
      >
        {{ updatedLabel }}
      </span>
      <span
        v-if="degradedEntries.length > 0"
        class="degraded"
        :title="degradedTooltip"
        :data-testid="`list-${title.toLowerCase()}-degraded`"
      >
        <i class="pi pi-exclamation-triangle" aria-hidden="true" />
        {{ degradedEntries.length }} degraded
      </span>
      <button
        class="refresh"
        type="button"
        :disabled="loading"
        @click="emit('refresh')"
      >
        <i class="pi pi-refresh" aria-hidden="true" />
        <span class="refresh-label">refresh</span>
      </button>
    </div>

    <div class="feed">
      <div v-if="loading && items.length === 0" class="state">Loading…</div>
      <div v-else-if="error && items.length === 0" class="state state-error">
        {{ error }}
      </div>
      <div v-else-if="filtered.length === 0" class="state">
        {{ items.length === 0 ? (emptyMessage ?? "Nothing here yet.") : "No matches." }}
      </div>
      <table v-else class="list-table">
        <thead>
          <tr>
            <th
              v-for="col in columns"
              :key="col.key"
              :style="col.width ? { width: col.width + 'px' } : undefined"
            >
              {{ col.label }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, i) in filtered" :key="i">
            <td
              v-for="col in columns"
              :key="col.key"
              :class="[
                col.dim ? 'cell-dim' : '',
                cellFor(row, col).className,
              ]"
            >
              {{ cellFor(row, col).text }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.list-view {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

.toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--nyx-border);
  background: var(--nyx-surface);
  flex-shrink: 0;
}

.title {
  font-size: 12px;
  color: var(--nyx-bright);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  margin: 0;
  font-weight: 600;
}

.search {
  flex: 1;
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  color: var(--nyx-text);
  font-family: var(--nyx-mono);
  font-size: 11px;
  padding: 4px 8px;
  border-radius: var(--nyx-radius);
}

.search:focus {
  outline: none;
  border-color: var(--nyx-accent);
}

.count {
  font-size: 10px;
  color: var(--nyx-dim);
}

.ts {
  font-size: 10px;
  color: var(--nyx-dim);
  white-space: nowrap;
}

.degraded {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 10px;
  color: var(--nyx-red);
  border: 1px solid var(--nyx-red);
  border-radius: var(--nyx-radius);
  padding: 2px 6px;
  cursor: help;
  white-space: nowrap;
}

.refresh {
  background: none;
  border: 1px solid var(--nyx-border);
  color: var(--nyx-dim);
  font-family: var(--nyx-mono);
  font-size: 11px;
  padding: 4px 10px;
  border-radius: var(--nyx-radius);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.refresh:hover:not(:disabled) {
  color: var(--nyx-text);
  border-color: var(--nyx-muted);
}

.refresh:disabled {
  opacity: 0.4;
  cursor: default;
}

.refresh-label {
  font-size: 11px;
}

.feed {
  flex: 1;
  overflow: auto;
  padding: 0;
}

.state {
  padding: 30px;
  color: var(--nyx-muted);
  font-size: 11px;
  text-align: center;
}

.state-error {
  color: var(--nyx-red);
}

.list-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 11px;
}

.list-table thead th {
  position: sticky;
  top: 0;
  background: var(--nyx-bg);
  border-bottom: 1px solid var(--nyx-border);
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  font-weight: 500;
  font-size: 10px;
  text-align: left;
  padding: 8px 12px;
  white-space: nowrap;
}

.list-table tbody tr {
  border-bottom: 1px solid var(--nyx-border);
  transition: background 0.1s;
}

.list-table tbody tr:hover {
  background: var(--nyx-surface);
}

.list-table tbody td {
  padding: 8px 12px;
  color: var(--nyx-text);
  vertical-align: top;
  word-break: break-word;
}

.cell-dim {
  color: var(--nyx-dim);
}

.cell-running {
  color: var(--nyx-green);
}

.cell-disabled {
  color: var(--nyx-red);
}

.cell-accent {
  color: var(--nyx-accent);
}
</style>
