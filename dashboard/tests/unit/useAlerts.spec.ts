import { beforeEach, describe, expect, it, vi } from "vitest";
import { effectScope, nextTick, ref } from "vue";
import { createPinia, setActivePinia } from "pinia";

// useAlerts (#1110 phase 2) aggregates two signals:
//   - timeline event stream (webhook/hook/lifecycle/stream markers)
//   - polling-derived team health (fallback when SSE is down)
//
// We mock useTeam (and therefore useHealth's upstream) to a deterministic
// no-problems state so the event-driven paths are observable in isolation,
// and drive the timeline store via its `__pushForTest` hook.

const sharedMembers = ref<Array<{ name: string; error?: string }>>([
  { name: "iris" },
]);
const sharedError = ref<string>("");
const sharedLoading = ref<boolean>(false);

vi.mock("../../src/composables/useTeam", () => ({
  useTeam: () => ({
    members: sharedMembers,
    error: sharedError,
    loading: sharedLoading,
  }),
}));

import { useAlerts, __resetUseAlerts } from "../../src/composables/useAlerts";
import { useTimelineStore } from "../../src/stores/timeline";
import type { EventEnvelope } from "../../src/composables/useEventStream";

function make(
  id: string,
  type: string,
  agent: string | null,
  payload: Record<string, unknown> = {},
  ts: string = "2026-04-18T00:00:00Z",
): EventEnvelope {
  return { id, type, version: 1, ts, agent_id: agent, payload };
}

function run<T>(fn: () => T): { value: T; stop: () => void } {
  const scope = effectScope();
  const value = scope.run(fn) as T;
  return { value, stop: () => scope.stop() };
}

