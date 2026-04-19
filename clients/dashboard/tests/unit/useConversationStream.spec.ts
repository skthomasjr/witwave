import { describe, expect, it, vi } from "vitest";
import { effectScope } from "vue";
import { useConversationStream } from "../../src/composables/useConversationStream";

// Unit coverage for the per-session conversation stream assembler
// (#1110 phase 5). The tests drive `useConversationStream` through a
// mocked fetch that returns a `ReadableStream` of SSE frames and assert
// the composable reassembles `conversation.chunk` envelopes into
// complete `ConversationTurn` rows per the accumulation rules in
// `useConversationStream.ts`.

function bytes(s: string): Uint8Array {
  return new TextEncoder().encode(s);
}

function makeStream(chunks: string[]): ReadableStream<Uint8Array> {
  let i = 0;
  return new ReadableStream<Uint8Array>({
    pull(controller) {
      if (i >= chunks.length) {
        controller.close();
        return;
      }
      controller.enqueue(bytes(chunks[i]));
      i += 1;
    },
  });
}

function okStreamResponse(chunks: string[]): Response {
  const stream = makeStream(chunks);
  return new Response(stream, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  });
}

function chunkFrame(
  id: string,
  payload: {
    session_id_hash: string;
    role: "user" | "assistant";
    seq: number;
    content: string;
    final: boolean;
  },
  ts: string = "2026-04-18T00:00:00.000Z",
): string {
  const body = JSON.stringify({
    type: "conversation.chunk",
    version: 1,
    id,
    ts,
    agent_id: null,
    payload,
  });
  return `event: conversation.chunk\nid: ${id}\ndata: ${body}\n\n`;
}

function turnFrame(
  id: string,
  payload: {
    session_id_hash: string;
    role: "user" | "assistant";
    content_bytes: number;
    model?: string;
  },
  ts: string = "2026-04-18T00:00:00.000Z",
): string {
  const body = JSON.stringify({
    type: "conversation.turn",
    version: 1,
    id,
    ts,
    agent_id: null,
    payload,
  });
  return `event: conversation.turn\nid: ${id}\ndata: ${body}\n\n`;
}

function run<T>(fn: () => T): { value: T; scope: ReturnType<typeof effectScope> } {
  const scope = effectScope();
  const value = scope.run(fn) as T;
  return { value, scope };
}

const SESSION = "abcdef012345"; // 12-char session hash

async function waitMicrotasks(ms = 20): Promise<void> {
  await new Promise((r) => setTimeout(r, ms));
}

