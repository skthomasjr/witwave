<script setup lang="ts">
import { computed } from "vue";

// One card renderer for every prompt kind the harness recognises
// (job, task, trigger, webhook, continuation, heartbeat). The caller
// feeds us `kind` + `item` and we project the right metadata for the
// card body. Keeping a single component means visual treatment stays
// consistent across the Automation view; each kind just picks which
// secondary lines to show.

export type PromptKind = "job" | "task" | "trigger" | "webhook" | "continuation" | "heartbeat";

interface Props {
  kind: PromptKind;
  // The raw harness row plus the `_agent` tag added by useAgentFanout.
  // Typed `unknown` here because each kind has a different shape; the
  // view-side code pulls named fields out with `as any` at call sites.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  item: any;
}

const props = defineProps<Props>();
defineEmits<{ (e: "click"): void }>();

const kindLabel = computed(() => props.kind.toUpperCase());

// Per-kind accent colour. Matches the palette used in the Metrics view
// (purple/teal/green/amber/red/orange) so the visual language stays
// consistent if you end up glancing back and forth.
const kindColor = computed(() => {
  switch (props.kind) {
    case "job":
      return "#7c6af7";
    case "task":
      return "#3ecfcf";
    case "trigger":
      return "#fbbf24";
    case "webhook":
      return "#fb923c";
    case "continuation":
      return "#a78bfa";
    case "heartbeat":
      return "#4ade80";
  }
  return "#777";
});

// Display name: most kinds have an explicit `name`; heartbeat doesn't
// (it's singleton-per-agent), so we fall back to the agent name.
const displayName = computed<string>(() => {
  if (props.kind === "heartbeat") return `${props.item._agent}/heartbeat`;
  return props.item.name ?? "(unnamed)";
});

const agentName = computed<string>(() => props.item._agent ?? "");

// Primary metadata line — the one-liner under the name that describes
// "when/how this fires". Different per kind:
const primaryLine = computed<string>(() => {
  switch (props.kind) {
    case "job":
    case "task":
      return props.item.schedule ?? "— on-demand";
    case "trigger":
      return `POST /triggers/${props.item.endpoint ?? ""}`;
    case "webhook":
      return props.item.url ?? "—";
    case "continuation": {
      const ca = props.item.continues_after;
      if (!ca) return "—";
      return Array.isArray(ca) ? `after: ${ca.join(", ")}` : `after: ${ca}`;
    }
    case "heartbeat":
      return props.item.schedule ?? "— disabled";
  }
  return "";
});

// Secondary chip line — backend / enabled / state / counts.
interface Chip {
  label: string;
  tone?: "ok" | "warn" | "err" | "dim";
}

const chips = computed<Chip[]>(() => {
  const out: Chip[] = [];
  const b = props.item.backend_id;
  if (b !== undefined && b !== null) {
    out.push({ label: `backend: ${b}`, tone: "dim" });
  } else if (b === null) {
    out.push({ label: "backend: default", tone: "dim" });
  }
  if (typeof props.item.enabled === "boolean") {
    out.push({
      label: props.item.enabled ? "enabled" : "disabled",
      tone: props.item.enabled ? "ok" : "warn",
    });
  }
  if (props.item.running === true) {
    out.push({ label: "running", tone: "ok" });
  }
  // Per-kind extra chips.
  if (props.kind === "webhook") {
    const active = props.item.active_deliveries;
    const max = props.item.max_concurrent_deliveries;
    if (typeof active === "number" && typeof max === "number") {
      out.push({
        label: `deliveries ${active}/${max}`,
        tone: active >= max ? "warn" : "dim",
      });
    }
    if (props.item.notify_on_kind?.length) {
      out.push({
        label: `on: ${props.item.notify_on_kind.join(",")}`,
        tone: "dim",
      });
    }
  }
  if (props.kind === "continuation") {
    const active = props.item.active_fires;
    const max = props.item.max_concurrent_fires;
    if (typeof active === "number" && typeof max === "number") {
      out.push({
        label: `fires ${active}/${max}`,
        tone: active >= max ? "warn" : "dim",
      });
    }
    const when: string[] = [];
    if (props.item.on_success) when.push("on-success");
    if (props.item.on_error) when.push("on-error");
    if (when.length) out.push({ label: when.join("+"), tone: "dim" });
  }
  if (props.kind === "trigger" && props.item.signed) {
    out.push({ label: "HMAC signed", tone: "ok" });
  }
  return out;
});

