import { describe, expect, it, vi } from "vitest";
import { effectScope } from "vue";
import {
  __parseSSEChunk,
  useEventStream,
} from "../../src/composables/useEventStream";

// Unit coverage for the SSE client that backs the timeline view
// (#1110 phase 1). Covers three things:
//   1. The pure parser (`__parseSSEChunk`) — event / id / data / retry
//      framing, keepalive comment lines, multi-line data, partial
//      buffer carryover.
//   2. The composable end-to-end with a mocked fetch that returns a
//      ReadableStream — parses events, tracks lastEventId, reconnects
//      on stream end, and treats stream.overrun as a reset signal.
//   3. Clean close() teardown on explicit call.

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

function run<T>(fn: () => T): { value: T; scope: ReturnType<typeof effectScope> } {
  const scope = effectScope();
  const value = scope.run(fn) as T;
  return { value, scope };
}

describe("__parseSSEChunk", () => {
  it("parses a simple event with id and data", () => {
    const buf = "event: job.fired\nid: 42\ndata: {\"type\":\"job.fired\"}\n\n";
    const { messages, remainder } = __parseSSEChunk(buf);
    expect(messages).toHaveLength(1);
    expect(messages[0].type).toBe("job.fired");
    expect(messages[0].id).toBe("42");
    expect(messages[0].data).toBe('{"type":"job.fired"}');
    expect(remainder).toBe("");
  });

  it("treats lines starting with `:` as keepalive comments", () => {
    const buf = ": keepalive\n\nevent: x\ndata: {}\n\n";
    const { messages } = __parseSSEChunk(buf);
    // First block is pure comment — yields no message. Second is a real event.
    expect(messages).toHaveLength(1);
    expect(messages[0].type).toBe("x");
  });

  it("returns the tail as remainder when the final block is incomplete", () => {
    const buf = "event: a\ndata: 1\n\nevent: b\ndata: par";
    const { messages, remainder } = __parseSSEChunk(buf);
    expect(messages).toHaveLength(1);
    expect(messages[0].type).toBe("a");
    expect(remainder).toBe("event: b\ndata: par");
  });

  it("honours the SSE single-leading-space strip after the colon", () => {
    const buf = "data:no-space\n\ndata: with-space\n\n";
    const { messages } = __parseSSEChunk(buf);
    expect(messages[0].data).toBe("no-space");
    expect(messages[1].data).toBe("with-space");
  });

  it("parses retry: field as a non-negative integer", () => {
    const buf = "retry: 2500\ndata: x\n\n";
    const { messages } = __parseSSEChunk(buf);
    expect(messages[0].retryMs).toBe(2500);
  });
});

