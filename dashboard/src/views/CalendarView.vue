<script setup lang="ts">
import { computed, ref } from "vue";
// @ts-expect-error — vue-cal's package.json exports a default-import Vue SFC
// without bundled .d.ts for this entry. The component contract we use is
// narrow (props, event handlers) and exercised at runtime, so the `any`-ish
// surface is fine here.
import VueCal from "vue-cal";
import "vue-cal/dist/vuecal.css";
import { useAgentFanout } from "../composables/useAgentFanout";
import type { ConversationEntry } from "../types/chat";

// Conversation timeline — every conversation log row from every agent,
// rendered on a week/day/month calendar. Per-agent color keeps swimlane
// identification visual; clicking an event opens a side panel with the
// full text. Vue-cal owns the grid; we just feed it events and let the
// user pick a view.
//
// This replaces the old "upcoming scheduled fires" list. We kept the idea
// of `schedules → table` around long enough to prove people actually
// wanted the log-as-events view instead — if we miss the schedule view,
// reintroduce it behind a /schedules route.

type Row = ConversationEntry & { _agent: string };

const { items, perAgentErrors, loading, error, refresh } = useAgentFanout<ConversationEntry>({
  endpoint: "conversations",
  query: { limit: "500" },
});

const degradedEntries = computed<[string, string][]>(() =>
  Object.entries(perAgentErrors.value),
);
const degradedTooltip = computed(() =>
  degradedEntries.value.map(([a, m]) => `${a}: ${m}`).join("\n"),
);

// Agents get stable colors — matches the nyx palette and avoids churn when
// the team list changes order.
const AGENT_PALETTE = [
  "#7c6af7",
  "#3ecfcf",
  "#4ade80",
  "#fbbf24",
  "#f87171",
  "#fb923c",
  "#a78bfa",
  "#34d399",
];

const agentColor = computed(() => {
  const ordered = [...new Set(items.value.map((r) => r._agent))].sort();
  const map = new Map<string, string>();
  ordered.forEach((name, i) => map.set(name, AGENT_PALETTE[i % AGENT_PALETTE.length]));
  return map;
});

interface CalEvent {
  start: Date;
  end: Date;
  title: string;
  content: string;
  class: string;
  background?: boolean;
  _raw: Row;
  _color: string;
}

function truncate(s: string, n: number): string {
  const t = (s ?? "").replace(/\s+/g, " ").trim();
  return t.length > n ? `${t.slice(0, n - 1)}…` : t;
}

const events = computed<CalEvent[]>(() => {
  const out: CalEvent[] = [];
  for (const row of items.value) {
    const start = new Date(row.ts);
    if (Number.isNaN(start.getTime())) continue;
    // Conversation rows are points in time, not durations. Give events a
    // nominal 1-minute span so vue-cal can render a visible block.
    const end = new Date(start.getTime() + 60_000);
    const color = agentColor.value.get(row._agent) ?? "#7c6af7";
    out.push({
      start,
      end,
      title: `${row.role}: ${truncate(row.text ?? "", 80)}`,
      content: truncate(row.text ?? "", 300),
      class: `role-${row.role}`,
      _raw: row,
      _color: color,
    });
  }
  return out;
});

const selectedView = ref<"day" | "week" | "month">("day");
const selected = ref<CalEvent | null>(null);

function onEventClick(e: CalEvent) {
  selected.value = e;
}

function formatTs(ts: string): string {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  const ms = String(d.getMilliseconds()).padStart(3, "0");
  return d.toLocaleString().replace(/(\d{1,2}:\d{2}:\d{2})/, (m) => `${m}.${ms}`);
}
</script>

<template>
  <div class="calendar-view" data-testid="list-calendar">
    <div class="toolbar">
      <h2 class="title">Calendar</h2>
      <span class="hint">Conversation log timeline</span>
      <div class="legend">
        <span
          v-for="[name, color] in agentColor"
          :key="name"
          class="legend-chip"
          :style="{ borderColor: color }"
        >
          <span class="dot" :style="{ background: color }" /> {{ name }}
        </span>
      </div>
      <span class="count">{{ events.length }}</span>
      <span
        v-if="degradedEntries.length > 0"
        class="degraded"
        :title="degradedTooltip"
        data-testid="list-calendar-degraded"
      >
        <i class="pi pi-exclamation-triangle" aria-hidden="true" />
        {{ degradedEntries.length }} degraded
      </span>
      <button class="refresh" type="button" :disabled="loading" @click="refresh">
        <i class="pi pi-refresh" aria-hidden="true" />
        <span>refresh</span>
      </button>
    </div>

    <div class="body">
      <div class="cal-wrap" :class="{ 'has-selection': !!selected }">
        <VueCal
          class="vuecal--nyx"
          :events="events"
          :time-from="0 * 60"
          :time-to="24 * 60"
          :time-step="60"
          :default-view="selectedView"
          :disable-views="[]"
          :hide-view-selector="false"
          :hide-weekends="false"
          :on-event-click="onEventClick"
          small
          todayButton
          events-on-month-view="short"
        >
          <template #event="{ event }">
            <div
              class="nyx-ev"
              :style="{ borderLeftColor: event._color ?? '#7c6af7' }"
            >
              <div class="nyx-ev-title">{{ event.title }}</div>
              <div class="nyx-ev-time">
                {{
                  new Date(event.start).toLocaleTimeString([], {
                    hour: "2-digit",
                    minute: "2-digit",
                  })
                }}
              </div>
            </div>
          </template>
        </VueCal>
      </div>

      <aside v-if="selected" class="detail">
        <button class="detail-close" type="button" @click="selected = null">
          <i class="pi pi-times" aria-hidden="true" />
        </button>
        <div class="detail-meta">
          <span
            class="detail-agent"
            :style="{ color: selected._color }"
          >@{{ selected._raw._agent }}</span>
          <span class="detail-role">{{ selected._raw.role }}</span>
          <span class="detail-source">{{ selected._raw.agent }}</span>
          <span v-if="selected._raw.model" class="detail-model">{{ selected._raw.model }}</span>
        </div>
        <div class="detail-ts">{{ formatTs(selected._raw.ts) }}</div>
        <div class="detail-body">{{ selected._raw.text ?? "" }}</div>
      </aside>
    </div>

    <div v-if="loading && items.length === 0" class="state">Loading…</div>
    <div v-else-if="error && items.length === 0" class="state state-error">
      {{ error }}
    </div>
  </div>
