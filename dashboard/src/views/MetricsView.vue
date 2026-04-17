<script setup lang="ts">
import { computed } from "vue";
import { Line } from "vue-chartjs";
import {
  Chart,
  LineElement,
  PointElement,
  LineController,
  LinearScale,
  TimeScale,
  CategoryScale,
  Filler,
  Tooltip,
} from "chart.js";
import { useMetrics } from "../composables/useMetrics";
import { sumSamples } from "../utils/prometheus";

// Metrics view. Stat cards show "last value across team"; tiny line charts
// show per-agent history over the polling window. Intentionally light — for
// deep trend analysis point Grafana at /metrics. The agent_a2a_* names come
// straight from harness/metrics.py; if those rename, this list needs an
// update.

Chart.register(
  LineElement,
  PointElement,
  LineController,
  LinearScale,
  TimeScale,
  CategoryScale,
  Filler,
  Tooltip,
);

const { history, loading, error, refresh } = useMetrics();

interface StatDef {
  label: string;
  metric: string;
  unit?: string;
  format?: "int" | "seconds";
}

const stats: StatDef[] = [
  { label: "a2a requests", metric: "agent_a2a_requests_total", format: "int" },
  { label: "a2a running", metric: "agent_running_tasks", format: "int" },
  { label: "backends", metric: "agent_backends_total", format: "int" },
  { label: "proxy tasks", metric: "agent_tasks_triggered_total", format: "int" },
  { label: "jobs fired", metric: "agent_jobs_triggered_total", format: "int" },
  { label: "triggers fired", metric: "agent_triggers_requests_total", format: "int" },
  { label: "webhook delivs.", metric: "agent_webhooks_deliveries_total", format: "int" },
  { label: "heartbeats", metric: "agent_heartbeats_total", format: "int" },
];

const agents = computed(() => {
  const seen = new Set<string>();
  for (const s of history.value) seen.add(s._agent);
  return Array.from(seen).sort();
});

const latestByAgent = computed(() => {
  const map = new Map<string, (typeof history.value)[number]>();
  for (const s of history.value) {
    const prior = map.get(s._agent);
    if (!prior || prior.ts < s.ts) map.set(s._agent, s);
  }
  return map;
});

function statTotal(metric: string): number {
  let total = 0;
  for (const snap of latestByAgent.value.values()) {
    total += sumSamples(snap.samples, metric);
  }
  return total;
}

function formatStat(v: number, fmt?: StatDef["format"]): string {
  if (!Number.isFinite(v)) return "—";
  if (fmt === "int") return Math.round(v).toLocaleString();
  if (fmt === "seconds") return `${v.toFixed(2)}s`;
  return v.toFixed(2);
}

function seriesFor(metric: string): { labels: string[]; datasets: unknown[] } {
  const perAgent = new Map<string, { x: number[]; y: number[] }>();
  for (const s of history.value) {
    if (!perAgent.has(s._agent)) perAgent.set(s._agent, { x: [], y: [] });
    const bucket = perAgent.get(s._agent)!;
    bucket.x.push(s.ts);
    bucket.y.push(sumSamples(s.samples, metric));
  }
  const colors = ["#7c6af7", "#3ecfcf", "#4ade80", "#fbbf24", "#f87171"];
  const labels = perAgent.values().next().value?.x.map((t: number) =>
    new Date(t).toLocaleTimeString([], { minute: "2-digit", second: "2-digit" }),
  ) ?? [];
  const datasets = Array.from(perAgent.entries()).map(([agent, pts], i) => ({
    label: agent,
    data: pts.y,
    borderColor: colors[i % colors.length],
    backgroundColor: colors[i % colors.length] + "22",
    tension: 0.25,
    fill: false,
    pointRadius: 0,
    borderWidth: 1.5,
  }));
  return { labels, datasets };
}

const chartOptions = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: {
    legend: { display: false },
    tooltip: { enabled: true },
  },
  scales: {
    x: { display: false },
    y: {
      display: true,
      ticks: { color: "#777", font: { size: 10 } },
      grid: { color: "#262626" },
    },
  },
};
</script>

<template>
  <div class="metrics-view" data-testid="list-metrics">
    <div class="toolbar">
      <h2 class="title">Metrics</h2>
      <span class="hint">{{ agents.length }} agent{{ agents.length === 1 ? "" : "s" }}</span>
      <span class="count" />
      <button class="refresh" type="button" :disabled="loading" @click="refresh">
        <i class="pi pi-refresh" aria-hidden="true" />
        <span>refresh</span>
      </button>
    </div>
    <div class="scroll">
      <div v-if="loading && history.length === 0" class="state">Loading…</div>
      <div v-else-if="error && history.length === 0" class="state state-error">
        {{ error }}
      </div>
      <template v-else>
        <div class="stat-row">
          <div v-for="s in stats" :key="s.metric" class="stat">
            <div class="stat-lbl">{{ s.label }}</div>
            <div class="stat-val">{{ formatStat(statTotal(s.metric), s.format) }}</div>
          </div>
        </div>
        <div class="chart-grid">
          <div v-for="s in stats" :key="s.metric" class="chart-card">
            <div class="chart-title">{{ s.label }}</div>
            <div class="chart-body">
              <Line :data="seriesFor(s.metric) as never" :options="chartOptions as never" />
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.metrics-view {
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
  font-size: 11px;
  color: var(--nyx-dim);
}

.count {
  flex: 1;
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
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 14px;
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

.stat-row {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 10px;
}

.stat {
  background: var(--nyx-surface);
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  padding: 13px 15px;
}

.stat-lbl {
  font-size: 10px;
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
}

.stat-val {
  font-size: 1.45rem;
  color: var(--nyx-bright);
  margin-top: 5px;
  line-height: 1;
}

.chart-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 14px;
}

.chart-card {
  background: var(--nyx-surface);
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  padding: 12px;
}

.chart-title {
  font-size: 10px;
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  margin-bottom: 8px;
}

.chart-body {
  height: 120px;
}
</style>
