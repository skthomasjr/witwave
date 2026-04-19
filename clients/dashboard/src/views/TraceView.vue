<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { useAgentFanout } from "../composables/useAgentFanout";
import { exportCsv, exportJson, timestamped } from "../utils/export";
import type { TraceEntry } from "../types/chat";

// Tool activity feed across the team (#592, consolidated in
// #tool-audit-merge). Fans out per-agent to /api/agents/<name>/trace
// and renders a chronological list. tool-activity.jsonl carries three event
// types; we show two visible kinds:
//   • tool_use  — paired with its matching tool_result for duration
//                 and status, enriched with response preview + hook
//                 decision from any matching tool_audit row.
//   • tool_audit — standalone only when there's no matching tool_use
//                 (e.g. denied at the hook before the model saw it).
// tool_result is never rendered on its own — it's folded into its
// tool_use row.

type Row = TraceEntry & { _agent: string };

const limit = ref<number>(100);
// searchTerm is bound to the input directly; searchTermDebounced trails
// by 200ms and is what the filtered computed reads (#953). Without the
// debounce each keystroke re-ran the O(N) filter which JSON.stringified
// every tool input per row — perceptible jank on a 500-row feed.
const searchTerm = ref<string>("");
const searchTermDebounced = ref<string>("");
let _searchTimer: number | null = null;
watch(searchTerm, (val) => {
  if (_searchTimer !== null) window.clearTimeout(_searchTimer);
  _searchTimer = window.setTimeout(() => {
    searchTermDebounced.value = val;
    _searchTimer = null;
  }, 200);
});
// #1008: clear any pending debounce on unmount so a late setTimeout
// can't fire against a destroyed component (surfaced under Suspense
// / KeepAlive teardown when the user types and navigates away within
// the 200ms window).
onBeforeUnmount(() => {
  if (_searchTimer !== null) {
    window.clearTimeout(_searchTimer);
    _searchTimer = null;
  }
});
const agentFilter = ref<string>("");
const toolFilter = ref<string>("");
const statusFilter = ref<string>("");
const typeFilter = ref<string>("");

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

// Pair tool_use rows with their tool_result (for duration) and any
// matching tool_audit row (for response preview + hook decision).
// Keyed by "<_agent>|<id>" because backends only guarantee uniqueness
// within their own stream; a naive id-only map would cross-contaminate.
interface RenderRow {
  key: string;
  kind: "tool_use" | "tool_audit";
  ts: string;
  agent: string;
  sourceTeam: string;
  tool: string;
  sessionId: string;
  durationMs: number | null;
  status: "ok" | "error" | "pending" | "denied";
  decision: string | null;
  rule: string | null;
  preview: string | null;
  useRow: Row;
  resultRow?: Row;
  auditRow?: Row;
  // Pre-lowercased concatenated haystack used by the search filter
  // (#953). Computed once when the row is built so a keystroke only
  // costs O(N) string.includes across rendered rows rather than
  // O(N) JSON.stringify + toLowerCase on every tick.
  _haystack: string;
}

