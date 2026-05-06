<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from "vue";
import { RouterLink } from "vue-router";
import { useI18n } from "vue-i18n";
import { useAgentFanout } from "../composables/useAgentFanout";
import {
  useConversationStream,
  type ConversationTurn,
  type UseConversationStreamReturn,
} from "../composables/useConversationStream";
import { apiGet, ApiError } from "../api/client";
import { renderMarkdown } from "../utils/markdown";
import { exportCsv, exportJson, timestamped } from "../utils/export";
import type { ConversationEntry } from "../types/chat";

// Aggregated conversation feed across all team members. Legacy ui read only
// the front-door agent's log; with direct routing we fan out and merge.
// Filters mirror the legacy ones minus the tool filter (no tool data on the
// line level today — re-add when the harness exposes it).
//
// #1110 phase 5: when an operator narrows the view to a specific
// (agent, session_id) the feed switches from /api/agents/<name>/conversations
// polling to the per-session SSE stream shipped in phase 4. One-shot fetch
// seeds the backlog, then `useConversationStream` supplies the live tail
// with assistant-turn chunk reassembly. Dropping back to the fleet view
// (clearing either filter) falls back to the polling fanout so the
// cross-agent overview stays intact.

type Row = ConversationEntry & { _agent: string };

const { t } = useI18n();

const limit = ref<number>(100);
const searchTerm = ref<string>("");
// Debounced copy of searchTerm used by the filter computed (#745).
// Without this, every keystroke recomputes the filter over the full
// items list — visibly janks the UI on large teams.
const searchTermDebounced = ref<string>("");
const SEARCH_DEBOUNCE_MS = 150;
let _searchTimer: ReturnType<typeof setTimeout> | null = null;
watch(searchTerm, (v) => {
  if (_searchTimer !== null) clearTimeout(_searchTimer);
  _searchTimer = setTimeout(() => {
    searchTermDebounced.value = v;
    _searchTimer = null;
  }, SEARCH_DEBOUNCE_MS);
});
onBeforeUnmount(() => {
  if (_searchTimer !== null) clearTimeout(_searchTimer);
});
const agentFilter = ref<string>("");
const roleFilter = ref<string>("");
const sessionFilter = ref<string>("");

// Fleet fanout — still the source of truth when no single session is
// selected. The composable handles per-agent polling, error tagging, and
// visibility-aware pause. When we're in stream mode (both agent + session
// selected) we ignore `items` and surface merged backlog+stream rows
// instead; this keeps the existing empty/loading/degraded affordances
// wired for the overview case.
// #1538: pause the fanout while drilling into a single session. Stream
// mode ignores the fanout's items entirely so the per-interval /
// conversations fetches were wasted bandwidth. streamMode flips true
// once both agent and session filters are set; the paused getter reads
// that reactively so toggling the filters re-engages fanout without
// re-instantiating the composable.
const { items, perAgentErrors, loading, error, refresh } = useAgentFanout<ConversationEntry>({
  endpoint: "conversations",
  // Pass the computed itself (not `.value`) so the composable can watch the
  // limit dropdown and re-fetch when it changes. Previously `.value` captured
  // the mount-time snapshot and further dropdown changes were ignored (#495).
  query: computed(() => ({ limit: String(limit.value) })),
  paused: () => streamMode.value,
});

const degradedEntries = computed<[string, string][]>(() => Object.entries(perAgentErrors.value));
const degradedTooltip = computed(() => degradedEntries.value.map(([a, m]) => `${a}: ${m}`).join("\n"));

const agentOptions = computed(() => {
  const set = new Set<string>();
  for (const i of items.value) set.add(i._agent);
  return Array.from(set).sort();
});

