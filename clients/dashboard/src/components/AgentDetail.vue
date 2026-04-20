<script setup lang="ts">
import { computed } from "vue";
import type { TeamMember } from "../types/team";
import { renderMarkdown } from "../utils/markdown";
import ChatPanel from "./ChatPanel.vue";

// Right-hand detail pane. Shows member metadata at the top and a live chat
// panel below. ChatPanel is :key'd on the member name so switching agents
// resets its internal state (Tier 1 — no cross-agent cache yet, see #470).

const props = defineProps<{
  member: TeamMember | null;
  activeBackendId: string | null;
}>();

const emit = defineEmits<{
  (e: "select-backend", backendId: string): void;
}>();

const witwaveAgent = computed(() => props.member?.agents.find((a) => a.role === "witwave") ?? null);
const backends = computed(() => props.member?.agents.filter((a) => a.role === "backend") ?? []);
const descriptionHtml = computed(() => renderMarkdown(witwaveAgent.value?.card?.description));
</script>

<template>
  <div class="agents-right">
    <div v-if="!member" class="agents-right-placeholder" data-testid="detail-placeholder">
      Select an agent to view details.
    </div>
    <div v-else class="detail-body" data-testid="detail-body">
      <header class="detail-header">
        <h2 class="detail-title">{{ member.name }}</h2>
        <span class="detail-url">{{ member.url }}</span>
      </header>

      <section v-if="witwaveAgent?.card?.description" class="detail-section">
        <div class="detail-desc" v-html="descriptionHtml" />
      </section>

      <ChatPanel
        :key="member.name"
        :agent-name="member.name"
        :backends="backends"
        :active-backend-id="activeBackendId"
        @select-backend="(id) => emit('select-backend', id)"
      />
    </div>
  </div>
</template>

<style scoped>
.agents-right {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  height: 100%;
}

.agents-right-placeholder {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--witwave-muted);
  font-size: 11px;
}

.detail-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.detail-header,
.detail-section {
  padding: 12px 18px;
}

.detail-header {
  display: flex;
  align-items: baseline;
  gap: 12px;
  border-bottom: 1px solid var(--witwave-border);
}

.detail-title {
  font-size: 1rem;
  color: var(--witwave-bright);
  letter-spacing: 0.04em;
  margin: 0;
  font-weight: 600;
}

.detail-url {
  color: var(--witwave-dim);
  font-size: 11px;
  word-break: break-all;
}

.detail-desc {
  font-size: 12px;
  color: var(--witwave-text);
  line-height: 1.6;
  margin: 0;
}

.detail-desc :deep(p) {
  margin: 0 0 6px;
}

.detail-desc :deep(p:last-child) {
  margin-bottom: 0;
}

.detail-desc :deep(h1),
.detail-desc :deep(h2),
.detail-desc :deep(h3) {
  font-size: 12px;
  color: var(--witwave-bright);
  margin: 8px 0 4px;
}

.detail-desc :deep(ul),
.detail-desc :deep(ol) {
  padding-left: 18px;
  margin: 4px 0;
}

.detail-desc :deep(li) {
  margin: 2px 0;
}

.detail-desc :deep(code) {
  background: var(--witwave-border);
  border-radius: 3px;
  padding: 1px 4px;
  font-size: 11px;
}

.detail-desc :deep(a) {
  color: var(--witwave-accent);
  text-decoration: none;
}

.detail-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.detail-list li {
  display: grid;
  grid-template-columns: 160px 1fr auto;
  gap: 10px;
  padding: 6px 10px;
  border: 1px solid var(--witwave-border);
  border-radius: var(--witwave-radius);
  font-size: 11px;
  background: var(--witwave-surface);
}

.detail-list li.is-active {
  border-color: var(--witwave-accent);
}

.detail-list-id {
  color: var(--witwave-bright);
}

.detail-list-url {
  color: var(--witwave-dim);
  word-break: break-all;
}

.detail-list-state {
  color: var(--witwave-dim);
  font-size: 10px;
  text-transform: uppercase;
}

.detail-note {
  font-size: 11px;
  color: var(--witwave-muted);
  margin: 0;
}
</style>
