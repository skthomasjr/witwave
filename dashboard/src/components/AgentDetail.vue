<script setup lang="ts">
import { computed } from "vue";
import type { TeamMember } from "../types/team";

// Right-hand detail pane. For now shows lightweight metadata for the selected
// member — chat is deferred to the next pass (parity plan, #470). Once chat
// lands, this component splits into a "detail header" + conversation feed.

const props = defineProps<{
  member: TeamMember | null;
  activeBackendId: string | null;
}>();

const nyxAgent = computed(() => props.member?.agents.find((a) => a.role === "nyx") ?? null);
const backends = computed(() => props.member?.agents.filter((a) => a.role === "backend") ?? []);
const activeBackend = computed(
  () => backends.value.find((b) => b.id === props.activeBackendId) ?? null,
);
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

      <section v-if="nyxAgent?.card?.description" class="detail-section">
        <h3>description</h3>
        <p class="detail-desc">{{ nyxAgent.card.description }}</p>
      </section>

      <section v-if="backends.length" class="detail-section">
        <h3>backends</h3>
        <ul class="detail-list">
          <li
            v-for="b in backends"
            :key="b.id"
            :class="{ 'is-active': b.id === activeBackendId }"
          >
            <span class="detail-list-id">{{ b.id }}</span>
            <span class="detail-list-url">{{ b.url ?? "" }}</span>
            <span class="detail-list-state">{{ b.card ? "up" : "down" }}</span>
          </li>
        </ul>
      </section>

      <p class="detail-note">
        Chat arrives in the next parity pass — see issue #470.
        <span v-if="activeBackend"> Current backend selection: {{ activeBackend.id }}.</span>
      </p>
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
  color: var(--nyx-muted);
  font-size: 11px;
}

.detail-body {
  padding: 18px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.detail-header {
  display: flex;
  align-items: baseline;
  gap: 12px;
  border-bottom: 1px solid var(--nyx-border);
  padding-bottom: 10px;
}

.detail-title {
  font-size: 1rem;
  color: var(--nyx-bright);
  letter-spacing: 0.04em;
  margin: 0;
  font-weight: 600;
}

.detail-url {
  color: var(--nyx-dim);
  font-size: 11px;
  word-break: break-all;
}

.detail-section h3 {
  font-size: 10px;
  color: var(--nyx-dim);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  margin: 0 0 6px;
}

.detail-desc {
  font-size: 12px;
  color: var(--nyx-text);
  line-height: 1.6;
  white-space: pre-wrap;
  margin: 0;
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
  border: 1px solid var(--nyx-border);
  border-radius: var(--nyx-radius);
  font-size: 11px;
  background: var(--nyx-surface);
}

.detail-list li.is-active {
  border-color: var(--nyx-accent);
}

.detail-list-id {
  color: var(--nyx-bright);
}

.detail-list-url {
  color: var(--nyx-dim);
  word-break: break-all;
}

.detail-list-state {
  color: var(--nyx-dim);
  font-size: 10px;
  text-transform: uppercase;
}

.detail-note {
  font-size: 11px;
  color: var(--nyx-muted);
  margin: 0;
}
</style>
