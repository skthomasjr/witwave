// Chat types. Matches harness /proxy/{agent} JSON-RPC contract and
// /conversations/{agent} list shape.

export type ChatRole = "user" | "agent" | "error";

export interface ChatMessage {
  // Stable, locally-generated identifier used as the Vue `v-for` key so list
  // diffing survives filtering and reorders without cross-row DOM reuse
  // (#550). Assigned at push time; never derived from array position.
  id: string;
  role: ChatRole;
  text: string;
  // For agent replies: the resolved backend card name (e.g. "iris-claude") so
  // the UI can show which backend answered. Absent for user/error rows.
  label?: string;
  // Present for backfilled conversation-log rows, absent for live sends.
  ts?: string;
}

export interface ConversationEntry {
  ts: string;
  agent: string;
  session_id?: string;
  role: string;
  model?: string | null;
  tokens?: number | null;
  text?: string | null;
  // W3C trace-context ID (#636). When present, the dashboard surfaces an
  // "Open trace" action that jumps to /otel-traces/<trace_id> (#632).
  trace_id?: string | null;
}

// Trace row emitted by backends and merged by the harness /trace proxy
// (#592). Shape follows claude/executor.py _log_tool_event for SDK-level
// events, and log_tool_audit for PostToolUse hook rows (consolidated
// into tool-activity.jsonl as event_type='tool_audit' in #tool-audit-merge).
//
//  - tool_use   : (id, name, input)
//  - tool_result: (tool_use_id, content, is_error)
//  - tool_audit : (tool_use_id, tool_name, tool_input, tool_response_preview,
//                  decision?, rule?, reason?)
export interface TraceEntry {
  ts: string;
  agent?: string;
  agent_id?: string;
  session_id?: string;
  event_type: string;
  model?: string | null;
  // tool_use rows
  id?: string;
  name?: string;
  input?: unknown;
  // tool_result rows
  tool_use_id?: string;
  content?: unknown;
  is_error?: boolean;
  // tool_audit rows
  tool_name?: string;
  tool_input?: unknown;
  tool_response_preview?: string;
  decision?: string;
  rule?: string;
  reason?: string;
}

export interface A2AMessagePart {
  kind: string;
  text?: string;
}

export interface A2AResponse {
  jsonrpc: "2.0";
  id: number;
  result?: {
    parts?: A2AMessagePart[];
    status?: {
      message?: {
        parts?: A2AMessagePart[];
      };
    };
  };
  error?: {
    code: number;
    message: string;
    data?: unknown;
  };
}

export function extractReplyText(resp: A2AResponse): string {
  const parts = resp.result?.parts ?? resp.result?.status?.message?.parts ?? [];
  return parts
    .filter((p) => p.kind === "text")
    .map((p) => p.text ?? "")
    .join("");
}
