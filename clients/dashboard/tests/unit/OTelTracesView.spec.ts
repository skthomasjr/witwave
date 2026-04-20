import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import OTelTracesView from "../../src/views/OTelTracesView.vue";

// Smoke spec for OTelTracesView (#632). Two cases:
//   1. No baseUrl configured → falls back to in-cluster mode, fans out to
//      /api/team + /api/agents/<name>/api/traces, shows the "in-cluster" badge.
//   2. Base URL set → fetches Jaeger list + detail; click opens the drawer.
// The view depends on vue-router for deep-linking (/otel-traces/:traceId), so
// the test provides a real in-memory router rather than stubbing RouterLink.

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/otel-traces", name: "otel-traces", component: OTelTracesView },
      {
        path: "/otel-traces/:traceId",
        name: "otel-traces-detail",
        component: OTelTracesView,
      },
    ],
  });
}

describe("OTelTracesView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    // Scrub any runtime-injected trace URL left behind by a previous test.
    delete (window as unknown as { __WITWAVE_CONFIG__?: unknown }).__WITWAVE_CONFIG__;
  });

  it("falls back to in-cluster mode when no trace URL is set", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url === "/api/team") {
          return Promise.resolve(okJson([{ name: "iris", url: "http://iris" }]));
        }
        if (url.startsWith("/api/agents/iris/api/traces")) {
          return Promise.resolve(
            okJson({
              data: [
                {
                  traceID: "zzz999",
                  spans: [
                    {
                      traceID: "zzz999",
                      spanID: "s1",
                      operationName: "heartbeat.fire",
                      references: [],
                      startTime: 1_700_000_000_000_000,
                      duration: 500_000,
                      processID: "p1",
                      tags: [],
                    },
                  ],
                  processes: { p1: { serviceName: "iris-harness" } },
                },
              ],
            }),
          );
        }
        return Promise.resolve(okJson({ data: [] }));
      }),
    );

    const router = makeRouter();
    router.push("/otel-traces");
    await router.isReady();
    const wrapper = mount(OTelTracesView, { global: { plugins: [router] } });
    await flushPromises();
    await flushPromises();

    expect(wrapper.find("[data-testid='list-otel-traces']").exists()).toBe(true);
    expect(wrapper.text()).toContain("in-cluster");
    expect(wrapper.text()).toContain("heartbeat.fire");
    expect(wrapper.text()).toContain("zzz999");
  });

  it("deep-links into the detail drawer via /otel-traces/:traceId (#826)", async () => {
    // Route-param mount + in-cluster fallback. The view should load the
    // detail for the traceId from the URL without needing a list click.
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url === "/api/team") {
          return Promise.resolve(okJson([{ name: "iris", url: "http://iris" }]));
        }
        if (url.startsWith("/api/agents/iris/api/traces/deadbeef")) {
          return Promise.resolve(
            okJson({
              data: [
                {
                  traceID: "deadbeef",
                  spans: [
                    {
                      traceID: "deadbeef",
                      spanID: "root",
                      operationName: "harness.continuation.fire",
                      references: [],
                      startTime: 1_700_000_000_000_000,
                      duration: 1_000_000,
                      processID: "p1",
                      tags: [],
                    },
                  ],
                  processes: { p1: { serviceName: "iris-harness" } },
                },
              ],
            }),
          );
        }
        return Promise.resolve(okJson({ data: [] }));
      }),
    );

    const router = makeRouter();
    router.push("/otel-traces/deadbeef");
    await router.isReady();
    const wrapper = mount(OTelTracesView, { global: { plugins: [router] } });
    await flushPromises();
    await flushPromises();

    // Drawer opens for the deep-linked trace id without any list click.
    const drawer = wrapper.find("[data-testid='otel-drawer']");
    expect(drawer.exists()).toBe(true);
    expect(drawer.text()).toContain("deadbeef");
  });

  it("honours window.__WITWAVE_CONFIG__.traceApiUrl over in-cluster fallback (#826)", async () => {
    // Runtime-injected config should win: fetch must target the external
    // base URL, not the /api/team in-cluster fan-out. Cross-origin URL
    // requires the explicit opt-in flag (#1061) — otherwise the same-origin
    // guard rejects it to keep CSP connect-src in sync with the feature.
    (
      window as unknown as {
        __WITWAVE_CONFIG__: { traceApiUrl: string; traceApiAllowCrossOrigin: boolean };
      }
    ).__WITWAVE_CONFIG__ = {
      traceApiUrl: "https://tempo.example:16686",
      traceApiAllowCrossOrigin: true,
    };

    const fetchSpy = vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : (input as URL).toString();
      if (url.startsWith("https://tempo.example:16686/api/traces")) {
        return Promise.resolve(okJson({ data: [] }));
      }
      return Promise.resolve(okJson({ data: [] }));
    });
    vi.stubGlobal("fetch", fetchSpy);

    const router = makeRouter();
    router.push("/otel-traces");
    await router.isReady();
    const wrapper = mount(OTelTracesView, { global: { plugins: [router] } });
    await flushPromises();

    // External endpoint badge rendered; no in-cluster badge.
    expect(wrapper.text()).toContain("https://tempo.example:16686");
    expect(wrapper.text()).not.toContain("in-cluster");
    // /api/team must not have been called — fallback path is skipped when
    // an external base URL is configured.
    const calls = fetchSpy.mock.calls.map((c) =>
      typeof c[0] === "string" ? c[0] : (c[0] as URL).toString(),
    );
    expect(calls.some((u) => u === "/api/team")).toBe(false);
  });

  it("fetches a trace list from the Jaeger API when configured", async () => {
    // Cross-origin trace URL requires the explicit allowCrossOrigin
    // opt-in (#1061).
    (
      window as unknown as {
        __WITWAVE_CONFIG__: { traceApiUrl: string; traceApiAllowCrossOrigin: boolean };
      }
    ).__WITWAVE_CONFIG__ = {
      traceApiUrl: "http://jaeger.test",
      traceApiAllowCrossOrigin: true,
    };

    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : (input as URL).toString();
        if (url.includes("/api/traces?")) {
          return Promise.resolve(
            okJson({
              data: [
                {
                  traceID: "abc123",
                  spans: [
                    {
                      traceID: "abc123",
                      spanID: "s1",
                      operationName: "POST /message/send",
                      references: [],
                      startTime: 1_700_000_000_000_000,
                      duration: 250_000,
                      processID: "p1",
                      tags: [{ key: "span.kind", value: "server" }],
                    },
                  ],
                  processes: { p1: { serviceName: "iris-harness" } },
                },
              ],
            }),
          );
        }
        return Promise.resolve(okJson({ data: [] }));
      }),
    );

    const router = makeRouter();
    router.push("/otel-traces");
    await router.isReady();
    const wrapper = mount(OTelTracesView, { global: { plugins: [router] } });
    await flushPromises();

    expect(wrapper.find("[data-testid='list-otel-traces']").exists()).toBe(true);
    expect(wrapper.text()).toContain("iris-harness");
    expect(wrapper.text()).toContain("POST /message/send");
    // Short form of the trace id in the list cell.
    expect(wrapper.text()).toContain("abc123");
  });
});
