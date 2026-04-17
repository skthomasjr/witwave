import { ref, shallowRef } from "vue";
import { apiGet, apiPost, ApiError } from "../api/client";
import type {
  A2AResponse,
  ChatMessage,
  ConversationEntry,
} from "../types/chat";
import { extractReplyText } from "../types/chat";

// Tier 1 chat state for a single agent. No panel cache, no localStorage —
// callers get a fresh instance per agent by using :key="member.name" on the
// mounting component. Cache / persistence land in a follow-up pass.
//
// Session continuity within a single live view is handled by the contextId
// we send — we reuse it across sends so the backend can thread the
// conversation. It resets when the composable is recreated.

function randomId(): string {
  // crypto.randomUUID isn't universally typed in older TS lib sets, but every
  // supported target (vitest jsdom, modern browsers) has it.
  return (crypto as Crypto & { randomUUID(): string }).randomUUID();
}

export interface UseChatOptions {
  agentName: string;
}

export function useChat(opts: UseChatOptions) {
  const messages = shallowRef<ChatMessage[]>([]);
  const sending = ref(false);
  const loadingHistory = ref(false);
  const historyError = ref<string>("");

  const contextId = randomId();

  function push(msg: ChatMessage): void {
    messages.value = [...messages.value, msg];
  }

  async function loadHistory(): Promise<void> {
    loadingHistory.value = true;
    historyError.value = "";
    try {
      // Direct to the named agent's own /conversations — routed by the
      // dashboard nginx to that agent's service, no harness fan-out (#470).
      const entries = await apiGet<ConversationEntry[]>(
        `/agents/${encodeURIComponent(opts.agentName)}/conversations`,
        { query: { limit: "50" } },
      );
      messages.value = entries
        .filter((e) => (e.text ?? "").trim().length > 0)
        .map<ChatMessage>((e) => ({
          role: e.role === "user" ? "user" : "agent",
          text: e.text ?? "",
          label: e.role === "user" ? "you" : e.agent,
          ts: e.ts,
        }));
    } catch (e) {
      historyError.value =
        e instanceof ApiError ? e.message : (e as Error).message;
    } finally {
      loadingHistory.value = false;
    }
  }

  async function send(text: string, backendId?: string): Promise<void> {
    const trimmed = text.trim();
    if (!trimmed || sending.value) return;

    push({ role: "user", text: trimmed, label: "you" });
    sending.value = true;

    // Direct to the named agent's A2A root. Backend selection travels in
    // message metadata — every harness's executor already honors
    // metadata.backend_id, so the dashboard never needs to know a backend's
    // internal URL (which is pod-local and unreachable cross-pod anyway).
    const metadata: Record<string, unknown> = {};
    if (backendId) metadata.backend_id = backendId;
    const body = {
      jsonrpc: "2.0" as const,
      id: 1,
      method: "message/send",
      params: {
        message: {
          messageId: randomId(),
          contextId,
          role: "user",
          parts: [{ kind: "text", text: trimmed }],
          ...(backendId ? { metadata } : {}),
        },
      },
    };

    try {
      const resp = await apiPost<A2AResponse>(
        `/agents/${encodeURIComponent(opts.agentName)}/`,
        body,
      );
      if (resp.error) {
        push({
          role: "error",
          text: resp.error.message || "request failed",
          label: "error",
        });
        return;
      }
      const replyText = extractReplyText(resp);
      if (replyText) {
        push({
          role: "agent",
          text: replyText,
          label: backendId || opts.agentName,
        });
      } else {
        push({
          role: "error",
          text: "empty response",
          label: "error",
        });
      }
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : (e as Error).message;
      push({ role: "error", text: msg, label: "error" });
    } finally {
      sending.value = false;
    }
  }

  return {
    messages,
    sending,
    loadingHistory,
    historyError,
    loadHistory,
    send,
  };
}
