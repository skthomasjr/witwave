<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { apiGet, ApiError } from "../api/client";
import { renderMarkdown } from "../utils/markdown";
import type { ConversationEntry } from "../types/chat";

// Slide-in drawer showing the conversation scoped to a prompt's session.
// Opens from the Automation view when an operator clicks a prompt card.
// Fetches /api/agents/<agent>/conversations and filters client-side to
// the session_id the card carries. If no session_id is provided we show
// all recent conversations for that agent (best we can do — e.g.
// heartbeat uses a derived session the client can compute).

interface Props {
  open: boolean;
  agent: string | null;
  sessionId: string | null;
  // Optional title override — the caller passes the prompt's display
  // name so the drawer header says "job/ping" rather than just
  // "Conversation".
  title?: string;
}

const props = withDefaults(defineProps<Props>(), { title: "Conversation" });
const emit = defineEmits<{ (e: "close"): void }>();

const entries = ref<ConversationEntry[]>([]);
const loading = ref(false);
const error = ref<string>("");
let aborter: AbortController | null = null;

async function fetchConversation() {
  if (!props.open || !props.agent) return;
  aborter?.abort();
  aborter = new AbortController();
  loading.value = true;
  error.value = "";
  try {
    // apiGet prepends the /api base internally (see api/client.ts).
    // Using `query:` rather than an inline ?limit= keeps encoding in
    // one place and avoids the doubled-prefix 404 I hit first pass.
    const raw = await apiGet<ConversationEntry[]>(
      `/agents/${encodeURIComponent(props.agent)}/conversations`,
      { signal: aborter.signal, query: { limit: "500" } },
    );
    // Filter by session_id if we have one. Otherwise take the latest
    // 50 entries as a best-effort preview (keeps the drawer useful
    // for kinds without a stable session — e.g. webhooks, which
    // return hasConversation=false today, but this keeps us safe).
    const rows = props.sessionId
      ? raw.filter((r) => r.session_id === props.sessionId)
      : raw.slice(-50);
    // Pure chronological order matches ConversationsView.
    entries.value = rows.sort((a, b) => {
      const ta = Date.parse(a.ts);
      const tb = Date.parse(b.ts);
      return ta - tb;
    });
  } catch (e) {
    if ((e as { name?: string }).name === "AbortError") return;
    error.value = e instanceof ApiError ? `HTTP ${e.status}` : String(e);
  } finally {
    loading.value = false;
  }
}

// Refetch whenever the drawer opens or target changes.
watch(
  () => [props.open, props.agent, props.sessionId],
  () => {
    if (props.open) fetchConversation();
  },
);

// Global Escape handler — lets the user close without reaching for the
// X button. Only listens while the drawer is mounted.
function onKeyDown(e: KeyboardEvent) {
  if (e.key === "Escape" && props.open) emit("close");
}

onMounted(() => {
  window.addEventListener("keydown", onKeyDown);
  if (props.open) fetchConversation();
});
onUnmounted(() => {
  window.removeEventListener("keydown", onKeyDown);
  aborter?.abort();
});

function formatTs(ts: string): string {
  try {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return ts;
    return d.toLocaleTimeString();
  } catch {
    return ts;
  }
}

// Role → semantic class. `user` bubbles right-aligned, `agent` left,
// anything else (system / error) uses the dim style.
function roleClass(role: string): string {
  if (role === "user") return "role-user";
  if (role === "agent") return "role-agent";
  return "role-other";
}

const subtitle = computed(() => {
  if (!props.agent) return "";
  if (props.sessionId) return `${props.agent} · session ${props.sessionId.slice(0, 8)}…`;
  return `${props.agent} · recent activity`;
});
</script>

