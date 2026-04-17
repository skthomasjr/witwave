import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import JobsView from "../../src/views/JobsView.vue";

// JobsView is representative of every Group A view (Tasks/Triggers/
// Webhooks/Continuations/Heartbeat) — same useAgentFanout composable,
// same ListView component, different columns + endpoint. One smoke test
// here covers the pattern without duplicating per-view boilerplate.

function okJson(data: unknown): Response {
  return {
    ok: true,
    status: 200,
    json: async () => data,
  } as unknown as Response;
}

describe("JobsView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("fetches jobs from every team member and merges into one table", async () => {
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
        if (url.includes("/agents/bob/jobs")) {
          return Promise.resolve(
            okJson([
              { name: "ping", schedule: "*/5 * * * *", session_id: "s1", backend_id: null, model: null, running: false },
              { name: "backup", schedule: "0 3 * * *", session_id: "s2", backend_id: "claude", model: null, running: true },
            ]),
          );
        }
        if (url.includes("/agents/fred/jobs")) {
          return Promise.resolve(
            okJson([
              { name: "fred-only", schedule: "@hourly", session_id: "s3", backend_id: null, model: null, running: false },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const wrapper = mount(JobsView);
    await flushPromises();

    const list = wrapper.find("[data-testid='list-jobs']");
    expect(list.exists()).toBe(true);
    expect(wrapper.text()).toContain("ping");
    expect(wrapper.text()).toContain("backup");
    expect(wrapper.text()).toContain("fred-only");
    // Count reflects the merged total.
    expect(wrapper.text()).toContain("3 / 3");
  });
});
