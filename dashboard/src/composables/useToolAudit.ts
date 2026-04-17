import { computed, ref, type Ref } from "vue";
import { useAgentFanout, type AgentSourced } from "./useAgentFanout";

// Per-agent tool-audit fan-out (#635). Mirrors useAgentFanout's
// conversations/trace shape and pushes filter params to the harness so each
// backend does the filtering cheaply. Tool / session / decision filters arrive
// as refs so a watch inside the fanout composable re-fetches on every change
// without waiting for the next poll tick.

export interface ToolAuditEntry {
  ts: string | number;
  agent?: string;
  agent_id?: string;
  session_id?: string;
  model?: string;
  tool_use_id?: string;
  // a2-claude writes tool_name; a2-codex writes tool. Expose both so the table
  // stays honest instead of synthesising one.
  tool_name?: string;
  tool?: string;
  tool_input?: unknown;
  tool_response_preview?: string;
  decision?: string;
  rule?: string;
  rule_name?: string;
  reason?: string;
  source?: string;
  command?: string;
  traceparent?: string;
  [k: string]: unknown;
}

export type ToolAuditRow = ToolAuditEntry & AgentSourced;

export interface UseToolAuditOptions {
  limit: Ref<number>;
  decision: Ref<string>;
  tool: Ref<string>;
  session: Ref<string>;
  intervalMs?: number;
}

export function useToolAudit(opts: UseToolAuditOptions) {
  // Only forward non-empty filters to the backend. URLSearchParams ignores
  // undefined values inside the shared API client, so we set keys to undefined
  // rather than empty strings when unused.
  const query = computed(() => ({
    limit: String(opts.limit.value),
    decision: opts.decision.value || undefined,
    tool: opts.tool.value || undefined,
    session: opts.session.value || undefined,
  }));

  const { items, perAgentErrors, loading, error, lastUpdated, refresh } =
    useAgentFanout<ToolAuditEntry>({
      endpoint: "tool-audit",
      intervalMs: opts.intervalMs ?? 5000,
      query,
    });

  return {
    items: items as Ref<ToolAuditRow[]>,
    perAgentErrors,
    loading,
    error,
    lastUpdated,
    refresh,
  };
}
