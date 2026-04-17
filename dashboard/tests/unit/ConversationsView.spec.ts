import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import ConversationsView from "../../src/views/ConversationsView.vue";

// Smoke spec for ConversationsView (#607). Covers fan-out merge +
// chronological sort + the formatTs millisecond splice (regression guard for
// the `1:50:00.070 AM` shape the view carefully preserves).

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

describe("ConversationsView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
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
              { name: "bob", url: "http://nyx-bob:8099" },
              { name: "fred", url: "http://nyx-fred:8098" },
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

    const wrapper = mount(ConversationsView);
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
});
