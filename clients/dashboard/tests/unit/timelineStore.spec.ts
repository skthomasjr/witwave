import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { ref, nextTick, type Ref } from "vue";
import type { EventEnvelope, UseEventStreamReturn } from "../../src/composables/useEventStream";
import type { TeamMember } from "../../src/types/team";

// Coverage for the timeline store's selectors + ring eviction + fanout
// (#1110 phases 1 and 1.5).
//
//   - Pure bookkeeping (filterByType / filterByAgent / search / ring) runs
//     against the direct `__pushForTest` seam, same as before.
//   - The override `start({ url })` path still opens one stream, which
//     locks in regression coverage for the tests / single-agent dev
//     setups that pass `opts.url` explicitly.
//   - The fanout path opens one stream per team member, merges their
//     envelopes in ts order, reacts to members joining / leaving, and
//     tags harness-global (agent_id=null) events with the originating
//     member name.
//
// Both composables (`useTeam`, `useEventStream`) are stubbed via vi.mock
// so the test never touches real fetch / ReadableStream machinery.

// --- Fake useEventStream ---------------------------------------------------
//
// One fake per (url) call. Tests grab them via `getFake(url)` and drive
// `events`, `connected`, `reconnecting`, `error` reactively.

interface FakeStream extends UseEventStreamReturn {
  url: string;
  closed: boolean;
}

const fakeStreams: FakeStream[] = [];

function makeFakeStream(url: string): FakeStream {
  const fake: FakeStream = {
    url,
    closed: false,
    events: ref<EventEnvelope[]>([]) as Ref<EventEnvelope[]>,
    connected: ref(false),
    reconnecting: ref(false),
    error: ref(""),
    lastEventId: ref(""),
    // #1606 + #1634 added these counters to UseEventStreamReturn;
    // the fake must satisfy the interface so vue-tsc passes in CI.
    droppedEventCount: ref(0),
    parseFailureCount: ref(0),
    open: () => {},
    close: () => {
      fake.closed = true;
    },
  };
  fakeStreams.push(fake);
  return fake;
}

function getFake(url: string): FakeStream {
  const f = fakeStreams.find((s) => s.url === url && !s.closed);
  if (!f) throw new Error(`no open fake stream for ${url}`);
  return f;
}

vi.mock("../../src/composables/useEventStream", async () => {
  const actual = await vi.importActual<
    typeof import("../../src/composables/useEventStream")
  >("../../src/composables/useEventStream");
  return {
    ...actual,
    useEventStream: (url: string) => makeFakeStream(url),
  };
});

// --- Fake useTeam ----------------------------------------------------------

const fakeMembers = ref<TeamMember[]>([]);

vi.mock("../../src/composables/useTeam", () => ({
  useTeam: () => ({
    members: fakeMembers,
    error: ref(""),
    loading: ref(false),
    refresh: async () => {},
  }),
}));

import { useTimelineStore, agentStreamUrl } from "../../src/stores/timeline";

function make(
  id: string,
  type: string,
  agent: string | null,
  ts: string = "2026-04-18T00:00:00Z",
  payload: Record<string, unknown> = {},
): EventEnvelope {
  return { id, type, version: 1, ts, agent_id: agent, payload };
}

function pushToStream(stream: FakeStream, env: EventEnvelope): void {
  // Simulate append within the composable's own ref — the store's
  // watcher diffs forward from `lastSeenLength`.
  stream.events.value = [...stream.events.value, env];
}

