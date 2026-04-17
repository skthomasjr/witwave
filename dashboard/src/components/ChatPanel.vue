<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useTemplateRef, watch } from "vue";
import type { Agent } from "../types/team";
import { useChat } from "../composables/useChat";
import { renderMarkdown } from "../utils/markdown";

// Tier 1 chat panel — single round-trip send to the harness /proxy endpoint
// via the active backend. No streaming, no cross-agent cache, no
// localStorage — mounted with :key="agentName" so switching agents gives us
// a clean instance (see AgentDetail.vue).

const props = defineProps<{
  agentName: string;
  backends: Agent[];
  activeBackendId: string | null;
}>();

const emit = defineEmits<{
  (e: "select-backend", backendId: string): void;
}>();

const { messages, sending, loadingHistory, historyError, loadHistory, send } =
  useChat({ agentName: props.agentName });

const input = ref("");
const feed = useTemplateRef<HTMLElement>("feed");

const selectedBackendId = computed({
  get: () => props.activeBackendId ?? props.backends[0]?.id ?? "",
  set: (id: string) => emit("select-backend", id),
});

function onChangeBackend(e: Event) {
  const target = e.target as HTMLSelectElement;
  selectedBackendId.value = target.value;
}

async function onSubmit(e: Event) {
  e.preventDefault();
  const text = input.value;
  input.value = "";
  await send(text, selectedBackendId.value || undefined);
  await scrollToBottom();
}

// Enter submits; Shift+Enter inserts a newline. Mirrors the common chat-app
// convention so users can draft multi-line messages without reaching for a
// dedicated "new line" button.
function onKeydown(e: KeyboardEvent) {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    void onSubmit(e);
  }
}

async function scrollToBottom() {
  await nextTick();
  const el = feed.value;
  if (el) el.scrollTop = el.scrollHeight;
}

watch(messages, () => void scrollToBottom());

onMounted(async () => {
  await loadHistory();
  await scrollToBottom();
});
</script>

<template>
  <div class="chat-panel" data-testid="chat-panel">
    <div class="chat-toolbar">
      <label class="chat-toolbar-label">backend</label>
      <select
        class="chat-backend-select"
        :value="selectedBackendId"
        :disabled="backends.length === 0"
        data-testid="chat-backend-select"
        @change="onChangeBackend"
      >
        <option v-for="b in backends" :key="b.id" :value="b.id">
          {{ b.id }}
        </option>
      </select>
    </div>

    <div class="chat-feed" ref="feed" data-testid="chat-feed">
      <div v-if="loadingHistory" class="chat-empty">Loading conversation…</div>
      <div v-else-if="historyError" class="chat-empty chat-error">
        Could not load history: {{ historyError }}
      </div>
      <div v-else-if="messages.length === 0" class="chat-empty">
        No messages yet. Say something!
      </div>
      <template v-else>
        <div
          v-for="(m, i) in messages"
          :key="i"
          class="cm"
          :class="m.role"
          :data-testid="`chat-msg-${m.role}`"
        >
          <!--
            The user's own messages are already visually distinct (right-
            aligned, accent-tinted) — a "you" / "user" label above them is
            redundant. Keep the role line for agent/error rows so the user
            can see which backend answered.
          -->
          <div v-if="m.role !== 'user'" class="role">{{ m.label || m.role }}</div>
          <div
            v-if="m.role === 'agent'"
            class="bbl"
            v-html="renderMarkdown(m.text)"
          />
          <div v-else class="bbl">{{ m.text }}</div>
        </div>
        <div v-if="sending" class="cm thinking" data-testid="chat-thinking">
          <div class="bbl"><span /><span /><span /></div>
        </div>
      </template>
    </div>

    <form class="chat-input" @submit="onSubmit">
      <textarea
        v-model="input"
        rows="3"
        placeholder="message… (enter to send, shift+enter for newline)"
        :disabled="sending"
        data-testid="chat-input"
        @keydown="onKeydown"
      />
      <button
        type="submit"
        class="chat-send-btn"
        :disabled="sending || input.trim().length === 0"
        aria-label="send message"
        title="Send (enter)"
        data-testid="chat-send"
      >
        <i class="pi pi-send" aria-hidden="true" />
      </button>
    </form>
  </div>
</template>

<style scoped>
.chat-panel {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  border-top: 1px solid var(--nyx-border);
}

.chat-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  border-bottom: 1px solid var(--nyx-border);
  background: var(--nyx-bg);
}

.chat-toolbar-label {
  font-size: 10px;
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
}

