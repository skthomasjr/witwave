import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import MetricsView from "../../src/views/MetricsView.vue";

// Smoke spec for MetricsView (#607). Feeds a tiny prometheus text payload
// and asserts the stat cards + at least one chart-card land. Chart.js is
// left to render into a detached canvas — the assertion only inspects
// textual surfaces (titles, stat labels, counts).

const PROM_SAMPLE = `
# HELP harness_uptime_seconds Uptime
# TYPE harness_uptime_seconds gauge
harness_uptime_seconds{agent="bob"} 120
# HELP harness_active_sessions Active
# TYPE harness_active_sessions gauge
harness_active_sessions{agent="bob"} 3
# HELP harness_tasks Total tasks
# TYPE harness_tasks counter
harness_tasks_total{status="ok"} 7
harness_tasks_total{status="error"} 1
# HELP harness_a2a_requests Total
# TYPE harness_a2a_requests counter
harness_a2a_requests_total{status="ok"} 5
`;

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

function okText(data: string): Response {
  return { ok: true, status: 200, text: async () => data } as unknown as Response;
}

describe("MetricsView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders stat cards and at least one chart given prometheus text", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.endsWith("/api/team")) {
          return Promise.resolve(
            okJson([{ name: "bob", url: "http://nyx-bob:8099" }]),
          );
        }
        if (url.includes("/agents/bob/metrics")) {
          return Promise.resolve(okText(PROM_SAMPLE));
        }
        return Promise.resolve(okText(""));
      }),
    );

    const wrapper = mount(MetricsView);
    await flushPromises();

    expect(wrapper.find("[data-testid='list-metrics']").exists()).toBe(true);
    expect(wrapper.text()).toContain("Metrics");
    expect(wrapper.text()).toContain("Max Uptime");
    expect(wrapper.text()).toContain("Active Sessions");
    expect(wrapper.text()).toContain("Tasks Total");
    // At least one chart should materialize from the sample payload. The
    // stat "Tasks by Outcome" chart title is a chart-card <h3>, which the
    // view renders unconditionally when its breakdown has labels.
    expect(wrapper.text()).toContain("Tasks by Outcome");
  });
});