describe("useTimelineStore — bookkeeping", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    fakeStreams.length = 0;
    fakeMembers.value = [];
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
      make("1", "webhook.delivered", "iris", "2026-04-18T00:00:00Z", {
        name: "ping",
        url_host: "example.com",
      }),
    );
    store.__pushForTest(
      make("2", "webhook.delivered", "iris", "2026-04-18T00:00:01Z", {
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
      store.__pushForTest(make(String(i), "t", "iris", `2026-04-18T00:00:0${i}Z`));
    }
    expect(store.events.map((e) => e.id)).toEqual(["3", "4", "5"]);
  });

  it("caps seenIds growth so a long-lived tab can't leak (#1605)", () => {
    const store = useTimelineStore();
    // Generous ring so we never trip the ring-eviction path that already
    // prunes seenIds; the test must exercise the cap-only path.
    store.ringSize = 10_000;

    const SEEN_IDS_CAP = 5000;
    // Push slightly above the cap. The dedup set must stay bounded; the
    // observable consequence is that an "old" id pushed again after the
    // cap is exceeded is re-admitted (because it was evicted from the
    // dedup set), whereas without the cap it would be deduped forever.
    const oldId = "old-1";
    store.__pushForTest(
      make(oldId, "t", "iris", "2026-04-18T00:00:00.000Z"),
    );
    for (let i = 0; i < SEEN_IDS_CAP + 100; i += 1) {
      // Unique ids and monotonic ts so we exercise the fast-append path.
      const ms = String(i).padStart(6, "0");
      store.__pushForTest(
        make(`fill-${i}`, "t", "iris", `2026-04-18T01:00:00.${ms.slice(-3)}Z`),
      );
    }

    const beforeReadmit = store.events.length;
    // Re-push the original "old" envelope. If the cap is enforced, its
    // id was evicted from seenIds and the re-push lands as a fresh event.
    // Without the cap the re-push would be silently deduped, so the
    // length would not change.
    store.__pushForTest(
      make(oldId, "t", "iris", "2026-04-18T02:00:00.000Z"),
    );
    expect(store.events.length).toBe(beforeReadmit + 1);

    // Sanity: ring stayed under its (very generous) limit, proving the
    // bound is on the dedup set, not the ring.
    expect(store.events.length).toBeLessThanOrEqual(10_000);
  });
});

describe("useTimelineStore — override single-stream path (regression guard)", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    fakeStreams.length = 0;
    fakeMembers.value = [];
  });

  afterEach(() => {
    const store = useTimelineStore();
    store.__resetForTest();
  });

  it("opens a single stream when opts.url is passed and ignores team membership", async () => {
    const store = useTimelineStore();
    store.start({ url: "/custom/events/stream" });
    await nextTick();

    // Only one stream opened, against the explicit URL — no per-agent fanout.
    expect(fakeStreams).toHaveLength(1);
    expect(fakeStreams[0].url).toBe("/custom/events/stream");

    // Adding a member later should NOT open a per-agent stream.
    fakeMembers.value = [{ name: "iris", url: "http://iris", agents: [] }];
    await nextTick();
    expect(fakeStreams).toHaveLength(1);
  });

  it("merges events from the single stream into the ring without re-tagging agent_id", async () => {
    const store = useTimelineStore();
    store.start({ url: "/events/stream" });
    await nextTick();
    const s = getFake("/events/stream");

    pushToStream(s, make("1", "job.fired", "iris", "2026-04-18T00:00:00Z"));
    pushToStream(s, make("2", "stream.gap", null, "2026-04-18T00:00:01Z"));
    await nextTick();

    expect(store.events.map((e) => e.id)).toEqual(["1", "2"]);
    // Override path does NOT tag null agent_id — there's no originating
    // member identity to attribute it to.
    expect(store.events[1].agent_id).toBeNull();
  });
});

