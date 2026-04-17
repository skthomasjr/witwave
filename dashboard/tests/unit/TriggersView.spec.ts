import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import TriggersView from "../../src/views/TriggersView.vue";

// Smoke spec for the Triggers ListView wrapper (#607). Asserts the
// signed/open + enabled/disabled column render paths both land.

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

describe("TriggersView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders triggers merged from every team member", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.endsWith("/api/team")) {
          return Promise.resolve(
            okJson([{ name: "bob", url: "http://nyx-bob:8099" }]),
          );
        }
        if (url.includes("/agents/bob/triggers")) {
          return Promise.resolve(
            okJson([
              {
                name: "deploy-hook",
                endpoint: "/triggers/deploy",
                signed: true,
                enabled: true,
                description: "CI deploy webhook",
              },
              {
                name: "open-ping",
                endpoint: "/triggers/ping",
                signed: false,
                enabled: false,
                description: "Diagnostic",
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const wrapper = mount(TriggersView);
    await flushPromises();

    expect(wrapper.find("[data-testid='list-triggers']").exists()).toBe(true);
    expect(wrapper.text()).toContain("deploy-hook");
    expect(wrapper.text()).toContain("open-ping");
    expect(wrapper.text()).toContain("signed");
    expect(wrapper.text()).toContain("disabled");
  });
});