.chat-backend-select {
  background: var(--nyx-surface);
  border: 1px solid var(--nyx-border);
  color: var(--nyx-text);
  font-family: var(--nyx-mono);
  font-size: 11px;
  padding: 4px 8px;
  border-radius: var(--nyx-radius);
  cursor: pointer;
}

.chat-backend-select:focus {
  outline: none;
  border-color: var(--nyx-accent);
}

.chat-feed {
  flex: 1;
  overflow-y: auto;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.chat-empty {
  color: var(--nyx-muted);
  font-size: 11px;
  text-align: center;
  padding: 18px;
}

.chat-empty.chat-error {
  color: var(--nyx-red);
}

.cm {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 85%;
}

.cm.user {
  align-self: flex-end;
  align-items: flex-end;
}

.cm.agent,
.cm.error,
.cm.thinking {
  align-self: flex-start;
}

.cm .role {
  font-size: 10px;
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
}

.cm .bbl {
  background: var(--nyx-surface);
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  padding: 8px 12px;
  font-size: 12px;
  color: var(--nyx-text);
  line-height: 1.55;
  word-break: break-word;
}

/* Plain-text bubbles (user / error) need pre-wrap so newlines render.
   Agent bubbles render through marked → DOMPurify, which already produces
   block elements for paragraph breaks; leaving pre-wrap on those caused
   a visible trailing newline after marked's final </p>. */
.cm.user .bbl,
.cm.error .bbl {
  white-space: pre-wrap;
}

.cm.user .bbl {
  background: color-mix(in srgb, var(--nyx-accent) 18%, var(--nyx-surface));
  border-color: color-mix(in srgb, var(--nyx-accent) 35%, var(--nyx-border));
  color: var(--nyx-bright);
}

.cm.error .bbl {
  border-color: color-mix(in srgb, var(--nyx-red) 45%, var(--nyx-border));
  color: var(--nyx-red);
}

.cm.thinking .bbl {
  display: inline-flex;
  gap: 4px;
  padding: 10px 12px;
}

.cm.thinking .bbl span {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--nyx-dim);
  animation: chat-dot 1s infinite ease-in-out;
}

.cm.thinking .bbl span:nth-child(2) {
  animation-delay: 0.15s;
}
.cm.thinking .bbl span:nth-child(3) {
  animation-delay: 0.3s;
}

@keyframes chat-dot {
  0%,
  80%,
  100% {
    opacity: 0.3;
    transform: translateY(0);
  }
  40% {
    opacity: 1;
    transform: translateY(-2px);
  }
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
  color: var(--nyx-bright);
  margin: 8px 0 4px;
}
.cm.agent .bbl :deep(ul),
.cm.agent .bbl :deep(ol) {
  padding-left: 18px;
  margin: 4px 0;
}
.cm.agent .bbl :deep(code) {
  background: var(--nyx-border);
  border-radius: 3px;
  padding: 1px 4px;
  font-size: 11px;
}
.cm.agent .bbl :deep(pre) {
  background: var(--nyx-bg);
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  padding: 8px 10px;
  overflow-x: auto;
  margin: 6px 0;
}
.cm.agent .bbl :deep(pre code) {
  background: transparent;
  padding: 0;
}
.cm.agent .bbl :deep(a) {
  color: var(--nyx-accent);
  text-decoration: none;
}

.chat-input {
  display: flex;
  align-items: stretch;
  gap: 8px;
  padding: 10px 14px;
  border-top: 1px solid var(--nyx-border);
  background: var(--nyx-bg);
}

.chat-input textarea {
  flex: 1;
  background: var(--nyx-surface);
  border: 1px solid var(--nyx-border);
  color: var(--nyx-text);
  font-family: var(--nyx-mono);
  font-size: 12px;
  padding: 8px 10px;
  border-radius: var(--nyx-radius);
  resize: vertical;
  min-height: 56px;
  line-height: 1.45;
}

.chat-input textarea:focus {
  outline: none;
  border-color: var(--nyx-accent);
}

.chat-send-btn {
  background: var(--nyx-accent);
  color: var(--nyx-bright);
  border: none;
  border-radius: var(--nyx-radius);
  width: 36px;
  height: 36px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  align-self: flex-end;
  transition: opacity 0.12s;
}

.chat-send-btn i {
  font-size: 14px;
  line-height: 1;
}

.chat-send-btn:hover:not(:disabled) {
  opacity: 0.9;
}

.chat-send-btn:disabled {
  opacity: 0.4;
  cursor: default;
}
</style>
