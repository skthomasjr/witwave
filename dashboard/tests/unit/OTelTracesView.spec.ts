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
    delete (window as unknown as { __NYX_CONFIG__?: unknown }).__NYX_CONFIG__;
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

  it("fetches a trace list from the Jaeger API when configured", async () => {
    (window as unknown as { __NYX_CONFIG__: { traceApiUrl: string } }).__NYX_CONFIG__ = {
      traceApiUrl: "http://jaeger.test",
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
