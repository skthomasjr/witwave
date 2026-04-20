<script setup lang="ts">
import { computed } from "vue";
import { storeToRefs } from "pinia";
import Splitter from "primevue/splitter";
import SplitterPanel from "primevue/splitterpanel";
import { useTeam } from "../composables/useTeam";
import { useTeamPreferences } from "../composables/useTeamPreferences";
import { useSelectionStore } from "../stores/selection";
import AgentList from "../components/AgentList.vue";
import AgentDetail from "../components/AgentDetail.vue";
import type { TeamMember } from "../types/team";

// Team view — two-pane layout matching legacy ui/ #agents-view.
// Left: scrollable agent cards with per-backend health bubbles.
// Right: details for the selected member (chat lands in a follow-up pass, #470).
// Polling is handled by useTeam(); selection state lives in the Pinia
// selection store (#748) so other views (e.g. Conversations) can
// surface the currently-selected agent without re-threading props.

const { members, loading, error } = useTeam();
const { pinnedAgents, onlyDegraded, isPinned, togglePin, setOnlyDegraded } =
  useTeamPreferences();

const selectionStore = useSelectionStore();
const { selectedName, activeBackendId } = storeToRefs(selectionStore);

// Derived list applied to the presentational AgentList (#1109):
//   1. Optionally filter out healthy agents so only degraded/failing
//      members render.
//   2. Stable sort so pinned entries float to the top in pin-order;
//      unpinned entries keep the directory's original order.
const displayedMembers = computed<TeamMember[]>(() => {
  const base = onlyDegraded.value
    ? members.value.filter((m) => Boolean(m.error))
    : members.value.slice();
  // Stable partition: pinned members first in pin order, the rest in the
  // directory's natural order. Array.prototype.sort is stable in V8 /
  // Safari since 2018 but we avoid relying on that by partitioning.
  const pinSet = new Set(pinnedAgents.value);
  const pinned: TeamMember[] = [];
  const rest: TeamMember[] = [];
  for (const m of base) {
    if (pinSet.has(m.name)) pinned.push(m);
    else rest.push(m);
  }
  pinned.sort(
    (a, b) =>
      pinnedAgents.value.indexOf(a.name) - pinnedAgents.value.indexOf(b.name),
  );
  return [...pinned, ...rest];
});

const selectedMember = computed(
  () => members.value.find((m) => m.name === selectedName.value) ?? null,
);

function selectAgent(name: string) {
  // Drop backend selection when switching agents — the legacy UI syncs a
  // dropdown instead, but since chat isn't wired yet we just clear.
  selectionStore.selectAgent(name);
}

function selectBackend(name: string, backendId: string) {
  selectionStore.selectBackend(name, backendId);
}

function onTogglePin(name: string): void {
  togglePin(name);
}
</script>

<template>
  <Splitter class="team-splitter">
    <SplitterPanel :size="45" :min-size="25">
      <div class="team-toolbar">
        <label class="filter-label">
          <input
            type="checkbox"
            :checked="onlyDegraded"
            data-testid="team-only-degraded"
            @change="
              setOnlyDegraded(($event.target as HTMLInputElement).checked)
            "
          />
          only degraded
        </label>
        <span
          v-if="pinnedAgents.length > 0"
          class="pin-count"
          data-testid="team-pin-count"
          :title="`Pinned: ${pinnedAgents.join(', ')}`"
        >
          <i class="pi pi-bookmark-fill" aria-hidden="true" />
          {{ pinnedAgents.length }} pinned
        </span>
      </div>
      <AgentList
        :members="displayedMembers"
        :loading="loading"
        :error="error"
        :selected-name="selectedName"
        :active-backend-id="activeBackendId"
        :is-pinned="isPinned"
        @select="selectAgent"
        @select-backend="selectBackend"
        @toggle-pin="onTogglePin"
      />
    </SplitterPanel>
    <SplitterPanel :size="55" :min-size="25">
      <AgentDetail
        :member="selectedMember"
        :active-backend-id="activeBackendId"
        @select-backend="(id) => selectionStore.setActiveBackend(id)"
      />
    </SplitterPanel>
  </Splitter>
</template>

<style scoped>
.team-splitter {
  height: 100%;
  border: none;
  background: transparent;
}

.team-splitter :deep(.p-splitter-gutter) {
  background: var(--witwave-border);
}

.team-splitter :deep(.p-splitterpanel) {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.team-toolbar {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 6px 14px;
  border-bottom: 1px solid var(--witwave-border);
  font-family: var(--witwave-mono);
  font-size: 11px;
  color: var(--witwave-dim);
  flex-shrink: 0;
}

.filter-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
}

.pin-count {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--witwave-yellow);
}
</style>
