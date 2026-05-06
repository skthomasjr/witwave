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
import { breakdownByLabel, histAvg, maxGauge, sumGauge, sumTotal, type FamilyMap } from "../utils/prometheus";

// Metrics view — organised into thematic sections so operators can scan
// agent health, automation activity, LLM behaviour, and performance at a
// glance without knowing which prometheus family to look at. Polling
// cadence is user-controlled via the toolbar.

Chart.register(BarController, BarElement, DoughnutController, ArcElement, CategoryScale, LinearScale, Tooltip);
Chart.defaults.color = "#777";
Chart.defaults.borderColor = "#262626";
Chart.defaults.font.family = "'SF Mono','Fira Code',monospace";
Chart.defaults.font.size = 11;

// Palette — green for success, red for failure, amber for warning,
// plus neutral tones for categorical data. Outcome-charts pick
// semantically when possible.
const PALETTE = ["#7c6af7", "#3ecfcf", "#4ade80", "#fbbf24", "#f87171", "#fb923c", "#a78bfa", "#34d399"];
const OK = "#4ade80";
const WARN = "#fbbf24";
const ERR = "#f87171";

const intervalMs = ref<number>(5000);
const { merged, loading, error, lastUpdated, perAgentErrors, refresh } = useMetrics({ intervalMs });

// Raw-metrics escape hatch (toggle off by default). Useful when the
// curated sections don't show something the user needs to eyeball —
// e.g. a very specific histogram bucket or a freshly-added metric the
// dashboard hasn't been taught about yet. Renders the full parsed
// families + their samples in a collapsible mono panel.
const showRaw = ref<boolean>(false);

const degradedEntries = computed<[string, string][]>(() => Object.entries(perAgentErrors.value));
const degradedTooltip = computed(() => degradedEntries.value.map(([a, m]) => `${a}: ${m}`).join("\n"));

