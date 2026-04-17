<script setup lang="ts">
import { useAgentFanout } from "../composables/useAgentFanout";
import ListView from "../components/ListView.vue";
import type { Webhook } from "../types/scheduler";

type Row = Webhook & { _agent: string };

const { items, loading, error, refresh } = useAgentFanout<Webhook>({
  endpoint: "webhooks",
});

const columns = [
  { key: "_agent", label: "agent", width: 80 },
  { key: "name", label: "name" },
  { key: "url", label: "url", dim: true },
  { key: "notify_when", label: "when", width: 110 },
  {
    key: "active",
    label: "active",
    width: 90,
    render: (row: Row) =>
      `${row.active_deliveries}/${row.max_concurrent_deliveries}`,
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
];
</script>

<template>
  <ListView
    title="Webhooks"
    :items="items"
    :columns="columns"
    :search-keys="['_agent', 'name', 'url', 'notify_when', 'description']"
    :loading="loading"
    :error="error"
    empty-message="No webhooks configured."
    @refresh="refresh"
  />
</template>
