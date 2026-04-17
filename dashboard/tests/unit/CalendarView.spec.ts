import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import CalendarView from "../../src/views/CalendarView.vue";

// Smoke spec for CalendarView (#607). Vue-cal owns the grid; we only verify
// the mount succeeds, events are derived from conversation rows, and the
// per-agent legend chip is rendered. Behaviors beyond "renders" are out of
// scope — vue-cal is a black box for this contract.

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

describe("CalendarView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("mounts and renders an agent legend chip for each team member", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.includes("/api/team")) {
          return Promise.resolve(
            okJson([{ name: "bob", url: "http://nyx-bob:8099" }]),
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

    const wrapper = mount(CalendarView);
    await flushPromises();

    expect(wrapper.find("[data-testid='list-calendar']").exists()).toBe(true);
    expect(wrapper.text()).toContain("Calendar");
    expect(wrapper.text()).toContain("bob");
    // Event count surface (single event from single conversation row).
    expect(wrapper.text()).toContain("1");
  });
});