// Session dropdown is populated from the current fanout so the operator
// can pick one without having to remember the id. When an agent filter
// is active we show only that agent's sessions; otherwise we show
// sessions across the fleet prefixed with the agent name.
const sessionOptions = computed<{ value: string; label: string }[]>(() => {
  const agent = agentFilter.value;
  const seen = new Map<string, string>();
  for (const row of items.value) {
    const sid = row.session_id ?? "";
    if (!sid) continue;
    if (agent && row._agent !== agent) continue;
    const label = agent ? sid : `${row._agent} · ${sid}`;
    if (!seen.has(sid)) seen.set(sid, label);
  }
  return Array.from(seen.entries())
    .sort((a, b) => (a[1] < b[1] ? -1 : a[1] > b[1] ? 1 : 0))
    .map(([value, label]) => ({ value, label }));
});

// Stream mode is active iff we can address a single backend + session.
const streamMode = computed(() => agentFilter.value !== "" && sessionFilter.value !== "");

// -- Stream + backlog state (only used when streamMode is true) -----------

const backlogRows = ref<Row[]>([]);
const backlogError = ref<string>("");
const backlogLoading = ref<boolean>(false);
let backlogAborter: AbortController | null = null;
let convStream: UseConversationStreamReturn | null = null;
// Stop handles for the watchers wired up in openStream(). Without these,
// the watchers stay live after teardownStream() and continue mutating
// component refs from the closed stream's still-reactive sources after a
// filter switch (#1661).
let streamWatchers: Array<() => void> = [];
const streamConnected = ref<boolean>(false);
const streamReconnecting = ref<boolean>(false);
const streamError = ref<string>("");
const streamTurns = ref<ConversationTurn[]>([]);
// Tracks the current (agent, session) stream instance by monotonically
// incrementing generation. Any bridged turn carries the gen at which it
// was observed; switching sessions bumps the gen and filters out stale
// turns that might still flush from the previous composable. (#1165)
const currentStreamGen = ref<number>(0);
// Per-turn gen tagging keyed by turnId so late flushes from the prior
// session don't mis-tag as the new session's rows.
const streamTurnGens = new Map<string, number>();
// Polling-fallback latch: once the stream has been disconnected for >15s
// we flip this true and show the "polling fallback" pill. Flips back
// false as soon as the stream reconnects.
const pollingFallback = ref<boolean>(false);
let disconnectTimer: ReturnType<typeof setTimeout> | null = null;
const DISCONNECT_GRACE_MS = 15_000;

function clearDisconnectTimer(): void {
  if (disconnectTimer !== null) {
    clearTimeout(disconnectTimer);
    disconnectTimer = null;
  }
}

function teardownStream(): void {
  clearDisconnectTimer();
  // Stop the bridged watchers BEFORE closing the stream so a final
  // synchronous flush from the composable can't sneak through and mutate
  // component refs that we're about to reset (#1661). Iterate defensively
  // — a thrown stop handle from one watcher must not strand the others.
  for (const stop of streamWatchers) {
    try {
      stop();
    } catch {
      // ignore
    }
  }
  streamWatchers = [];
  if (convStream) {
    try {
      convStream.close();
    } catch {
      // ignore
    }
    convStream = null;
  }
  streamConnected.value = false;
  streamReconnecting.value = false;
  streamError.value = "";
  streamTurns.value = [];
  streamTurnGens.clear();
  pollingFallback.value = false;
}

async function loadBacklog(agent: string, session: string): Promise<void> {
  backlogAborter?.abort();
  backlogAborter = new AbortController();
  backlogLoading.value = true;
  backlogError.value = "";
  try {
    const raw = await apiGet<ConversationEntry | ConversationEntry[]>(
      `/agents/${encodeURIComponent(agent)}/conversations`,
      {
        signal: backlogAborter.signal,
        query: { session_id: session, limit: String(limit.value) },
      },
    );
    const arr = Array.isArray(raw) ? raw : [raw];
    backlogRows.value = arr.map((e) => ({ ...e, _agent: agent }));
  } catch (e) {
    if ((e as { name?: string }).name === "AbortError") return;
    backlogError.value = e instanceof ApiError ? e.message : (e as Error).message;
    backlogRows.value = [];
  } finally {
    backlogLoading.value = false;
  }
}

