import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { ref, nextTick } from "vue";
import { createI18n } from "vue-i18n";
import ConversationsView from "../../src/views/ConversationsView.vue";
import en from "../../src/i18n/locales/en.json";
import type {
  ConversationTurn,
  UseConversationStreamReturn,
} from "../../src/composables/useConversationStream";

// Smoke spec for ConversationsView (#607, streaming coverage #1110 phase 5).
// Covers fan-out merge + chronological sort + the formatTs millisecond
// splice (regression guard for the `1:50:00.070 AM` shape the view
// carefully preserves) plus the new per-session SSE stream path.

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

function makeI18n() {
  return createI18n({
    legacy: false,
    locale: "en",
    fallbackLocale: "en",
    messages: { en },
  });
}

// Shared reactive stream mock. Individual specs reassign the refs to
// simulate connection-state transitions and chunk arrival.
const mockTurns = ref<ConversationTurn[]>([]);
const mockConnected = ref<boolean>(true);
const mockReconnecting = ref<boolean>(false);
const mockError = ref<string>("");
const mockClose = vi.fn();

function mockStreamReturn(): UseConversationStreamReturn {
  return {
    turns: mockTurns,
    toolUses: ref([]),
    connected: mockConnected,
    reconnecting: mockReconnecting,
    error: mockError,
    close: mockClose,
  };
}

vi.mock("../../src/composables/useConversationStream", async () => {
  const actual = await vi.importActual<
    typeof import("../../src/composables/useConversationStream")
  >("../../src/composables/useConversationStream");
  return {
    ...actual,
    useConversationStream: () => mockStreamReturn(),
  };
});

