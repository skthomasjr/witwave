import { describe, expect, it, beforeEach } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { useTimelineStore } from "../../src/stores/timeline";
import type { EventEnvelope } from "../../src/composables/useEventStream";

// Coverage for the timeline store's selectors + ring eviction.
// The store's `start()` path is exercised end-to-end in
// `useEventStream.spec.ts`; here we focus on the pure bookkeeping that
// the view relies on (filter/search + ring size).

function make(
  id: string,
  type: string,
  agent: string | null,
  payload: Record<string, unknown> = {},
): EventEnvelope {
  return {
    id,
    type,
    version: 1,
    ts: "2026-04-18T00:00:00Z",
    agent_id: agent,
    payload,
  };
}

describe("useTimelineStore", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it("starts empty with default ring size", () => {
    const store = useTimelineStore();
    expect(store.events).toEqual([]);
    expect(store.ringSize).toBe(1000);
    expect(store.eventCount).toBe(0);
  });

  it("filters by type", () => {
    const store = useTimelineStore();
    store.__pushForTest(make("1", "job.fired", "iris"));
    store.__pushForTest(make("2", "webhook.delivered", "iris"));
    store.__pushForTest(make("3", "hook.decision", "nova"));

    expect(store.filterByType(["job.fired"]).map((e) => e.id)).toEqual(["1"]);
    expect(
      store
        .filterByType(["job.fired", "hook.decision"])
        .map((e) => e.id)
        .sort(),
    ).toEqual(["1", "3"]);
    // Empty list ⇒ pass-through.
    expect(store.filterByType([]).length).toBe(3);
  });

  it("filters by agent", () => {
    const store = useTimelineStore();
    store.__pushForTest(make("1", "job.fired", "iris"));
    store.__pushForTest(make("2", "job.fired", "nova"));
    store.__pushForTest(make("3", "stream.gap", null));

    expect(store.filterByAgent(["iris"]).map((e) => e.id)).toEqual(["1"]);
    expect(store.filterByAgent(["iris", "nova"]).map((e) => e.id)).toEqual([
      "1",
      "2",
    ]);
    // Null agent_id matches the `__global__` sentinel only.
    expect(store.filterByAgent(["__global__"]).map((e) => e.id)).toEqual([
      "3",
    ]);
  });

  it("search matches across payload fields", () => {
    const store = useTimelineStore();
    store.__pushForTest(
      make("1", "webhook.delivered", "iris", {
        name: "ping",
        url_host: "example.com",
      }),
    );
    store.__pushForTest(
      make("2", "webhook.delivered", "iris", {
        name: "pong",
        url_host: "other.example.org",
      }),
    );

    expect(store.search("example.com").map((e) => e.id)).toEqual(["1"]);
    expect(
      store
        .search("example")
        .map((e) => e.id)
        .sort(),
    ).toEqual(["1", "2"]);
    expect(store.search("").length).toBe(2);
    expect(store.search("no-such-token")).toEqual([]);
  });

  it("__pushForTest evicts oldest when the ring is full", () => {
    const store = useTimelineStore();
    // Shrink the ring so eviction is observable with a small fixture.
    store.ringSize = 3;
    for (let i = 1; i <= 5; i += 1) {
      store.__pushForTest(make(String(i), "t", "iris"));
    }
    expect(store.events.map((e) => e.id)).toEqual(["3", "4", "5"]);
  });
});
