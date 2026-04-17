<script setup lang="ts">
import { computed, ref } from "vue";
import { Bar, Doughnut } from "vue-chartjs";
import {
  ArcElement,
  BarController,
  BarElement,
  CategoryScale,
  Chart,
  DoughnutController,
  LinearScale,
  Tooltip,
} from "chart.js";
import { useMetrics } from "../composables/useMetrics";
import {
  breakdownByLabel,
  histAvg,
  maxGauge,
  sumGauge,
  sumTotal,
  type FamilyMap,
} from "../utils/prometheus";

// Metrics view — mirrors legacy ui/ renderMetrics: 10 stat cards + a grid
// of label-breakdown bar/doughnut charts. Polling is driven by the interval
// select; auto-refresh off disables polling.

Chart.register(
  BarController,
  BarElement,
  DoughnutController,
  ArcElement,
  CategoryScale,
  LinearScale,
  Tooltip,
);
Chart.defaults.color = "#777";
Chart.defaults.borderColor = "#262626";
Chart.defaults.font.family = "'SF Mono','Fira Code',monospace";
Chart.defaults.font.size = 11;

const PALETTE = [
  "#7c6af7",
  "#3ecfcf",
  "#4ade80",
  "#fbbf24",
  "#f87171",
  "#fb923c",
  "#a78bfa",
  "#34d399",
];

const intervalMs = ref<number>(5000);
const { merged, loading, error, lastUpdated, refresh } = useMetrics({ intervalMs });

