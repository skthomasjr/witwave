<script setup lang="ts">
import { computed, ref } from "vue";
import { useAgentFanout } from "../composables/useAgentFanout";
import PromptCard, { type PromptKind } from "../components/PromptCard.vue";
import ConversationDrawer from "../components/ConversationDrawer.vue";
import { formatShortTime, toIsoDateTime } from "../utils/intl";
import type {
  Continuation,
  Heartbeat,
  Job,
  Task,
  Trigger,
  Webhook,
} from "../types/scheduler";

// Unified "Automation" view (#prompt-cards-v1). Replaces the six
// separate nav tabs for jobs, tasks, triggers, webhooks, continuations,
// and heartbeat — every prompt kind is one conceptual thing (a file in
// `.nyx/<kind>/` OR a NyxPrompt CR, describing when/how to fire a
// prompt). Operators see the whole automation posture of the team in
// one scroll, grouped by kind, and can click any card to inspect the
// conversation that came out of its session.
//
// Future enhancements parked by design:
//   - arrow/graph overlay for continuations showing the upstream chain
//   - table-mode toggle (cards are fine at current scale; revisit when
//     an install has >100 prompts of a single kind)
//   - per-kind outcome chips pulled from /metrics (last run, last error)

// ── Fan-out per kind ──────────────────────────────────────────────────────
// Each endpoint still exists on the harness; we just consume them all
// together. Fan-out stays per-kind so errors bubble up cleanly (one
// slow endpoint doesn't block the others).

const jobsFan = useAgentFanout<Job>({ endpoint: "jobs" });
const tasksFan = useAgentFanout<Task>({ endpoint: "tasks" });
const triggersFan = useAgentFanout<Trigger>({ endpoint: "triggers" });
const webhooksFan = useAgentFanout<Webhook>({ endpoint: "webhooks" });
const continuationsFan = useAgentFanout<Continuation>({
  endpoint: "continuations",
});
const heartbeatFan = useAgentFanout<Heartbeat>({ endpoint: "heartbeat" });

// ── Toolbar filters ──────────────────────────────────────────────────────
const searchTerm = ref("");
const agentFilter = ref<string>("");
// Disabled items are listed by default (users want visibility into what's
// parked) but can be hidden via this toggle. Stored in component state,
// not localStorage — we want every fresh visit to start inclusive.
const showDisabled = ref<boolean>(true);
const activeKinds = ref<Record<PromptKind, boolean>>({
  job: true,
  task: true,
  trigger: true,
  webhook: true,
  continuation: true,
  heartbeat: true,
});

function toggleKind(kind: PromptKind) {
  activeKinds.value[kind] = !activeKinds.value[kind];
}
function showAll() {
  (Object.keys(activeKinds.value) as PromptKind[]).forEach((k) => {
    activeKinds.value[k] = true;
  });
}
function showOnly(kind: PromptKind) {
  (Object.keys(activeKinds.value) as PromptKind[]).forEach((k) => {
    activeKinds.value[k] = k === kind;
  });
}

