<script setup lang="ts">
import { computed, ref } from "vue";
import { useAgentFanout } from "../composables/useAgentFanout";
import type { TraceEntry } from "../types/chat";

// Tool-audit trace feed across the team (#592). Fans out per-agent to
// /api/agents/<name>/trace and renders a chronological list. The harness
// /trace endpoint merges tool_use and tool_result events emitted by each
// backend's PostToolUse hook. We pair each tool_use with its matching
// tool_result (by id / tool_use_id) to derive duration and status.

type Row = TraceEntry & { _agent: string };

const limit = ref<number>(100);
const searchTerm = ref<string>("");
const agentFilter = ref<string>("");
const toolFilter = ref<string>("");
const statusFilter = ref<string>("");

const { items, perAgentErrors, loading, error, refresh } = useAgentFanout<TraceEntry>({
  endpoint: "trace",
  query: computed(() => ({ limit: String(limit.value) })),
});

const degradedEntries = computed<[string, string][]>(() =>
  Object.entries(perAgentErrors.value),
);
const degradedTooltip = computed(() =>
  degradedEntries.value.map(([a, m]) => `${a}: ${m}`).join("\n"),
);

// Pair tool_use rows with their tool_result by id. The key is
// "<_agent>|<id>" because backends only guarantee uniqueness within their
// own stream; a naive id-only map would cross-contaminate between agents.
interface RenderRow {
  key: string;
  ts: string;
  agent: string;
  sourceTeam: string;
  tool: string;
  sessionId: string;
  durationMs: number | null;
  status: "ok" | "error" | "pending";
  useRow: Row;
  resultRow?: Row;
}

const rendered = computed<RenderRow[]>(() => {
  const results = new Map<string, Row>();
  for (const r of items.value) {
    if (r.event_type === "tool_result" && r.tool_use_id) {
      results.set(`${r._agent}|${r.tool_use_id}`, r);
    }
  }
  const rows: RenderRow[] = [];
  for (const r of items.value) {
    if (r.event_type !== "tool_use") continue;
    const id = r.id ?? "";
    const res = id ? results.get(`${r._agent}|${id}`) : undefined;
    let duration: number | null = null;
    if (res) {
      const a = Date.parse(r.ts);
      const b = Date.parse(res.ts);
      if (!Number.isNaN(a) && !Number.isNaN(b)) duration = b - a;
    }
    const status: RenderRow["status"] = !res
      ? "pending"
      : res.is_error
        ? "error"
        : "ok";
    rows.push({
      key: `${r._agent}|${id || r.ts}`,
      ts: r.ts,
      agent: r.agent ?? "",
      sourceTeam: r._agent,
      tool: r.name ?? "",
      sessionId: r.session_id ?? "",
      durationMs: duration,
      status,
      useRow: r,
      resultRow: res,
    });
  }
  // Newest first — trace views are debugging-oriented; most-recent-first is
  // the common read order. Date.parse compares by instant.
  rows.sort((a, b) => Date.parse(b.ts) - Date.parse(a.ts));
  return rows;
});

const agentOptions = computed(() => {
  const set = new Set<string>();
  for (const r of rendered.value) set.add(r.sourceTeam);
  return Array.from(set).sort();
});

const toolOptions = computed(() => {
  const set = new Set<string>();
  for (const r of rendered.value) if (r.tool) set.add(r.tool);
  return Array.from(set).sort();
});

const filtered = computed(() => {
  const q = searchTerm.value.trim().toLowerCase();
  return rendered.value.filter((row) => {
    if (agentFilter.value && row.sourceTeam !== agentFilter.value) return false;
    if (toolFilter.value && row.tool !== toolFilter.value) return false;
    if (statusFilter.value && row.status !== statusFilter.value) return false;
    if (q) {
      const hay = `${row.tool} ${row.agent} ${row.sessionId} ${JSON.stringify(row.useRow.input ?? "")}`.toLowerCase();
      if (!hay.includes(q)) return false;
    }
    return true;
  });
});

function formatTs(ts: string): string {
  try {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return ts;
    const ms = String(d.getMilliseconds()).padStart(3, "0");
    const s = d.toLocaleString();
    return s.replace(/(\d{1,2}:\d{2}:\d{2})/, (match) => `${match}.${ms}`);
  } catch {
    return ts;
  }
}

