<script setup lang="ts">
import { useAgentFanout } from "../composables/useAgentFanout";
import ListView from "../components/ListView.vue";
import type { Task } from "../types/scheduler";

type Row = Task & { _agent: string };

const { items, perAgentErrors, loading, error, refresh } = useAgentFanout<Task>({
  endpoint: "tasks",
});

const columns = [
  { key: "_agent", label: "agent", width: 80 },
  { key: "name", label: "name" },
  { key: "days_expr", label: "days", width: 80 },
  { key: "timezone", label: "tz", dim: true, width: 140 },
  {
    key: "window",
    label: "window",
    width: 130,
    render: (row: Row) => `${row.window_start}–${row.window_end}`,
  },
  {
    key: "loop",
    label: "loop",
    width: 60,
    render: (row: Row) =>
      row.loop
        ? { text: "yes", class: "cell-accent" }
        : { text: "no", class: "cell-dim" },
  },
  {
    key: "running",
    label: "state",
    width: 80,
    render: (row: Row) =>
      row.running
        ? { text: "running", class: "cell-running" }
        : { text: "idle", class: "cell-dim" },
  },
];
</script>

<template>
  <ListView
    title="Tasks"
    :items="items"
    :columns="columns"
    :search-keys="['_agent', 'name', 'days_expr', 'timezone']"
    :loading="loading"
    :error="error"
    :per-agent-errors="perAgentErrors"
    empty-message="No tasks configured."
    @refresh="refresh"
  />
</template>
