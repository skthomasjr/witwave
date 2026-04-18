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

const flatSpans = computed<FlatRow[]>(() => {
  if (!detail.value) return [];
  const { roots, traceStart, traceEnd } = buildSpanTree(detail.value);
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

const selectedId = computed<string>(
  () => (route.params.traceId as string | undefined) ?? "",
);

const totalDuration = computed<number>(() => {
  if (!detail.value) return 0;
  const { traceStart, traceEnd } = buildSpanTree(detail.value);
  return Math.max(traceEnd - traceStart, 0);
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
    </div>

    <div class="body">
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
          <button class="btn" type="button" @click="closeDrawer">Close</button>
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
              <div
                class="span-bar"
                :style="{ left: `${row.leftPct}%`, width: `${row.widthPct}%` }"
              />
            </div>
            <div v-if="highlightsForSpan(row.node.span).length > 0" class="span-tags">
              <span
                v-for="h in highlightsForSpan(row.node.span)"
                :key="h.key"
                class="tag"
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
  min-width: 240px;
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  color: var(--nyx-text);
  font-family: var(--nyx-mono);
  font-size: 11px;
  padding: 4px 8px;
  border-radius: var(--nyx-radius);
}

.select,
.btn,
.refresh {
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  color: var(--nyx-text);
  font-family: var(--nyx-mono);
  font-size: 11px;
  padding: 4px 10px;
  border-radius: var(--nyx-radius);
  cursor: pointer;
}

.btn:hover,
.refresh:hover:not(:disabled) {
  color: var(--nyx-bright);
  border-color: var(--nyx-muted);
}

.refresh:disabled {
  opacity: 0.4;
  cursor: default;
}

.count,
.endpoint {
  font-size: 10px;
  color: var(--nyx-dim);
}

.endpoint {
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.badge-incluster {
  font-size: 10px;
  font-family: var(--nyx-mono);
  color: var(--nyx-accent);
  background: color-mix(in srgb, var(--nyx-accent) 12%, var(--nyx-surface));
  border: 1px solid color-mix(in srgb, var(--nyx-accent) 40%, var(--nyx-border));
  border-radius: 3px;
  padding: 1px 6px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.state .hint {
  margin-top: 8px;
  font-size: 10px;
  color: var(--nyx-dim);
}

.state .hint code {
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
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
  border-right: 1px solid var(--nyx-border);
  max-width: 55%;
}

.drawer {
  width: 45%;
  min-width: 420px;
  overflow: auto;
  padding: 14px;
  background: var(--nyx-surface);
}

.drawer-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding-bottom: 10px;
  margin-bottom: 10px;
  border-bottom: 1px solid var(--nyx-border);
}

.drawer-title {
  font-size: 12px;
  color: var(--nyx-bright);
  font-family: var(--nyx-mono);
}

.drawer-sub {
  font-size: 10px;
  color: var(--nyx-dim);
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

.state-unconfigured {
  text-align: left;
  max-width: 720px;
  margin: 30px auto;
  color: var(--nyx-text);
  font-size: 12px;
  line-height: 1.55;
}

.state-unconfigured h3 {
  font-size: 13px;
  color: var(--nyx-bright);
  margin: 0 0 8px;
}

.state-unconfigured code {
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  border-radius: 3px;
  padding: 1px 4px;
  font-size: 11px;
}

.state-unconfigured .hint {
  color: var(--nyx-dim);
  font-size: 11px;
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

.trace-row {
  cursor: pointer;
}

.trace-row:hover {
  background: var(--nyx-surface);
}

.trace-row.selected {
  background: color-mix(in srgb, var(--nyx-accent) 14%, var(--nyx-surface));
}

.ts,
.tid {
  color: var(--nyx-dim);
  white-space: nowrap;
}

.svc,
.op {
  color: var(--nyx-text);
}

.dur,
.spans {
  color: var(--nyx-bright);
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
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  background: var(--nyx-bg);
}

.span-row.status-error {
  border-color: color-mix(in srgb, var(--nyx-red) 40%, var(--nyx-border));
}

.span-meta {
  display: flex;
  gap: 8px;
  font-size: 11px;
  font-family: var(--nyx-mono);
  align-items: baseline;
}

.span-svc {
  color: var(--nyx-accent);
}

.span-op {
  color: var(--nyx-text);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.span-dur {
  color: var(--nyx-dim);
  font-size: 10px;
}

.span-bar-track {
  position: relative;
  height: 6px;
  background: var(--nyx-surface);
  border-radius: 2px;
}

.span-bar {
  position: absolute;
  top: 0;
  bottom: 0;
  background: var(--nyx-accent);
  border-radius: 2px;
  min-width: 2px;
}

.status-error .span-bar {
  background: var(--nyx-red);
}

.span-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.tag {
  font-size: 10px;
  font-family: var(--nyx-mono);
  background: var(--nyx-surface);
  border: 1px solid var(--nyx-border);
  border-radius: 3px;
  padding: 0 5px;
  color: var(--nyx-dim);
}
</style>