function openStream(agent: string, session: string): void {
  teardownStream();
  // Bump gen so the turns view filters out anything tagged with an
  // earlier session's gen. (#1165)
  currentStreamGen.value += 1;
  const gen = currentStreamGen.value;
  const stream = useConversationStream(agent, session);
  convStream = stream;
  // Bridge the composable's refs onto component-owned refs so watchers in
  // the view react to both the stream lifecycle and to mode toggles.
  // Capture each watcher's stop handle so teardownStream() can detach
  // them before nulling convStream — otherwise stale callbacks fire on
  // the closed stream after a filter switch (#1661).
  streamWatchers.push(
    watch(
      stream.connected,
      (v) => {
        streamConnected.value = v;
        if (v) {
          pollingFallback.value = false;
          clearDisconnectTimer();
        } else if (disconnectTimer === null) {
          disconnectTimer = setTimeout(() => {
            pollingFallback.value = true;
            disconnectTimer = null;
            // While in fallback, re-fetch the backlog on each grace window
            // so the list still advances even if the stream remains down.
            // The fanout itself keeps polling on its own cadence, so we
            // only need to nudge the backlog view.
            if (streamMode.value && convStream === stream) {
              void loadBacklog(agent, session);
            }
          }, DISCONNECT_GRACE_MS);
        }
      },
      { immediate: true },
    ),
  );
  streamWatchers.push(
    watch(stream.reconnecting, (v) => (streamReconnecting.value = v), {
      immediate: true,
    }),
  );
  streamWatchers.push(watch(stream.error, (v) => (streamError.value = v), { immediate: true }));
  streamWatchers.push(
    watch(
      stream.turns,
      (v) => {
        // Tag every incoming turn with the gen under which it was
        // observed; stale flushes from the prior session land with an
        // older gen and get filtered out at render time. (#1165)
        for (const turn of v) {
          if (!streamTurnGens.has(turn.turnId)) {
            streamTurnGens.set(turn.turnId, gen);
          }
        }
        streamTurns.value = v.slice();
      },
      { immediate: true, deep: true },
    ),
  );
}

// Mode orchestration — open/close the stream as the (agent, session)
// selection changes. Switching sessions tears the previous stream down
// first so we never double-subscribe.
watch(
  [agentFilter, sessionFilter],
  async ([agent, session], _prev, onCleanup) => {
    if (!agent || !session) {
      teardownStream();
      backlogRows.value = [];
      return;
    }
    onCleanup(() => {
      // If the selection changes again before backlog lands, abort.
      backlogAborter?.abort();
    });
    // Clear streamTurns synchronously before awaiting the backlog so a
    // session switch cannot briefly render the prior session's rows
    // tagged with the new (agent, session) pair. (#1165)
    streamTurns.value = [];
    streamTurnGens.clear();
    // One-shot backlog, then open the stream. The order matters so that
    // `streamTurns` only starts accumulating after the backlog snapshot
    // lands — otherwise a chunk that overlaps the final backlog row
    // could render twice before the de-dupe pass runs.
    await loadBacklog(agent, session);
    openStream(agent, session);
  },
  { immediate: false },
);

onBeforeUnmount(() => {
  backlogAborter?.abort();
  teardownStream();
});

// -- Row assembly ----------------------------------------------------------

// Convert streaming turns into the row shape the template renders. The
// first-chunk `ts` is stable so backlog de-dupe on (agent|session|ts|role)
// can skip stream rows that the backlog already contains.
function streamTurnsAsRows(agent: string, session: string): Row[] {
  const gen = currentStreamGen.value;
  // Only render turns whose gen matches the current session — late
  // flushes from the previous composable land with an older gen and
  // would otherwise get mis-tagged with the new (agent, session). (#1165)
  return streamTurns.value
    .filter((turn) => streamTurnGens.get(turn.turnId) === gen)
    .map(
      (turn) =>
        ({
          ts: turn.ts,
          agent,
          session_id: session,
          role: turn.role === "assistant" ? "agent" : "user",
          text: turn.content,
          _agent: agent,
          // Attach the turn id + in-progress flag so the template can show a
          // typing indicator and so the v-for key survives chunk appends.
          // Not part of the ConversationEntry wire shape; we carry it as
          // extra own-properties and cast on use.
          __turnId: turn.turnId,
          __incomplete: !turn.complete,
        }) as Row & { __turnId: string; __incomplete: boolean },
    );
}