// Whether this card is clickable for the conversation drawer. We need
// BOTH a session_id (to filter) AND an agent. Most kinds carry a
// session_id; webhook doesn't (delivery is fire-and-forget). Heartbeat
// uses a derived session the harness owns — we can still show it.
const hasConversation = computed<boolean>(() => {
  if (props.kind === "webhook") return false;
  if (props.kind === "heartbeat") return true;
  return !!props.item.session_id;
});

// Disabled items are listed so operators can see what's parked, but
// visually they fade back (reduced opacity + desaturated chip colour)
// so eye draws first to the active ones. Click-through still works so
// the conversation history from the last active runs can be reviewed.
const isDisabled = computed<boolean>(() => props.item.enabled === false);
</script>

<template>
  <button
    type="button"
    class="prompt-card"
    :class="{ 'is-clickable': hasConversation, 'is-disabled': isDisabled }"
    :style="{ '--kind-color': kindColor }"
    :disabled="!hasConversation"
    :title="
      isDisabled
        ? hasConversation
          ? 'Disabled. Click to view last conversation.'
          : 'Disabled.'
        : hasConversation
          ? 'Click to view conversation'
          : 'No conversation available for this kind'
    "
    @click="$emit('click')"
  >
    <header class="head">
      <span class="chip-kind">{{ kindLabel }}</span>
      <span class="name" :title="displayName">{{ displayName }}</span>
      <span class="chip-agent" :title="`Agent ${agentName}`">{{ agentName }}</span>
    </header>
    <div class="primary">{{ primaryLine }}</div>
    <footer v-if="chips.length > 0" class="chips">
      <span v-for="(c, i) in chips" :key="i" class="chip" :class="c.tone ? `chip-${c.tone}` : ''">
        {{ c.label }}
      </span>
    </footer>
  </button>
</template>

<style scoped>
.prompt-card {
  background: var(--witwave-surface);
  border: 1px solid var(--witwave-border);
  border-left: 3px solid var(--kind-color);
  border-radius: var(--witwave-radius);
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  text-align: left;
  font-family: var(--witwave-mono);
  color: var(--witwave-text);
  cursor: default;
  transition:
    border-color 0.12s,
    background 0.12s;
  width: 100%;
}
.prompt-card.is-clickable {
  cursor: pointer;
}
.prompt-card.is-clickable:hover {
  border-color: var(--kind-color);
  background: var(--witwave-bg);
}
.prompt-card:disabled {
  opacity: 0.75;
}
.prompt-card.is-disabled {
  opacity: 0.45;
  filter: saturate(0.55);
}
.prompt-card.is-disabled.is-clickable:hover {
  opacity: 0.8;
  filter: saturate(0.85);
}

.head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.chip-kind {
  font-size: 9px;
  font-weight: 700;
  letter-spacing: 0.08em;
  color: var(--kind-color);
  border: 1px solid var(--kind-color);
  background: rgba(255, 255, 255, 0.02);
  padding: 2px 6px;
  border-radius: var(--witwave-radius);
  flex-shrink: 0;
}
.name {
  font-size: 12px;
  color: var(--witwave-bright);
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
  min-width: 0;
}
.chip-agent {
  font-size: 10px;
  color: var(--witwave-dim);
  background: var(--witwave-bg);
  padding: 2px 8px;
  border-radius: var(--witwave-radius);
  flex-shrink: 0;
}

.primary {
  font-size: 11px;
  color: var(--witwave-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}
.chip {
  font-size: 9px;
  padding: 2px 6px;
  border-radius: var(--witwave-radius);
  color: var(--witwave-dim);
  border: 1px solid var(--witwave-border);
  white-space: nowrap;
}
.chip-ok {
  color: var(--witwave-green);
  border-color: var(--witwave-green);
}
.chip-warn {
  color: var(--witwave-yellow);
  border-color: var(--witwave-yellow);
}
.chip-err {
  color: var(--witwave-red);
  border-color: var(--witwave-red);
}
.chip-dim {
  color: var(--witwave-dim);
}
</style>
