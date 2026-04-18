import { describe, expect, it, afterEach, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import ToolAuditView from "../../src/views/ToolAuditView.vue";

// Smoke spec for ToolAuditView (#635). Exercises the per-agent fan-out
// (claude + codex timestamp shapes merged), the decision pill column, and
// the row-click JSON expansion. Uses a memory-history router because the
// view binds filter state to route queries.

function okJson(data: unknown): Response {
  return { ok: true, status: 200, json: async () => data } as unknown as Response;
}

function makeRouter() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/tool-audit", name: "tool-audit", component: ToolAuditView },
      { path: "/", name: "home", component: { template: "<div />" } },
    ],
  });
  return router;
}

describe("ToolAuditView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders rows from claude and codex backends and expands JSON on click", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url =
          typeof input === "string" ? input : (input as URL).toString();
        if (url.includes("/api/team")) {
          return Promise.resolve(
            okJson([
              { name: "bob", url: "http://nyx-bob:8099" },
              { name: "fred", url: "http://nyx-fred:8098" },
            ]),
          );
        }
        if (url.includes("/agents/bob/tool-audit")) {
          // claude-shaped row: ISO ts, tool_name, PostToolUse audit.
          return Promise.resolve(
            okJson([
              {
                ts: "2026-04-16T10:00:00.500Z",
                agent: "bob",
                agent_id: "claude",
                session_id: "s1",
                model: "claude-opus-4-6",
                tool_use_id: "toolu_1",
                tool_name: "Read",
                tool_input: { file_path: "/tmp/foo" },
                tool_response_preview: "hello",
              },
            ]),
          );
        }
        if (url.includes("/agents/fred/tool-audit")) {
          // codex-shaped row: numeric ts, tool + decision (deny).
          return Promise.resolve(
            okJson([
              {
                ts: 1_744_790_400,
                tool: "LocalShell",
                decision: "deny",
                rule: "network",
                reason: "no outbound network",
                command: "curl http://evil.example",
              },
            ]),
          );
        }
        return Promise.resolve(okJson([]));
      }),
    );

    const router = makeRouter();
    router.push("/tool-audit");
    await router.isReady();
    const wrapper = mount(ToolAuditView, {
      global: { plugins: [router] },
    });
    await flushPromises();

    expect(wrapper.find("[data-testid='list-tool-audit']").exists()).toBe(
      true,
    );
    // Both rows surface (one per agent).
    expect(wrapper.text()).toContain("Read");
    expect(wrapper.text()).toContain("LocalShell");
    expect(wrapper.text()).toContain("2 / 2");
    // Deny decision pill present.
    expect(wrapper.text()).toContain("deny");
    expect(wrapper.text()).toContain("@bob");
    expect(wrapper.text()).toContain("@fred");

    // Click first row chevron → expanded JSON block appears.
    const firstRow = wrapper.find("tr.row");
    await firstRow.trigger("click");
    expect(wrapper.find("tr.expanded").exists()).toBe(true);
    // The expanded pre includes the source-of-truth keys from the backend.
    const preText = wrapper.find("tr.expanded pre").text();
    expect(preText).toMatch(/tool_name|tool/);
  });
});
