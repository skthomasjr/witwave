<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useToolAudit, type ToolAuditRow } from "../composables/useToolAudit";

// Per-agent tool-audit viewer (#635). Fans out to each team member's
// /api/agents/<name>/tool-audit and renders a chronological table of
// tool-call audit rows. Filters are URL-query-bound so links round-trip: copy
// the URL, paste it anywhere, and the same filter state reappears on load.
// Row click expands the raw JSON entry in place — no separate detail pane
// because the JSON is the source of truth that operators need.

const route = useRoute();
const router = useRouter();

// URL-backed filter state. The ref values seed from the current query on
// mount; a watcher (below) pushes changes back via router.replace so we don't
// pollute browser history with every keystroke.
const limit = ref<number>(pickLimit(route.query.limit));
const decisionFilter = ref<string>(pickDecision(route.query.decision));
const toolFilter = ref<string>(strQuery(route.query.tool));
const sessionFilter = ref<string>(strQuery(route.query.session));
const agentFilter = ref<string>(strQuery(route.query.agent));
const searchTerm = ref<string>(strQuery(route.query.q));

function strQuery(v: unknown): string {
  if (typeof v === "string") return v;
  if (Array.isArray(v) && typeof v[0] === "string") return v[0];
  return "";
}

function pickLimit(v: unknown): number {
  const s = strQuery(v);
  const n = Number(s);
  if (!Number.isFinite(n) || n <= 0) return 100;
  return n;
}

function pickDecision(v: unknown): string {
  const s = strQuery(v).toLowerCase();
  return s === "allow" || s === "warn" || s === "deny" ? s : "";
}

watch(
  [limit, decisionFilter, toolFilter, sessionFilter, agentFilter, searchTerm],
  () => {
    // Only include non-default keys in the URL so links stay readable.
    const q: Record<string, string> = {};
    if (limit.value !== 100) q.limit = String(limit.value);
    if (decisionFilter.value) q.decision = decisionFilter.value;
    if (toolFilter.value) q.tool = toolFilter.value;
    if (sessionFilter.value) q.session = sessionFilter.value;
    if (agentFilter.value) q.agent = agentFilter.value;
    if (searchTerm.value) q.q = searchTerm.value;
    void router.replace({ query: q });
  },
  { deep: false },
);

// decision / tool / session are pushed to the backend so the per-agent read
// bounds early. agent + searchTerm are client-side since they apply across
// merged results, not per-source.
const { items, perAgentErrors, loading, error, refresh } = useToolAudit({
  limit,
  decision: decisionFilter,
  tool: toolFilter,
  session: sessionFilter,
});

const degradedEntries = computed<[string, string][]>(() =>
  Object.entries(perAgentErrors.value),
);
const degradedTooltip = computed(() =>
  degradedEntries.value.map(([a, m]) => `${a}: ${m}`).join("\n"),
);

function rowTool(r: ToolAuditRow): string {
  return r.tool_name || r.tool || "";
}

function rowRule(r: ToolAuditRow): string {
  return r.rule_name || r.rule || "";
}

function rowDecision(r: ToolAuditRow): string {
  return (r.decision || "").toLowerCase();
}

// Backends emit ts as either ISO-8601 (a2-claude) or epoch seconds (a2-codex).
// Normalise to a millisecond number for sorting; render separately.
function rowTimestampMs(r: ToolAuditRow): number {
  const t = r.ts;
  if (typeof t === "number") return t * 1000;
  if (typeof t === "string") {
    const parsed = Date.parse(t);
    if (!Number.isNaN(parsed)) return parsed;
    // Numeric string fall-through for epoch-as-string edge cases.
    const asNum = Number(t);
    if (Number.isFinite(asNum)) return asNum * 1000;
  }
  return 0;
}

const sorted = computed<ToolAuditRow[]>(() =>
  [...items.value].sort((a, b) => rowTimestampMs(b) - rowTimestampMs(a)),
);

const agentOptions = computed(() => {
  const set = new Set<string>();
  for (const r of sorted.value) set.add(r._agent);
  return Array.from(set).sort();
});

