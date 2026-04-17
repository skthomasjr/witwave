<script setup lang="ts">
import { computed, ref } from "vue";
import Splitter from "primevue/splitter";
import SplitterPanel from "primevue/splitterpanel";
import { useTeam } from "../composables/useTeam";
import AgentList from "../components/AgentList.vue";
import AgentDetail from "../components/AgentDetail.vue";

// Team view — two-pane layout matching legacy ui/ #agents-view.
// Left: scrollable agent cards with per-backend health bubbles.
// Right: details for the selected member (chat lands in a follow-up pass, #470).
// Polling is handled by useTeam(); selection state lives here.

const { members, loading, error } = useTeam();

const selectedName = ref<string | null>(null);
const activeBackendId = ref<string | null>(null);

const selectedMember = computed(
  () => members.value.find((m) => m.name === selectedName.value) ?? null,
);

function selectAgent(name: string) {
  selectedName.value = name;
  // Drop backend selection when switching agents — the legacy UI syncs a
  // dropdown instead, but since chat isn't wired yet we just clear.
  activeBackendId.value = null;
}

function selectBackend(name: string, backendId: string) {
  selectedName.value = name;
  activeBackendId.value = backendId;
}
</script>

<template>
  <Splitter class="team-splitter">
    <SplitterPanel :size="45" :min-size="25">
      <AgentList
        :members="members"
        :loading="loading"
        :error="error"
        :selected-name="selectedName"
        :active-backend-id="activeBackendId"
        @select="selectAgent"
        @select-backend="selectBackend"
      />
    </SplitterPanel>
    <SplitterPanel :size="55" :min-size="25">
      <AgentDetail
        :member="selectedMember"
        :active-backend-id="activeBackendId"
        @select-backend="(id) => (activeBackendId = id)"
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
  background: var(--nyx-border);
}

.team-splitter :deep(.p-splitterpanel) {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
</style>