function fmtNum(n: number | null): string {
  if (n === null || !Number.isFinite(n)) return "—";
  if (Math.abs(n) >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (Math.abs(n) >= 1e3) return `${(n / 1e3).toFixed(1)}k`;
  if (n === Math.floor(n)) return String(n);
  return n.toFixed(2);
}

function fmtSec(n: number | null): string {
  return n === null ? "—" : `${Math.round(n)} s`;
}

function fmtMs(n: number | null): string {
  return n === null ? "—" : `${Math.round(n * 1000)} ms`;
}

const stats = computed(() => {
  const m = merged.value;
  return [
    { label: "Max Uptime", val: fmtSec(maxGauge(m, "agent_uptime_seconds")) },
    { label: "Active Sessions", val: fmtNum(sumGauge(m, "agent_active_sessions")) },
    { label: "Running Tasks", val: fmtNum(sumGauge(m, "agent_running_tasks")) },
    { label: "Running Sched Tasks", val: fmtNum(sumGauge(m, "agent_sched_task_running_items")) },
    { label: "Tasks Total", val: fmtNum(sumTotal(m, "agent_tasks")) },
    { label: "A2A Requests", val: fmtNum(sumTotal(m, "agent_a2a_requests")) },
    { label: "Heartbeats", val: fmtNum(sumTotal(m, "agent_heartbeat_runs")) },
    { label: "Job Runs", val: fmtNum(sumTotal(m, "agent_job_runs")) },
    { label: "Bus Messages", val: fmtNum(sumTotal(m, "agent_bus_messages")) },
    { label: "Webhooks Shed", val: fmtNum(sumTotal(m, "agent_webhooks_delivery_shed")) },
  ];
});

interface ChartSpec {
  id: string;
  title: string;
  type: "bar" | "doughnut";
  build: (m: FamilyMap) => { labels: string[]; values: number[] };
}

const durationTitle = computed(() => {
  const avg = histAvg(merged.value, "agent_a2a_request_duration_seconds");
  return `A2A Avg Request Duration${avg !== null ? `: ${fmtMs(avg)}` : ""}`;
});

const chartSpecs = computed<ChartSpec[]>(() => {
  const m = merged.value;
  function byLabel(key: string, label: string) {
    const bd = breakdownByLabel(m, key, label);
    return { labels: Object.keys(bd), values: Object.values(bd) };
  }
  return [
    { id: "tasks-outcome", title: "Tasks by Outcome", type: "bar", build: () => byLabel("agent_tasks", "status") },
    { id: "a2a-outcome", title: "A2A Requests by Outcome", type: "bar", build: () => byLabel("agent_a2a_requests", "status") },
    { id: "hb-outcome", title: "Heartbeat Runs by Outcome", type: "bar", build: () => byLabel("agent_heartbeat_runs", "status") },
    { id: "job-runs", title: "Job Runs by Name", type: "bar", build: () => byLabel("agent_job_runs", "name") },
    { id: "task-runs", title: "Task Runs by Name", type: "bar", build: () => byLabel("agent_sched_task_runs", "name") },
    { id: "bus-kind", title: "Bus Messages by Kind", type: "doughnut", build: () => byLabel("agent_bus_messages", "kind") },
    {
      id: "a2a-dur",
      title: durationTitle.value,
      type: "bar",
      build: () => {
        const samples = (m.get("agent_a2a_request_duration_seconds")?.samples ?? []).filter(
          (s) => s.name.endsWith("_bucket") && s.labels.le !== "+Inf",
        );
        return {
          labels: samples.map((s) => `${s.labels.le}s`),
          values: samples.map((s) => s.value),
        };
      },
    },
    { id: "model-reqs", title: "Requests by Model", type: "doughnut", build: () => byLabel("agent_model_requests", "model") },
    { id: "trigger-codes", title: "Trigger Requests by Response Code", type: "bar", build: () => byLabel("agent_triggers_requests", "code") },
    { id: "webhooks", title: "Webhook Deliveries by Result", type: "bar", build: () => byLabel("agent_webhooks_delivery", "result") },
    { id: "cont-runs", title: "Continuation Runs by Outcome", type: "bar", build: () => byLabel("agent_continuation_runs", "status") },
    { id: "task-restarts", title: "Agent Task Restarts by Task", type: "bar", build: () => byLabel("agent_task_restarts", "task") },
    { id: "webhooks-shed", title: "Webhook Deliveries Shed by Subscription", type: "bar", build: () => byLabel("agent_webhooks_delivery_shed", "subscription") },
  ];
});

interface PreparedChart {
  id: string;
  title: string;
  type: "bar" | "doughnut";
  data: { labels: string[]; datasets: unknown[] };
  options: unknown;
}

const preparedCharts = computed<PreparedChart[]>(() => {
  const out: PreparedChart[] = [];
  for (const spec of chartSpecs.value) {
    const { labels, values } = spec.build(merged.value);
    if (!labels.length) continue;
    const colors = labels.map((_, i) => PALETTE[i % PALETTE.length]);
    const data = {
      labels,
      datasets: [
        {
          data: values,
          backgroundColor:
            spec.type === "doughnut" ? colors : colors.map((c) => `${c}bb`),
          borderColor: colors,
          borderWidth: spec.type === "doughnut" ? 0 : 1,
          borderRadius: spec.type === "bar" ? 3 : 0,
        },
      ],
    };
    const options: unknown =
        spec.type === "doughnut"
          ? {
              responsive: true,
              maintainAspectRatio: false,
              animation: false,
              plugins: {
                legend: {
                  display: true,
                  position: "right",
                  labels: { boxWidth: 10, padding: 8, font: { size: 11 } },
                },
                tooltip: {
                  callbacks: { label: (ctx: { raw: number }) => ` ${fmtNum(ctx.raw)}` },
                },
              },
            }
          : {
              responsive: true,
              maintainAspectRatio: false,
              animation: false,
              plugins: {
                legend: { display: false },
                tooltip: {
                  callbacks: { label: (ctx: { raw: number }) => ` ${fmtNum(ctx.raw)}` },
                },
              },
              scales: {
                x: { grid: { color: "#1a1a1a" }, ticks: { maxRotation: 40, minRotation: 0 } },
                y: { grid: { color: "#1a1a1a" }, beginAtZero: true },
              },
            };
    out.push({ id: spec.id, title: spec.title, type: spec.type, data, options });
  }
  return out;
});

const updatedLabel = computed(() => {
  if (lastUpdated.value === null) return "";
  return `updated ${new Date(lastUpdated.value).toLocaleTimeString()}`;
});

</script>

<template>
  <div class="metrics-view" data-testid="list-metrics">
    <div class="toolbar">
      <h2 class="title">Metrics</h2>
      <label class="toolbar-lbl">refresh</label>
      <select v-model.number="intervalMs" class="select">
        <option :value="5000">5s</option>
        <option :value="15000">15s</option>
        <option :value="30000">30s</option>
        <option :value="60000">1m</option>
        <option :value="0">off</option>
      </select>
      <span class="ts">{{ updatedLabel }}</span>
      <button class="refresh" type="button" :disabled="loading" @click="refresh">
        <i class="pi pi-refresh" aria-hidden="true" />
        <span>refresh</span>
      </button>
    </div>

    <div class="scroll">
      <div v-if="loading && merged.size === 0" class="state">Loading…</div>
      <div v-else-if="error && merged.size === 0" class="state state-error">
        {{ error }}
      </div>
      <template v-else>
        <div class="stat-row">
          <div v-for="s in stats" :key="s.label" class="stat">
            <div class="stat-lbl">{{ s.label }}</div>
            <div class="stat-val">{{ s.val }}</div>
          </div>
        </div>

        <div v-if="preparedCharts.length === 0" class="placeholder">
          No chart-able data yet — agents may still be warming up.
        </div>
        <div v-else class="chart-grid">
          <div v-for="c in preparedCharts" :key="c.id" class="chart-card">
            <h3>{{ c.title }}</h3>
            <div class="chart-body">
              <Bar
                v-if="c.type === 'bar'"
                :data="c.data as never"
                :options="c.options as never"
              />
              <Doughnut
                v-else
                :data="c.data as never"
                :options="c.options as never"
              />
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

.toolbar-lbl {
  font-size: 10px;
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
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

.ts {
  font-size: 11px;
  color: var(--nyx-dim);
  margin-left: auto;
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
  gap: 18px;
}

.state,
.placeholder {
  padding: 30px;
  color: var(--nyx-muted);
  font-size: 12px;
  text-align: center;
  grid-column: 1 / -1;
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
  grid-template-columns: repeat(auto-fill, minmax(380px, 1fr));
  gap: 14px;
}

.chart-card {
  background: var(--nyx-surface);
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  padding: 15px;
}

.chart-card h3 {
  font-size: 10px;
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  margin: 0 0 12px;
  font-weight: 500;
}

.chart-body {
  height: 170px;
}
</style>