const rendered = computed<RenderRow[]>(() => {
  const results = new Map<string, Row>();
  const audits = new Map<string, Row>();
  for (const r of items.value) {
    if (r.event_type === "tool_result" && r.tool_use_id) {
      results.set(`${r._agent}|${r.tool_use_id}`, r);
    } else if (r.event_type === "tool_audit") {
      const id = r.tool_use_id ?? "";
      if (id) audits.set(`${r._agent}|${id}`, r);
    }
  }
  const rows: RenderRow[] = [];
  const auditIdsConsumed = new Set<string>();
  for (const r of items.value) {
    if (r.event_type !== "tool_use") continue;
    const id = r.id ?? "";
    const key = `${r._agent}|${id}`;
    const res = id ? results.get(key) : undefined;
    const aud = id ? audits.get(key) : undefined;
    if (aud) auditIdsConsumed.add(key);
    let duration: number | null = null;
    if (res) {
      const a = Date.parse(r.ts);
      const b = Date.parse(res.ts);
      if (!Number.isNaN(a) && !Number.isNaN(b)) duration = b - a;
    }
    const decision = (aud?.decision ?? null) || null;
    const status: RenderRow["status"] = decision === "deny"
      ? "denied"
      : !res
        ? "pending"
        : res.is_error
          ? "error"
          : "ok";
    const _tool = r.name ?? aud?.tool_name ?? "";
    const _sid = r.session_id ?? "";
    const _preview = (aud?.tool_response_preview ?? null) || null;
    const _inputStr = JSON.stringify(r.input ?? r.tool_input ?? "");
    rows.push({
      key: `use|${r._agent}|${id || r.ts}`,
      kind: "tool_use",
      ts: r.ts,
      agent: r.agent ?? "",
      sourceTeam: r._agent,
      tool: _tool,
      sessionId: _sid,
      durationMs: duration,
      status,
      decision,
      rule: (aud?.rule ?? null) || null,
      preview: _preview,
      useRow: r,
      resultRow: res,
      auditRow: aud,
      _haystack: `${_tool} ${r.agent ?? ""} ${_sid} ${_inputStr} ${_preview ?? ""}`.toLowerCase(),
    });
  }
  // Surface any orphan audit rows (e.g. denied by hook before the model
  // ever emitted a tool_use). They show up as their own rows so the
  // operator can see blocked calls too.
  for (const [key, aud] of audits) {
    if (auditIdsConsumed.has(key)) continue;
    const decision = (aud.decision ?? null) || null;
    const _aTool = aud.tool_name ?? "";
    const _aSid = aud.session_id ?? "";
    const _aPrev = (aud.tool_response_preview ?? null) || null;
    rows.push({
      key: `audit|${aud._agent}|${aud.tool_use_id ?? aud.ts}`,
      kind: "tool_audit",
      ts: aud.ts,
      agent: aud.agent ?? "",
      sourceTeam: aud._agent,
      tool: _aTool,
      sessionId: _aSid,
      durationMs: null,
      status: decision === "deny" ? "denied" : "ok",
      decision,
      rule: (aud.rule ?? null) || null,
      preview: _aPrev,
      useRow: aud,
      _haystack: `${_aTool} ${aud.agent ?? ""} ${_aSid} ${_aPrev ?? ""}`.toLowerCase(),
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
  // Uses the debounced value (#953) + precomputed row._haystack so the
  // O(N) filter pass no longer JSON.stringifies inputs per keystroke.
  const q = searchTermDebounced.value.trim().toLowerCase();
  return rendered.value.filter((row) => {
    if (agentFilter.value && row.sourceTeam !== agentFilter.value) return false;
    if (toolFilter.value && row.tool !== toolFilter.value) return false;
    if (statusFilter.value && row.status !== statusFilter.value) return false;
    if (typeFilter.value && row.kind !== typeFilter.value) return false;
    if (q && !row._haystack.includes(q)) return false;
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

// Export handlers (#1105). Serialise the filtered view so the download
// matches what's on screen. JSON preserves nested tool_input/preview
// structures; CSV surfaces the flat columns incident reviewers typically
// paste into spreadsheets.
const traceExportColumns = [
  "ts",
  "sourceTeam",
  "agent",
  "sessionId",
  "kind",
  "tool",
  "status",
  "durationMs",
  "decision",
  "rule",
  "preview",
];
function exportRowToPlain(row: RenderRow): Record<string, unknown> {
  // Flatten to the columns CSV cares about; JSON callers also get this
  // shape (with useRow/resultRow/auditRow included below) so the two
  // file formats are consistent per event.
  return {
    ts: row.ts,
    sourceTeam: row.sourceTeam,
    agent: row.agent,
    sessionId: row.sessionId,
    kind: row.kind,
    tool: row.tool,
    status: row.status,
    durationMs: row.durationMs,
    decision: row.decision,
    rule: row.rule,
    preview: row.preview,
    useRow: row.useRow,
    resultRow: row.resultRow,
    auditRow: row.auditRow,
  };
}
function onExportTraceJson(): void {
  exportJson(
    filtered.value.map(exportRowToPlain),
    timestamped("nyx-trace", "json"),
  );
}
function onExportTraceCsv(): void {
  exportCsv(
    filtered.value.map(exportRowToPlain),
    traceExportColumns,
    timestamped("nyx-trace", "csv"),
  );
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
        <option value="denied">denied</option>
      </select>
      <select v-model="typeFilter" class="select" aria-label="type">
        <option value="">all types</option>
        <option value="tool_use">tool_use</option>
        <option value="tool_audit">tool_audit</option>
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
      <button
        class="export"
        type="button"
        :disabled="filtered.length === 0"
        title="Download filtered trace rows as JSON"
        data-testid="export-trace-json"
        @click="onExportTraceJson"
      >
        <i class="pi pi-download" aria-hidden="true" /> JSON
      </button>
      <button
        class="export"
        type="button"
        :disabled="filtered.length === 0"
        title="Download filtered trace rows as CSV"
        data-testid="export-trace-csv"
        @click="onExportTraceCsv"
      >
        <i class="pi pi-download" aria-hidden="true" /> CSV
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
            <th>Type</th>
            <th>Tool</th>
            <th>Duration</th>
            <th>Status</th>
            <th>Agent</th>
            <th>Session</th>
            <th>Input / Preview</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in filtered"
            :key="row.key"
            :class="`status-row-${row.status}`"
          >
            <td class="ts">{{ formatTs(row.ts) }}</td>
            <td class="kind">
              <span :class="`pill pill-kind-${row.kind}`">{{ row.kind }}</span>
            </td>
            <td class="tool">{{ row.tool }}</td>
            <td class="dur">{{ formatDuration(row.durationMs) }}</td>
            <td class="status">
              <span :class="`pill pill-${row.status}`">{{ row.status }}</span>
              <span v-if="row.rule" class="rule" :title="`rule: ${row.rule}`">·{{ row.rule }}</span>
            </td>
            <td class="agent">
              <span class="agent-name">{{ row.agent }}</span>
              <span class="team">@{{ row.sourceTeam }}</span>
            </td>
            <td class="session">{{ row.sessionId }}</td>
            <td class="input">
              <div>{{ formatInput(row.useRow.input ?? row.useRow.tool_input) }}</div>
              <div v-if="row.preview" class="preview" :title="row.preview">→ {{ row.preview }}</div>
            </td>
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

.pill-denied {
  background: color-mix(in srgb, var(--nyx-red) 14%, transparent);
  color: var(--nyx-red);
  border: 1px solid color-mix(in srgb, var(--nyx-red) 50%, var(--nyx-border));
  font-weight: 600;
}

.pill-kind-tool_use {
  background: var(--nyx-surface);
  color: var(--nyx-dim);
  border: 1px solid var(--nyx-border);
}

.pill-kind-tool_audit {
  background: color-mix(in srgb, var(--nyx-accent) 14%, transparent);
  color: var(--nyx-accent);
  border: 1px solid color-mix(in srgb, var(--nyx-accent) 40%, var(--nyx-border));
}

.rule {
  margin-left: 4px;
  font-size: 10px;
  color: var(--nyx-dim);
  font-family: var(--nyx-mono);
}

.preview {
  font-size: 10px;
  color: var(--nyx-dim);
  margin-top: 2px;
  max-width: 420px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
