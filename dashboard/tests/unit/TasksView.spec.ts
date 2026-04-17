import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import TasksView from "../../src/views/TasksView.vue";

// Smoke spec for the Tasks ListView wrapper (#607). Mirrors the JobsView
// shape: fan-out fetch mock + a single merged-render assertion. Intentionally
// thin — the per-view risk is the column render functions, not the polling.

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

describe("TasksView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders rows merged from every team member", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.endsWith("/api/team")) {
          return Promise.resolve(
            okJson([{ name: "bob", url: "http://nyx-bob:8099" }]),
          );
        }
        if (url.includes("/agents/bob/tasks")) {
          return Promise.resolve(
            okJson([
              {
                name: "nightly",
                days_expr: "Mon-Fri",
                timezone: "UTC",
                window_start: "22:00",
                window_end: "23:00",
                loop: true,
                running: false,
                session_id: "s1",
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const wrapper = mount(TasksView);
    await flushPromises();

    expect(wrapper.find("[data-testid='list-tasks']").exists()).toBe(true);
    expect(wrapper.text()).toContain("nightly");
    expect(wrapper.text()).toContain("22:00");
    expect(wrapper.text()).toContain("23:00");
  });
});
