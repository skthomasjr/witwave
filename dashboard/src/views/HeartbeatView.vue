<script setup lang="ts">
import { useAgentFanout } from "../composables/useAgentFanout";
import ListView from "../components/ListView.vue";
import type { Heartbeat } from "../types/scheduler";

// Heartbeat is a single config object per agent (not a list). Fan-out wraps
// it in a one-element array and stamps _agent, so ListView renders one row
// per team member — same mental model as the other scheduler views.

type Row = Heartbeat & { _agent: string };

const { items, perAgentErrors, loading, error, refresh } = useAgentFanout<Heartbeat>({
  endpoint: "heartbeat",
});

const columns = [
  { key: "_agent", label: "agent", width: 110 },
  {
    key: "enabled",
    label: "state",
    width: 100,
    render: (row: Row) =>
      row.enabled
        ? { text: "enabled", class: "cell-running" }
        : { text: "disabled", class: "cell-disabled" },
  },
  {
    key: "schedule",
    label: "schedule",
    render: (row: Row) => row.schedule ?? "— off",
    dim: true,
  },
  {
    key: "backend_id",
    label: "backend",
    render: (row: Row) => row.backend_id ?? "default",
    dim: true,
  },
  {
    key: "model",
    label: "model",
    render: (row: Row) => row.model ?? "default",
    dim: true,
  },
];
</script>

<template>
  <ListView
    title="Heartbeat"
    :items="items"
    :columns="columns"
    :search-keys="['_agent', 'schedule', 'backend_id', 'model']"
    :loading="loading"
    :error="error"
    :per-agent-errors="perAgentErrors"
    empty-message="No heartbeat configured."
    @refresh="refresh"
  />
</template>