</template>

<style scoped>
.calendar-view {
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

.hint {
  font-size: 11px;
  color: var(--nyx-dim);
}

.legend {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.legend-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 10px;
  color: var(--nyx-dim);
  border: 1px solid;
  padding: 2px 8px;
  border-radius: 20px;
}

.dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  display: inline-block;
}

.count {
  margin-left: auto;
  font-size: 10px;
  color: var(--nyx-dim);
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

.body {
  flex: 1;
  display: flex;
  overflow: hidden;
}

.cal-wrap {
  flex: 1;
  min-width: 0;
  overflow: hidden;
}

.detail {
  width: 380px;
  flex-shrink: 0;
  border-left: 1px solid var(--nyx-border);
  background: var(--nyx-surface);
  display: flex;
  flex-direction: column;
  padding: 16px;
  gap: 10px;
  overflow-y: auto;
  position: relative;
}

.detail-close {
  position: absolute;
  top: 10px;
  right: 10px;
  background: none;
  border: 1px solid var(--nyx-border);
  color: var(--nyx-dim);
  width: 24px;
  height: 24px;
  border-radius: var(--nyx-radius);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.detail-close:hover {
  color: var(--nyx-text);
  border-color: var(--nyx-muted);
}

.detail-meta {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--nyx-dim);
}

.detail-agent {
  font-weight: 600;
}

.detail-ts {
  font-size: 11px;
  color: var(--nyx-dim);
}

.detail-body {
  font-size: 12px;
  color: var(--nyx-text);
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  padding: 10px 12px;
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
</style>

<!-- vue-cal palette overrides to match the nyx dark theme. Not scoped because
     vue-cal renders into deeply-nested DOM that ::v-deep can reach but a
     plain global rule is less fragile as vue-cal's internals evolve. -->
<style>
.vuecal--nyx.vuecal {
  background: var(--nyx-bg);
  color: var(--nyx-text);
}
.vuecal--nyx .vuecal__menu,
.vuecal--nyx .vuecal__title-bar {
  background: var(--nyx-surface);
  border-bottom: 1px solid var(--nyx-border);
}
.vuecal--nyx .vuecal__menu button,
.vuecal--nyx .vuecal__title-bar button {
  color: var(--nyx-dim);
}
.vuecal--nyx .vuecal__menu button.active,
.vuecal--nyx .vuecal__title-bar button.active {
  color: var(--nyx-bright);
  background: var(--nyx-bg);
}
.vuecal--nyx .vuecal__no-event {
  color: var(--nyx-muted);
}
.vuecal--nyx .vuecal__cell {
  color: var(--nyx-text);
}
.vuecal--nyx .vuecal__cell--today {
  background: color-mix(in srgb, var(--nyx-accent) 6%, transparent);
}
.vuecal--nyx .vuecal__cell--selected {
  background: color-mix(in srgb, var(--nyx-accent) 12%, transparent);
}
.vuecal--nyx .vuecal__cell,
.vuecal--nyx .vuecal__bg,
.vuecal--nyx .vuecal__time-column .vuecal__time-cell {
  border-color: var(--nyx-border);
}
.vuecal--nyx .vuecal__event {
  background: var(--nyx-surface);
  color: var(--nyx-bright);
  padding: 0;
  border-radius: 3px;
  overflow: hidden;
}
.vuecal--nyx .nyx-ev {
  border-left: 3px solid var(--nyx-accent);
  padding: 2px 6px;
  font-size: 11px;
  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 1px;
  line-height: 1.3;
}
.vuecal--nyx .nyx-ev-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 500;
}
.vuecal--nyx .nyx-ev-time {
  opacity: 0.55;
  font-size: 10px;
}
.vuecal--nyx .vuecal__now-line {
  color: var(--nyx-accent);
}
</style>