describe("useEventStream", () => {
  it("parses events, pushes them into the ref, and tracks lastEventId", async () => {
    const payload1 = JSON.stringify({
      type: "job.fired",
      version: 1,
      id: "1",
      ts: "2026-04-18T00:00:00Z",
      agent_id: "iris",
      payload: { name: "daily", schedule: "0 9 * * *", duration_ms: 10, outcome: "success" },
    });
    const payload2 = JSON.stringify({
      type: "webhook.delivered",
      version: 1,
      id: "2",
      ts: "2026-04-18T00:00:01Z",
      agent_id: "iris",
      payload: { name: "ping", url_host: "example.com", status_code: 200, duration_ms: 30 },
    });
    const body = [
      `event: job.fired\nid: 1\ndata: ${payload1}\n\n`,
      `event: webhook.delivered\nid: 2\ndata: ${payload2}\n\n`,
    ];
    const fetchImpl = vi.fn().mockResolvedValue(okStreamResponse(body));

    const { value, scope } = run(() =>
      useEventStream("/api/events/stream", {
        fetchImpl,
        autoConnect: false,
        token: "secret",
      }),
    );
    value.open();

    // Let microtasks + stream pump drain.
    await new Promise((r) => setTimeout(r, 20));

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    const call = fetchImpl.mock.calls[0];
    expect(call[0]).toBe("/api/events/stream");
    const headers = (call[1] as RequestInit).headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer secret");
    expect(headers.Accept).toBe("text/event-stream");

    expect(value.events.value.length).toBe(2);
    expect(value.events.value[0].type).toBe("job.fired");
    expect(value.events.value[1].type).toBe("webhook.delivered");
    expect(value.lastEventId.value).toBe("2");

    value.close();
    scope.stop();
  });

  it("passes Last-Event-ID on reconnect after the stream ends", async () => {
    const first = [
      `event: a\nid: 7\ndata: ${JSON.stringify({
        type: "a",
        version: 1,
        id: "7",
        ts: "2026-04-18T00:00:00Z",
        agent_id: null,
        payload: {},
      })}\n\n`,
    ];
    const second = [
      `event: b\nid: 8\ndata: ${JSON.stringify({
        type: "b",
        version: 1,
        id: "8",
        ts: "2026-04-18T00:00:01Z",
        agent_id: null,
        payload: {},
      })}\n\n`,
    ];
    // Third+ calls hang so the reconnect loop doesn't re-fire during
    // the observation window and blow the call count past 2.
    const hangingResponse = (): Promise<Response> =>
      new Promise<Response>((_resolve, reject) => {
        // Signal surfaces via fetchImpl's init arg; we simply never
        // resolve — the outer close() below aborts.
        void _resolve;
        void reject;
      });
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(okStreamResponse(first))
      .mockResolvedValueOnce(okStreamResponse(second))
      .mockImplementation(() => hangingResponse());

    const { value, scope } = run(() =>
      useEventStream("/api/events/stream", {
        fetchImpl,
        autoConnect: false,
        initialDelayMs: 1,
        maxDelayMs: 5,
      }),
    );
    value.open();

    // Wait for the first two streams to drain and reconnect to fire.
    await new Promise((r) => setTimeout(r, 60));

    expect(fetchImpl).toHaveBeenCalledWith(
      "/api/events/stream",
      expect.anything(),
    );
    expect(fetchImpl.mock.calls.length).toBeGreaterThanOrEqual(2);
    const secondHeaders = (fetchImpl.mock.calls[1][1] as RequestInit)
      .headers as Record<string, string>;
    expect(secondHeaders["Last-Event-ID"]).toBe("7");
    expect(value.events.value.map((e) => e.id)).toEqual(["7", "8"]);

    value.close();
    scope.stop();
  });

  it("resets lastEventId to resume_id on stream.overrun", async () => {
    const overrun = JSON.stringify({
      type: "stream.overrun",
      version: 1,
      id: "99",
      ts: "2026-04-18T00:00:00Z",
      agent_id: null,
      payload: { queue_depth: 1000, queue_max: 1000, resume_id: "500" },
    });
    const body = [
      `event: x\nid: 3\ndata: ${JSON.stringify({
        type: "x",
        version: 1,
        id: "3",
        ts: "2026-04-18T00:00:00Z",
        agent_id: null,
        payload: {},
      })}\n\n`,
      `event: stream.overrun\nid: 99\ndata: ${overrun}\n\n`,
    ];
    const fetchImpl = vi.fn().mockResolvedValue(okStreamResponse(body));

    const { value, scope } = run(() =>
      useEventStream("/api/events/stream", {
        fetchImpl,
        autoConnect: false,
      }),
    );
    value.open();
    await new Promise((r) => setTimeout(r, 20));

    // Only the non-control event lands in the list; overrun is consumed.
    expect(value.events.value.map((e) => e.type)).toEqual(["x"]);
    // resume_id wins over the overrun's own id so reconnect restarts
    // from the new head.
    expect(value.lastEventId.value).toBe("500");

    value.close();
    scope.stop();
  });

  it("surfaces stream.gap as a synthetic event in the list", async () => {
    const gap = JSON.stringify({
      type: "stream.gap",
      version: 1,
      id: "50",
      ts: "2026-04-18T00:00:00Z",
      agent_id: null,
      payload: { last_seen_id: "10", resume_id: "40" },
    });
    const body = [`event: stream.gap\nid: 50\ndata: ${gap}\n\n`];
    const fetchImpl = vi.fn().mockResolvedValue(okStreamResponse(body));

    const { value, scope } = run(() =>
      useEventStream("/api/events/stream", {
        fetchImpl,
        autoConnect: false,
      }),
    );
    value.open();
    await new Promise((r) => setTimeout(r, 20));

    expect(value.events.value).toHaveLength(1);
    expect(value.events.value[0].type).toBe("stream.gap");

    value.close();
    scope.stop();
  });

  it("close() cancels the in-flight fetch and stops the reconnect loop", async () => {
    // A fetch that never resolves until aborted.
    const fetchImpl = vi.fn(
      (_input: unknown, init?: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          const sig = init?.signal;
          if (sig) {
            sig.addEventListener("abort", () => {
              const e = new Error("aborted");
              (e as Error & { name: string }).name = "AbortError";
              reject(e);
            });
          }
        }),
    );

    const { value, scope } = run(() =>
      useEventStream("/api/events/stream", {
        fetchImpl: fetchImpl as unknown as typeof fetch,
        autoConnect: false,
      }),
    );
    value.open();
    await new Promise((r) => setTimeout(r, 5));
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    value.close();
    // A short wait ensures no reconnect scheduled by the abort path
    // attempts a second fetch.
    await new Promise((r) => setTimeout(r, 30));
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    scope.stop();
  });

  it("evicts the oldest events when maxEvents is exceeded", async () => {
    const chunks: string[] = [];
    for (let i = 1; i <= 5; i += 1) {
      chunks.push(
        `id: ${i}\ndata: ${JSON.stringify({
          type: "t",
          version: 1,
          id: String(i),
          ts: "2026-04-18T00:00:00Z",
          agent_id: null,
          payload: {},
        })}\n\n`,
      );
    }
    const fetchImpl = vi.fn().mockResolvedValue(okStreamResponse(chunks));
    const { value, scope } = run(() =>
      useEventStream("/api/events/stream", {
        fetchImpl,
        autoConnect: false,
        maxEvents: 3,
      }),
    );
    value.open();
    await new Promise((r) => setTimeout(r, 20));
    expect(value.events.value.map((e) => e.id)).toEqual(["3", "4", "5"]);
    value.close();
    scope.stop();
  });
});
