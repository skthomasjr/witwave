import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import ContinuationsView from "../../src/views/ContinuationsView.vue";

// Smoke spec for the Continuations ListView wrapper (#607). Exercises the
// `continues_after` array-vs-string join and the success+error flag merge.

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

describe("ContinuationsView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders continuations with upstream list + trigger flags", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.endsWith("/api/team")) {
          return Promise.resolve(
            okJson([{ name: "bob", url: "http://nyx-bob:8099" }]),
          );
        }
        if (url.includes("/agents/bob/continuations")) {
          return Promise.resolve(
            okJson([
              {
                name: "post-deploy",
                continues_after: ["job:deploy", "trigger:release"],
                on_success: true,
                on_error: true,
                delay: 30,
                active_fires: 0,
                max_concurrent_fires: 2,
                description: "Fires after deploy",
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const wrapper = mount(ContinuationsView);
    await flushPromises();

    expect(wrapper.find("[data-testid='list-continuations']").exists()).toBe(true);
    expect(wrapper.text()).toContain("post-deploy");
    expect(wrapper.text()).toContain("job:deploy, trigger:release");
    expect(wrapper.text()).toContain("success+error");
    expect(wrapper.text()).toContain("30s");
    expect(wrapper.text()).toContain("0/2");
  });
});
