import { onUnmounted, ref, shallowRef } from "vue";
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

// Default send timeout. Long enough to accommodate slow LLM responses without
// leaving the UI wedged forever when the harness, backend, or network stalls
// mid-request (#535). Callers can override per useChat() invocation.
const DEFAULT_SEND_TIMEOUT_MS = 120_000;
const DEFAULT_HISTORY_TIMEOUT_MS = 30_000;

function randomId(): string {
  // crypto.randomUUID isn't universally typed in older TS lib sets, but every
  // supported target (vitest jsdom, modern browsers) has it.
  return (crypto as Crypto & { randomUUID(): string }).randomUUID();
}

export interface UseChatOptions {
  agentName: string;
  sendTimeoutMs?: number;
  historyTimeoutMs?: number;
}

export function useChat(opts: UseChatOptions) {
  const messages = shallowRef<ChatMessage[]>([]);
  const sending = ref(false);
  const loadingHistory = ref(false);
  const historyError = ref<string>("");

  const contextId = randomId();
  const sendTimeoutMs = opts.sendTimeoutMs ?? DEFAULT_SEND_TIMEOUT_MS;
  const historyTimeoutMs = opts.historyTimeoutMs ?? DEFAULT_HISTORY_TIMEOUT_MS;

  // Tracks the in-flight send so both the user (via cancel()) and the
  // unmount teardown can abort an orphaned fetch. Only one send is allowed
  // at a time because `sending` gates submission.
  let sendController: AbortController | null = null;
  let historyController: AbortController | null = null;

  function push(msg: Omit<ChatMessage, "id"> & { id?: string }): void {
    // Always stamp a stable id at push time so the chat feed can key on it
    // instead of the array index (#550). Callers may supply their own id
    // (e.g. backfill reuses harness ts) but the common path is a fresh uuid.
    const stamped: ChatMessage = { ...msg, id: msg.id ?? randomId() };
    messages.value = [...messages.value, stamped];
  }

  function isAbortError(e: unknown): boolean {
    if (e instanceof DOMException) {
      return e.name === "AbortError" || e.name === "TimeoutError";
    }
    const err = e as { name?: string } | null;
    return !!err && (err.name === "AbortError" || err.name === "TimeoutError");
  }

  function isTimeoutError(e: unknown): boolean {
    if (e instanceof DOMException) return e.name === "TimeoutError";
    const err = e as { name?: string } | null;
    return !!err && err.name === "TimeoutError";
  }

  async function loadHistory(): Promise<void> {
    historyController?.abort();
    const controller = new AbortController();
    historyController = controller;
    loadingHistory.value = true;
    historyError.value = "";
    try {
      // Direct to the named agent's own /conversations — routed by the
      // dashboard nginx to that agent's service, no harness fan-out (#470).
      const entries = await apiGet<ConversationEntry[]>(
        `/agents/${encodeURIComponent(opts.agentName)}/conversations`,
        {
          query: { limit: "50" },
          signal: controller.signal,
          timeoutMs: historyTimeoutMs,
        },
      );
      messages.value = entries
        .filter((e) => (e.text ?? "").trim().length > 0)
        .map<ChatMessage>((e) => ({
          id: randomId(),
          role: e.role === "user" ? "user" : "agent",
          text: e.text ?? "",
          label: e.role === "user" ? "you" : e.agent,
          ts: e.ts,
        }));
    } catch (e) {
      if (isAbortError(e)) return;
      historyError.value =
        e instanceof ApiError ? e.message : (e as Error).message;
    } finally {
      if (historyController === controller) historyController = null;
      loadingHistory.value = false;
    }
  }

  async function send(text: string, backendId?: string): Promise<void> {
    const trimmed = text.trim();
    if (!trimmed || sending.value) return;

    push({ role: "user", text: trimmed, label: "you" });
    sending.value = true;

    const controller = new AbortController();
    sendController = controller;

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
        { signal: controller.signal, timeoutMs: sendTimeoutMs },
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
      if (isTimeoutError(e)) {
        push({
          role: "error",
          text: `request timed out after ${Math.round(sendTimeoutMs / 1000)}s`,
          label: "timeout",
        });
      } else if (isAbortError(e)) {
        // User cancelled — keep the label distinct from "timeout" so operators
        // can tell a deliberate cancel from a stalled backend.
        push({
          role: "error",
          text: "cancelled",
          label: "cancelled",
        });
      } else {
        const msg = e instanceof ApiError ? e.message : (e as Error).message;
        push({ role: "error", text: msg, label: "error" });
      }
    } finally {
      if (sendController === controller) sendController = null;
      sending.value = false;
    }
  }

  function cancel(): void {
    sendController?.abort();
  }

  // The composable is recreated per agent via :key="member.name", so
  // onUnmounted fires on both agent switch and full unmount. Aborting here
  // guarantees the orphaned fetch releases its connection instead of
  // outliving the panel until the tab reloads.
  onUnmounted(() => {
    sendController?.abort();
    historyController?.abort();
  });

  return {
    messages,
    sending,
    loadingHistory,
    historyError,
    loadHistory,
    send,
    cancel,
  };
}
