import { describe, expect, it, vi } from "vitest";
import { effectScope, ref } from "vue";

// Unit tests for useHealth (#967). The composable drives the header
// status dot, so a stale `connecting` or a hidden `partial` shows the
// wrong cluster state. Mocks useTeam so the reactive inputs are fully
// controllable and the spec stays offline.

const sharedMembers = ref<Array<{ name: string; error?: string }>>([]);
const sharedError = ref<string>("");
const sharedLoading = ref<boolean>(true);

vi.mock("../../src/composables/useTeam", () => ({
  useTeam: () => ({
    members: sharedMembers,
    error: sharedError,
    loading: sharedLoading,
  }),
}));

import { useHealth } from "../../src/composables/useHealth";

function setTeam(members: Array<{ name: string; error?: string }>, opts: { error?: string; loading?: boolean } = {}) {
  sharedMembers.value = members;
  sharedError.value = opts.error ?? "";
  sharedLoading.value = opts.loading ?? false;
}

function run<T>(fn: () => T): T {
  const scope = effectScope();
  try {
    return scope.run(fn) as T;
  } finally {
    scope.stop();
  }
}

describe("useHealth", () => {
  it("reports connecting while the first fetch is in flight with no members", () => {
    setTeam([], { loading: true });
    const { state, detail } = run(() => useHealth());
    expect(state.value).toBe("connecting");
    expect(detail.value).toBe("");
  });

  it("reports empty when the directory resolves with zero members", () => {
    setTeam([], { loading: false });
    const { state, detail } = run(() => useHealth());
    expect(state.value).toBe("empty");
    expect(detail.value).toContain("no agents configured");
  });

  it("reports ok when every member has no error", () => {
    setTeam([{ name: "iris" }, { name: "nova" }]);
    const { state } = run(() => useHealth());
    expect(state.value).toBe("ok");
  });

  it("reports partial with failing names in detail", () => {
    setTeam([{ name: "iris" }, { name: "nova", error: "timeout" }]);
    const { state, detail } = run(() => useHealth());
    expect(state.value).toBe("partial");
    expect(detail.value).toBe("failing: nova");
  });

  it("reports err when every member failed", () => {
    setTeam([
      { name: "iris", error: "timeout" },
      { name: "nova", error: "timeout" },
    ]);
    const { state, detail } = run(() => useHealth());
    expect(state.value).toBe("err");
    expect(detail.value).toBe("all agents unreachable: iris, nova");
  });

  it("reports err when the directory itself failed and members list is empty", () => {
    setTeam([], { error: "connection refused", loading: false });
    const { state, detail } = run(() => useHealth());
    expect(state.value).toBe("err");
    expect(detail.value).toBe("connection refused");
  });
});
