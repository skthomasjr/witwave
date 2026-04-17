<script setup lang="ts">
import type { TeamMember } from "../types/team";
import AgentCard from "./AgentCard.vue";

// Left-hand scrollable list of agent cards. Pure presentation — polling and
// selection state live in TeamView. Footer is reserved for the legacy "version
// / last-updated" strip, currently empty.

defineProps<{
  members: TeamMember[];
  loading: boolean;
  error: string;
  selectedName: string | null;
  activeBackendId: string | null;
}>();

const emit = defineEmits<{
  (e: "select", name: string): void;
  (e: "select-backend", name: string, backendId: string): void;
}>();
</script>

<template>
  <div class="agents-left">
    <div class="agents-left-scroll">
      <div v-if="loading && members.length === 0" class="agents-placeholder" data-testid="team-loading">
        Loading agents…
      </div>
      <div v-else-if="error && members.length === 0" class="agents-placeholder" data-testid="team-error">
        Could not load team: {{ error }}
      </div>
      <div v-else-if="members.length === 0" class="agents-placeholder" data-testid="team-empty">
        No agents found.
      </div>
      <template v-else>
        <AgentCard
          v-for="m in members"
          :key="m.name"
          :member="m"
          :selected="selectedName === m.name"
          :active-backend-id="selectedName === m.name ? activeBackendId : null"
          @select="emit('select', m.name)"
          @select-backend="(id) => emit('select-backend', m.name, id)"
        />
      </template>
    </div>
    <div class="agents-footer" data-testid="team-footer" />
  </div>
</template>

<style scoped>
.agents-left {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  height: 100%;
}

.agents-left-scroll {
  flex: 1;
  overflow-y: auto;
  padding: 14px 12px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.agents-placeholder {
  color: var(--nyx-muted);
  font-size: 11px;
  padding: 20px 10px;
  text-align: center;
}

.agents-footer {
  flex-shrink: 0;
  padding: 5px 14px;
  border-top: 1px solid var(--nyx-border);
  font-size: 10px;
  color: #333;
  letter-spacing: 0.03em;
  user-select: none;
  min-height: 22px;
}
</style>
