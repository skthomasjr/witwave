<script setup lang="ts">
import { computed } from "vue";
import { CronExpressionParser } from "cron-parser";
import { useAgentFanout } from "../composables/useAgentFanout";
import type { Job, Task, Heartbeat } from "../types/scheduler";

// Upcoming-schedules view. Legacy ui/ painted a full day-grid calendar;
// here we keep it simpler: sort every job/task/heartbeat's next fire in
// the next 24 hours into a single time-ordered list with a "now" marker.
// Faithful to the data, less pixel-heavy, and easy to extend into a grid
// later if the need arises.

type Entry = {
  agent: string;
  kind: "job" | "task" | "heartbeat";
  name: string;
  schedule: string;
  next: Date | null;
};

const jobs = useAgentFanout<Job>({ endpoint: "jobs" });
const tasks = useAgentFanout<Task>({ endpoint: "tasks" });
const heartbeats = useAgentFanout<Heartbeat>({ endpoint: "heartbeat" });

function nextFire(cron: string | null, tz?: string): Date | null {
  if (!cron) return null;
  try {
    return CronExpressionParser.parse(cron, {
      currentDate: new Date(),
      tz,
    })
      .next()
      .toDate();
  } catch {
    return null;
  }
}

const entries = computed<Entry[]>(() => {
  const out: Entry[] = [];
  for (const j of jobs.items.value) {
    if (!j.schedule) continue;
    out.push({
      agent: j._agent,
      kind: "job",
      name: j.name,
      schedule: j.schedule,
      next: nextFire(j.schedule),
    });
  }
  for (const t of tasks.items.value) {
    // Tasks use custom day/window semantics, not pure cron — surface the
    // raw days/window expression instead of computing a next-fire.
    out.push({
      agent: t._agent,
      kind: "task",
      name: t.name,
      schedule: `days=${t.days_expr} ${t.window_start}–${t.window_end} ${t.timezone}`,
      next: null,
    });
  }
  for (const h of heartbeats.items.value) {
    if (!h.enabled || !h.schedule) continue;
    out.push({
      agent: h._agent,
      kind: "heartbeat",
      name: "heartbeat",
      schedule: h.schedule,
      next: nextFire(h.schedule),
    });
  }
  return out.sort((a, b) => {
    if (a.next && b.next) return a.next.getTime() - b.next.getTime();
    if (a.next) return -1;
    if (b.next) return 1;
    return a.agent.localeCompare(b.agent) || a.name.localeCompare(b.name);
  });
});

const loading = computed(
  () => jobs.loading.value || tasks.loading.value || heartbeats.loading.value,
);
const error = computed(
  () => jobs.error.value || tasks.error.value || heartbeats.error.value,
);

function refresh() {
  jobs.refresh();
  tasks.refresh();
  heartbeats.refresh();
}

function formatNext(d: Date | null): string {
  if (!d) return "—";
  const now = Date.now();
  const mins = Math.max(0, Math.round((d.getTime() - now) / 60000));
  if (mins < 60) return `in ${mins}m`;
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return `in ${hrs}h`;
  return d.toLocaleString();
}
</script>

<template>
  <div class="calendar-view" data-testid="list-calendar">
    <div class="toolbar">
      <h2 class="title">Calendar</h2>
      <span class="hint">upcoming schedules, sorted by next fire</span>
      <span class="count">{{ entries.length }}</span>
      <button class="refresh" type="button" :disabled="loading" @click="refresh">
        <i class="pi pi-refresh" aria-hidden="true" />
        <span>refresh</span>
      </button>
    </div>
    <div class="feed">
      <div v-if="loading && entries.length === 0" class="state">Loading…</div>
      <div v-else-if="error && entries.length === 0" class="state state-error">
        {{ error }}
      </div>
      <div v-else-if="entries.length === 0" class="state">
        No scheduled items.
      </div>
      <table v-else class="cal-table">
        <thead>
          <tr>
            <th>when</th>
            <th>agent</th>
            <th>kind</th>
            <th>name</th>
            <th>schedule</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(e, i) in entries" :key="i">
            <td class="when">{{ formatNext(e.next) }}</td>
            <td class="agent">{{ e.agent }}</td>
            <td class="kind" :class="`kind-${e.kind}`">{{ e.kind }}</td>
            <td>{{ e.name }}</td>
            <td class="dim">{{ e.schedule }}</td>
          </tr>
        </tbody>
      </table>
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
  flex: 1;
  font-size: 11px;
  color: var(--nyx-dim);
}

.count {
  font-size: 10px;
  color: var(--nyx-dim);
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

.feed {
  flex: 1;
  overflow: auto;
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

.cal-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 11px;
}

.cal-table thead th {
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
}

.cal-table tbody tr {
  border-bottom: 1px solid var(--nyx-border);
}

.cal-table tbody td {
  padding: 8px 12px;
  color: var(--nyx-text);
}

.when {
  color: var(--nyx-accent);
  font-weight: 500;
  width: 110px;
}

.agent {
  width: 90px;
}

.kind {
  width: 90px;
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.kind-job {
  color: var(--nyx-teal);
}

.kind-task {
  color: var(--nyx-yellow);
}

.kind-heartbeat {
  color: var(--nyx-green);
}

.dim {
  color: var(--nyx-dim);
}
</style>