const toolOptions = computed(() => {
  const set = new Set<string>();
  for (const r of sorted.value) {
    const t = rowTool(r);
    if (t) set.add(t);
  }
  return Array.from(set).sort();
});

const filtered = computed(() => {
  const q = searchTerm.value.trim().toLowerCase();
  return sorted.value.filter((r) => {
    if (agentFilter.value && r._agent !== agentFilter.value) return false;
    if (q) {
      const hay = `${rowTool(r)} ${rowRule(r)} ${r.session_id ?? ""} ${r.reason ?? ""} ${r.command ?? ""}`.toLowerCase();
      if (!hay.includes(q)) return false;
    }
    return true;
  });
});

function formatTs(r: ToolAuditRow): string {
  const ms = rowTimestampMs(r);
  if (!ms) return String(r.ts ?? "");
  try {
    const d = new Date(ms);
    const msStr = String(d.getMilliseconds()).padStart(3, "0");
    const s = d.toLocaleString();
    return s.replace(/(\d{1,2}:\d{2}:\d{2})/, (m) => `${m}.${msStr}`);
  } catch {
    return String(r.ts ?? "");
  }
}

const expanded = ref<Set<string>>(new Set());

function rowKey(r: ToolAuditRow, i: number): string {
  return `${r._agent}|${r.session_id ?? ""}|${r.tool_use_id ?? ""}|${String(r.ts)}|${i}`;
}