function fmtNum(n: number | null): string {
  if (n === null || !Number.isFinite(n)) return "—";
  if (Math.abs(n) >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (Math.abs(n) >= 1e3) return `${(n / 1e3).toFixed(1)}k`;
  if (n === Math.floor(n)) return String(n);
  return n.toFixed(2);
}
function fmtSec(n: number | null): string {
  if (n === null) return "—";
  if (n >= 3600) return `${(n / 3600).toFixed(1)} h`;
  if (n >= 60) return `${(n / 60).toFixed(1)} m`;
  return `${Math.round(n)} s`;
}
function fmtMs(n: number | null): string {
  return n === null ? "—" : `${Math.round(n * 1000)} ms`;
}
function fmtPct(n: number | null): string {
  return n === null ? "—" : `${n.toFixed(1)}%`;
}

// ── Overview (stat cards) ────────────────────────────────────────────────
//
// The top row highlights the 8 numbers an operator most often wants to
// see. Ordered: health → activity → load → quality.

interface Stat {
  label: string;
  val: string;
  tone?: "ok" | "warn" | "err" | "";
  hint?: string;
}

const stats = computed<Stat[]>(() => {
  const m = merged.value;

  // Health — how many agents are reporting (live harness_up samples).
  const upSamples = (m.get("harness_up")?.samples ?? []).filter(
    (s) => s.name.endsWith("harness_up") || s.name === "harness_up",
  );
  const upCount = upSamples.filter((s) => s.value > 0).length;
  const totalAgents = upSamples.length;

  // Task outcomes — success/error/timeout/cancelled breakdown for rate.
  const taskOutcomes = breakdownByLabel(m, "harness_tasks", "status");
  const taskTotal = Object.values(taskOutcomes).reduce((a, b) => a + b, 0);
  const taskOk = taskOutcomes["success"] ?? 0;
  const errRate = taskTotal > 0 ? ((taskTotal - taskOk) / taskTotal) * 100 : 0;

  // Session starts — new + resumed across all backends, signal of LLM
  // activity; sessions is the cleanest "something is actually running".
  const sessionStarts = sumTotal(m, "backend_session_starts");

  const avgA2aMs = histAvg(m, "harness_a2a_request_duration_seconds");
  const avgTaskMs = histAvg(m, "backend_task_duration_seconds");

  return [
    {
      label: "Agents Up",
      val: totalAgents > 0 ? `${upCount} / ${totalAgents}` : "—",
      tone: totalAgents > 0 && upCount === totalAgents ? "ok" : upCount > 0 ? "warn" : "err",
      hint: "Agents reporting harness_up=1",
    },
    {
      label: "Max Uptime",
      val: fmtSec(maxGauge(m, "harness_uptime_seconds")),
      hint: "Longest-running harness in the team",
    },
    {
      label: "Active Sessions",
      val: fmtNum(sumGauge(m, "harness_active_sessions")),
      hint: "Sum of harness_active_sessions across agents",
    },
    {
      label: "A2A Requests",
      val: fmtNum(sumTotal(m, "harness_a2a_requests")),
      hint: "Total A2A HTTP requests accepted",
    },
    {
      label: "Session Starts",
      val: fmtNum(sessionStarts),
      hint: "New + resumed LLM sessions across backends",
    },
    {
      label: "Tasks Completed",
      val: fmtNum(taskTotal),
      hint: "Total agent tasks processed (all outcomes)",
    },
    {
      label: "Error Rate",
      val: taskTotal > 0 ? fmtPct(errRate) : "—",
      tone: errRate >= 10 ? "err" : errRate >= 1 ? "warn" : taskTotal > 0 ? "ok" : "",
      hint: "Non-success tasks ÷ total tasks",
    },
    {
      label: "Avg Task Latency",
      val: fmtMs(avgTaskMs ?? avgA2aMs),
      hint: "Backend task duration (falls back to A2A duration)",
    },
  ];
});

// ── Sections + charts ────────────────────────────────────────────────────

interface ChartSpec {
  id: string;
  title: string;
  type: "bar" | "doughnut";
  build: (m: FamilyMap) => { labels: string[]; values: number[]; colors?: string[] };
}
interface Section {
  id: string;
  title: string;
  subtitle: string;
  charts: ChartSpec[];
}

// Colour-by-label helper — semantic colours for known statuses so
// success/error across charts stay visually consistent.
function semanticColors(labels: string[]): string[] {
  return labels.map((l) => {
    const lower = l.toLowerCase();
    if (/^(success|ok|200|complete|completed|healthy|ready|resumed)$/.test(lower)) return OK;
    if (/^(error|err|fail|failed|timeout|5\d\d|4\d\d|cancelled)$/.test(lower)) return ERR;
    if (/^(warn|warning|degraded|skip|skipped|shed)$/.test(lower)) return WARN;
    return "";
  });
}

function colorize(labels: string[], fallbackPalette = PALETTE): string[] {
  const semantic = semanticColors(labels);
  return labels.map((_, i) => semantic[i] || fallbackPalette[i % fallbackPalette.length]);
}

const a2aDurationTitle = computed(() => {
  const avg = histAvg(merged.value, "harness_a2a_request_duration_seconds");
  return `A2A Request Duration${avg !== null ? ` — avg ${fmtMs(avg)}` : ""}`;
});
const taskDurationTitle = computed(() => {
  const avg = histAvg(merged.value, "backend_task_duration_seconds");
  return `Backend Task Duration${avg !== null ? ` — avg ${fmtMs(avg)}` : ""}`;
});
const loopLagTitle = computed(() => {
  const avgH = histAvg(merged.value, "harness_event_loop_lag_seconds");
  const avgB = histAvg(merged.value, "backend_event_loop_lag_seconds");
  const worst = Math.max(avgH ?? 0, avgB ?? 0);
  return `Event-Loop Lag${worst > 0 ? ` — worst ${fmtMs(worst)}` : ""}`;
});

function byLabel(m: FamilyMap, key: string, label: string) {
  const bd = breakdownByLabel(m, key, label);
  const labels = Object.keys(bd);
  return { labels, values: labels.map((k) => bd[k]) };
}

function histBuckets(m: FamilyMap, key: string) {
  const samples = (m.get(key)?.samples ?? []).filter((s) => s.name.endsWith("_bucket") && s.labels.le !== "+Inf");
  // Aggregate bucket counts across label sets — we want the cumulative
  // count per `le` bucket for the cluster-wide view.
  const acc = new Map<string, number>();
  for (const s of samples) {
    const le = s.labels.le;
    acc.set(le, (acc.get(le) ?? 0) + s.value);
  }
  // Sort numerically.
  const entries = [...acc.entries()].sort((a, b) => parseFloat(a[0]) - parseFloat(b[0]));
  return {
    labels: entries.map(([le]) => `${le}s`),
    values: entries.map(([, v]) => v),
  };
}

const sections = computed<Section[]>(() => [
  {
    id: "activity",
    title: "Activity",
    subtitle: "What the automation layer is actually doing.",
    charts: [
      {
        id: "jobs-by-name",
        title: "Job Runs by Name",
        type: "bar",
        build: (m) => byLabel(m, "harness_job_runs", "name"),
      },
      {
        id: "jobs-outcome",
        title: "Job Runs by Outcome",
        type: "doughnut",
        build: (m) => {
          const r = byLabel(m, "harness_job_runs", "status");
          return { ...r, colors: colorize(r.labels) };
        },
      },
      {
        id: "sched-tasks",
        title: "Scheduled Tasks by Name",
        type: "bar",
        build: (m) => byLabel(m, "harness_sched_task_runs", "name"),
      },
      {
        id: "triggers-codes",
        title: "Trigger Requests by Response Code",
        type: "bar",
        build: (m) => byLabel(m, "harness_triggers_requests", "code"),
      },
      {
        id: "webhooks-result",
        title: "Webhook Deliveries by Result",
        type: "doughnut",
        build: (m) => {
          const r = byLabel(m, "harness_webhooks_delivery", "result");
          return { ...r, colors: colorize(r.labels) };
        },
      },
      {
        id: "continuation-outcome",
        title: "Continuation Runs by Outcome",
        type: "doughnut",
        build: (m) => {
          const r = byLabel(m, "harness_continuation_runs", "status");
          return { ...r, colors: colorize(r.labels) };
        },
      },
      {
        id: "heartbeat-outcome",
        title: "Heartbeat Runs",
        type: "doughnut",
        build: (m) => {
          const r = byLabel(m, "harness_heartbeat_runs", "status");
          return { ...r, colors: colorize(r.labels) };
        },
      },
    ],
  },
  {
    id: "llm",
    title: "LLM Insights",
    subtitle: "Model usage, session behaviour, tool audit, and MCP.",
    charts: [
      {
        id: "model-mix",
        title: "Requests by Model",
        type: "doughnut",
        build: (m) => byLabel(m, "backend_model_requests", "model"),
      },
      {
        id: "session-starts",
        title: "Session Starts (new vs resumed)",
        type: "doughnut",
        build: (m) => byLabel(m, "backend_session_starts", "type"),
      },
      {
        id: "context-exhaustion",
        title: "Context Exhaustion Events by Backend",
        type: "bar",
        build: (m) => {
          const r = byLabel(m, "backend_context_exhaustion", "agent");
          return { ...r, colors: r.labels.map(() => ERR) };
        },
      },
      {
        id: "context-warnings",
        title: "Context Warnings by Backend",
        type: "bar",
        build: (m) => {
          const r = byLabel(m, "backend_context_warnings", "agent");
          return { ...r, colors: r.labels.map(() => WARN) };
        },
      },
      {
        id: "tool-audit",
        title: "Tool Audit Entries by Decision",
        type: "doughnut",
        build: (m) => {
          const r = byLabel(m, "backend_tool_audit_entries", "decision");
          return { ...r, colors: colorize(r.labels) };
        },
      },
      {
        id: "hook-decisions",
        title: "Hook Decisions by Tool",
        type: "bar",
        build: (m) => byLabel(m, "backend_hook_decisions", "tool"),
      },
    ],
  },
  {
    id: "perf",
    title: "Performance",
    subtitle: "Latency + event-loop health. Tails matter more than means here.",
    charts: [
      {
        id: "a2a-dur",
        title: a2aDurationTitle.value,
        type: "bar",
        build: (m) => histBuckets(m, "harness_a2a_request_duration_seconds"),
      },
      {
        id: "task-dur",
        title: taskDurationTitle.value,
        type: "bar",
        build: (m) => histBuckets(m, "backend_task_duration_seconds"),
      },
      {
        id: "job-dur",
        title: "Job Duration",
        type: "bar",
        build: (m) => histBuckets(m, "harness_job_duration_seconds"),
      },
      {
        id: "loop-lag",
        title: loopLagTitle.value,
        type: "bar",
        build: (m) => histBuckets(m, "harness_event_loop_lag_seconds"),
      },
      {
        id: "bus-wait",
        title: "Bus Queue Wait",
        type: "bar",
        build: (m) => histBuckets(m, "harness_bus_wait_seconds"),
      },
    ],
  },
  {
    id: "quality",
    title: "Reliability",
    subtitle: "Errors, restarts, timeouts, and capacity shedding.",
    charts: [
      {
        id: "tasks-outcome",
        title: "Tasks by Outcome",
        type: "doughnut",
        build: (m) => {
          const r = byLabel(m, "harness_tasks", "status");
          return { ...r, colors: colorize(r.labels) };
        },
      },
      {
        id: "a2a-outcome",
        title: "A2A Requests by Outcome",
        type: "doughnut",
        build: (m) => {
          const r = byLabel(m, "harness_a2a_requests", "status");
          return { ...r, colors: colorize(r.labels) };
        },
      },
      {
        id: "task-restarts",
        title: "Task Restarts by Task",
        type: "bar",
        build: (m) => byLabel(m, "harness_task_restarts", "task"),
      },
      {
        id: "webhooks-shed",
        title: "Webhook Deliveries Shed by Subscription",
        type: "bar",
        build: (m) => {
          const r = byLabel(m, "harness_webhooks_delivery_shed", "subscription");
          return { ...r, colors: r.labels.map(() => WARN) };
        },
      },
      {
        id: "bus-errors",
        title: "Bus Worker Errors (last minute)",
        type: "bar",
        build: (m) => {
          const total = sumTotal(m, "harness_bus_errors");
          return {
            labels: total > 0 ? ["bus_errors"] : [],
            values: total > 0 ? [total] : [],
            colors: [ERR],
          };
        },
      },
      {
        id: "checkpoint-errors",
        title: "Job Checkpoint Write Errors",
        type: "bar",
        build: (m) => {
          const total = sumTotal(m, "harness_checkpoint_write_errors");
          return {
            labels: total > 0 ? ["write_errors"] : [],
            values: total > 0 ? [total] : [],
            colors: [ERR],
          };
        },
      },
    ],
  },
]);

// Flatten sections → prepared charts, skipping charts with no data so
// the view stays dense when traffic is sparse. Section header is still
// shown if at least one chart in it has data.

interface PreparedChart {
  id: string;
  title: string;
  type: "bar" | "doughnut";
  data: { labels: string[]; datasets: unknown[] };
  options: unknown;
}
interface PreparedSection {
  id: string;
  title: string;
  subtitle: string;
  charts: PreparedChart[];
}

const preparedSections = computed<PreparedSection[]>(() => {
  const out: PreparedSection[] = [];
  for (const section of sections.value) {
    const charts: PreparedChart[] = [];
    for (const spec of section.charts) {
      const built = spec.build(merged.value);
      if (!built.labels.length) continue;
      const colors = built.colors && built.colors.length ? built.colors : colorize(built.labels);
      const data = {
        labels: built.labels,
        datasets: [
          {
            data: built.values,
            backgroundColor: spec.type === "doughnut" ? colors : colors.map((c) => `${c}bb`),
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
      charts.push({ id: spec.id, title: spec.title, type: spec.type, data, options });
    }
    if (charts.length > 0) {
      out.push({ id: section.id, title: section.title, subtitle: section.subtitle, charts });
    }
  }
  return out;
});

const updatedLabel = computed(() => {
  if (lastUpdated.value === null) return "";
  return `updated ${new Date(lastUpdated.value).toLocaleTimeString()}`;
});

// Flattened raw-metrics list. Sorted so scanning is predictable.
interface RawRow {
  name: string;
  help: string;
  type: string;
  samples: { labels: string; value: number }[];
}
const rawRows = computed<RawRow[]>(() => {
  const out: RawRow[] = [];
  for (const [name, fam] of merged.value.entries()) {
    out.push({
      name,
      help: fam.help,
      type: fam.type,
      samples: fam.samples.map((s) => ({
        labels: Object.entries(s.labels)
          .sort((a, b) => a[0].localeCompare(b[0]))
          .map(([k, v]) => `${k}="${v}"`)
          .join(","),
        value: s.value,
      })),
    });
  }
  out.sort((a, b) => a.name.localeCompare(b.name));
  return out;
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
      <label class="toggle" title="Show the full parsed Prometheus output">
        <input v-model="showRaw" type="checkbox" />
        <span>raw</span>
      </label>
      <span class="ts">{{ updatedLabel }}</span>
      <span
        v-if="degradedEntries.length > 0"
        class="degraded"
        :title="degradedTooltip"
        data-testid="list-metrics-degraded"
      >
        <i class="pi pi-exclamation-triangle" aria-hidden="true" />
        {{ degradedEntries.length }} scrape failed
      </span>
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
        <!-- Overview stat row -->
        <div class="section-header">
          <h3 class="section-title">Overview</h3>
          <p class="section-sub">Agent health + the metrics operators check first.</p>
        </div>
        <div class="stat-row">
          <div v-for="s in stats" :key="s.label" class="stat" :class="s.tone ? `stat-${s.tone}` : ''" :title="s.hint">
            <div class="stat-lbl">{{ s.label }}</div>
            <div class="stat-val">{{ s.val }}</div>
          </div>
        </div>

        <!-- Sections -->
        <div v-if="preparedSections.length === 0" class="placeholder">
          No chart-able data yet — agents may still be warming up.
        </div>
        <!-- Raw metrics escape hatch (#user-ask-raw). Opt-in via the
             toolbar; rendered before the thematic sections so it's easy
             to find but not imposing when closed. -->
        <section v-if="showRaw" class="section raw-section">
          <div class="section-header">
            <h3 class="section-title">Raw Prometheus output</h3>
            <p class="section-sub">
              Every parsed metric family across the team, sorted by name. Toggle off once you've found what you needed.
            </p>
          </div>
          <div class="raw-table-wrap">
            <table class="raw-table">
              <thead>
                <tr>
                  <th>metric</th>
                  <th>type</th>
                  <th>labels</th>
                  <th class="v">value</th>
                </tr>
              </thead>
              <tbody>
                <template v-for="row in rawRows" :key="row.name">
                  <tr v-if="row.samples.length === 0">
                    <td>{{ row.name }}</td>
                    <td>{{ row.type || "—" }}</td>
                    <td colspan="2" class="empty">
                      {{ row.help || "(no samples)" }}
                    </td>
                  </tr>
                  <tr v-for="(s, i) in row.samples" :key="`${row.name}-${i}`">
                    <td>{{ i === 0 ? row.name : "" }}</td>
                    <td>{{ i === 0 ? row.type || "—" : "" }}</td>
                    <td class="labels">{{ s.labels || "—" }}</td>
                    <td class="v">{{ fmtNum(s.value) }}</td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>
        </section>

        <div v-for="sec in preparedSections" :key="sec.id" class="section">
          <div class="section-header">
            <h3 class="section-title">{{ sec.title }}</h3>
            <p class="section-sub">{{ sec.subtitle }}</p>
          </div>
          <div class="chart-grid">
            <div v-for="c in sec.charts" :key="c.id" class="chart-card">
              <h4>{{ c.title }}</h4>
              <div class="chart-body">
                <Bar v-if="c.type === 'bar'" :data="c.data as never" :options="c.options as never" />
                <Doughnut v-else :data="c.data as never" :options="c.options as never" />
              </div>
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
  border-bottom: 1px solid var(--witwave-border);
  background: var(--witwave-surface);
  flex-shrink: 0;
}

.title {
  font-size: 12px;
  color: var(--witwave-bright);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  margin: 0;
  font-weight: 600;
}

.toolbar-lbl {
  font-size: 10px;
  color: var(--witwave-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
}

.select {
  background: var(--witwave-bg);
  border: 1px solid var(--witwave-border);
  color: var(--witwave-text);
  font-family: var(--witwave-mono);
  font-size: 11px;
  padding: 4px 8px;
  border-radius: var(--witwave-radius);
  cursor: pointer;
}
.select:focus {
  outline: none;
  border-color: var(--witwave-accent);
}

.toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 10px;
  color: var(--witwave-dim);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  cursor: pointer;
}
.toggle input {
  accent-color: var(--witwave-accent, #7c6af7);
  cursor: pointer;
}

.ts {
  font-size: 11px;
  color: var(--witwave-dim);
  margin-left: auto;
}

.raw-section {
  gap: 8px;
}
.raw-table-wrap {
  overflow-x: auto;
  border: 1px solid var(--witwave-border);
  border-radius: var(--witwave-radius);
  background: var(--witwave-surface);
}
.raw-table {
  width: 100%;
  border-collapse: collapse;
  font-family: var(--witwave-mono);
  font-size: 10.5px;
}
.raw-table th,
.raw-table td {
  padding: 5px 10px;
  border-bottom: 1px solid var(--witwave-border);
  text-align: left;
  vertical-align: top;
}
.raw-table th {
  position: sticky;
  top: 0;
  background: var(--witwave-bg);
  color: var(--witwave-dim);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  font-weight: 600;
  font-size: 9px;
}
.raw-table td.v {
  text-align: right;
  color: var(--witwave-bright);
  white-space: nowrap;
}
.raw-table td.labels {
  color: var(--witwave-dim);
  word-break: break-word;
}
.raw-table td.empty {
  color: var(--witwave-muted);
  font-style: italic;
}
.raw-table tr:last-child td {
  border-bottom: none;
}

.degraded {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 10px;
  color: var(--witwave-red);
  border: 1px solid var(--witwave-red);
  border-radius: var(--witwave-radius);
  padding: 2px 6px;
  cursor: help;
  white-space: nowrap;
}

.refresh {
  background: none;
  border: 1px solid var(--witwave-border);
  color: var(--witwave-dim);
  font-family: var(--witwave-mono);
  font-size: 11px;
  padding: 4px 10px;
  border-radius: var(--witwave-radius);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.refresh:hover:not(:disabled) {
  color: var(--witwave-text);
  border-color: var(--witwave-muted);
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

.state,
.placeholder {
  padding: 30px;
  color: var(--witwave-muted);
  font-size: 12px;
  text-align: center;
  grid-column: 1 / -1;
}

.state-error {
  color: var(--witwave-red);
}

.section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.section-header {
  display: flex;
  align-items: baseline;
  gap: 10px;
  border-bottom: 1px solid var(--witwave-border);
  padding-bottom: 6px;
}
.section-title {
  font-size: 12px;
  color: var(--witwave-bright);
  text-transform: uppercase;
  letter-spacing: 0.09em;
  margin: 0;
  font-weight: 700;
}
.section-sub {
  margin: 0;
  font-size: 11px;
  color: var(--witwave-dim);
  line-height: 1.3;
}

.stat-row {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 10px;
}

.stat {
  background: var(--witwave-surface);
  border: 1px solid var(--witwave-border);
  border-left-width: 3px;
  border-radius: var(--witwave-radius);
  padding: 13px 15px;
  cursor: help;
}
.stat-ok {
  border-left-color: var(--witwave-green, #4ade80);
}
.stat-warn {
  border-left-color: var(--witwave-yellow, #fbbf24);
}
.stat-err {
  border-left-color: var(--witwave-red, #f87171);
}

.stat-lbl {
  font-size: 10px;
  color: var(--witwave-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
}
.stat-val {
  font-size: 1.45rem;
  color: var(--witwave-bright);
  margin-top: 5px;
  line-height: 1;
}

.chart-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
  gap: 14px;
}

.chart-card {
  background: var(--witwave-surface);
  border: 1px solid var(--witwave-border);
  border-radius: var(--witwave-radius);
  padding: 15px;
}

.chart-card h4 {
  font-size: 10px;
  color: var(--witwave-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  margin: 0 0 12px;
  font-weight: 500;
}

.chart-body {
  height: 170px;
}
</style>
