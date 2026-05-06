<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  buildSpanTree,
  flattenSpanTree,
  formatMicros,
  formatStart,
  highlightsForSpan,
  statusForSpan,
  useOTelTraces,
} from "../composables/useOTelTraces";
import type { SpanNode } from "../composables/useOTelTraces";
import { exportCsv, exportJson, timestamped } from "../utils/export";

// OTel distributed-trace viewer (#632). Queries an operator-configured
// Jaeger/Tempo HTTP API for recent traces and renders the span tree on
// selection. Distinct from TraceView.vue (#592), which reads the harness
// /trace JSONL feed for per-agent tool events. Both views coexist — their
// data sources and use cases don't overlap.

const route = useRoute();
const router = useRouter();

const serviceInput = ref<string>("");

const {
  configured,
  baseUrl,
  inClusterMode,
  limit,
  service,
  list,
  listError,
  listLoading,
  detail,
  detailError,
  detailLoading,
  refreshList,
  loadDetail,
  clearDetail,
  retryProbe,
} = useOTelTraces({ limit: 20 });

// Seed the service input from the composable's ref and keep them in sync —
// the input edits a local ref so typing doesn't thrash fetches on each
// keypress; the "Apply" action pushes the value through.
serviceInput.value = service.value;

function applyService(): void {
  service.value = serviceInput.value.trim();
  void refreshList();
}

// Deep-link: /otel-traces/<id> auto-opens the detail drawer for that id
// (#636 hands us trace_id on conversation rows; this is the landing pad).
function openTrace(traceID: string): void {
  router.push({ name: "otel-traces-detail", params: { traceId: traceID } });
}

function closeDrawer(): void {
  clearDetail();
  router.push({ name: "otel-traces" });
}

// Export handlers (#1105). The list tab exports the TraceListRow summary
// (one row per trace) in the order currently rendered; the detail drawer
// exports the span tree of the open trace as JSON. CSV on the detail
// view would lose the span-tree structure, so it's JSON-only.
const otelListColumns = ["traceID", "startTime", "duration", "spanCount", "rootService", "rootOperation"];
function onExportListJson(): void {
  exportJson(list.value, timestamped("witwave-otel-traces", "json"));
}
function onExportListCsv(): void {
  exportCsv(
    list.value as unknown as Record<string, unknown>[],
    otelListColumns,
    timestamped("witwave-otel-traces", "csv"),
  );
}
function onExportDetailJson(): void {
  if (!detail.value) return;
  exportJson([detail.value], timestamped(`witwave-otel-trace-${detail.value.traceID}`, "json"));
}

watch(
  () => (route.params.traceId as string | undefined) ?? "",
  async (traceId) => {
    if (!traceId) {
      clearDetail();
      return;
    }
    await loadDetail(traceId);
  },
  { immediate: true },
);

interface FlatRow {
  node: SpanNode;
  widthPct: number;
  leftPct: number;
}

// Share one buildSpanTree() invocation between flatSpans and
// totalDuration (#898). Previously each computed called buildSpanTree
// independently, duplicating O(span-count) work on every recompute of
// the detail — compounds noticeably with the larger trace sizes
// introduced by #746.
const spanTree = computed(() => {
  if (!detail.value) return null;
  return buildSpanTree(detail.value);
});

const flatSpans = computed<FlatRow[]>(() => {
  const tree = spanTree.value;
  if (!tree) return [];
  const { roots, traceStart, traceEnd } = tree;
  const total = Math.max(traceEnd - traceStart, 1);
  const nodes = flattenSpanTree(roots);
  return nodes.map((node) => {
    const offset = node.offsetMicros;
    const dur = Math.max(node.span.duration, 0);
    return {
      node,
      leftPct: (offset / total) * 100,
      widthPct: Math.max((dur / total) * 100, 0.25),
    };
  });
});

const selectedId = computed<string>(() => (route.params.traceId as string | undefined) ?? "");