// Pure chronological order. Within a session, a response's ts is always
// >= the matching request's ts, so the two rows land adjacent naturally —
// no session grouping needed. session_id breaks ties deterministically in
// the (rare) case two rows share a ts down to the microsecond.
// Use Date.parse so timezone-offset vs Z-formatted timestamps compare by
// actual instant instead of string shape.
const merged = computed<Row[]>(() => {
  if (!streamMode.value) return items.value;
  // Stream mode: backlog + stream turns, de-duped on (agent|session|ts|role).
  const backlog = backlogRows.value;
  const stream = streamTurnsAsRows(agentFilter.value, sessionFilter.value);
  const seen = new Set<string>();
  const out: Row[] = [];
  const keyOf = (r: Row): string => `${r._agent}|${r.session_id ?? ""}|${r.ts}|${r.role}`;
  for (const r of backlog) {
    const k = keyOf(r);
    if (seen.has(k)) continue;
    seen.add(k);
    out.push(r);
  }
  for (const r of stream) {
    const k = keyOf(r);
    if (seen.has(k)) continue;
    seen.add(k);
    out.push(r);
  }
  return out;
});

const sorted = computed(() =>
  [...merged.value].sort((a, b) => {
    const ta = Date.parse(a.ts);
    const tb = Date.parse(b.ts);
    if (ta !== tb) return ta - tb;
    const sa = a.session_id ?? "";
    const sb = b.session_id ?? "";
    return sa < sb ? -1 : sa > sb ? 1 : 0;
  }),
);

// Cache per-row lowercased text so the search filter is O(N) on each
// recompute (not O(N*text-length)). WeakMap keyed on the row identity
// so rows that drop out of the aggregate are garbage-collected too.
const _textLowerCache = new WeakMap<Row, string>();
function _rowText(row: Row): string {
  let lower = _textLowerCache.get(row);
  if (lower === undefined) {
    lower = (row.text ?? "").toLowerCase();
    _textLowerCache.set(row, lower);
  }
  return lower;
}

const filtered = computed(() => {
  const q = searchTermDebounced.value.trim().toLowerCase();
  return sorted.value.filter((row) => {
    if (agentFilter.value && row._agent !== agentFilter.value) return false;
    if (roleFilter.value && row.role !== roleFilter.value) return false;
    if (sessionFilter.value && row.session_id !== sessionFilter.value) return false;
    if (q && !_rowText(row).includes(q)) return false;
    return true;
  });
});

// Stable per-row v-for key (#1064). The previous key combined
// (agent|session_id|ts|role), which collided on legitimate duplicate
// turns — two retries sharing a ms timestamp, or coarse-clock hosts —
// making Vue reuse the first DOM node and silently drop the second
// row's content. Walk the filtered list once per recompute and append
// an incrementing suffix whenever the base key repeats. WeakMap-cached
// off the row identity so stable-identity rows keep the same key
// across recomputes (no flicker from filter changes).
//
// Streaming rows carry a `__turnId` that is stable across chunk
// appends; we fold it into the base so growing assistant turns keep
// the same key (no DOM reuse collision) while completed-turn rows
// still disambiguate off (agent|session|ts|role).
const _rowKeyCache = new WeakMap<Row, string>();
const rowKeys = computed(() => {
  // Rebuild the used-key set + suffix map from scratch on every
  // recompute (#1532). Previously these were module-scope and only
  // ever grew; on long-lived tabs they ballooned memory indefinitely
  // as rows aged out of `filtered` but their keys stayed in the Set.
  // Scoping them to this recompute keeps the WeakMap-cached keys for
  // still-visible rows (so Vue doesn't remount DOM) while freeing keys
  // for rows that have dropped out of the view.
  const usedKeys = new Set<string>();
  const nextSuffix = new Map<string, number>();
  const out = new Map<Row, string>();
  for (const row of filtered.value) {
    const cached = _rowKeyCache.get(row);
    if (cached !== undefined) {
      out.set(row, cached);
      usedKeys.add(cached);
      continue;
    }
    const turnId = (row as Row & { __turnId?: string }).__turnId;
    const base = turnId ? `turn:${turnId}` : `${row._agent}|${row.session_id ?? ""}|${row.ts}|${row.role}`;
    let key: string;
    if (!usedKeys.has(base)) {
      key = base;
    } else {
      let n = nextSuffix.get(base) ?? 1;
      while (usedKeys.has(`${base}#${n}`)) n += 1;
      key = `${base}#${n}`;
      nextSuffix.set(base, n + 1);
    }
    usedKeys.add(key);
    _rowKeyCache.set(row, key);
    out.set(row, key);
  }
  return out;
});
function keyForRow(row: Row): string {
  return rowKeys.value.get(row) ?? "";
}

