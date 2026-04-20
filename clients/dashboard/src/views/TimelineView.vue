<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { storeToRefs } from "pinia";
import { useTimelineStore } from "../stores/timeline";
import { useTeam } from "../composables/useTeam";
import type { EventEnvelope } from "../composables/useEventStream";

// Live timeline view for the harness `/events/stream` SSE feed
// (#1110, phase 1). Left column is the chronological event list
// (newest at top), toolbar filters by agent / type / free-text, and
// pinned rows stick to the top regardless of new activity.
//
// The connection lifecycle is owned by the Pinia store so navigating
// away and back doesn't drop the stream; the view only mounts/unmounts
// UI state (pins, filters, expanded rows).

const { t } = useI18n();

const timeline = useTimelineStore();
const { events, connected, reconnecting, error } = storeToRefs(timeline);
const { members } = useTeam();

// Start the feed on first mount. `start()` is idempotent — subsequent
// mounts are no-ops and reuse the existing connection.
onMounted(() => {
  timeline.start();
});
onBeforeUnmount(() => {
  // Intentionally do NOT stop() on unmount — we want the feed to keep
  // running so the phase-2 AlertBanner can subscribe from elsewhere.
});

// -- UI state --------------------------------------------------------------

const selectedAgents = ref<string[]>([]);
const selectedTypes = ref<string[]>([]);
const searchTerm = ref<string>("");
const expandedIds = ref<Set<string>>(new Set());

const PINNED_STORAGE_KEY = "witwave.timeline.pinned";
const pinnedIds = ref<Set<string>>(new Set(readPinnedFromStorage()));