const totalDuration = computed<number>(() => {
  const tree = spanTree.value;
  if (!tree) return 0;
  return Math.max(tree.traceEnd - tree.traceStart, 0);
});
</script>

<template>
  <div class="otel-view" data-testid="list-otel-traces">
    <div class="toolbar">
      <h2 class="title">Traces</h2>
      <input
        v-model="serviceInput"
        class="search"
        type="text"
        placeholder="service (e.g. iris-claude)"
        @keyup.enter="applyService"
      />
      <button class="btn" type="button" @click="applyService">Apply</button>
      <select v-model.number="limit" class="select" aria-label="limit" @change="refreshList">
        <option :value="10">10</option>
        <option :value="20">20</option>
        <option :value="50">50</option>
        <option :value="100">100</option>
      </select>
      <span class="count">{{ list.length }} traces</span>
      <span v-if="baseUrl" class="endpoint" :title="baseUrl">endpoint: {{ baseUrl }}</span>
      <span
        v-else-if="inClusterMode"
        class="badge-incluster"
        title="Reading spans from the harness in-memory ring buffer. No external collector required."
        >in-cluster</span
      >
      <button class="refresh" type="button" :disabled="listLoading" @click="refreshList">
        <i class="pi pi-refresh" aria-hidden="true" />
      </button>
      <button
        class="export"
        type="button"
        :disabled="list.length === 0"
        title="Download trace list as JSON"
        data-testid="export-otel-list-json"
        @click="onExportListJson"
      >
        <i class="pi pi-download" aria-hidden="true" /> JSON
      </button>
      <button
        class="export"
        type="button"
        :disabled="list.length === 0"
        title="Download trace list as CSV"
        data-testid="export-otel-list-csv"
        @click="onExportListCsv"
      >
        <i class="pi pi-download" aria-hidden="true" /> CSV
      </button>
    </div>

    <div v-if="!configured" class="state state-unconfigured" data-testid="otel-not-configured">
      <h3>Trace viewer is not configured</h3>
      <p>
        No external trace backend is set and the in-cluster span source is not reachable. The dashboard renders traces
        from one of two sources:
      </p>
      <ul>
        <li>
          <strong>External Jaeger / Tempo.</strong> Set <code>VITE_TRACE_API_URL</code> at build time (or
          <code>traceApiUrl</code> in the chart values) to point at your collector's HTTP API.
        </li>
        <li>
          <strong>In-cluster in-memory ring buffer.</strong> Ensure <code>OTEL_IN_MEMORY_SPANS</code> is enabled on the
          backends and the dashboard can reach <code>/api/team</code>. This path recovers automatically once at least
          one backend responds.
        </li>
      </ul>
      <p class="hint">
        If you expect in-cluster mode to be active, the probe retries every minute — or you can retry now.
      </p>
      <button class="btn" type="button" @click="retryProbe">Retry probe</button>
    </div>

    <div v-else class="body">
      <div class="list-pane" :class="{ 'has-detail': selectedId }">
        <div v-if="listLoading && list.length === 0" class="state">Loading…</div>
        <div v-else-if="listError && list.length === 0" class="state state-error">
          {{ listError }}
        </div>
        <div v-else-if="list.length === 0" class="state">
          <div>No traces returned.</div>
          <div v-if="inClusterMode" class="hint">
            Spans accumulate only after workloads run. Ring buffer holds the last
            <code>OTEL_IN_MEMORY_SPANS</code> spans (default 1000) per pod.
          </div>
        </div>
        <table v-else class="tbl">
          <thead>
            <tr>
              <th>Started</th>
              <th>Service</th>
              <th>Operation</th>
              <th>Duration</th>
              <th>Spans</th>
              <th>Trace ID</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="row in list"
              :key="row.traceID"
              :class="{ selected: row.traceID === selectedId }"
              class="trace-row"
              @click="openTrace(row.traceID)"
            >
              <td class="ts">{{ formatStart(row.startTime) }}</td>
              <td class="svc">{{ row.rootService }}</td>
              <td class="op">{{ row.rootOperation }}</td>
              <td class="dur">{{ formatMicros(row.duration) }}</td>
              <td class="spans">{{ row.spanCount }}</td>
              <td class="tid" :title="row.traceID">{{ row.traceID.slice(0, 16) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <aside v-if="selectedId" class="drawer" data-testid="otel-drawer">
        <div class="drawer-head">
          <div>
            <div class="drawer-title">Trace {{ selectedId.slice(0, 16) }}</div>
            <div class="drawer-sub">
              {{ detail ? `${detail.spans.length} spans, ${formatMicros(totalDuration)}` : "" }}
            </div>
          </div>
          <div class="drawer-actions">
            <button
              class="btn"
              type="button"
              :disabled="!detail"
              title="Download trace detail as JSON"
              data-testid="export-otel-detail-json"
              @click="onExportDetailJson"
            >
              <i class="pi pi-download" aria-hidden="true" /> JSON
            </button>
            <button class="btn" type="button" @click="closeDrawer">Close</button>
          </div>
        </div>
        <div v-if="detailLoading" class="state">Loading trace…</div>
        <div v-else-if="detailError" class="state state-error">{{ detailError }}</div>
        <div v-else-if="!detail" class="state">No detail.</div>
        <div v-else class="spans-list">
          <div
            v-for="row in flatSpans"
            :key="row.node.span.spanID"
            class="span-row"
            :class="`status-${statusForSpan(row.node.span)}`"
            :style="{ paddingLeft: `${row.node.depth * 14}px` }"
          >
            <div class="span-meta">
              <span class="span-svc">{{ row.node.service }}</span>
              <span class="span-op">{{ row.node.span.operationName }}</span>
              <span class="span-dur">{{ formatMicros(row.node.span.duration) }}</span>
            </div>
            <div class="span-bar-track">
              <div class="span-bar" :style="{ left: `${row.leftPct}%`, width: `${row.widthPct}%` }" />
            </div>
            <div v-if="highlightsForSpan(row.node.span).length > 0" class="span-tags">
              <span v-for="h in highlightsForSpan(row.node.span)" :key="h.key" class="tag"
                >{{ h.key }}={{ h.value }}</span
              >
            </div>
          </div>
        </div>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.otel-view {
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
  flex-wrap: wrap;
}

.title {
  font-size: 12px;
  color: var(--witwave-bright);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  margin: 0;
  font-weight: 600;
}

.search {
  min-width: 240px;
  background: var(--witwave-bg);
  border: 1px solid var(--witwave-border);
  color: var(--witwave-text);
  font-family: var(--witwave-mono);
  font-size: 11px;
  padding: 4px 8px;
  border-radius: var(--witwave-radius);
}

.select,
.btn,
.refresh {
  background: var(--witwave-bg);
  border: 1px solid var(--witwave-border);
  color: var(--witwave-text);
  font-family: var(--witwave-mono);
  font-size: 11px;
  padding: 4px 10px;
  border-radius: var(--witwave-radius);
  cursor: pointer;
}

.btn:hover,
.refresh:hover:not(:disabled) {
  color: var(--witwave-bright);
  border-color: var(--witwave-muted);
}

.refresh:disabled {
  opacity: 0.4;
  cursor: default;
}

.count,
.endpoint {
  font-size: 10px;
  color: var(--witwave-dim);
}

.endpoint {
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.badge-incluster {
  font-size: 10px;
  font-family: var(--witwave-mono);
  color: var(--witwave-accent);
  background: color-mix(in srgb, var(--witwave-accent) 12%, var(--witwave-surface));
  border: 1px solid color-mix(in srgb, var(--witwave-accent) 40%, var(--witwave-border));
  border-radius: 3px;
  padding: 1px 6px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.state .hint {
  margin-top: 8px;
  font-size: 10px;
  color: var(--witwave-dim);
}

.state .hint code {
  background: var(--witwave-bg);
  border: 1px solid var(--witwave-border);
  border-radius: 3px;
  padding: 1px 4px;
}

.body {
  flex: 1;
  display: flex;
  min-height: 0;
  overflow: hidden;
}

.list-pane {
  flex: 1;
  overflow: auto;
  min-width: 0;
}

.list-pane.has-detail {
  border-right: 1px solid var(--witwave-border);
  max-width: 55%;
}

.drawer {
  width: 45%;
  min-width: 420px;
  overflow: auto;
  padding: 14px;
  background: var(--witwave-surface);
}

.drawer-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding-bottom: 10px;
  margin-bottom: 10px;
  border-bottom: 1px solid var(--witwave-border);
}

.drawer-title {
  font-size: 12px;
  color: var(--witwave-bright);
  font-family: var(--witwave-mono);
}

.drawer-sub {
  font-size: 10px;
  color: var(--witwave-dim);
}

.state {
  padding: 30px;
  color: var(--witwave-muted);
  font-size: 11px;
  text-align: center;
}

.state-error {
  color: var(--witwave-red);
}

.state-unconfigured {
  text-align: left;
  max-width: 720px;
  margin: 30px auto;
  color: var(--witwave-text);
  font-size: 12px;
  line-height: 1.55;
}

.state-unconfigured h3 {
  font-size: 13px;
  color: var(--witwave-bright);
  margin: 0 0 8px;
}

.state-unconfigured code {
  background: var(--witwave-bg);
  border: 1px solid var(--witwave-border);
  border-radius: 3px;
  padding: 1px 4px;
  font-size: 11px;
}

.state-unconfigured .hint {
  color: var(--witwave-dim);
  font-size: 11px;
}

.tbl {
  width: 100%;
  border-collapse: collapse;
  font-size: 11px;
  font-family: var(--witwave-mono);
}

.tbl th,
.tbl td {
  text-align: left;
  padding: 6px 10px;
  border-bottom: 1px solid var(--witwave-border);
  vertical-align: top;
}

.tbl th {
  color: var(--witwave-dim);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-size: 10px;
  font-weight: 600;
  background: var(--witwave-surface);
  position: sticky;
  top: 0;
  z-index: 1;
}

.trace-row {
  cursor: pointer;
}

.trace-row:hover {
  background: var(--witwave-surface);
}

.trace-row.selected {
  background: color-mix(in srgb, var(--witwave-accent) 14%, var(--witwave-surface));
}

.ts,
.tid {
  color: var(--witwave-dim);
  white-space: nowrap;
}

.svc,
.op {
  color: var(--witwave-text);
}

.dur,
.spans {
  color: var(--witwave-bright);
  white-space: nowrap;
}

.spans-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.span-row {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 4px 6px;
  border: 1px solid var(--witwave-border);
  border-radius: var(--witwave-radius);
  background: var(--witwave-bg);
}

.span-row.status-error {
  border-color: color-mix(in srgb, var(--witwave-red) 40%, var(--witwave-border));
}

.span-meta {
  display: flex;
  gap: 8px;
  font-size: 11px;
  font-family: var(--witwave-mono);
  align-items: baseline;
}

.span-svc {
  color: var(--witwave-accent);
}

.span-op {
  color: var(--witwave-text);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.span-dur {
  color: var(--witwave-dim);
  font-size: 10px;
}

.span-bar-track {
  position: relative;
  height: 6px;
  background: var(--witwave-surface);
  border-radius: 2px;
}

.span-bar {
  position: absolute;
  top: 0;
  bottom: 0;
  background: var(--witwave-accent);
  border-radius: 2px;
  min-width: 2px;
}

.status-error .span-bar {
  background: var(--witwave-red);
}

.span-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.tag {
  font-size: 10px;
  font-family: var(--witwave-mono);
  background: var(--witwave-surface);
  border: 1px solid var(--witwave-border);
  border-radius: 3px;
  padding: 0 5px;
  color: var(--witwave-dim);
}
</style>
