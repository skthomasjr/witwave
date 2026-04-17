import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import PrimeVue from "primevue/config";
import TeamView from "../../src/views/TeamView.vue";

// Dashboard TeamView smoke tests. Shape matches harness /team contract
// (src/types/team.ts) — array of team members each with an `agents` array of
// nyx + backend cards. Chat is deferred per #470, so these tests cover list
// render, selection, and the loading/error/empty placeholders only.

function mountView() {
  return mount(TeamView, {
    global: {
      plugins: [PrimeVue],
    },
  });
}

function mockTeamResponse(data: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => data,
    } as unknown as Response),
  );
}

describe("TeamView", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders loading state then agent cards on fetch success", async () => {
    mockTeamResponse([
      {
        name: "iris",
        url: "http://iris:8000",
        agents: [
          {
            id: "iris-nyx",
            role: "nyx",
            url: "http://iris:8000",
            card: { name: "iris", description: "Primary agent" },
          },
          {
            id: "iris-a2-claude",
            role: "backend",
            url: "http://iris-a2-claude:8080",
            card: { name: "iris-claude" },
          },
        ],
      },
      {
        name: "nova",
        url: "http://nova:8001",
        agents: [{ id: "nova-nyx", role: "nyx", url: "http://nova:8001", card: { name: "nova" } }],
      },
    ]);

    const wrapper = mountView();
    expect(wrapper.find("[data-testid='team-loading']").exists()).toBe(true);

    await flushPromises();

    const cards = wrapper.findAll("[data-testid='agent-card']");
    expect(cards.length).toBe(2);
    expect(wrapper.text()).toContain("iris");
    expect(wrapper.text()).toContain("nova");
    expect(wrapper.text()).toContain("iris-a2-claude");
  });

  it("renders error placeholder on fetch failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: false, status: 503 } as unknown as Response),
    );

    const wrapper = mountView();
    await flushPromises();

    const err = wrapper.find("[data-testid='team-error']");
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain("HTTP 503");
  });

  it("renders empty-state when team list is empty", async () => {
    mockTeamResponse([]);

    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.find("[data-testid='team-empty']").exists()).toBe(true);
  });

  it("renders an unreachable card when a member reports an error", async () => {
    mockTeamResponse([
      {
        name: "ghost",
        url: "http://ghost:8000",
        agents: [],
        error: "connection refused",
      },
    ]);

    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.find("[data-testid='agent-card-unreachable']").exists()).toBe(true);
  });

  it("shows the right-pane placeholder until an agent is selected", async () => {
    mockTeamResponse([
      {
        name: "iris",
        url: "http://iris:8000",
        agents: [{ id: "iris-nyx", role: "nyx", url: "http://iris:8000", card: { name: "iris" } }],
      },
    ]);

    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.find("[data-testid='detail-placeholder']").exists()).toBe(true);

    await wrapper.find("[data-testid='agent-card']").trigger("click");
    expect(wrapper.find("[data-testid='detail-body']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='detail-placeholder']").exists()).toBe(false);
  });
});