function isIncomplete(row: Row): boolean {
  return (row as Row & { __incomplete?: boolean }).__incomplete === true;
}

// Connection-pill state — only meaningful in stream mode. Green when the
// per-session stream is live; yellow while reconnecting (short blip);
// red once the disconnect grace window has lapsed and the view is
// falling back to the backlog refetch path.
type PillState = "live" | "reconnecting" | "polling" | "idle";
const pillState = computed<PillState>(() => {
  if (!streamMode.value) return "idle";
  if (pollingFallback.value) return "polling";
  if (streamConnected.value) return "live";
  if (streamReconnecting.value) return "reconnecting";
  return "reconnecting";
});
const pillLabel = computed(() => {
  switch (pillState.value) {
    case "live":
      return t("conversations.streaming");
    case "reconnecting":
      return t("conversations.reconnecting");
    case "polling":
      return t("conversations.polling");
    default:
      return "";
  }
});

// Export handlers (#1105). Exports the currently-filtered view so the
// downloaded file reflects what the operator is looking at on screen
// (agent/role/search filters, current limit). For post-mortem use.
const exportColumns = ["ts", "_agent", "agent", "session_id", "role", "model", "tokens", "trace_id", "text"];
function onExportJson(): void {
  exportJson(filtered.value, timestamped("witwave-conversations", "json"));
}
function onExportCsv(): void {
  exportCsv(
    filtered.value as unknown as Record<string, unknown>[],
    exportColumns,
    timestamped("witwave-conversations", "csv"),
  );
}

// Format the date part via toLocaleString, then splice ms into the time
// between seconds and the AM/PM marker. toLocaleString's plain concatenation
// put ms *after* AM/PM (e.g. "1:50:00 AM.070") which read wrong; this puts
// it where seconds normally would end up ("1:50:00.070 AM").
function formatTs(ts: string): string {
  try {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return ts;
    const ms = String(d.getMilliseconds()).padStart(3, "0");
    const s = d.toLocaleString();
    // Match the last H:MM:SS (or HH:MM:SS) group and insert .<ms> right after it.
    return s.replace(/(\d{1,2}:\d{2}:\d{2})/, (match) => `${match}.${ms}`);
  } catch {
    return ts;
  }
}

// Computed views mostly used by the existing "empty/loading/error" block.
// In stream mode the backlog is authoritative for the initial render so
// we prefer its loading/error state when available.
const activeLoading = computed(() => (streamMode.value ? backlogLoading.value : loading.value));
const activeError = computed(() => (streamMode.value ? backlogError.value || streamError.value : error.value));
const activeItemCount = computed(() => (streamMode.value ? merged.value.length : items.value.length));
</script>

