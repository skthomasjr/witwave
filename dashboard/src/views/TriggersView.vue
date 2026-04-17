<script setup lang="ts">
import { useAgentFanout } from "../composables/useAgentFanout";
import ListView from "../components/ListView.vue";
import type { Trigger } from "../types/scheduler";

type Row = Trigger & { _agent: string };

const { items, perAgentErrors, loading, error, refresh } = useAgentFanout<Trigger>({
  endpoint: "triggers",
});

const columns = [
  { key: "_agent", label: "agent", width: 80 },
  { key: "name", label: "name" },
  { key: "endpoint", label: "endpoint", dim: true },
  {
    key: "signed",
    label: "auth",
    width: 80,
    render: (row: Row) =>
      row.signed
        ? { text: "signed", class: "cell-accent" }
        : { text: "open", class: "cell-dim" },
  },
  {
    key: "enabled",
    label: "state",
    width: 80,
    render: (row: Row) =>
      row.enabled
        ? { text: "enabled", class: "cell-running" }
        : { text: "disabled", class: "cell-disabled" },
  },
  { key: "description", label: "description", dim: true },
];
</script>

<template>
  <ListView
    title="Triggers"
    :items="items"
    :columns="columns"
    :search-keys="['_agent', 'name', 'endpoint', 'description']"
    :loading="loading"
    :error="error"
    :per-agent-errors="perAgentErrors"
    empty-message="No triggers configured."
    @refresh="refresh"
  />
</template>