function toggleExpand(key: string): void {
  const next = new Set(expanded.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  expanded.value = next;
}

function prettyJson(r: ToolAuditRow): string {
  // Drop the synthetic _agent tag so the raw row the backend wrote is the
  // visible payload. Operators use this view to audit exact on-disk content.
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { _agent, ...rest } = r;
  try {
    return JSON.stringify(rest, null, 2);
  } catch {
    return String(r);
  }
}
</script>

<template>
  <div class="tool-audit-view" data-testid="list-tool-audit">
    <div class="toolbar">
      <h2 class="title">Tool audit</h2>
      <input
        v-model="searchTerm"
        class="search"
        type="text"
        placeholder="filter tool / rule / reason / command…"
      />
      <select v-model="agentFilter" class="select" aria-label="agent">
        <option value="">all agents</option>
        <option v-for="a in agentOptions" :key="a" :value="a">{{ a }}</option>
      </select>
      <select v-model="decisionFilter" class="select" aria-label="decision">
        <option value="">all decisions</option>
        <option value="allow">allow</option>
        <option value="warn">warn</option>
        <option value="deny">deny</option>
      </select>
      <select v-model="toolFilter" class="select" aria-label="tool">
        <option value="">all tools</option>
        <option v-for="t in toolOptions" :key="t" :value="t">{{ t }}</option>
      </select>
      <input
        v-model="sessionFilter"
        class="session-input"
        type="text"
        placeholder="session id"
        aria-label="session"
      />
      <select v-model.number="limit" class="select" aria-label="limit">
        <option :value="50">50</option>
        <option :value="100">100</option>
        <option :value="250">250</option>
        <option :value="500">500</option>
      </select>
      <span class="count">{{ filtered.length }} / {{ items.length }}</span>
      <span
        v-if="degradedEntries.length > 0"
        class="degraded"
        :title="degradedTooltip"
        data-testid="list-tool-audit-degraded"
      >
        <i class="pi pi-exclamation-triangle" aria-hidden="true" />
        {{ degradedEntries.length }} degraded
      </span>
      <button class="refresh" type="button" :disabled="loading" @click="refresh">
        <i class="pi pi-refresh" aria-hidden="true" />
      </button>
    </div>

    <div class="feed">
      <div v-if="loading && items.length === 0" class="state">Loading…</div>
      <div v-else-if="error && items.length === 0" class="state state-error">
        {{ error }}
      </div>
      <div v-else-if="filtered.length === 0" class="state">No audit rows.</div>
      <table v-else class="tbl">
        <thead>
          <tr>
            <th />
            <th>Timestamp</th>
            <th>Tool</th>
            <th>Decision</th>
            <th>Rule</th>
            <th>Source</th>
            <th>Agent</th>
            <th>Session</th>
            <th>Traceparent</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="(row, i) in filtered" :key="rowKey(row, i)">
            <tr
              class="row"
              :class="`decision-row-${rowDecision(row) || 'none'}`"
              @click="toggleExpand(rowKey(row, i))"
            >
              <td class="chev">
                <i
                  class="pi"
                  :class="
                    expanded.has(rowKey(row, i))
                      ? 'pi-chevron-down'
                      : 'pi-chevron-right'
                  "
                  aria-hidden="true"
                />
              </td>
              <td class="ts">{{ formatTs(row) }}</td>
              <td class="tool">{{ rowTool(row) }}</td>
              <td class="decision">
                <span
                  v-if="rowDecision(row)"
                  :class="`pill pill-${rowDecision(row)}`"
                  >{{ rowDecision(row) }}</span
                >
              </td>
              <td class="rule">{{ rowRule(row) }}</td>
              <td class="source">{{ row.source ?? "" }}</td>
              <td class="agent">
                <span class="agent-name">{{ row.agent ?? "" }}</span>
                <span class="team">@{{ row._agent }}</span>
              </td>
              <td class="session">{{ row.session_id ?? "" }}</td>
              <td class="traceparent">{{ row.traceparent ?? "" }}</td>
            </tr>
            <tr v-if="expanded.has(rowKey(row, i))" class="expanded">
              <td colspan="9">
                <pre class="json">{{ prettyJson(row) }}</pre>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.tool-audit-view {
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

.session-input {
  width: 200px;
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
.select:focus,
.session-input:focus {
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
  font-family: var(--nyx-mono);
  font-size: 11px;
  color: var(--nyx-text);
}

.tbl thead th {
  position: sticky;
  top: 0;
  background: var(--nyx-surface);
  border-bottom: 1px solid var(--nyx-border);
  color: var(--nyx-dim);
  text-align: left;
  padding: 6px 10px;
  text-transform: uppercase;
  font-size: 10px;
  letter-spacing: 0.05em;
  font-weight: 600;
}

.tbl tbody td {
  padding: 6px 10px;
  border-bottom: 1px solid var(--nyx-border);
  vertical-align: top;
}

.row {
  cursor: pointer;
}

.row:hover {
  background: color-mix(in srgb, var(--nyx-accent) 8%, transparent);
}

.chev {
  width: 14px;
  color: var(--nyx-dim);
}

.ts {
  white-space: nowrap;
  color: var(--nyx-dim);
}

.tool,
.rule {
  color: var(--nyx-bright);
}

.source {
  color: var(--nyx-dim);
}

.agent-name {
  color: var(--nyx-text);
}

.team {
  color: var(--nyx-accent);
  margin-left: 6px;
}

.session,
.traceparent {
  color: var(--nyx-dim);
  word-break: break-all;
}

.pill {
  display: inline-block;
  padding: 1px 6px;
  border-radius: var(--nyx-radius);
  font-size: 10px;
  border: 1px solid var(--nyx-border);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.pill-allow {
  color: var(--nyx-green, #3db37a);
  border-color: color-mix(in srgb, var(--nyx-green, #3db37a) 45%, var(--nyx-border));
}

.pill-warn {
  color: var(--nyx-amber, #d2a24c);
  border-color: color-mix(in srgb, var(--nyx-amber, #d2a24c) 45%, var(--nyx-border));
}

.pill-deny {
  color: var(--nyx-red);
  border-color: color-mix(in srgb, var(--nyx-red) 45%, var(--nyx-border));
}

.decision-row-deny {
  background: color-mix(in srgb, var(--nyx-red) 6%, transparent);
}

.decision-row-warn {
  background: color-mix(in srgb, var(--nyx-amber, #d2a24c) 6%, transparent);
}

.expanded td {
  background: var(--nyx-bg);
  padding: 10px 14px;
}

.json {
  margin: 0;
  white-space: pre;
  font-family: var(--nyx-mono);
  font-size: 11px;
  color: var(--nyx-text);
  overflow-x: auto;
}
</style>
