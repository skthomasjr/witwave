import { describe, expect, it, beforeEach, vi } from "vitest";
import { mount } from "@vue/test-utils";
import { axe } from "vitest-axe";
import PrimeVue from "primevue/config";
import { createPinia, setActivePinia } from "pinia";

import AgentCard from "../../src/components/AgentCard.vue";
import AlertBanner from "../../src/components/AlertBanner.vue";
import PromptCard from "../../src/components/PromptCard.vue";
import AgentList from "../../src/components/AgentList.vue";

// a11y smoke baseline (#970). vitest-axe runs axe-core over the
// rendered DOM of each component and asserts there are zero
// violations at the default ruleset. This is a floor, not a
// ceiling — it catches missing labels, bad ARIA attributes, and
// colour-independent semantic issues. Full rollout (every view,
// focus-order / colour-contrast passes, CI gating) is follow-up.

// Skip rules that are known-false-positive in isolated component
// mounts (e.g. colour-contrast needs computed styles jsdom doesn't
// compute; region landmark requires a full page).
const runRules = {
  rules: {
    "color-contrast": { enabled: false },
    region: { enabled: false },
  },
};

describe("a11y smoke", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it("AgentCard has no detectable a11y violations", async () => {
    const wrapper = mount(AgentCard, {
      global: { plugins: [PrimeVue] },
      props: {
        member: {
          name: "iris",
          url: "http://iris:8000",
          agents: [
            {
              id: "iris-witwave",
              role: "witwave",
              url: "http://iris:8000",
              card: { name: "iris", description: "Primary agent" },
            },
          ],
        },
      },
    });
    const results = await axe(wrapper.element, runRules);
    expect(results).toHaveNoViolations();
  });

  it("AlertBanner has no detectable a11y violations when no alert", async () => {
    const wrapper = mount(AlertBanner, {
      global: { plugins: [PrimeVue] },
    });
    const results = await axe(wrapper.element, runRules);
    expect(results).toHaveNoViolations();
  });

  it("PromptCard (job) has no detectable a11y violations", async () => {
    const wrapper = mount(PromptCard, {
      global: { plugins: [PrimeVue] },
      props: {
        kind: "job",
        item: {
          name: "nightly-backup",
          _agent: "iris",
          cron: "0 3 * * *",
          prompt: "Run backup",
        },
      },
    });
    const results = await axe(wrapper.element, runRules);
    expect(results).toHaveNoViolations();
  });

  it("AgentList empty state has no detectable a11y violations", async () => {
    // AgentList in the "no agents found" terminal state — cheap mount
    // that exercises the placeholder branch without needing fetch mocks.
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve({ ok: true, status: 200, json: async () => [] } as unknown as Response)));
    const wrapper = mount(AgentList, {
      global: { plugins: [PrimeVue] },
      props: {
        members: [],
        loading: false,
        error: "",
        selectedName: null,
        activeBackendId: null,
      },
    });
    const results = await axe(wrapper.element, runRules);
    expect(results).toHaveNoViolations();
  });
});
