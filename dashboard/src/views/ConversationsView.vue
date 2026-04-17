<script setup lang="ts">
import { computed, ref } from "vue";
import { useAgentFanout } from "../composables/useAgentFanout";
import { renderMarkdown } from "../utils/markdown";
import type { ConversationEntry } from "../types/chat";

// Aggregated conversation feed across all team members. Legacy ui read only
// the front-door agent's log; with direct routing we fan out and merge.
// Filters mirror the legacy ones minus the tool filter (no tool data on the
// line level today — re-add when the harness exposes it).

type Row = ConversationEntry & { _agent: string };

const limit = ref<number>(100);
const searchTerm = ref<string>("");
const agentFilter = ref<string>("");
const roleFilter = ref<string>("");

const { items, loading, error, refresh } = useAgentFanout<ConversationEntry>({
  endpoint: "conversations",
  query: computed(() => ({ limit: String(limit.value) })).value,
});

const agentOptions = computed(() => {
  const set = new Set<string>();
  for (const i of items.value) set.add(i._agent);
  return Array.from(set).sort();
});

const sorted = computed(() =>
  [...items.value].sort((a, b) => (a.ts < b.ts ? 1 : a.ts > b.ts ? -1 : 0)),
);

const filtered = computed(() => {
  const q = searchTerm.value.trim().toLowerCase();
  return sorted.value.filter((row) => {
    if (agentFilter.value && row._agent !== agentFilter.value) return false;
    if (roleFilter.value && row.role !== roleFilter.value) return false;
    if (q && !(row.text ?? "").toLowerCase().includes(q)) return false;
    return true;
  });
});

function formatTs(ts: string): string {
  try {
    const d = new Date(ts);
    return d.toLocaleString();
  } catch {
    return ts;
  }
}
</script>

<template>
  <div class="conversations-view" data-testid="list-conversations">
    <div class="toolbar">
      <h2 class="title">Conversations</h2>
      <input
        v-model="searchTerm"
        class="search"
        type="text"
        placeholder="filter messages…"
      />
      <select v-model="agentFilter" class="select" aria-label="agent">
        <option value="">all agents</option>
        <option v-for="a in agentOptions" :key="a" :value="a">{{ a }}</option>
      </select>
      <select v-model="roleFilter" class="select" aria-label="role">
        <option value="">all roles</option>
        <option value="user">user</option>
        <option value="agent">agent</option>
        <option value="system">system</option>
      </select>
      <select v-model.number="limit" class="select" aria-label="limit">
        <option :value="50">50</option>
        <option :value="100">100</option>
        <option :value="250">250</option>
        <option :value="500">500</option>
      </select>
      <span class="count">{{ filtered.length }} / {{ items.length }}</span>
      <button class="refresh" type="button" :disabled="loading" @click="refresh">
        <i class="pi pi-refresh" aria-hidden="true" />
      </button>
    </div>

    <div class="feed">
      <div v-if="loading && items.length === 0" class="state">Loading…</div>
      <div v-else-if="error && items.length === 0" class="state state-error">
        {{ error }}
      </div>
      <div v-else-if="filtered.length === 0" class="state">No messages.</div>
      <div
        v-for="(row, i) in filtered"
        :key="i"
        class="cm"
        :class="row.role === 'user' ? 'user' : row.role === 'agent' ? 'agent' : 'other'"
      >
        <div class="meta">
          <span class="meta-ts">{{ formatTs(row.ts) }}</span>
          <span class="meta-role">{{ row.role }}</span>
          <span class="meta-agent">{{ row.agent }}</span>
          <span class="meta-team">@{{ row._agent }}</span>
          <span v-if="row.model" class="meta-model">{{ row.model }}</span>
        </div>
        <div
          v-if="row.role === 'agent'"
          class="bbl"
          v-html="renderMarkdown(row.text ?? '')"
        />
        <div v-else class="bbl">{{ row.text ?? "" }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.conversations-view {
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
  flex-wrap: wrap;
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
  min-width: 200px;
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  color: var(--nyx-text);
  font-family: var(--nyx-mono);
  font-size: 11px;
  padding: 4px 8px;
  border-radius: var(--nyx-radius);
}

.select {
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  color: var(--nyx-text);
  font-family: var(--nyx-mono);
  font-size: 11px;
  padding: 4px 8px;
  border-radius: var(--nyx-radius);
  cursor: pointer;
}

.search:focus,
.select:focus {
  outline: none;
  border-color: var(--nyx-accent);
}

.count {
  font-size: 10px;
  color: var(--nyx-dim);
}

.refresh {
  background: none;
  border: 1px solid var(--nyx-border);
  color: var(--nyx-dim);
  padding: 4px 10px;
  border-radius: var(--nyx-radius);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
}

.refresh:hover:not(:disabled) {
  color: var(--nyx-text);
  border-color: var(--nyx-muted);
}

.refresh:disabled {
  opacity: 0.4;
  cursor: default;
}

.feed {
  flex: 1;
  overflow-y: auto;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
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

.cm {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 90%;
}

.cm.user {
  align-self: flex-end;
  align-items: flex-end;
}

.cm.agent,
.cm.other {
  align-self: flex-start;
}

.meta {
  display: flex;
  gap: 10px;
  font-size: 10px;
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.meta-team {
  color: var(--nyx-accent);
}

.meta-model {
  color: var(--nyx-dim);
}

.bbl {
  background: var(--nyx-surface);
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  padding: 8px 12px;
  font-size: 12px;
  color: var(--nyx-text);
  line-height: 1.55;
  word-break: break-word;
}

/* pre-wrap only on plain-text roles; agent rows render markdown (marked +
   DOMPurify) which already emits block elements for paragraph breaks. */
.cm.user .bbl,
.cm.other .bbl {
  white-space: pre-wrap;
}

.cm.user .bbl {
  background: color-mix(in srgb, var(--nyx-accent) 18%, var(--nyx-surface));
  border-color: color-mix(in srgb, var(--nyx-accent) 35%, var(--nyx-border));
  color: var(--nyx-bright);
}

.cm.agent .bbl :deep(p) {
  margin: 0 0 6px;
}
.cm.agent .bbl :deep(p:last-child) {
  margin-bottom: 0;
}
.cm.agent .bbl :deep(h1),
.cm.agent .bbl :deep(h2),
.cm.agent .bbl :deep(h3) {
  font-size: 12px;
  color: var(--nyx-bright);
  margin: 8px 0 4px;
}
.cm.agent .bbl :deep(code) {
  background: var(--nyx-border);
  border-radius: 3px;
  padding: 1px 4px;
  font-size: 11px;
}
.cm.agent .bbl :deep(pre) {
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  padding: 8px 10px;
  overflow-x: auto;
}
.cm.agent .bbl :deep(a) {
  color: var(--nyx-accent);
  text-decoration: none;
}
</style>
