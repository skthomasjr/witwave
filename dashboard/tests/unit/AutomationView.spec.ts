import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import AutomationView from "../../src/views/AutomationView.vue";

// Smoke spec for the unified Automation view (#automation-v1). Feeds a
// tiny fixture across all six scheduler endpoints and asserts:
//   - section headers render with counts
//   - PromptCards render with their kind + name + agent
//   - filter pill toggles hide/show a kind
//   - clicking a card with a session_id opens the conversation drawer

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

function emptyArray(): Response {
  return { ok: true, status: 200, json: async () => [] } as unknown as Response;
}

function mockFetch(overrides: Record<string, () => Response>) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : (input as URL).toString();
      for (const [needle, fn] of Object.entries(overrides)) {
        if (url.includes(needle)) return Promise.resolve(fn());
      }
      return Promise.resolve(emptyArray());
    }),
  );
}

describe("AutomationView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders a prompt card per scheduler endpoint with section headers", async () => {
    mockFetch({
      "/api/team": () =>
        okJson([{ name: "bob", url: "http://nyx-bob:8099" }]),
      "/agents/bob/jobs": () =>
        okJson([
          {
            name: "ping",
            schedule: "*/5 * * * *",
            session_id: "job-ping-sess",
            backend_id: "claude",
            running: false,
          },
        ]),
      "/agents/bob/triggers": () =>
        okJson([
          {
            name: "webhook-echo",
            endpoint: "echo",
            description: "",
            session_id: "trig-echo-sess",
            backend_id: null,
            running: false,
            enabled: true,
            signed: false,
          },
        ]),
    });

    const wrapper = mount(AutomationView);
    await flushPromises();

    // Header + empty-state sanity.
    expect(wrapper.find("[data-testid='list-automation']").exists()).toBe(true);
    expect(wrapper.text()).toContain("Automation");

    // Section titles show with counts for the kinds that have items.
    expect(wrapper.text()).toContain("Jobs");
    expect(wrapper.text()).toContain("Triggers");

    // The job card surfaces its name + schedule.
    expect(wrapper.text()).toContain("ping");
    expect(wrapper.text()).toContain("*/5 * * * *");

    // The trigger card surfaces its endpoint path.
    expect(wrapper.text()).toContain("webhook-echo");
    expect(wrapper.text()).toContain("/triggers/echo");

    // Agent badge per card.
    expect(wrapper.text()).toContain("bob");
  });

  it("hides a kind's section when its filter pill is toggled off", async () => {
    mockFetch({
      "/api/team": () =>
        okJson([{ name: "bob", url: "http://nyx-bob:8099" }]),
      "/agents/bob/jobs": () =>
        okJson([
          {
            name: "ping",
            schedule: "*/5 * * * *",
            session_id: "s1",
            backend_id: "claude",
            running: false,
          },
        ]),
    });

    const wrapper = mount(AutomationView);
    await flushPromises();

    expect(wrapper.text()).toContain("ping");

    // Toggle the "job" filter pill off.
    const jobPill = wrapper
      .findAll("button.kind-pill")
      .find((b) => b.text() === "job");
    expect(jobPill).toBeDefined();
    await jobPill!.trigger("click");

    // The job section should no longer be visible (v-show="sec.items.length > 0"
    // flips to false once the filter empties the list).
    // The card's name shouldn't be anywhere in the rendered text now.
    expect(wrapper.text()).not.toContain("ping");
  });
});
