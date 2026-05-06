import { describe, expect, it, vi } from "vitest";
import { effectScope } from "vue";

// Regression coverage for #1633 — useChat.loadHistory previously left the
// spinner stuck visible when the underlying fetch rejected with a non-Abort
// error. The catch path stored the message in `historyError` but never
// reset `loadingHistory`, so ChatPanel's spinner ran forever. The fix moves
// the reset into a `finally` block so every exit path (success, abort,
// thrown error) clears the flag.

const apiGetMock = vi.fn();

vi.mock("../../src/api/client", async () => {
  const actual = await vi.importActual<typeof import("../../src/api/client")>("../../src/api/client");
  return {
    ...actual,
    apiGet: (...args: unknown[]) => apiGetMock(...args),
  };
});

import { useChat } from "../../src/composables/useChat";

function run<T>(fn: () => T): T {
  const scope = effectScope();
  try {
    return scope.run(fn) as T;
  } finally {
    scope.stop();
  }
}

describe("useChat.loadHistory", () => {
  it("clears loadingHistory and surfaces the message when apiGet rejects with a non-Abort error", async () => {
    apiGetMock.mockRejectedValueOnce(new Error("boom"));

    const chat = run(() => useChat({ agentName: "bob" }));
    await chat.loadHistory();

    expect(chat.loadingHistory.value).toBe(false);
    expect(chat.historyError.value).toBe("boom");
  });

  it("clears loadingHistory when the rejection is an AbortError", async () => {
    const abort = new DOMException("aborted", "AbortError");
    apiGetMock.mockRejectedValueOnce(abort);

    const chat = run(() => useChat({ agentName: "bob" }));
    await chat.loadHistory();

    expect(chat.loadingHistory.value).toBe(false);
    // AbortError exits early before setting historyError — the empty-string
    // initial value is the contract callers depend on (#1633 only required
    // the spinner reset; preserve the existing error-suppression behaviour).
    expect(chat.historyError.value).toBe("");
  });
});
