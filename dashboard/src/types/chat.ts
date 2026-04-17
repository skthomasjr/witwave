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