function formatDuration(ms: number | null): string {
  if (ms === null) return "–";
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

function formatInput(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") return v;
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}
</script>

<template>
  <div class="trace-view" data-testid="list-trace">
    <div class="toolbar">
      <h2 class="title">Trace</h2>
      <input
        v-model="searchTerm"
        class="search"
        type="text"
        placeholder="filter tool / input / session…"
      />
      <select v-model="agentFilter" class="select" aria-label="agent">
        <option value="">all agents</option>
        <option v-for="a in agentOptions" :key="a" :value="a">{{ a }}</option>
      </select>
      <select v-model="toolFilter" class="select" aria-label="tool">
        <option value="">all tools</option>
        <option v-for="t in toolOptions" :key="t" :value="t">{{ t }}</option>
      </select>
      <select v-model="statusFilter" class="select" aria-label="status">
        <option value="">all statuses</option>
        <option value="ok">ok</option>
        <option value="error">error</option>
        <option value="pending">pending</option>
      </select>
      <select v-model.number="limit" class="select" aria-label="limit">
        <option :value="50">50</option>
        <option :value="100">100</option>
        <option :value="250">250</option>
        <option :value="500">500</option>
      </select>
      <span class="count">{{ filtered.length }} / {{ rendered.length }}</span>
      <span
        v-if="degradedEntries.length > 0"
        class="degraded"
        :title="degradedTooltip"
        data-testid="list-trace-degraded"
      >
        <i class="pi pi-exclamation-triangle" aria-hidden="true" />
        {{ degradedEntries.length }} degraded
      </span>
      <button class="refresh" type="button" :disabled="loading" @click="refresh">
        <i class="pi pi-refresh" aria-hidden="true" />
      </button>
    </div>

    <div class="feed">
      <div v-if="loading && rendered.length === 0" class="state">Loading…</div>
      <div v-else-if="error && rendered.length === 0" class="state state-error">
        {{ error }}
      </div>
      <div v-else-if="filtered.length === 0" class="state">No trace events.</div>
      <table v-else class="tbl">
        <thead>
          <tr>
            <th>Timestamp</th>
            <th>Tool</th>
            <th>Duration</th>
            <th>Status</th>
            <th>Agent</th>
            <th>Session</th>
            <th>Input</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in filtered"
            :key="row.key"
            :class="`status-row-${row.status}`"
          >
            <td class="ts">{{ formatTs(row.ts) }}</td>
            <td class="tool">{{ row.tool }}</td>
            <td class="dur">{{ formatDuration(row.durationMs) }}</td>
            <td class="status">
              <span :class="`pill pill-${row.status}`">{{ row.status }}</span>
            </td>
            <td class="agent">
              <span class="agent-name">{{ row.agent }}</span>
              <span class="team">@{{ row.sourceTeam }}</span>
            </td>
            <td class="session">{{ row.sessionId }}</td>
            <td class="input">{{ formatInput(row.useRow.input) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.trace-view {
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

.tbl {
  width: 100%;
  border-collapse: collapse;
  font-size: 11px;
  font-family: var(--nyx-mono);
}

.tbl th,
.tbl td {
  text-align: left;
  padding: 6px 10px;
  border-bottom: 1px solid var(--nyx-border);
  vertical-align: top;
}

.tbl th {
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-size: 10px;
  font-weight: 600;
  background: var(--nyx-surface);
  position: sticky;
  top: 0;
  z-index: 1;
}

.tbl tbody tr:hover {
  background: var(--nyx-surface);
}

.ts {
  color: var(--nyx-dim);
  white-space: nowrap;
}

.tool {
  color: var(--nyx-bright);
  white-space: nowrap;
}

.dur {
  color: var(--nyx-text);
  white-space: nowrap;
}

.agent-name {
  color: var(--nyx-text);
}

.team {
  color: var(--nyx-accent);
  margin-left: 6px;
}

.session {
  color: var(--nyx-muted);
  font-size: 10px;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.input {
  color: var(--nyx-text);
  max-width: 400px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pill {
  display: inline-block;
  padding: 1px 6px;
  border-radius: var(--nyx-radius);
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.pill-ok {
  background: color-mix(in srgb, var(--nyx-green) 20%, transparent);
  color: var(--nyx-green);
  border: 1px solid color-mix(in srgb, var(--nyx-green) 35%, var(--nyx-border));
}

.pill-error {
  background: color-mix(in srgb, var(--nyx-red) 20%, transparent);
  color: var(--nyx-red);
  border: 1px solid color-mix(in srgb, var(--nyx-red) 35%, var(--nyx-border));
}

.pill-pending {
  background: color-mix(in srgb, var(--nyx-yellow) 20%, transparent);
  color: var(--nyx-yellow);
  border: 1px solid color-mix(in srgb, var(--nyx-yellow) 35%, var(--nyx-border));
}
</style>