describe("ConversationsView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockTurns.value = [];
    mockConnected.value = true;
    mockReconnecting.value = false;
    mockError.value = "";
    mockClose.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("merges messages from both agents and renders in chronological order", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.includes("/api/team")) {
          return Promise.resolve(
            okJson([
              { name: "bob", url: "http://witwave-bob:8099" },
              { name: "fred", url: "http://witwave-fred:8098" },
            ]),
          );
        }
        if (url.includes("/agents/bob/conversations")) {
          return Promise.resolve(
            okJson([
              {
                ts: "2026-04-16T10:00:00.070Z",
                role: "user",
                text: "hello bob",
                agent: "bob",
                session_id: "s1",
              },
              {
                ts: "2026-04-16T10:00:01.000Z",
                role: "agent",
                text: "greetings",
                agent: "bob",
                session_id: "s1",
                model: "claude-opus-4-6",
              },
            ]),
          );
        }
        if (url.includes("/agents/fred/conversations")) {
          return Promise.resolve(
            okJson([
              {
                ts: "2026-04-16T10:00:00.500Z",
                role: "user",
                text: "hello fred",
                agent: "fred",
                session_id: "s2",
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    // RouterLink is stubbed because these specs mount the view in isolation
    // without installing vue-router — the component references RouterLink for
    // the #632 "open trace" action on rows that carry trace_id.
    const wrapper = mount(ConversationsView, {
      global: {
        plugins: [makeI18n()],
        stubs: { RouterLink: { template: "<a><slot /></a>" } },
      },
    });
    await flushPromises();

    expect(wrapper.find("[data-testid='list-conversations']").exists()).toBe(true);
    expect(wrapper.text()).toContain("hello bob");
    expect(wrapper.text()).toContain("hello fred");
    expect(wrapper.text()).toContain("greetings");
    // Merged count surface (filtered / total).
    expect(wrapper.text()).toContain("3 / 3");

    // ms splice lands between seconds and AM/PM, not after. Look for a
    // 3-digit ms group following a colonized time.
    expect(wrapper.text()).toMatch(/\d{1,2}:\d{2}:\d{2}\.\d{3}/);
  });

  it("renders an 'open trace' link on rows that carry trace_id (#632)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.includes("/api/team")) {
          return Promise.resolve(
            okJson([{ name: "bob", url: "http://witwave-bob:8099" }]),
          );
        }
        if (url.includes("/agents/bob/conversations")) {
          return Promise.resolve(
            okJson([
              {
                ts: "2026-04-16T10:00:00.000Z",
                role: "user",
                text: "hello",
                agent: "bob",
                session_id: "s1",
                trace_id: "abcdef0123456789abcdef0123456789",
              },
              {
                ts: "2026-04-16T10:00:01.000Z",
                role: "agent",
                text: "hi",
                agent: "bob",
                session_id: "s1",
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const wrapper = mount(ConversationsView, {
      global: {
        plugins: [makeI18n()],
        stubs: { RouterLink: { template: "<a><slot /></a>" } },
      },
    });
    await flushPromises();

    const links = wrapper.findAll("[data-testid='conversation-open-trace']");
    // Only the row with trace_id should surface the action.
    expect(links).toHaveLength(1);
    expect(wrapper.text()).toContain("open trace");
  });

  it("switches to streaming mode when both agent and session filters are set", async () => {
    // Seed the fanout with two rows so the session dropdown gets
    // populated with the id we want to pick.
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.includes("/api/team")) {
          return Promise.resolve(
            okJson([{ name: "bob", url: "http://witwave-bob:8099" }]),
          );
        }
        if (url.includes("/agents/bob/conversations")) {
          // Both the fanout call and the stream-mode backlog refetch
          // hit the same endpoint. Backlog branch passes a session_id
          // query param; the fanout doesn't. Either way, same payload.
          return Promise.resolve(
            okJson([
              {
                ts: "2026-04-16T10:00:00.000Z",
                role: "user",
                text: "backlog user",
                agent: "bob",
                session_id: "s1",
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const wrapper = mount(ConversationsView, {
      global: {
        plugins: [makeI18n()],
        stubs: { RouterLink: { template: "<a><slot /></a>" } },
      },
    });
    await flushPromises();

    // Pick the agent + session. The session <option> is added by a
    // computed sourced from the fanout items, so the order matters.
    const selects = wrapper.findAll("select");
    const agentSelect = selects.find((s) => s.attributes("aria-label") === "agent")!;
    await agentSelect.setValue("bob");
    await flushPromises();

    const sessionSelect = selects.find((s) => s.attributes("aria-label") === "session")!;
    await sessionSelect.setValue("s1");
    await flushPromises();

    // Streaming pill now visible and labelled "Streaming live".
    const pill = wrapper.find("[data-testid='conversations-stream-pill']");
    expect(pill.exists()).toBe(true);
    expect(pill.text()).toContain("Streaming live");

    // Backlog row still visible after switching.
    expect(wrapper.text()).toContain("backlog user");

    // Now push a streaming turn through the mock and assert the view
    // renders both + shows the typing indicator for the incomplete turn.
    mockTurns.value = [
      {
        turnId: "assistant-2026-04-16T10:00:05.000Z-abcdef-1",
        role: "assistant",
        content: "incoming…",
        complete: false,
        ts: "2026-04-16T10:00:05.000Z",
      },
    ];
    await nextTick();
    await flushPromises();

    expect(wrapper.text()).toContain("incoming…");
    expect(wrapper.find("[data-testid='conversation-typing']").exists()).toBe(true);
  });

  it("shows connection-pill state transitions (live → reconnecting)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.includes("/api/team")) {
          return Promise.resolve(
            okJson([{ name: "bob", url: "http://witwave-bob:8099" }]),
          );
        }
        if (url.includes("/agents/bob/conversations")) {
          return Promise.resolve(
            okJson([
              {
                ts: "2026-04-16T10:00:00.000Z",
                role: "user",
                text: "hi",
                agent: "bob",
                session_id: "s1",
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const wrapper = mount(ConversationsView, {
      global: {
        plugins: [makeI18n()],
        stubs: { RouterLink: { template: "<a><slot /></a>" } },
      },
    });
    await flushPromises();

    const selects = wrapper.findAll("select");
    const agentSelect = selects.find((s) => s.attributes("aria-label") === "agent")!;
    await agentSelect.setValue("bob");
    await flushPromises();
    const sessionSelect = selects.find((s) => s.attributes("aria-label") === "session")!;
    await sessionSelect.setValue("s1");
    await flushPromises();

    // Live label first.
    expect(
      wrapper.find("[data-testid='conversations-stream-pill']").text(),
    ).toContain("Streaming live");

    // Flip the mock to reconnecting and re-render.
    mockConnected.value = false;
    mockReconnecting.value = true;
    await nextTick();
    await flushPromises();

    expect(
      wrapper.find("[data-testid='conversations-stream-pill']").text(),
    ).toContain("Reconnecting");
  });

  it("does not duplicate rows when a streaming turn overlaps the backlog", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.includes("/api/team")) {
          return Promise.resolve(
            okJson([{ name: "bob", url: "http://witwave-bob:8099" }]),
          );
        }
        if (url.includes("/agents/bob/conversations")) {
          return Promise.resolve(
            okJson([
              {
                ts: "2026-04-16T10:00:00.000Z",
                role: "user",
                text: "one and only",
                agent: "bob",
                session_id: "s1",
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const wrapper = mount(ConversationsView, {
      global: {
        plugins: [makeI18n()],
        stubs: { RouterLink: { template: "<a><slot /></a>" } },
      },
    });
    await flushPromises();

    const selects = wrapper.findAll("select");
    const agentSelect = selects.find((s) => s.attributes("aria-label") === "agent")!;
    await agentSelect.setValue("bob");
    await flushPromises();
    const sessionSelect = selects.find((s) => s.attributes("aria-label") === "session")!;
    await sessionSelect.setValue("s1");
    await flushPromises();

    // Stream a turn with the SAME (_agent, session, ts, role) as the
    // backlog row. The de-dupe keeps only the backlog copy.
    mockTurns.value = [
      {
        turnId: "user-2026-04-16T10:00:00.000Z-abcdef-1",
        role: "user",
        content: "one and only",
        complete: true,
        ts: "2026-04-16T10:00:00.000Z",
      },
    ];
    await nextTick();
    await flushPromises();

    const occurrences = wrapper.text().split("one and only").length - 1;
    expect(occurrences).toBe(1);
  });
});
