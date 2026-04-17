<script setup lang="ts">
import { useAgentFanout } from "../composables/useAgentFanout";
import ListView from "../components/ListView.vue";
import type { Continuation } from "../types/scheduler";

type Row = Continuation & { _agent: string };

function formatUpstream(v: string | string[]): string {
  return Array.isArray(v) ? v.join(", ") : v;
}

const { items, perAgentErrors, loading, error, refresh } = useAgentFanout<Continuation>({
  endpoint: "continuations",
});

const columns = [
  { key: "_agent", label: "agent", width: 80 },
  { key: "name", label: "name" },
  {
    key: "continues_after",
    label: "after",
    render: (row: Row) => formatUpstream(row.continues_after),
    dim: true,
  },
  {
    key: "on_success",
    label: "trig",
    width: 110,
    render: (row: Row) => {
      const flags: string[] = [];
      if (row.on_success) flags.push("success");
      if (row.on_error) flags.push("error");
      return flags.join("+") || "—";
    },
    dim: true,
  },
  {
    key: "delay",
    label: "delay",
    width: 70,
    render: (row: Row) => (row.delay != null ? `${row.delay}s` : "—"),
    dim: true,
  },
  {
    key: "active",
    label: "active",
    width: 90,
    render: (row: Row) => `${row.active_fires}/${row.max_concurrent_fires}`,
  },
];
</script>

<template>
  <ListView
    title="Continuations"
    :items="items"
    :columns="columns"
    :search-keys="['_agent', 'name', 'description']"
    :loading="loading"
    :error="error"
    :per-agent-errors="perAgentErrors"
    empty-message="No continuations configured."
    @refresh="refresh"
  />
</template>