describe("useTimelineStore — fanout path", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    fakeStreams.length = 0;
    fakeMembers.value = [];
  });

  afterEach(() => {
    const store = useTimelineStore();
    store.__resetForTest();
  });

  it("opens one stream per team member", async () => {
    fakeMembers.value = [
      { name: "iris", url: "http://iris", agents: [] },
      { name: "nova", url: "http://nova", agents: [] },
      { name: "kira", url: "http://kira", agents: [] },
    ];

    const store = useTimelineStore();
    store.start();
    await nextTick();

    const urls = fakeStreams.map((s) => s.url).sort();
    expect(urls).toEqual(
      [
        agentStreamUrl("iris"),
        agentStreamUrl("kira"),
        agentStreamUrl("nova"),
      ].sort(),
    );
  });

  it("merges events from different agents in ts order regardless of arrival order", async () => {
    fakeMembers.value = [
      { name: "iris", url: "http://iris", agents: [] },
      { name: "nova", url: "http://nova", agents: [] },
    ];

    const store = useTimelineStore();
    store.start();
    await nextTick();

    const iris = getFake(agentStreamUrl("iris"));
    const nova = getFake(agentStreamUrl("nova"));

    // Nova arrives first in the stream but has an earlier ts — must
    // land first in the merged ring.
    pushToStream(iris, make("i1", "job.fired", "iris", "2026-04-18T00:00:02Z"));
    await nextTick();
    pushToStream(nova, make("n1", "job.fired", "nova", "2026-04-18T00:00:01Z"));
    await nextTick();
    pushToStream(iris, make("i2", "job.fired", "iris", "2026-04-18T00:00:03Z"));
    await nextTick();

    expect(store.events.map((e) => e.id)).toEqual(["n1", "i1", "i2"]);
  });

  it("tags harness-global events (agent_id=null) with the originating member", async () => {
    fakeMembers.value = [{ name: "iris", url: "http://iris", agents: [] }];

    const store = useTimelineStore();
    store.start();
    await nextTick();

    const iris = getFake(agentStreamUrl("iris"));
    pushToStream(iris, make("g1", "stream.gap", null, "2026-04-18T00:00:00Z"));
    await nextTick();

    expect(store.events).toHaveLength(1);
    // Tagged with the member name so filterByAgent works uniformly.
    expect(store.events[0].agent_id).toBe("iris");
    expect(store.filterByAgent(["iris"]).map((e) => e.id)).toEqual(["g1"]);
  });

  it("opens a new stream when a member is added and closes one when a member is removed", async () => {
    fakeMembers.value = [{ name: "iris", url: "http://iris", agents: [] }];

    const store = useTimelineStore();
    store.start();
    await nextTick();

    expect(fakeStreams).toHaveLength(1);
    expect(fakeStreams[0].url).toBe(agentStreamUrl("iris"));

    // Add nova — a new stream should open.
    fakeMembers.value = [
      { name: "iris", url: "http://iris", agents: [] },
      { name: "nova", url: "http://nova", agents: [] },
    ];
    await nextTick();
    expect(fakeStreams.filter((s) => !s.closed)).toHaveLength(2);

    // Remove iris — its stream should close, nova's should stay open.
    fakeMembers.value = [{ name: "nova", url: "http://nova", agents: [] }];
    await nextTick();

    const irisStream = fakeStreams.find((s) => s.url === agentStreamUrl("iris"));
    const novaStream = fakeStreams.find((s) => s.url === agentStreamUrl("nova"));
    expect(irisStream?.closed).toBe(true);
    expect(novaStream?.closed).toBe(false);
  });

  it("aggregates connection state across streams", async () => {
    fakeMembers.value = [
      { name: "iris", url: "http://iris", agents: [] },
      { name: "nova", url: "http://nova", agents: [] },
    ];

    const store = useTimelineStore();
    store.start();
    await nextTick();

    const iris = getFake(agentStreamUrl("iris"));
    const nova = getFake(agentStreamUrl("nova"));

    // All streams connected ⇒ connected.value true.
    iris.connected.value = true;
    nova.connected.value = true;
    await nextTick();
    expect(store.connected).toBe(true);

    // One stream reconnecting ⇒ reconnecting.value true.
    nova.connected.value = false;
    nova.reconnecting.value = true;
    await nextTick();
    expect(store.connected).toBe(false);
    expect(store.reconnecting).toBe(true);

    // Error from iris surfaces with agent-name prefix.
    iris.error.value = "network timeout";
    await nextTick();
    expect(store.error).toBe("iris: network timeout");
  });
});