describe("useAlerts (timeline-driven)", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    __resetUseAlerts();
    sharedMembers.value = [{ name: "iris" }];
    sharedError.value = "";
    sharedLoading.value = false;
  });

  it("webhook.failed with reason=timeout surfaces a warning alert", async () => {
    const store = useTimelineStore();
    const { value } = run(() => useAlerts());

    store.__pushForTest(
      make("1", "webhook.failed", "iris", {
        name: "ping",
        url_host: "example.com",
        reason: "timeout",
        duration_ms: 30000,
      }),
    );
    await nextTick();

    expect(value.alerts.value).toHaveLength(1);
    const alert = value.alerts.value[0];
    expect(alert.severity).toBe("warning");
    expect(alert.id).toBe("webhook.iris.ping.example.com");
    expect(alert.title).toContain("ping");
    expect(alert.count).toBe(1);
  });

  it("duplicate webhook failure within the window updates count, not new alert", async () => {
    const store = useTimelineStore();
    const { value } = run(() => useAlerts());

    store.__pushForTest(
      make("1", "webhook.failed", "iris", {
        name: "ping",
        url_host: "example.com",
        reason: "timeout",
      }),
    );
    await nextTick();
    store.__pushForTest(
      make("2", "webhook.failed", "iris", {
        name: "ping",
        url_host: "example.com",
        reason: "exception",
      }),
    );
    await nextTick();

    expect(value.alerts.value).toHaveLength(1);
    expect(value.alerts.value[0].count).toBe(2);
  });

  it("webhook.failed with a non-transport reason is ignored", async () => {
    const store = useTimelineStore();
    const { value } = run(() => useAlerts());

    store.__pushForTest(
      make("1", "webhook.failed", "iris", {
        name: "ping",
        url_host: "example.com",
        reason: "http_status",
      }),
    );
    await nextTick();
    expect(value.alerts.value).toHaveLength(0);
  });

  it("fires hook-deny alert at 10 events and does not re-fire on 11th", async () => {
    const store = useTimelineStore();
    const { value } = run(() => useAlerts());

    const base = Date.parse("2026-04-18T00:00:00Z");
    for (let i = 0; i < 10; i += 1) {
      store.__pushForTest(
        make(
          String(i + 1),
          "hook.decision",
          "iris",
          { backend: "claude", decision: "deny", tool: "Bash" },
          new Date(base + i * 1000).toISOString(),
        ),
      );
    }
    await nextTick();

    const firstHit = value.alerts.value.find((a) => a.id === "hook-deny.claude");
    expect(firstHit).toBeDefined();
    expect(firstHit!.severity).toBe("warning");

    // 11th within the window should not toast again (upsert de-dupes; armed
    // flag prevents re-firing).
    store.__pushForTest(
      make("11", "hook.decision", "iris", {
        backend: "claude",
        decision: "deny",
        tool: "Bash",
      }, new Date(base + 10_000).toISOString()),
    );
    await nextTick();
    const stillOne = value.alerts.value.filter((a) => a.id === "hook-deny.claude");
    expect(stillOne).toHaveLength(1);
  });

  it("hook-deny alert re-arms after the window drains below the reset threshold", async () => {
    const store = useTimelineStore();
    const { value } = run(() => useAlerts());

    // Burst 10 events inside a 2-second span to arm the alert.
    const base = Date.parse("2026-04-18T00:00:00Z");
    for (let i = 0; i < 10; i += 1) {
      store.__pushForTest(
        make(
          String(i + 1),
          "hook.decision",
          "iris",
          { backend: "claude", decision: "deny" },
          new Date(base + i * 100).toISOString(),
        ),
      );
    }
    await nextTick();
    expect(value.alerts.value.find((a) => a.id === "hook-deny.claude")).toBeDefined();

    // Fast-forward 10 minutes — the next deny event's timestamp is outside
    // the 5-minute window, pruning every prior entry and dropping the
    // count to 1, which is below the reset threshold (5). The armed flag
    // clears and the alert auto-resolves.
    store.__pushForTest(
      make(
        "late-1",
        "hook.decision",
        "iris",
        { backend: "claude", decision: "deny" },
        new Date(base + 10 * 60 * 1000).toISOString(),
      ),
    );
    await nextTick();
    expect(value.alerts.value.find((a) => a.id === "hook-deny.claude")).toBeUndefined();

    // Another burst re-arms — this proves the hysteresis isn't one-shot.
    for (let i = 0; i < 10; i += 1) {
      store.__pushForTest(
        make(
          `burst2-${i}`,
          "hook.decision",
          "iris",
          { backend: "claude", decision: "deny" },
          new Date(base + 10 * 60 * 1000 + i * 100).toISOString(),
        ),
      );
    }
    await nextTick();
    expect(value.alerts.value.find((a) => a.id === "hook-deny.claude")).toBeDefined();
  });

  it("agent.lifecycle stopped fires error; matching started clears it", async () => {
    const store = useTimelineStore();
    const { value } = run(() => useAlerts());

    store.__pushForTest(
      make("1", "agent.lifecycle", "iris", { backend: "claude", event: "stopped" }),
    );
    await nextTick();
    const stopped = value.alerts.value.find((a) => a.id === "lifecycle.iris");
    expect(stopped).toBeDefined();
    expect(stopped!.severity).toBe("error");
    expect(stopped!.title).toContain("iris");

    store.__pushForTest(
      make("2", "agent.lifecycle", "iris", { backend: "claude", event: "started" }),
    );
    await nextTick();
    expect(value.alerts.value.find((a) => a.id === "lifecycle.iris")).toBeUndefined();
  });

  it("stream reconnect overrun surfaces a single info alert", async () => {
    const store = useTimelineStore();
    const { value } = run(() => useAlerts());

    store.__pushForTest(make("1", "stream.gap", null, { reason: "ring-eviction" }));
    await nextTick();

    const gap = value.alerts.value.find((a) => a.id === "stream-gap");
    expect(gap).toBeDefined();
    expect(gap!.severity).toBe("info");

    // Second marker within the window updates in place, still one alert.
    store.__pushForTest(make("2", "stream.overrun", null, {}));
    await nextTick();
    const count = value.alerts.value.filter((a) => a.id === "stream-gap").length;
    expect(count).toBe(1);
  });

  it("dismiss removes the alert from the active list for the session", async () => {
    const store = useTimelineStore();
    const { value } = run(() => useAlerts());

    store.__pushForTest(
      make("1", "webhook.failed", "iris", {
        name: "ping",
        url_host: "example.com",
        reason: "timeout",
      }),
    );
    await nextTick();
    expect(value.alerts.value).toHaveLength(1);

    value.dismiss("webhook.iris.ping.example.com");
    await nextTick();
    expect(value.alerts.value).toHaveLength(0);
  });

  it("active prefers the highest-severity alert when multiple fire", async () => {
    const store = useTimelineStore();
    const { value } = run(() => useAlerts());

    // info
    store.__pushForTest(make("1", "stream.gap", null, {}));
    // warning
    store.__pushForTest(
      make("2", "webhook.failed", "iris", {
        name: "ping",
        url_host: "example.com",
        reason: "timeout",
      }),
    );
    // error
    store.__pushForTest(
      make("3", "agent.lifecycle", "iris", { backend: "claude", event: "stopped" }),
    );
    await nextTick();

    expect(value.active.value).not.toBeNull();
    expect(value.active.value!.severity).toBe("error");
  });

  it("keeps poll-derived health alert when no event-driven alerts fire", async () => {
    sharedMembers.value = [
      { name: "iris", error: "timeout" },
      { name: "nova", error: "timeout" },
    ];
    sharedLoading.value = false;
    const { value } = run(() => useAlerts());
    await nextTick();

    expect(value.active.value).not.toBeNull();
    expect(value.active.value!.severity).toBe("error");
    expect(value.active.value!.title).toBe("All agents unreachable");
  });
});
