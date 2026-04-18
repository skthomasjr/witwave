import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import PromptCard from "../../src/components/PromptCard.vue";

// Unit tests for PromptCard (#968). PromptCard renders every
// automation-card shape (job, task, trigger, webhook, continuation,
// heartbeat). A regression in the heartbeat fallback (no `item.name`)
// or in the kind-label uppercasing shows up as a `(unnamed)` card
// everywhere in production and is otherwise invisible until a user
// reports it. Covers the branches that the Automation view depends on.

function makeJob(overrides: Record<string, unknown> = {}) {
  return { name: "nightly-rollup", _agent: "iris", ...overrides };
}

describe("PromptCard", () => {
  it("uppercases the kind label", () => {
    const wrapper = mount(PromptCard, { props: { kind: "job", item: makeJob() } });
    expect(wrapper.text()).toContain("JOB");
  });

  it("renders the item.name for kinds other than heartbeat", () => {
    const wrapper = mount(PromptCard, { props: { kind: "job", item: makeJob() } });
    expect(wrapper.text()).toContain("nightly-rollup");
  });

  it("falls back to '<agent>/heartbeat' for heartbeat kind", () => {
    const wrapper = mount(PromptCard, {
      props: { kind: "heartbeat", item: { _agent: "iris" } },
    });
    expect(wrapper.text()).toContain("iris/heartbeat");
  });

  it("falls back to '(unnamed)' when name is missing on a non-heartbeat kind", () => {
    const wrapper = mount(PromptCard, {
      props: { kind: "job", item: { _agent: "iris" } },
    });
    expect(wrapper.text()).toContain("(unnamed)");
  });

  it("emits `click` when the card is clickable (has session_id)", async () => {
    const wrapper = mount(PromptCard, {
      props: {
        kind: "job",
        item: makeJob({ session_id: "sess-1" }),
      },
    });
    await wrapper.trigger("click");
    expect(wrapper.emitted("click")).toBeTruthy();
  });

  it("does not emit `click` for webhook (no conversation surface)", async () => {
    const wrapper = mount(PromptCard, {
      props: {
        kind: "webhook",
        item: { name: "pager", _agent: "iris", url: "https://example.test" },
      },
    });
    await wrapper.trigger("click");
    // Button is :disabled so native click doesn't fire the handler.
    expect(wrapper.emitted("click")).toBeFalsy();
  });
});
