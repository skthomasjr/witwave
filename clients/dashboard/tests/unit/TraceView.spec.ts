import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import TraceView from "../../src/views/TraceView.vue";

// Smoke spec for TraceView (#592 / #607). Feeds a matched tool_use +
// tool_result pair and asserts the row renders with the tool name + ok
// pill. The pairing key is `<_agent>|<id>`, so the fixture must share id
// across both rows for the same _agent.

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

describe("TraceView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("pairs tool_use with tool_result and renders an ok row", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.includes("/api/team")) {
          return Promise.resolve(okJson([{ name: "bob", url: "http://witwave-bob:8099" }]));
        }
        if (url.includes("/agents/bob/trace")) {
          return Promise.resolve(
            okJson([
              {
                ts: "2026-04-16T10:00:00.000Z",
                event_type: "tool_use",
                id: "toolu_1",
                name: "Read",
                agent: "bob",
                session_id: "s1",
                input: { file_path: "/tmp/foo" },
              },
              {
                ts: "2026-04-16T10:00:00.250Z",
                event_type: "tool_result",
                tool_use_id: "toolu_1",
                agent: "bob",
                session_id: "s1",
                is_error: false,
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const wrapper = mount(TraceView);
    await flushPromises();

    expect(wrapper.find("[data-testid='list-trace']").exists()).toBe(true);
    expect(wrapper.text()).toContain("Trace");
    expect(wrapper.text()).toContain("Read");
    expect(wrapper.text()).toContain("ok");
    // Single row paired → count surface "1 / 1".
    expect(wrapper.text()).toContain("1 / 1");
  });
});
