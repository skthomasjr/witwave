import { describe, expect, it, vi } from "vitest";
import { effectScope } from "vue";
import { __parseSSEChunk, useEventStream } from "../../src/composables/useEventStream";

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
    const buf = 'event: job.fired\nid: 42\ndata: {"type":"job.fired"}\n\n';
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
        // Full jitter means the reconnect timer after the stream's
        // clean close can fire between 0 and `initialDelayMs`. Use a
        // large delay so a second fetch can't land inside the 20ms
        // observation window. (#1152 corrected the jitter math — the
        // prior additive floor masked this test's 20ms race.)
        initialDelayMs: 5_000,
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
    // Note: cycle-1 #1615 raised MIN_BACKOFF_MS from 50ms → 500ms, so
    // the reconnect after the first stream ends is floored at 500ms.
    // 700ms keeps a comfortable margin without making the suite slow.
    await new Promise((r) => setTimeout(r, 700));

    expect(fetchImpl).toHaveBeenCalledWith("/api/events/stream", expect.anything());
    expect(fetchImpl.mock.calls.length).toBeGreaterThanOrEqual(2);
    const secondHeaders = (fetchImpl.mock.calls[1][1] as RequestInit).headers as Record<string, string>;
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

    // The non-control event lands in the list, and the overrun marker
    // is surfaced too (#1237) — downstream consumers like useAlerts key
    // off the envelope to raise the "stream caught up after reconnect"
    // banner.
    expect(value.events.value.map((e) => e.type)).toEqual(["x", "stream.overrun"]);
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

  it("drops events above the per-stream rate cap and increments droppedEventCount", async () => {
    // #1606 — runaway-backend safeguard. Cap is 200 events/sec per
    // stream; emit 250 inside the same second and assert the overflow
    // is dropped (not buffered, not slow-polled) and surfaced via
    // droppedEventCount on the reactive return.
    const TOTAL = 250;
    const CAP = 200;
    const chunks: string[] = [];
    for (let i = 1; i <= TOTAL; i += 1) {
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
    // Silence the throttled warn so test output stays clean. The warn
    // path itself is exercised — we just don't need it on stderr.
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});

    const { value, scope } = run(() =>
      useEventStream("/api/events/stream", {
        fetchImpl,
        autoConnect: false,
        // Big maxEvents so the rate cap is what bounds the list, not
        // the eviction floor.
        maxEvents: 10_000,
      }),
    );
    value.open();
    await new Promise((r) => setTimeout(r, 30));

    // All emits land inside one wall-clock second on a normal CI box.
    // Allow either exactly CAP or — if the bucket happens to roll
    // mid-pump — a slightly-over count, but assert at least some
    // events were dropped and the counter reflects it.
    expect(value.events.value.length).toBeLessThanOrEqual(TOTAL);
    expect(value.events.value.length).toBeGreaterThanOrEqual(CAP);
    expect(value.droppedEventCount.value).toBeGreaterThan(0);
    expect(value.events.value.length + value.droppedEventCount.value).toBe(TOTAL);
    expect(warnSpy).toHaveBeenCalled();

    warnSpy.mockRestore();
    value.close();
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

  it("recovers from terminal-failed state when close() + open() is called (#1653)", async () => {
    // #1615 added a terminal-failure gate: after MAX_CONSECUTIVE_FAILURES
    // (30) consecutive failed reconnects the loop stops scheduling and
    // surfaces a "stream-failed" error. #1653 fixes the regression where
    // an explicit reopen via close() + open() left consecutiveFailures
    // pinned at the terminal value, so the very next failure tripped the
    // gate again immediately. open() must reset the counter.
    vi.useFakeTimers();
    try {
      // fetch always rejects so every loop iteration drains into
      // scheduleReconnect.
      const fetchImpl = vi.fn().mockRejectedValue(new Error("connection refused"));

      const { value, scope } = run(() =>
        useEventStream("/api/events/stream", {
          fetchImpl: fetchImpl as unknown as typeof fetch,
          autoConnect: false,
          // Tiny delays so 31 reconnect cycles advance quickly under
          // fake timers. MIN_BACKOFF_MS=500 still floors each delay, but
          // we just advanceTimersByTime past it.
          initialDelayMs: 1,
          maxDelayMs: 1,
        }),
      );
      value.open();

      // Drive enough cycles to exceed MAX_CONSECUTIVE_FAILURES (30). Each
      // cycle = one fetch rejection + one MIN_BACKOFF_MS-floored timeout.
      // We pump in chunks so the awaited promise rejections settle before
      // the next timer fires.
      for (let i = 0; i < 35; i += 1) {
        await vi.advanceTimersByTimeAsync(600);
      }

      // We should now be in the terminal state: error ref set, no more
      // reconnect timers being scheduled. Capture the call count so we
      // can assert further fetches only happen after open() recovery.
      expect(value.error.value).toMatch(/stream-failed/);
      const callsAtTerminal = fetchImpl.mock.calls.length;

      // Confirm terminal: more time advances must NOT add fetches.
      await vi.advanceTimersByTimeAsync(5_000);
      expect(fetchImpl.mock.calls.length).toBe(callsAtTerminal);

      // Recovery path — close() then open(). Without #1653 the reset of
      // consecutiveFailures is missing, so the very next scheduleReconnect
      // sees count > MAX and re-enters terminal state without ever
      // issuing a real fetch loop iteration. With #1653 the counter is
      // back at 0 and the loop runs again.
      value.close();
      value.open();

      // One more cycle of fetch + reconnect. Under #1653 this lands a
      // fresh fetch; without #1653 the loop short-circuits.
      await vi.advanceTimersByTimeAsync(600);
      expect(fetchImpl.mock.calls.length).toBeGreaterThan(callsAtTerminal);

      value.close();
      scope.stop();
    } finally {
      vi.useRealTimers();
    }
  });

  it("increments parseFailureCount when a data payload is malformed JSON", async () => {
    // #1634 — malformed-payload observability. Previously the catch in
    // handleMessage silently dropped the event with no signal; the new
    // counter lets the UI / debug panel surface the rate of bad
    // payloads while keeping the stream alive.
    const goodPayload = JSON.stringify({
      type: "ok",
      version: 1,
      id: "2",
      ts: "2026-04-18T00:00:00Z",
      agent_id: null,
      payload: {},
    });
    const body = [
      // First message has invalid JSON in `data:` — should be dropped
      // and counted, not crash the stream.
      `event: bad\nid: 1\ndata: {not valid json\n\n`,
      // A second valid message proves the stream stayed alive.
      `event: ok\nid: 2\ndata: ${goodPayload}\n\n`,
    ];
    const fetchImpl = vi.fn().mockResolvedValue(okStreamResponse(body));

    const { value, scope } = run(() =>
      useEventStream("/api/events/stream", {
        fetchImpl,
        autoConnect: false,
        initialDelayMs: 5_000,
      }),
    );
    value.open();
    await new Promise((r) => setTimeout(r, 20));

    expect(value.parseFailureCount.value).toBe(1);
    // The malformed event was dropped; only the valid one landed.
    expect(value.events.value.map((e) => e.id)).toEqual(["2"]);
    // lastEventId still tracks the malformed message's id — the harness
    // ring cares about the id, not the payload.
    expect(value.lastEventId.value).toBe("2");

    value.close();
    scope.stop();
  });
});
