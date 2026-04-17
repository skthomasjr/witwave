// Types for the harness /team response. Shape mirrors harness/main.py:team_handler:
// an array of team members, each with its own fan-out /agents response.
//
// The dashboard treats these types as the contract with the harness. When the
// harness response changes, update here first and let the compiler find the
// breaks.

export interface AgentCard {
  name?: string;
  description?: string;
  version?: string;
  url?: string;
  protocolVersion?: string;
  skills?: unknown[];
  capabilities?: Record<string, unknown>;
}

export type AgentRole = "nyx" | "backend";

export interface Agent {
  id: string;
  role: AgentRole;
  url?: string;
  // Present when the card fetch succeeded. Absent (or null) when the member or
  // backend is unreachable — the legacy UI uses !!card as the health signal.
  card?: AgentCard | null;
}

export interface TeamMember {
  name: string;
  url: string;
  agents: Agent[];
  // Populated by harness when the /agents fan-out failed for this member.
  error?: string;
}

export type TeamResponse = TeamMember[];

export type BackendType = "claude" | "codex" | "gemini" | "unknown";

export function backendType(id: string | undefined): BackendType {
  const s = (id ?? "").toLowerCase();
  if (s.includes("claude")) return "claude";
  if (s.includes("codex") || s.includes("openai") || s.includes("gpt")) return "codex";
  if (s.includes("gemini") || s.includes("google")) return "gemini";
  return "unknown";
}