<template>
  <div class="conversations-view" data-testid="list-conversations">
    <div class="toolbar">
      <h2 class="title">Conversations</h2>
      <input v-model="searchTerm" class="search" type="text" placeholder="filter messages…" />
      <select v-model="agentFilter" class="select" aria-label="agent">
        <option value="">all agents</option>
        <option v-for="a in agentOptions" :key="a" :value="a">{{ a }}</option>
      </select>
      <select v-model="roleFilter" class="select" aria-label="role">
        <option value="">all roles</option>
        <option value="user">user</option>
        <option value="agent">agent</option>
        <option value="system">system</option>
      </select>
      <select v-model="sessionFilter" class="select" aria-label="session">
        <option value="">all sessions</option>
        <option v-for="s in sessionOptions" :key="s.value" :value="s.value">
          {{ s.label }}
        </option>
      </select>
      <select v-model.number="limit" class="select" aria-label="limit">
        <option :value="50">50</option>
        <option :value="100">100</option>
        <option :value="250">250</option>
        <option :value="500">500</option>
      </select>
      <span class="count">{{ filtered.length }} / {{ activeItemCount }}</span>
      <span v-if="streamMode" class="pill" :class="`pill-${pillState}`" data-testid="conversations-stream-pill">
        <i class="pi" :class="pillState === 'live' ? 'pi-circle-fill' : 'pi-sync'" aria-hidden="true" />
        {{ pillLabel }}
      </span>
      <span
        v-if="!streamMode && degradedEntries.length > 0"
        class="degraded"
        :title="degradedTooltip"
        data-testid="list-conversations-degraded"
      >
        <i class="pi pi-exclamation-triangle" aria-hidden="true" />
        {{ degradedEntries.length }} degraded
      </span>
      <button
        class="refresh"
        type="button"
        :disabled="activeLoading"
        @click="streamMode ? loadBacklog(agentFilter, sessionFilter) : refresh()"
      >
        <i class="pi pi-refresh" aria-hidden="true" />
      </button>
      <button
        class="export"
        type="button"
        :disabled="filtered.length === 0"
        title="Download filtered rows as JSON"
        data-testid="export-conversations-json"
        @click="onExportJson"
      >
        <i class="pi pi-download" aria-hidden="true" /> JSON
      </button>
      <button
        class="export"
        type="button"
        :disabled="filtered.length === 0"
        title="Download filtered rows as CSV"
        data-testid="export-conversations-csv"
        @click="onExportCsv"
      >
        <i class="pi pi-download" aria-hidden="true" /> CSV
      </button>
    </div>

    <div class="feed">
      <div v-if="activeLoading && activeItemCount === 0" class="state">Loading…</div>
      <div v-else-if="activeError && activeItemCount === 0" class="state state-error">
        {{ activeError }}
      </div>
      <div v-else-if="filtered.length === 0" class="state">No messages.</div>
      <div
        v-for="row in filtered"
        :key="keyForRow(row)"
        class="cm"
        :class="[
          row.role === 'user' ? 'user' : row.role === 'agent' ? 'agent' : 'other',
          { 'cm-typing': isIncomplete(row) },
        ]"
      >
        <div class="meta">
          <span class="meta-ts">{{ formatTs(row.ts) }}</span>
          <span class="meta-role">{{ row.role }}</span>
          <span class="meta-agent">{{ row.agent }}</span>
          <span class="meta-team">@{{ row._agent }}</span>
          <span v-if="row.model" class="meta-model">{{ row.model }}</span>
          <RouterLink
            v-if="row.trace_id"
            class="meta-trace"
            :to="{ name: 'otel-traces-detail', params: { traceId: row.trace_id } }"
            :title="row.trace_id"
            data-testid="conversation-open-trace"
          >
            open trace
          </RouterLink>
        </div>
        <div v-if="row.role === 'agent'" class="bbl" v-html="renderMarkdown(row.text ?? '')" />
        <div v-else class="bbl">{{ row.text ?? "" }}</div>
        <div
          v-if="isIncomplete(row)"
          class="typing"
          data-testid="conversation-typing"
          :aria-label="t('conversations.typing')"
        >
          <span class="typing-dot" />
          <span class="typing-dot" />
          <span class="typing-dot" />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.conversations-view {
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
  padding: 4px 8px;
  border-radius: var(--witwave-radius);
  cursor: pointer;
}

