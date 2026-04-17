import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import WebhooksView from "../../src/views/WebhooksView.vue";

// Smoke spec for the Webhooks ListView wrapper (#607). Verifies the
// active/max delivery column render along with enabled/disabled pills.

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

describe("WebhooksView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders webhooks with active/max delivery column", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.endsWith("/api/team")) {
          return Promise.resolve(
            okJson([{ name: "bob", url: "http://nyx-bob:8099" }]),
          );
        }
        if (url.includes("/agents/bob/webhooks")) {
          return Promise.resolve(
            okJson([
              {
                name: "slack-alerts",
                url: "https://hooks.example.com/slack",
                notify_when: "on-error",
                active_deliveries: 1,
                max_concurrent_deliveries: 4,
                enabled: true,
                description: "Slack alert feed",
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const wrapper = mount(WebhooksView);
    await flushPromises();

    expect(wrapper.find("[data-testid='list-webhooks']").exists()).toBe(true);
    expect(wrapper.text()).toContain("slack-alerts");
    expect(wrapper.text()).toContain("1/4");
    expect(wrapper.text()).toContain("enabled");
  });
});