describe("useConversationStream", () => {
  it("concatenates 3 assistant chunks + final into a single complete turn", async () => {
    const body = [
      chunkFrame("1", {
        session_id_hash: SESSION,
        role: "assistant",
        seq: 0,
        content: "Hel",
        final: false,
      }),
      chunkFrame("2", {
        session_id_hash: SESSION,
        role: "assistant",
        seq: 1,
        content: "lo ",
        final: false,
      }),
      chunkFrame("3", {
        session_id_hash: SESSION,
        role: "assistant",
        seq: 2,
        content: "world",
        final: true,
      }),
    ];
    const fetchImpl = vi.fn().mockResolvedValue(okStreamResponse(body));

    const { value, scope } = run(() =>
      useConversationStream("bob", "session-1", {
        fetchImpl,
        autoConnect: false,
      }),
    );
    // Grab the underlying stream reference via the composable's effect
    // scope indirection — simplest way is to poke open() by closing and
    // reopening; easier: useConversationStream respects autoConnect on
    // its wrapped stream, so flip the wrapped stream open via close/open.
    // We exposed `close()` but not `open()`. Easiest: pass autoConnect
    // true and rely on the composable's microtask-open semantics.
    // But we passed autoConnect: false — re-run with true:
    value.close();
    scope.stop();

    const fetchImpl2 = vi.fn().mockResolvedValue(okStreamResponse(body));
    const { value: value2, scope: scope2 } = run(() =>
      useConversationStream("bob", "session-1", {
        fetchImpl: fetchImpl2,
        autoConnect: true,
      }),
    );
    await waitMicrotasks();

    expect(value2.turns.value).toHaveLength(1);
    const turn = value2.turns.value[0];
    expect(turn.role).toBe("assistant");
    expect(turn.content).toBe("Hello world");
    expect(turn.complete).toBe(true);

    value2.close();
    scope2.stop();
  });

  it("surfaces torn streams: chunks 0,1,3 (missing 2) stay an incomplete single turn", async () => {
    const body = [
      chunkFrame("1", {
        session_id_hash: SESSION,
        role: "assistant",
        seq: 0,
        content: "A",
        final: false,
      }),
      chunkFrame("2", {
        session_id_hash: SESSION,
        role: "assistant",
        seq: 1,
        content: "B",
        final: false,
      }),
      // seq 2 omitted; seq 3 arrives without final=true
      chunkFrame("4", {
        session_id_hash: SESSION,
        role: "assistant",
        seq: 3,
        content: "D",
        final: false,
      }),
    ];
    const fetchImpl = vi.fn().mockResolvedValue(okStreamResponse(body));

    const { value, scope } = run(() =>
      useConversationStream("bob", "s", {
        fetchImpl,
        autoConnect: true,
      }),
    );
    await waitMicrotasks();

    // No `final=true` and no `conversation.turn` — the turn stays open,
    // and chunks continue to append to the same turn because seq > 0.
    expect(value.turns.value).toHaveLength(1);
    expect(value.turns.value[0].content).toBe("ABD");
    expect(value.turns.value[0].complete).toBe(false);

    value.close();
    scope.stop();
  });

  it("starts a new turn when seq=0 restarts mid-stream", async () => {
    const body = [
      chunkFrame("1", {
        session_id_hash: SESSION,
        role: "assistant",
        seq: 0,
        content: "first",
        final: false,
      }),
      chunkFrame("2", {
        session_id_hash: SESSION,
        role: "assistant",
        seq: 1,
        content: "-part",
        final: true,
      }),
      // Fresh assistant turn — seq=0 again. Should NOT extend the
      // (now-complete) previous turn even though both are assistant.
      chunkFrame(
        "3",
        {
          session_id_hash: SESSION,
          role: "assistant",
          seq: 0,
          content: "second",
          final: true,
        },
        "2026-04-18T00:00:01.000Z",
      ),
    ];
    const fetchImpl = vi.fn().mockResolvedValue(okStreamResponse(body));

    const { value, scope } = run(() =>
      useConversationStream("bob", "s", {
        fetchImpl,
        autoConnect: true,
      }),
    );
    await waitMicrotasks();

    expect(value.turns.value).toHaveLength(2);
    expect(value.turns.value[0].content).toBe("first-part");
    expect(value.turns.value[0].complete).toBe(true);
    expect(value.turns.value[1].content).toBe("second");
    expect(value.turns.value[1].complete).toBe(true);
    // turnIds differ so Vue keys don't collide on chunk updates.
    expect(value.turns.value[0].turnId).not.toBe(value.turns.value[1].turnId);

    value.close();
    scope.stop();
  });

  it("emits a user turn first when the user chunk leads the assistant", async () => {
    const body = [
      chunkFrame(
        "1",
        {
          session_id_hash: SESSION,
          role: "user",
          seq: 0,
          content: "hello?",
          final: true,
        },
        "2026-04-18T00:00:00.000Z",
      ),
      chunkFrame(
        "2",
        {
          session_id_hash: SESSION,
          role: "assistant",
          seq: 0,
          content: "hi",
          final: false,
        },
        "2026-04-18T00:00:00.500Z",
      ),
      chunkFrame(
        "3",
        {
          session_id_hash: SESSION,
          role: "assistant",
          seq: 1,
          content: " back",
          final: true,
        },
        "2026-04-18T00:00:00.600Z",
      ),
    ];
    const fetchImpl = vi.fn().mockResolvedValue(okStreamResponse(body));

    const { value, scope } = run(() =>
      useConversationStream("bob", "s", {
        fetchImpl,
        autoConnect: true,
      }),
    );
    await waitMicrotasks();

    expect(value.turns.value).toHaveLength(2);
    expect(value.turns.value[0].role).toBe("user");
    expect(value.turns.value[0].content).toBe("hello?");
    expect(value.turns.value[0].complete).toBe(true);
    expect(value.turns.value[1].role).toBe("assistant");
    expect(value.turns.value[1].content).toBe("hi back");
    expect(value.turns.value[1].complete).toBe(true);

    value.close();
    scope.stop();
  });

  it("marks an in-progress assistant turn complete when conversation.turn lands", async () => {
    const body = [
      chunkFrame("1", {
        session_id_hash: SESSION,
        role: "assistant",
        seq: 0,
        content: "streaming",
        final: false,
      }),
      chunkFrame("2", {
        session_id_hash: SESSION,
        role: "assistant",
        seq: 1,
        content: "…",
        final: false,
      }),
      // Finalise via the turn envelope rather than a chunk with final=true.
      turnFrame("3", {
        session_id_hash: SESSION,
        role: "assistant",
        content_bytes: 10,
        model: "claude-opus-4-6",
      }),
    ];
    const fetchImpl = vi.fn().mockResolvedValue(okStreamResponse(body));

    const { value, scope } = run(() =>
      useConversationStream("bob", "s", {
        fetchImpl,
        autoConnect: true,
      }),
    );
    await waitMicrotasks();

    expect(value.turns.value).toHaveLength(1);
    expect(value.turns.value[0].content).toBe("streaming…");
    expect(value.turns.value[0].complete).toBe(true);

    value.close();
    scope.stop();
  });
});