.search:focus,
.select:focus {
  outline: none;
  border-color: var(--witwave-accent);
}

.count {
  font-size: 10px;
  color: var(--witwave-dim);
}

.pill {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 10px;
  border-radius: var(--witwave-radius);
  padding: 2px 6px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  white-space: nowrap;
}

.pill-live {
  color: var(--witwave-green, #3fb950);
  border: 1px solid var(--witwave-green, #3fb950);
}

.pill-reconnecting {
  color: var(--witwave-yellow, #d29922);
  border: 1px solid var(--witwave-yellow, #d29922);
}

.pill-polling {
  color: var(--witwave-red, #f85149);
  border: 1px solid var(--witwave-red, #f85149);
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
  padding: 4px 10px;
  border-radius: var(--witwave-radius);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
}

.refresh:hover:not(:disabled) {
  color: var(--witwave-text);
  border-color: var(--witwave-muted);
}

.refresh:disabled {
  opacity: 0.4;
  cursor: default;
}

.feed {
  flex: 1;
  overflow-y: auto;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
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

.cm {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 90%;
}

.cm.user {
  align-self: flex-end;
  align-items: flex-end;
}

.cm.agent,
.cm.other {
  align-self: flex-start;
}

.meta {
  display: flex;
  gap: 10px;
  font-size: 10px;
  color: var(--witwave-dim);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.meta-team {
  color: var(--witwave-accent);
}

.meta-model {
  color: var(--witwave-dim);
}

.meta-trace {
  color: var(--witwave-accent);
  text-decoration: none;
  letter-spacing: 0.04em;
}

.meta-trace:hover {
  color: var(--witwave-bright);
  text-decoration: underline;
}

.bbl {
  background: var(--witwave-surface);
  border: 1px solid var(--witwave-border);
  border-radius: var(--witwave-radius);
  padding: 8px 12px;
  font-size: 12px;
  color: var(--witwave-text);
  line-height: 1.55;
  word-break: break-word;
}

/* pre-wrap only on plain-text roles; agent rows render markdown (marked +
   DOMPurify) which already emits block elements for paragraph breaks. */
.cm.user .bbl,
.cm.other .bbl {
  white-space: pre-wrap;
}

.cm.user .bbl {
  background: color-mix(in srgb, var(--witwave-accent) 18%, var(--witwave-surface));
  border-color: color-mix(in srgb, var(--witwave-accent) 35%, var(--witwave-border));
  color: var(--witwave-bright);
}

.cm.agent .bbl :deep(p) {
  margin: 0 0 6px;
}
.cm.agent .bbl :deep(p:last-child) {
  margin-bottom: 0;
}
.cm.agent .bbl :deep(h1),
.cm.agent .bbl :deep(h2),
.cm.agent .bbl :deep(h3) {
  font-size: 12px;
  color: var(--witwave-bright);
  margin: 8px 0 4px;
}
.cm.agent .bbl :deep(code) {
  background: var(--witwave-border);
  border-radius: 3px;
  padding: 1px 4px;
  font-size: 11px;
}
.cm.agent .bbl :deep(pre) {
  background: var(--witwave-bg);
  border: 1px solid var(--witwave-border);
  border-radius: var(--witwave-radius);
  padding: 8px 10px;
  overflow-x: auto;
}
.cm.agent .bbl :deep(a) {
  color: var(--witwave-accent);
  text-decoration: none;
}

/* Typing indicator — three dots that pulse out of phase, positioned
   under the assistant bubble while the turn is still streaming. */
.typing {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 2px 4px;
}

.typing-dot {
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: var(--witwave-muted);
  animation: typing-pulse 1.2s infinite ease-in-out;
}

.typing-dot:nth-child(2) {
  animation-delay: 0.2s;
}

.typing-dot:nth-child(3) {
  animation-delay: 0.4s;
}

@keyframes typing-pulse {
  0%,
  80%,
  100% {
    opacity: 0.2;
  }
  40% {
    opacity: 1;
  }
}
</style>
