import { describe, expect, it, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import TeamView from "../../src/views/TeamView.vue";

// Smoke test — the TeamView will grow as parity with ui/ is reached (#470).
// For now we prove: (1) the component hits /api/team, (2) loading states are
// disambiguated, (3) a successful fetch renders member names.

describe("TeamView", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("renders loading then team list on fetch success", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => [
          { name: "iris", url: "http://iris:8000", description: "Claude primary" },
          { name: "nova", url: "http://nova:8001" },
        ],
      } as unknown as Response),
    );

    const wrapper = mount(TeamView);
    expect(wrapper.find("[data-testid='team-loading']").exists()).toBe(true);

    await flushPromises();

    const list = wrapper.find("[data-testid='team-list']");
    expect(list.exists()).toBe(true);
    expect(list.text()).toContain("iris");
    expect(list.text()).toContain("nova");
  });

  it("renders error state on fetch failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 503 } as unknown as Response),
    );

    const wrapper = mount(TeamView);
    await flushPromises();

    const err = wrapper.find("[data-testid='team-error']");
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain("HTTP 503");
  });

  it("renders empty-state when team list is empty", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => [],
      } as unknown as Response),
    );

    const wrapper = mount(TeamView);
    await flushPromises();

    expect(wrapper.find("[data-testid='team-empty']").exists()).toBe(true);
  });
});