// Predicate stack — order is: agent-filter → disabled-toggle → text search.
function matchesAgent(item: Record<string, unknown>): boolean {
  if (!agentFilter.value) return true;
  return item._agent === agentFilter.value;
}
function matchesDisabled(item: Record<string, unknown>): boolean {
  if (showDisabled.value) return true;
  return item.enabled !== false;
}
function matchesSearch(item: Record<string, unknown>): boolean {
  const q = searchTerm.value.trim().toLowerCase();
  if (!q) return true;
  const haystack = [
    item.name,
    item._agent,
    item.schedule,
    item.url,
    item.endpoint,
    item.backend_id,
    item.description,
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  return haystack.includes(q);
}
function passes(item: Record<string, unknown>): boolean {
  return matchesAgent(item) && matchesDisabled(item) && matchesSearch(item);
}

// Union of all agent names across all six fan-outs so the agent filter
// dropdown stays synced with whatever the team currently holds.
const agentOptions = computed<string[]>(() => {
  const set = new Set<string>();
  for (const src of [
    jobsFan.items.value,
    tasksFan.items.value,
    triggersFan.items.value,
    webhooksFan.items.value,
    continuationsFan.items.value,
    heartbeatFan.items.value,
  ]) {
    for (const i of src as Array<{ _agent?: string }>) {
      if (i._agent) set.add(i._agent);
    }
  }
  return Array.from(set).sort();
});

// Per-section cards after filtering.
const sections = computed(() => {
  const raw = [
    { kind: "job" as PromptKind, title: "Jobs", items: jobsFan.items.value },
    { kind: "task" as PromptKind, title: "Tasks", items: tasksFan.items.value },
    { kind: "trigger" as PromptKind, title: "Triggers", items: triggersFan.items.value },
    { kind: "webhook" as PromptKind, title: "Webhooks", items: webhooksFan.items.value },
    {
      kind: "continuation" as PromptKind,
      title: "Continuations",
      items: continuationsFan.items.value,
    },
    {
      kind: "heartbeat" as PromptKind,
      title: "Heartbeat",
      items: heartbeatFan.items.value,
    },
  ];
  return raw.map((s) => ({
    ...s,
    items: activeKinds.value[s.kind]
      ? s.items.filter((it) => passes(it as unknown as Record<string, unknown>))
      : [],
  }));
});

const totalCount = computed(() =>
  sections.value.reduce((n, s) => n + s.items.length, 0),
);

// Aggregate error + loading state across the six fan-outs. Any agent
// that failed on ANY endpoint shows up in the degraded tooltip.
const allErrors = computed<Record<string, string>>(() => {
  const out: Record<string, string> = {};
  const sources = [
    jobsFan,
    tasksFan,
    triggersFan,
    webhooksFan,
    continuationsFan,
    heartbeatFan,
  ];
  for (const s of sources) {
    for (const [agent, msg] of Object.entries(s.perAgentErrors.value)) {
      if (!out[agent]) out[agent] = msg;
    }
  }
  return out;
});

const isLoading = computed(
  () =>
    jobsFan.loading.value ||
    tasksFan.loading.value ||
    triggersFan.loading.value ||
    webhooksFan.loading.value ||
    continuationsFan.loading.value ||
    heartbeatFan.loading.value,
);

const latestUpdate = computed<number | null>(() => {
  const xs = [
    jobsFan.lastUpdated.value,
    tasksFan.lastUpdated.value,
    triggersFan.lastUpdated.value,
    webhooksFan.lastUpdated.value,
    continuationsFan.lastUpdated.value,
    heartbeatFan.lastUpdated.value,
  ].filter((x): x is number => x !== null);
  return xs.length ? Math.max(...xs) : null;
});
// Locale-aware "updated HH:MM" label (#827). Formatters live in
// src/utils/intl.ts so a future i18n wiring (#819) can thread an
// explicit locale in at one seam.
const updatedLabel = computed(() =>
  latestUpdate.value ? `updated ${formatShortTime(latestUpdate.value)}` : "",
);
// ISO-8601 datetime for the <time datetime="..."> attribute so screen
// readers and tools that ingest HTML semantics can parse the timestamp.
const updatedIso = computed(() =>
  latestUpdate.value ? toIsoDateTime(latestUpdate.value) : "",
);

const degradedEntries = computed<[string, string][]>(() =>
  Object.entries(allErrors.value),
);
const degradedTooltip = computed(() =>
  degradedEntries.value.map(([a, m]) => `${a}: ${m}`).join("\n"),
);

function refreshAll() {
  jobsFan.refresh();
  tasksFan.refresh();
  triggersFan.refresh();
  webhooksFan.refresh();
  continuationsFan.refresh();
  heartbeatFan.refresh();
}

// ── Click → conversation drawer ──────────────────────────────────────────
interface DrawerTarget {
  kind: PromptKind;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  item: any;
}
const drawerTarget = ref<DrawerTarget | null>(null);

function openDrawer(kind: PromptKind, item: unknown) {
  drawerTarget.value = { kind, item };
}
function closeDrawer() {
  drawerTarget.value = null;
}

const drawerOpen = computed(() => drawerTarget.value !== null);
const drawerTitle = computed(() => {
  const t = drawerTarget.value;
  if (!t) return "";
  if (t.kind === "heartbeat") return `${t.item._agent}/heartbeat`;
  return `${t.kind}/${t.item.name ?? "(unnamed)"}`;
});
const drawerSessionId = computed<string | null>(() => {
  const t = drawerTarget.value;
  if (!t) return null;
  // Heartbeat's session is derived server-side as
  // uuid5(NAMESPACE_URL, "<agent>.heartbeat"). We don't have a stable
  // way to reproduce it client-side (different uuid5 implementations),
  // so fall back to showing all recent conversations when sessionId is
  // null — the drawer handles that gracefully.
  return (t.item.session_id as string | undefined) ?? null;
});
const drawerAgent = computed<string | null>(
  () => (drawerTarget.value?.item._agent as string | undefined) ?? null,
);
</script>

<template>
  <div class="automation-view" data-testid="list-automation">
    <div class="toolbar">
      <h2 class="title">Automation</h2>
      <input
        v-model="searchTerm"
        class="search"
        type="text"
        placeholder="filter prompts…"
      />
      <select
        v-model="agentFilter"
        class="select"
        aria-label="agent filter"
        title="Filter by agent"
      >
        <option value="">all agents</option>
        <option v-for="a in agentOptions" :key="a" :value="a">{{ a }}</option>
      </select>
      <label class="toggle" title="Hide disabled prompts">
        <input v-model="showDisabled" type="checkbox" />
        <span>show disabled</span>
      </label>
      <!-- Kind-filter pills expose aria-pressed so assistive tech can
           report which kinds are visible. Keyboard shortcut Shift+Enter
           on a focused pill isolates that kind (parity with shift+click);
           bare Enter/Space toggles. (#821) -->
      <div class="kind-filters" role="group" aria-label="kind filters">
        <button
          v-for="k in ['job', 'task', 'trigger', 'webhook', 'continuation', 'heartbeat'] as PromptKind[]"
          :key="k"
          type="button"
          class="kind-pill"
          :class="{ active: activeKinds[k] }"
          :data-kind="k"
          :aria-pressed="activeKinds[k] ? 'true' : 'false'"
          :aria-label="`toggle ${k} (shift+enter isolates)`"
          :title="`Click to toggle — shift+click or shift+enter to isolate`"
          @click="(ev: MouseEvent) => (ev.shiftKey ? showOnly(k) : toggleKind(k))"
          @keydown.shift.enter.prevent="showOnly(k)"
        >
          {{ k }}
        </button>
        <button
          type="button"
          class="kind-pill reset"
          title="Show all kinds"
          @click="showAll"
        >
          all
        </button>
      </div>
      <time v-if="updatedLabel" class="ts" :datetime="updatedIso">{{ updatedLabel }}</time>
      <span
        v-if="degradedEntries.length > 0"
        class="degraded"
        :title="degradedTooltip"
      >
        <i class="pi pi-exclamation-triangle" aria-hidden="true" />
        {{ degradedEntries.length }} scrape failed
      </span>
      <button class="refresh" type="button" :disabled="isLoading" @click="refreshAll">
        <i class="pi pi-refresh" aria-hidden="true" />
        <span>refresh</span>
      </button>
    </div>

    <div class="scroll">
      <div v-if="isLoading && totalCount === 0" class="state">Loading…</div>
      <div v-else-if="totalCount === 0" class="state">
        No prompts match the current filter.
      </div>
      <template v-else>
        <section
          v-for="sec in sections"
          v-show="sec.items.length > 0"
          :key="sec.kind"
          class="section"
          :data-kind="sec.kind"
        >
          <div class="section-head">
            <h3 class="section-title">{{ sec.title }}</h3>
            <span class="section-count">{{ sec.items.length }}</span>
          </div>
          <div class="grid">
            <PromptCard
              v-for="(it, i) in sec.items"
              :key="`${sec.kind}-${(it as any)._agent}-${(it as any).name ?? i}`"
              :kind="sec.kind"
              :item="it"
              @click="openDrawer(sec.kind, it)"
            />
          </div>
        </section>
      </template>
    </div>

    <ConversationDrawer
      :open="drawerOpen"
      :agent="drawerAgent"
      :session-id="drawerSessionId"
      :title="drawerTitle"
      @close="closeDrawer"
    />
  </div>
</template>

<style scoped>
.automation-view {
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
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  color: var(--nyx-text);
  font-family: var(--nyx-mono);
  font-size: 11px;
  padding: 5px 8px;
  border-radius: var(--nyx-radius);
  width: 200px;
}
.search:focus {
  outline: none;
  border-color: var(--nyx-accent);
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
.select:focus {
  outline: none;
  border-color: var(--nyx-accent);
}

.toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 10px;
  color: var(--nyx-dim);
  font-family: var(--nyx-mono);
  text-transform: lowercase;
  cursor: pointer;
}
.toggle input {
  accent-color: var(--nyx-accent, #7c6af7);
  cursor: pointer;
}

.kind-filters {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}

.kind-pill {
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  color: var(--nyx-dim);
  font-family: var(--nyx-mono);
  font-size: 10px;
  padding: 3px 10px;
  border-radius: 12px;
  cursor: pointer;
  text-transform: lowercase;
  letter-spacing: 0.05em;
}
.kind-pill:hover {
  color: var(--nyx-text);
  border-color: var(--nyx-muted);
}
.kind-pill.active {
  color: var(--nyx-bright);
  background: var(--nyx-surface);
  border-color: var(--nyx-muted);
}
.kind-pill.active[data-kind="job"] {
  border-color: #7c6af7;
  color: #7c6af7;
}
.kind-pill.active[data-kind="task"] {
  border-color: #3ecfcf;
  color: #3ecfcf;
}
.kind-pill.active[data-kind="trigger"] {
  border-color: #fbbf24;
  color: #fbbf24;
}
.kind-pill.active[data-kind="webhook"] {
  border-color: #fb923c;
  color: #fb923c;
}
.kind-pill.active[data-kind="continuation"] {
  border-color: #a78bfa;
  color: #a78bfa;
}
.kind-pill.active[data-kind="heartbeat"] {
  border-color: #4ade80;
  color: #4ade80;
}
.kind-pill.reset {
  color: var(--nyx-dim);
}

.ts {
  font-size: 11px;
  color: var(--nyx-dim);
  margin-left: auto;
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

.scroll {
  flex: 1;
  overflow-y: auto;
  padding: 18px;
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.state {
  padding: 30px;
  color: var(--nyx-muted);
  font-size: 12px;
  text-align: center;
  font-family: var(--nyx-mono);
}

.section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.section-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  border-bottom: 1px solid var(--nyx-border);
  padding-bottom: 6px;
}
.section-title {
  font-size: 11px;
  color: var(--nyx-bright);
  text-transform: uppercase;
  letter-spacing: 0.09em;
  margin: 0;
  font-weight: 700;
}
.section-count {
  font-size: 10px;
  color: var(--nyx-dim);
  background: var(--nyx-surface);
  border: 1px solid var(--nyx-border);
  padding: 1px 8px;
  border-radius: 10px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 10px;
}
</style>