function readPinnedFromStorage(): string[] {
  if (typeof window === "undefined" || !window.localStorage) return [];
  try {
    const raw = window.localStorage.getItem(PINNED_STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    return Array.isArray(parsed)
      ? parsed.filter((x): x is string => typeof x === "string")
      : [];
  } catch {
    return [];
  }
}

// #1381: cap pinned-id persistence so a long-lived tab doesn't blow
// past Safari's 5MB localStorage quota. Event IDs drop off the 1000-
// entry ring over days of pinning; keep the most-recently-pinned 1000.
const PINNED_PERSIST_CAP = 1000;

function persistPinned(): void {
  if (typeof window === "undefined" || !window.localStorage) return;
  try {
    let arr = Array.from(pinnedIds.value);
    // #1408: clamp the serialised array only; don't mutate
    // `pinnedIds.value` inside this watcher callback. Reassigning the
    // Set re-triggers the watcher and risks a persist/clamp loop near
    // the 1000 boundary. Accept a 1-cycle drift between localStorage
    // and in-memory state; the next pin/unpin will reconcile.
    if (arr.length > PINNED_PERSIST_CAP) {
      arr = arr.slice(-PINNED_PERSIST_CAP);
    }
    window.localStorage.setItem(
      PINNED_STORAGE_KEY,
      JSON.stringify(arr),
    );
  } catch {
    // Ignore quota / serialisation errors — pins are a UX nicety.
  }
}

// Relies on whole-ref replacement in `togglePinned` (the Set is
// reassigned, not mutated in place). Mutating the Set in place would
// silently break persistence — `deep: true` on a Set is a no-op because
// Vue does not proxy Set internals for structural reactivity. (#1164)
watch(pinnedIds, () => {
  persistPinned();
});

// -- Known types + agents --------------------------------------------------

const KNOWN_TYPES: ReadonlyArray<string> = [
  "job.fired",
  "task.fired",
  "heartbeat.fired",
  "continuation.fired",
  "trigger.fired",
  "webhook.delivered",
  "webhook.failed",
  "hook.decision",
  "a2a.request.received",
  "a2a.request.completed",
  "agent.lifecycle",
  "stream.gap",
  "stream.overrun",
];

const knownAgents = computed<string[]>(() => {
  const names = new Set<string>();
  for (const m of members.value) names.add(m.name);
  // Also include any agent_ids we've actually seen in the feed — the
  // harness may emit events for agents not in the current team snapshot
  // (e.g. transient config reloads).
  for (const e of events.value) {
    if (e.agent_id) names.add(e.agent_id);
  }
  return Array.from(names).sort();
});

// -- Filtered + ordered view -----------------------------------------------

const filtered = computed<EventEnvelope[]>(() => {
  let rows = events.value.slice().reverse(); // newest first

  if (selectedTypes.value.length > 0) {
    const set = new Set(selectedTypes.value);
    rows = rows.filter((r) => set.has(r.type));
  }
  if (selectedAgents.value.length > 0) {
    const set = new Set(selectedAgents.value);
    rows = rows.filter((r) => (r.agent_id ? set.has(r.agent_id) : false));
  }
  if (searchTerm.value.trim().length > 0) {
    const q = searchTerm.value.trim().toLowerCase();
    rows = rows.filter((r) => {
      try {
        return JSON.stringify(r).toLowerCase().includes(q);
      } catch {
        return false;
      }
    });
  }

  // Split into pinned + rest; pinned rows preserve insertion order
  // (newest-first within their group so new pins float above older ones).
  const pinned: EventEnvelope[] = [];
  const rest: EventEnvelope[] = [];
  for (const row of rows) {
    if (pinnedIds.value.has(row.id)) pinned.push(row);
    else rest.push(row);
  }
  return [...pinned, ...rest];
});

// -- Row interactions ------------------------------------------------------

function toggleExpanded(id: string): void {
  const next = new Set(expandedIds.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  expandedIds.value = next;
}

function togglePinned(id: string, ev?: Event): void {
  if (ev) ev.stopPropagation();
  const next = new Set(pinnedIds.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  pinnedIds.value = next;
}

// -- Connection pill -------------------------------------------------------

type PillState = "live" | "reconnecting" | "disconnected";

const pillState = computed<PillState>(() => {
  if (connected.value) return "live";
  if (reconnecting.value) return "reconnecting";
  return "disconnected";
});

const pillLabel = computed(() => {
  switch (pillState.value) {
    case "live":
      return t("timeline.pill.live");
    case "reconnecting":
      return t("timeline.pill.reconnecting");
    default:
      return t("timeline.pill.disconnected");
  }
});

// -- Rendering helpers -----------------------------------------------------

function formatTs(ts: string): string {
  if (!ts) return "";
  try {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return ts;
    return d.toISOString().slice(11, 23) + "Z";
  } catch {
    return ts;
  }
}

// Terse per-type summary — keeps the row compact; full payload lives in
// the expanded drawer. Unknown types fall back to a JSON preview so
// forward-compat payloads are still visible.
function summarise(e: EventEnvelope): string {
  const p = (e.payload ?? {}) as Record<string, unknown>;
  switch (e.type) {
    case "job.fired":
    case "task.fired":
    case "trigger.fired":
    case "continuation.fired":
      return `${p.name ?? "-"} · ${p.outcome ?? "-"} · ${formatDuration(p.duration_ms)}`;
    case "heartbeat.fired":
      return `${p.outcome ?? "-"} · ${formatDuration(p.duration_ms)}`;
    case "webhook.delivered":
      return `${p.name ?? "-"} → ${p.url_host ?? "-"} · ${p.status_code ?? "-"}`;
    case "webhook.failed":
      return `${p.name ?? "-"} → ${p.url_host ?? "-"} · ${p.reason ?? "-"}`;
    case "hook.decision":
      return `${p.backend ?? "-"} · ${p.tool ?? "-"} · ${p.decision ?? "-"}${p.rule_id ? ` (${p.rule_id})` : ""}`;
    case "a2a.request.received":
      return `${p.concern ?? "-"}${p.model ? ` · ${p.model}` : ""}`;
    case "a2a.request.completed":
      return `${p.concern ?? "-"} · ${p.outcome ?? "-"} · ${formatDuration(p.duration_ms)}`;
    case "agent.lifecycle":
      return `${p.backend ?? "-"} · ${p.event ?? "-"}${p.detail ? ` — ${p.detail}` : ""}`;
    case "stream.gap":
      return t("timeline.gap.marker");
    case "stream.overrun":
      return t("timeline.overrun.marker");
    default:
      try {
        return JSON.stringify(p).slice(0, 120);
      } catch {
        return "";
      }
  }
}

function formatDuration(v: unknown): string {
  if (typeof v !== "number" || !Number.isFinite(v)) return "–";
  if (v < 1000) return `${v} ms`;
  return `${(v / 1000).toFixed(2)} s`;
}

function isGapEvent(e: EventEnvelope): boolean {
  return e.type === "stream.gap" || e.type === "stream.overrun";
}

function formatPayload(e: EventEnvelope): string {
  try {
    return JSON.stringify(
      {
        id: e.id,
        ts: e.ts,
        agent_id: e.agent_id,
        version: e.version,
        payload: e.payload,
      },
      null,
      2,
    );
  } catch {
    return "(unserialisable)";
  }
}
</script>

<template>
  <div class="timeline-view" data-testid="view-timeline">
    <div class="toolbar">
      <h2 class="title">{{ t("timeline.title") }}</h2>
      <input
        v-model="searchTerm"
        class="search"
        type="text"
        :placeholder="t('timeline.search.placeholder')"
        data-testid="timeline-search"
      />
      <select
        v-model="selectedAgents"
        multiple
        class="select multi"
        :aria-label="t('timeline.filter.agents')"
        data-testid="timeline-agent-filter"
      >
        <option v-for="a in knownAgents" :key="a" :value="a">{{ a }}</option>
      </select>
      <select
        v-model="selectedTypes"
        multiple
        class="select multi"
        :aria-label="t('timeline.filter.types')"
        data-testid="timeline-type-filter"
      >
        <option v-for="kt in KNOWN_TYPES" :key="kt" :value="kt">
          {{ kt }}
        </option>
      </select>
      <span class="count" data-testid="timeline-count">
        {{ filtered.length }} / {{ events.length }}
      </span>
      <span
        class="pill"
        :class="`pill-${pillState}`"
        :title="error || pillLabel"
        data-testid="timeline-status-pill"
        role="status"
        aria-live="polite"
      >
        {{ pillLabel }}
      </span>
    </div>

    <div class="feed">
      <div v-if="filtered.length === 0" class="state">
        {{ t("timeline.empty") }}
      </div>
      <ul v-else class="rows" data-testid="timeline-rows">
        <li
          v-for="row in filtered"
          :key="row.id"
          :class="{
            row: true,
            expanded: expandedIds.has(row.id),
            pinned: pinnedIds.has(row.id),
            gap: isGapEvent(row),
          }"
          :data-event-id="row.id"
          :data-event-type="row.type"
          :data-testid="`timeline-row-${row.id}`"
          @click="toggleExpanded(row.id)"
        >
          <div class="row-head">
            <button
              type="button"
              class="pin-btn"
              :class="{ 'is-pinned': pinnedIds.has(row.id) }"
              :aria-pressed="pinnedIds.has(row.id)"
              :aria-label="
                pinnedIds.has(row.id)
                  ? t('timeline.pin.unpinLabel')
                  : t('timeline.pin.pinLabel')
              "
              :data-testid="`timeline-pin-${row.id}`"
              @click="togglePinned(row.id, $event)"
            >
              <i
                :class="
                  pinnedIds.has(row.id) ? 'pi pi-bookmark-fill' : 'pi pi-bookmark'
                "
                aria-hidden="true"
              />
            </button>
            <span class="ts">{{ formatTs(row.ts) }}</span>
            <span class="chip chip-agent">
              {{ row.agent_id ?? t("timeline.agentGlobal") }}
            </span>
            <span class="chip chip-type">{{ row.type }}</span>
            <span class="summary">{{ summarise(row) }}</span>
          </div>
          <pre v-if="expandedIds.has(row.id)" class="payload">{{
            formatPayload(row)
          }}</pre>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.timeline-view {
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
  flex: 1;
  min-width: 200px;
  background: var(--witwave-bg);
  border: 1px solid var(--witwave-border);
  color: var(--witwave-text);
  font-family: var(--witwave-mono);
  font-size: 11px;
  padding: 4px 8px;
  border-radius: var(--witwave-radius);
}

.select {
  background: var(--witwave-bg);
  border: 1px solid var(--witwave-border);
  color: var(--witwave-text);
  font-family: var(--witwave-mono);
  font-size: 11px;
  padding: 4px 6px;
  border-radius: var(--witwave-radius);
}

.select.multi {
  min-width: 120px;
  max-width: 180px;
  height: 28px;
}

.count {
  color: var(--witwave-dim);
  font-family: var(--witwave-mono);
  font-size: 11px;
}

.pill {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 999px;
  font-family: var(--witwave-mono);
  font-size: 10px;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  border: 1px solid var(--witwave-border);
}

.pill::before {
  content: "";
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--witwave-muted);
}

.pill-live {
  color: var(--witwave-green);
  border-color: var(--witwave-green);
}

.pill-live::before {
  background: var(--witwave-green);
}

.pill-reconnecting {
  color: var(--witwave-yellow);
  border-color: var(--witwave-yellow);
}

.pill-reconnecting::before {
  background: var(--witwave-yellow);
}

.pill-disconnected {
  color: var(--witwave-red);
  border-color: var(--witwave-red);
}

.pill-disconnected::before {
  background: var(--witwave-red);
}

.feed {
  flex: 1;
  overflow-y: auto;
  padding: 0;
}

.state {
  padding: 24px 14px;
  color: var(--witwave-dim);
  font-family: var(--witwave-mono);
  font-size: 12px;
  text-align: center;
}

.rows {
  list-style: none;
  margin: 0;
  padding: 0;
}

.row {
  border-bottom: 1px solid var(--witwave-border);
  padding: 6px 14px;
  cursor: pointer;
  font-family: var(--witwave-mono);
  font-size: 11px;
  transition: background 0.08s;
}

.row:hover {
  background: var(--witwave-surface);
}

.row.pinned {
  background: color-mix(in srgb, var(--witwave-accent) 8%, transparent);
  border-left: 2px solid var(--witwave-accent);
  padding-left: 12px;
}

.row.gap {
  background: color-mix(in srgb, var(--witwave-yellow) 10%, transparent);
  border-left: 2px solid var(--witwave-yellow);
  padding-left: 12px;
}

.row-head {
  display: flex;
  align-items: center;
  gap: 8px;
  overflow: hidden;
}

.pin-btn {
  background: none;
  border: none;
  color: var(--witwave-dim);
  cursor: pointer;
  padding: 2px 4px;
  border-radius: var(--witwave-radius);
}

.pin-btn:hover {
  color: var(--witwave-text);
  background: var(--witwave-bg);
}

.pin-btn.is-pinned {
  color: var(--witwave-accent);
}

.ts {
  color: var(--witwave-dim);
  min-width: 90px;
}

.chip {
  display: inline-block;
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 10px;
  letter-spacing: 0.04em;
}

.chip-agent {
  background: var(--witwave-bg);
  border: 1px solid var(--witwave-border);
  color: var(--witwave-text);
}

.chip-type {
  background: color-mix(in srgb, var(--witwave-teal) 14%, transparent);
  color: var(--witwave-teal);
  border: 1px solid color-mix(in srgb, var(--witwave-teal) 30%, transparent);
}

.summary {
  color: var(--witwave-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
}

.payload {
  background: var(--witwave-bg);
  border: 1px solid var(--witwave-border);
  border-radius: var(--witwave-radius);
  padding: 8px 10px;
  margin: 6px 0 2px 24px;
  font-size: 11px;
  color: var(--witwave-text);
  max-height: 320px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
