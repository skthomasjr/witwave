<script setup lang="ts">
import { useAgentFanout } from "../composables/useAgentFanout";
import ListView from "../components/ListView.vue";
import type { Job } from "../types/scheduler";

const { items, perAgentErrors, loading, error, lastUpdated, refresh } =
  useAgentFanout<Job>({
    endpoint: "jobs",
  });

const columns = [
  { key: "_agent", label: "agent", width: 80 },
  { key: "name", label: "name" },
  {
    key: "schedule",
    label: "schedule",
    render: (row: Job & { _agent: string }) => row.schedule ?? "— on-demand",
    dim: true,
  },
  {
    key: "backend_id",
    label: "backend",
    render: (row: Job & { _agent: string }) => row.backend_id ?? "default",
    dim: true,
  },
  {
    key: "running",
    label: "state",
    width: 80,
    render: (row: Job & { _agent: string }) =>
      row.running
        ? { text: "running", class: "cell-running" }
        : { text: "idle", class: "cell-dim" },
  },
];
</script>

<template>
  <ListView
    title="Jobs"
    :items="items"
    :columns="columns"
    :search-keys="['_agent', 'name', 'schedule', 'backend_id']"
    :loading="loading"
    :error="error"
    :per-agent-errors="perAgentErrors"
    :last-updated="lastUpdated"
    empty-message="No jobs configured."
    @refresh="refresh"
  />
</template>
