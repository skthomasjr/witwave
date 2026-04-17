import { onMounted, onUnmounted, ref } from "vue";
import { apiGet } from "../api/client";

// Polls /api/team (served locally by dashboard nginx — cheap and always
// available if the pod itself is up) to produce the status dot in the
// header. Green = last fetch ok, red = last fetch failed, gray = first
// fetch in flight. Deliberately lightweight; the per-view composables
// still own their own error state for the main UI.

export function useHealth(intervalMs = 10000) {
  const state = ref<"connecting" | "ok" | "err">("connecting");
  const detail = ref<string>("");

  let timer: ReturnType<typeof setInterval> | null = null;
  let aborter: AbortController | null = null;

  async function check(): Promise<void> {
    aborter?.abort();
    aborter = new AbortController();
    try {
      await apiGet<unknown>("/team", { signal: aborter.signal });
      state.value = "ok";
      detail.value = "";
    } catch (e) {
      if ((e as { name?: string }).name === "AbortError") return;
      state.value = "err";
      detail.value = (e as Error).message;
    }
  }

  onMounted(() => {
    void check();
    timer = setInterval(() => void check(), intervalMs);
  });

  onUnmounted(() => {
    if (timer !== null) clearInterval(timer);
    aborter?.abort();
  });

  return { state, detail };
}
