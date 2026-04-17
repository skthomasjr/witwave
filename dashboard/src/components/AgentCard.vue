<script setup lang="ts">
import { computed } from "vue";
import type { TeamMember } from "../types/team";
import BackendBubble from "./BackendBubble.vue";

// Single agent card for the left-hand list. Mirrors the legacy renderAgentCards
// output in ui/index.html (nyx-card with ac-header + ac-desc + backends-row).
// Markdown rendering of card.description is deliberately deferred (#470 parity
// follow-up) — we show plain text until marked+DOMPurify are pulled in.

const props = defineProps<{
  member: TeamMember;
  selected?: boolean;
  activeBackendId?: string | null;
}>();

const emit = defineEmits<{
  (e: "select"): void;
  (e: "select-backend", backendId: string): void;
}>();

const nyxAgent = computed(() => props.member.agents.find((a) => a.role === "nyx"));
const backends = computed(() => props.member.agents.filter((a) => a.role === "backend"));
const reachable = computed(() => !!nyxAgent.value?.card);
const unreachable = computed(() => !reachable.value && !!props.member.error);
const displayName = computed(
  () => props.member.name || nyxAgent.value?.card?.name || nyxAgent.value?.id || props.member.url,
);
const description = computed(() => (nyxAgent.value?.card?.description ?? "").trim());
</script>

<template>
  <div
    class="named-agent"
    :class="{ selected }"
    @click="emit('select')"
  >
    <div v-if="unreachable" class="nyx-card" data-testid="agent-card-unreachable">
      <div class="ac-header">
        <div class="ac-name">{{ displayName }}</div>
        <span class="ac-badge nyx">nyx</span>
      </div>
      <div class="ac-status">
        <div class="nyx-status-dot down" />
        <span>unreachable</span>
      </div>
    </div>

    <div v-else class="nyx-card" data-testid="agent-card">
      <div class="ac-header">
        <span class="ac-badge nyx">
          <div
            class="nyx-status-dot"
            :class="{ up: reachable, down: !reachable }"
            :title="reachable ? 'online' : 'offline'"
          />
          {{ displayName }}
        </span>
      </div>

      <div v-if="description" class="ac-desc">{{ description }}</div>

      <div v-if="backends.length" class="backends-row">
        <BackendBubble
          v-for="b in backends"
          :key="b.id"
          :backend="b"
          :active="selected && activeBackendId === b.id"
          @select="(id) => emit('select-backend', id)"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.named-agent {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.named-agent.selected > .nyx-card {
  border-color: var(--nyx-accent);
}

.nyx-card {
  cursor: pointer;
  background: var(--nyx-surface);
  border: 1px solid var(--nyx-border);
  border-top: 2px solid var(--nyx-accent);
  border-radius: var(--nyx-radius);
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  position: relative;
}

.ac-header {
  display: flex;
  align-items: flex-start;
  justify-content: flex-end;
  gap: 10px;
}

.ac-name {
  font-size: 0.95rem;
  color: var(--nyx-bright);
  letter-spacing: 0.04em;
}

.ac-badge {
  font-size: 10px;
  padding: 2px 7px;
  border-radius: 10px;
  background: var(--nyx-border);
  color: var(--nyx-dim);
  flex-shrink: 0;
}

.ac-badge.nyx {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  background: color-mix(in srgb, var(--nyx-accent) 20%, transparent);
  color: var(--nyx-accent);
}

.nyx-status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--nyx-muted);
  flex-shrink: 0;
  display: inline-block;
}

.nyx-status-dot.up {
  background: var(--nyx-green);
}

.nyx-status-dot.down {
  background: var(--nyx-red);
}

.ac-desc {
  font-size: 11px;
  color: var(--nyx-dim);
  line-height: 1.6;
  white-space: pre-wrap;
}

.ac-status {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--nyx-dim);
}

.backends-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 4px;
}
</style>
