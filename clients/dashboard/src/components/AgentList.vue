<script setup lang="ts">
import type { TeamMember } from "../types/team";
import AgentCard from "./AgentCard.vue";

// Left-hand scrollable list of agent cards. Pure presentation — polling and
// selection state live in TeamView. Footer is reserved for the legacy "version
// / last-updated" strip, currently empty.

// isPinned is optional so existing test mounts that don't pass it keep
// working (pin feature is off-by-default for them). toggle-pin emit
// likewise — unbound consumers silently drop.
const props = defineProps<{
  members: TeamMember[];
  loading: boolean;
  error: string;
  selectedName: string | null;
  activeBackendId: string | null;
  isPinned?: (name: string) => boolean;
}>();

const emit = defineEmits<{
  (e: "select", name: string): void;
  (e: "select-backend", name: string, backendId: string): void;
  (e: "toggle-pin", name: string): void;
}>();

function pinnedFor(name: string): boolean {
  return props.isPinned ? props.isPinned(name) : false;
}
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
      <div v-else-if="members.length === 0" class="agents-placeholder" data-testid="team-empty">No agents found.</div>
      <template v-else>
        <div v-for="m in members" :key="m.name" class="agents-card-row" :class="{ 'is-pinned': pinnedFor(m.name) }">
          <AgentCard
            :member="m"
            :selected="selectedName === m.name"
            :active-backend-id="selectedName === m.name ? activeBackendId : null"
            @select="emit('select', m.name)"
            @select-backend="(id) => emit('select-backend', m.name, id)"
          />
          <button
            v-if="isPinned"
            type="button"
            class="pin-btn"
            :class="{ 'is-pinned': pinnedFor(m.name) }"
            :aria-pressed="pinnedFor(m.name)"
            :title="pinnedFor(m.name) ? 'Unpin agent' : 'Pin agent to top of list'"
            :aria-label="pinnedFor(m.name) ? `Unpin ${m.name}` : `Pin ${m.name}`"
            :data-testid="`team-pin-${m.name}`"
            @click.stop="emit('toggle-pin', m.name)"
          >
            <i :class="pinnedFor(m.name) ? 'pi pi-bookmark-fill' : 'pi pi-bookmark'" aria-hidden="true" />
          </button>
        </div>
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
  color: var(--witwave-muted);
  font-size: 11px;
  padding: 20px 10px;
  text-align: center;
}

.agents-card-row {
  position: relative;
}

.agents-card-row.is-pinned {
  /* Subtle left border to signal pin without changing card layout. */
  box-shadow: inset 3px 0 0 var(--witwave-yellow);
  border-radius: var(--witwave-radius);
}

.pin-btn {
  position: absolute;
  top: 8px;
  right: 8px;
  background: none;
  border: none;
  color: var(--witwave-muted);
  cursor: pointer;
  padding: 4px 6px;
  border-radius: var(--witwave-radius);
  font-size: 12px;
  opacity: 0;
  transition:
    opacity 0.12s,
    color 0.12s;
}

.agents-card-row:hover .pin-btn,
.pin-btn.is-pinned,
.pin-btn:focus-visible {
  opacity: 1;
}

.pin-btn.is-pinned {
  color: var(--witwave-yellow);
}

.pin-btn:hover {
  color: var(--witwave-bright);
}

.agents-footer {
  flex-shrink: 0;
  padding: 5px 14px;
  border-top: 1px solid var(--witwave-border);
  font-size: 10px;
  color: #333;
  letter-spacing: 0.03em;
  user-select: none;
  min-height: 22px;
}
</style>
