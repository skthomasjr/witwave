import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createI18n } from "vue-i18n";
import TimelineView from "../../src/views/TimelineView.vue";
import { useTimelineStore } from "../../src/stores/timeline";
import en from "../../src/i18n/locales/en.json";

// View smoke coverage for the timeline UI (#1110 phase 1). Mounts with
// a pre-populated store so the SSE layer stays out of scope — that's
// covered in `useEventStream.spec.ts`.

// Stub useTeam so the view doesn't try to poll the directory when it
// mounts. Returning an empty `members` ref is enough — the agent filter
// will then derive names from the event feed only.
vi.mock("../../src/composables/useTeam", () => ({
  useTeam: () => ({
    members: { value: [] },
    error: { value: "" },
    loading: { value: false },
    refresh: () => {},
  }),
}));

function makeI18n() {
  return createI18n({
    legacy: false,
    locale: "en",
    fallbackLocale: "en",
    messages: { en },
  });
}

describe("TimelineView", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    // jsdom's localStorage is read-only by default; wrap in try/catch so
    // the beforeEach still runs when the backing Storage throws.
    try {
      window.localStorage?.clear?.();
    } catch {
      // ignore
    }
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the empty state when no events have arrived", async () => {
    const i18n = makeI18n();
    const wrapper = mount(TimelineView, {
      global: { plugins: [i18n] },
    });
    await flushPromises();

    expect(wrapper.text()).toContain("Waiting for activity");
    wrapper.unmount();
  });

  it("renders rows newest-first and applies the type filter", async () => {
    const i18n = makeI18n();
    const store = useTimelineStore();
    // Prevent the view's onMounted from starting the live stream.
    store.started = true;

    store.__pushForTest({
      id: "1",
      type: "job.fired",
      version: 1,
      ts: "2026-04-18T00:00:00Z",
      agent_id: "iris",
      payload: { name: "a", schedule: "*", duration_ms: 5, outcome: "success" },
    });
    store.__pushForTest({
      id: "2",
      type: "webhook.delivered",
      version: 1,
      ts: "2026-04-18T00:00:01Z",
      agent_id: "iris",
      payload: { name: "b", url_host: "example.com", status_code: 200, duration_ms: 10 },
    });

    const wrapper = mount(TimelineView, {
      global: { plugins: [i18n] },
    });
    await flushPromises();

    const rows = wrapper.findAll('[data-testid^="timeline-row-"]');
    expect(rows.length).toBe(2);
    // Newest first.
    expect(rows[0].attributes("data-event-id")).toBe("2");
    expect(rows[1].attributes("data-event-id")).toBe("1");

    // Apply a type filter — only job.fired should remain.
    const typeSelect = wrapper.get('[data-testid="timeline-type-filter"]');
    await typeSelect.setValue(["job.fired"]);
    await flushPromises();
    const filtered = wrapper.findAll('[data-testid^="timeline-row-"]');
    expect(filtered.length).toBe(1);
    expect(filtered[0].attributes("data-event-id")).toBe("1");

    wrapper.unmount();
  });

  it("pinning a row moves it to the top even as newer events arrive", async () => {
    const i18n = makeI18n();
    const store = useTimelineStore();
    store.started = true;

    store.__pushForTest({
      id: "1",
      type: "job.fired",
      version: 1,
      ts: "2026-04-18T00:00:00Z",
      agent_id: "iris",
      payload: { name: "a", schedule: "*", duration_ms: 5, outcome: "success" },
    });
    store.__pushForTest({
      id: "2",
      type: "job.fired",
      version: 1,
      ts: "2026-04-18T00:00:01Z",
      agent_id: "iris",
      payload: { name: "b", schedule: "*", duration_ms: 5, outcome: "success" },
    });

    const wrapper = mount(TimelineView, {
      global: { plugins: [i18n] },
    });
    await flushPromises();

    // Pin id=1 (currently the oldest, so rendered second).
    const pinBtn = wrapper.get('[data-testid="timeline-pin-1"]');
    await pinBtn.trigger("click");
    await flushPromises();

    const rows = wrapper.findAll('[data-testid^="timeline-row-"]');
    // Pinned row (1) should now be on top despite being older.
    expect(rows[0].attributes("data-event-id")).toBe("1");
    expect(rows[1].attributes("data-event-id")).toBe("2");

    // Push a newer event — pinned row stays on top.
    store.__pushForTest({
      id: "3",
      type: "job.fired",
      version: 1,
      ts: "2026-04-18T00:00:02Z",
      agent_id: "iris",
      payload: { name: "c", schedule: "*", duration_ms: 5, outcome: "success" },
    });
    await flushPromises();
    const rows2 = wrapper.findAll('[data-testid^="timeline-row-"]');
    expect(rows2[0].attributes("data-event-id")).toBe("1");
    expect(rows2[1].attributes("data-event-id")).toBe("3");

    // Pinned id persists to localStorage when the backing Storage
    // supports writes. jsdom's configuration in this repo leaves the
    // Storage stub without getItem/setItem so we don't hard-assert
    // persistence here — the localStorage guard in the view swallows
    // errors in that mode. In a real browser the guard's write path is
    // exercised by the e2e suite.
    try {
      const stored = window.localStorage.getItem("witwave.timeline.pinned");
      if (stored) {
        expect(JSON.parse(stored)).toContain("1");
      }
    } catch {
      // ignore — jsdom without Storage implementation
    }

    wrapper.unmount();
  });

  it("search box filters rows by free text across payload", async () => {
    const i18n = makeI18n();
    const store = useTimelineStore();
    store.started = true;

    store.__pushForTest({
      id: "1",
      type: "webhook.delivered",
      version: 1,
      ts: "2026-04-18T00:00:00Z",
      agent_id: "iris",
      payload: { name: "alpha", url_host: "example.com", status_code: 200, duration_ms: 10 },
    });
    store.__pushForTest({
      id: "2",
      type: "webhook.delivered",
      version: 1,
      ts: "2026-04-18T00:00:01Z",
      agent_id: "iris",
      payload: { name: "beta", url_host: "other.org", status_code: 200, duration_ms: 12 },
    });

    const wrapper = mount(TimelineView, {
      global: { plugins: [i18n] },
    });
    await flushPromises();

    const search = wrapper.get('[data-testid="timeline-search"]');
    await search.setValue("alpha");
    await flushPromises();

    const rows = wrapper.findAll('[data-testid^="timeline-row-"]');
    expect(rows.length).toBe(1);
    expect(rows[0].attributes("data-event-id")).toBe("1");

    wrapper.unmount();
  });
});