<template>
  <Teleport to="body">
    <transition name="drawer">
      <div v-if="open" class="drawer-backdrop" @click.self="emit('close')">
        <aside
          class="drawer"
          role="dialog"
          aria-modal="true"
          :aria-label="`Conversation for ${title}`"
        >
          <header class="drawer-head">
            <div class="heads">
              <h3 class="drawer-title">{{ title }}</h3>
              <div class="drawer-sub">{{ subtitle }}</div>
            </div>
            <button
              type="button"
              class="drawer-close"
              :aria-label="'Close conversation'"
              @click="emit('close')"
            >
              <i class="pi pi-times" aria-hidden="true" />
            </button>
          </header>

          <div class="drawer-body">
            <div v-if="loading" class="state">Loading…</div>
            <div v-else-if="error" class="state state-error">{{ error }}</div>
            <div v-else-if="entries.length === 0" class="state">
              No conversation yet.
            </div>
            <div v-else class="messages">
              <article
                v-for="(e, i) in entries"
                :key="`${e.ts}-${i}`"
                class="msg"
                :class="roleClass(e.role)"
              >
                <div class="msg-meta">
                  <span class="msg-role">{{ e.role }}</span>
                  <span class="msg-ts">{{ formatTs(e.ts) }}</span>
                  <span v-if="e.model" class="msg-model">{{ e.model }}</span>
                </div>
                <!-- Existing utility sanitises via DOMPurify, matches
                     ConversationsView. -->
                <div
                  class="msg-text"
                  v-html="renderMarkdown(e.text ?? '')"
                />
              </article>
            </div>
          </div>
        </aside>
      </div>
    </transition>
  </Teleport>
</template>

<style scoped>
.drawer-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  justify-content: flex-end;
  z-index: 1000;
}

.drawer {
  width: min(720px, 92vw);
  height: 100%;
  background: var(--nyx-bg);
  border-left: 1px solid var(--nyx-border);
  display: flex;
  flex-direction: column;
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.4);
}

.drawer-head {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--nyx-border);
  background: var(--nyx-surface);
  flex-shrink: 0;
}
.heads {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}
.drawer-title {
  font-size: 13px;
  color: var(--nyx-bright);
  margin: 0;
  font-weight: 600;
  font-family: var(--nyx-mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.drawer-sub {
  font-size: 10px;
  color: var(--nyx-dim);
  font-family: var(--nyx-mono);
}

.drawer-close {
  background: none;
  border: 1px solid var(--nyx-border);
  color: var(--nyx-dim);
  border-radius: var(--nyx-radius);
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}
.drawer-close:hover {
  color: var(--nyx-text);
  border-color: var(--nyx-muted);
}

.drawer-body {
  flex: 1;
  overflow-y: auto;
  padding: 14px 18px;
}

.state {
  padding: 30px;
  color: var(--nyx-muted);
  font-size: 12px;
  text-align: center;
  font-family: var(--nyx-mono);
}
.state-error {
  color: var(--nyx-red);
}

.messages {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.msg {
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  padding: 10px 12px;
  background: var(--nyx-surface);
  font-family: var(--nyx-mono);
  font-size: 11.5px;
  line-height: 1.45;
}
.msg.role-user {
  border-left: 3px solid var(--nyx-accent, #7c6af7);
}
.msg.role-agent {
  border-left: 3px solid var(--nyx-green, #4ade80);
}
.msg.role-other {
  border-left: 3px solid var(--nyx-muted);
  opacity: 0.85;
}

.msg-meta {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 6px;
  font-size: 9px;
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
.msg-role {
  font-weight: 700;
  color: var(--nyx-bright);
}
.msg-ts {
  color: var(--nyx-muted);
}
.msg-model {
  color: var(--nyx-dim);
  margin-left: auto;
}

.msg-text :deep(p) {
  margin: 0 0 6px;
}
.msg-text :deep(p:last-child) {
  margin-bottom: 0;
}
.msg-text :deep(code) {
  background: var(--nyx-bg);
  padding: 1px 4px;
  border-radius: 3px;
  font-size: 10.5px;
}
.msg-text :deep(pre) {
  background: var(--nyx-bg);
  padding: 8px 10px;
  border-radius: var(--nyx-radius);
  overflow-x: auto;
  font-size: 10.5px;
}

.drawer-enter-active,
.drawer-leave-active {
  transition: opacity 0.15s ease;
}
.drawer-enter-active .drawer,
.drawer-leave-active .drawer {
  transition: transform 0.18s ease;
}
.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
}
.drawer-enter-from .drawer,
.drawer-leave-to .drawer {
  transform: translateX(16px);
}
</style>
