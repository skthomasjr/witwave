import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import HeartbeatView from "../../src/views/HeartbeatView.vue";

// Smoke spec for HeartbeatView (#607). Heartbeat is a single object per
// agent; fan-out wraps it to a one-element array. Assertion verifies both
// enabled + disabled rows + the "default" backend/model fallbacks land.

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

describe("HeartbeatView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders one row per team member with enabled/disabled state", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.endsWith("/api/team")) {
          return Promise.resolve(
            okJson([
              { name: "bob", url: "http://nyx-bob:8099" },
              { name: "fred", url: "http://nyx-fred:8098" },
            ]),
          );
        }
        if (url.includes("/agents/bob/heartbeat")) {
          return Promise.resolve(
            okJson({
              enabled: true,
              schedule: "*/15 * * * *",
              backend_id: "claude",
              model: "claude-opus-4-6",
            }),
          );
        }
        if (url.includes("/agents/fred/heartbeat")) {
          return Promise.resolve(
            okJson({
              enabled: false,
              schedule: null,
              backend_id: null,
              model: null,
            }),
          );
        }
        return Promise.resolve(okJson({}));
      }),
    );

    const wrapper = mount(HeartbeatView);
    await flushPromises();

    expect(wrapper.find("[data-testid='list-heartbeat']").exists()).toBe(true);
    expect(wrapper.text()).toContain("bob");
    expect(wrapper.text()).toContain("fred");
    expect(wrapper.text()).toContain("enabled");
    expect(wrapper.text()).toContain("disabled");
    expect(wrapper.text()).toContain("— off");
    expect(wrapper.text()).toContain("default");
  });
});
