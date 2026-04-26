import { getCurrentInstance, onBeforeUnmount, ref, type Ref } from "vue";

// Phase-1 client for the harness `/events/stream` SSE feed (#1110).
//
// Why fetch + ReadableStream instead of EventSource: browser EventSource
// cannot set headers, and the harness requires `Authorization: Bearer`
// on every request (see docs/events/README.md#auth). The fetch-stream
// pattern also lets us surface transport-level errors to callers and
// cancel cleanly on unmount.
//
// Parser notes:
//   - SSE framing per https://html.spec.whatwg.org/multipage/server-sent-events.html
//   - `event:` field sets the event type (we mirror it into the envelope
//     `type` for convenience, but always prefer the JSON body).
//   - `id:` updates `lastEventId` which is sent back as `Last-Event-ID`
//     on reconnect so the harness ring buffer can replay missed events.
//   - `retry:` updates the reconnect delay ceiling for the next attempt.
//   - `: <comment>` lines are keepalives — ignored.
//   - Blank line delimits a message.
//
// Control events (`stream.gap`, `stream.overrun`) are observed inline:
//   - `stream.overrun` means the harness closed our queue; reset
//     `lastEventId` to the overrun's `resume_id` (when present) so
//     reconnect restarts from the current head rather than thrashing
//     the ring, then schedule a reconnect.
//   - `stream.gap` is surfaced as a synthetic event in the list so the
//     UI can render a "missed events" marker.

export interface EventEnvelope {
  type: string;
  version: number;
  id: string;
  ts: string;
  agent_id: string | null;
  payload: Record<string, unknown>;
}

export interface UseEventStreamOptions {
  // Maximum number of events to retain in the returned `events` ref.
  // Once the list exceeds this, the oldest entries are evicted. Default
  // 500; the store layer keeps a bigger ring when desired.
  maxEvents?: number;
  // Bearer token for `Authorization`. Optional: local dev may run with
  // the auth guard disabled.
  token?: string;
  // Connect immediately on composable creation. Default `true`. Unit
  // tests set this `false` and call `open()` explicitly so the request
  // is observed deterministically.
  autoConnect?: boolean;
  // Injection seam for tests — swap in a fetch mock without having to
  // stub global fetch for the whole module.
  fetchImpl?: typeof fetch;
  // Base reconnect delay (ms). Backoff is exponential up to `maxDelayMs`
  // with full-jitter randomisation.
  initialDelayMs?: number;
  maxDelayMs?: number;
  // If the server sends a `retry:` field it overrides the in-flight
  // backoff ceiling for the next attempt.
}

export interface UseEventStreamReturn {
  events: Ref<EventEnvelope[]>;
  connected: Ref<boolean>;
  reconnecting: Ref<boolean>;
  error: Ref<string>;
  lastEventId: Ref<string>;
  // Count of events dropped by the per-stream rate limiter (#1606).
  // Surfaced reactively so the debug panel / UI can show backpressure
  // without needing a separate metrics channel. Sibling to #1605's
  // seenIds-cap work in the same merge path.
  droppedEventCount: Ref<number>;
  // Count of SSE messages whose data payload failed JSON.parse (#1634).
  // Surfaced reactively so the debug panel / UI can observe malformed
  // payloads instead of dropping them silently. Sibling counter to
  // `droppedEventCount` (rate-limit drops); both flow through the same
  // merge path in `handleMessage`.
  parseFailureCount: Ref<number>;
  open: () => void;
  close: () => void;
}

const DEFAULT_MAX_EVENTS = 500;
const DEFAULT_INITIAL_DELAY_MS = 100;
const DEFAULT_MAX_DELAY_MS = 10_000;
// Minimum reconnect delay. Full-jitter with `Math.random() * ceiling`
// can produce 0ms, which turns a server bounce into a tight reconnect
// loop against an unhealthy upstream. Floor every computed delay at
// MIN_BACKOFF_MS so the reconnect pacer can't degenerate. (#1236)
//
// Raised from 50ms to 500ms in #1615: the prior 50ms floor was still
// aggressive enough that under fleet scale (N dashboard tabs all
// honouring `retry: 0` from a buggy/malicious backend), the harness
// saw ~N reconnects per 50ms — a tight DDoS amplification window
// exactly when the harness was already unhealthy.
const MIN_BACKOFF_MS = 500;
// Server `retry:` hints below this threshold are treated as suspect
// (likely a buggy backend) and clamped up to MIN_BACKOFF_MS rather
// than being honoured at face value. (#1615)
const MIN_TRUSTED_SERVER_RETRY_MS = 100;
// Stop reconnecting after this many consecutive failures. Without a
// terminal state, a permanently-broken stream loops forever, burning
// battery + amplifying load against a known-bad upstream. The UI sees
// the terminal state via the existing `error` ref. (#1615)
const MAX_CONSECUTIVE_FAILURES = 30;
// Per-stream event rate cap (#1606). A runaway backend that floods us
// with thousands of events/sec can saturate the merge path (Vue
// reactivity + downstream watchers in TimelineView/useAlerts). We
// drop above the cap rather than slow-poll or block — pure drop with
// telemetry. Sibling to #1605's seenIds cap in the same merge path.
const MAX_EVENTS_PER_SECOND_PER_STREAM = 200;
// Throttle for the rate-limit console.warn so a sustained flood
// doesn't itself fill the console.
const RATE_LIMIT_WARN_INTERVAL_MS = 5_000;

interface ParsedMessage {
  type: string;
  id: string;
  data: string;
  retryMs: number | null;
}

// Minimal SSE message assembler. The spec is byte-oriented; we work in
// strings here since fetch's ReadableStream hands us decoded text. The
// assembler yields one `ParsedMessage` per `\n\n`-terminated block.
export function __parseSSEChunk(
  buffer: string,
): { messages: ParsedMessage[]; remainder: string } {
  const messages: ParsedMessage[] = [];
  let remainder = buffer;

  // Normalise line endings so `\r\n` and `\r` separators work the same
  // as `\n`. The SSE spec allows all three; the harness uses `\n` but a
  // proxy could rewrap.
  remainder = remainder.replace(/\r\n|\r/g, "\n");

  while (true) {
    const boundary = remainder.indexOf("\n\n");
    if (boundary === -1) break;
    const block = remainder.slice(0, boundary);
    remainder = remainder.slice(boundary + 2);
    const parsed = parseBlock(block);
    if (parsed) messages.push(parsed);
  }

  return { messages, remainder };
}

function parseBlock(block: string): ParsedMessage | null {
  let type = "message";
  let id = "";
  const dataLines: string[] = [];
  let retryMs: number | null = null;
  let sawField = false;

  for (const rawLine of block.split("\n")) {
    if (rawLine.length === 0) continue;
    // Comment / keepalive line — ignore entirely.
    if (rawLine.startsWith(":")) continue;
    const colon = rawLine.indexOf(":");
    let field: string;
    let value: string;
    if (colon === -1) {
      field = rawLine;
      value = "";
    } else {
      field = rawLine.slice(0, colon);
      // SSE spec: single leading space after the colon is stripped.
      value = rawLine.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
    }
    sawField = true;
    switch (field) {
      case "event":
        type = value;
        break;
      case "id":
        id = value;
        break;
      case "data":
        dataLines.push(value);
        break;
      case "retry": {
        const n = Number.parseInt(value, 10);
        if (Number.isFinite(n) && n >= 0) retryMs = n;
        break;
      }
      default:
        // Unknown fields are ignored per SSE spec.
        break;
    }
  }

  if (!sawField) return null;
  return {
    type,
    id,
    data: dataLines.join("\n"),
    retryMs,
  };
}

function clampMaxEvents(max: number): number {
  if (!Number.isFinite(max) || max <= 0) return DEFAULT_MAX_EVENTS;
  return Math.floor(max);
}

function jitter(base: number): number {
  // Full jitter — 0..base. Prevents synchronized reconnect storms when
  // many dashboards watch the same harness after a bounce.
  return Math.floor(Math.random() * base);
}

function makeSyntheticGapEvent(envelope: EventEnvelope): EventEnvelope {
  // Re-emit the server's stream.gap verbatim as a UI marker. The
  // caller (TimelineView) branches on `type === 'stream.gap'` to render
  // a "missed events" row inline.
  return envelope;
}

export function useEventStream(
  url: string,
  options: UseEventStreamOptions = {},
): UseEventStreamReturn {
  const maxEvents = clampMaxEvents(options.maxEvents ?? DEFAULT_MAX_EVENTS);
  const initialDelayMs = options.initialDelayMs ?? DEFAULT_INITIAL_DELAY_MS;
  const maxDelayMs = options.maxDelayMs ?? DEFAULT_MAX_DELAY_MS;
  const fetchImpl = options.fetchImpl ?? fetch;

  const events = ref<EventEnvelope[]>([]) as Ref<EventEnvelope[]>;
  const connected = ref(false);
  const reconnecting = ref(false);
  const error = ref("");
  const lastEventId = ref("");
  const droppedEventCount = ref(0);
  // Count of malformed JSON payloads observed by handleMessage (#1634).
  // We keep dropping the event (one bad payload should not tear down
  // the stream) but expose the count so the UI can surface it.
  const parseFailureCount = ref(0);

  let aborter: AbortController | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let attempt = 0;
  let serverRetryHintMs: number | null = null;
  let closed = false;
  // Consecutive-failure counter — incremented on each scheduleReconnect
  // entry, reset to 0 on a successful connect. When >= MAX_CONSECUTIVE_FAILURES
  // we stop scheduling further reconnects and surface the terminal state
  // via the `error` ref. (#1615)
  let consecutiveFailures = 0;
  let lastRetryClampWarnAt = 0;
  // generation counter — every open() bumps this. runOnce/loop/
  // scheduleReconnect/fetch-then callbacks capture the generation at
  // entry and early-return if it no longer matches currentGen. This
  // prevents stale callbacks from a prior open() session from touching
  // refs or scheduling work after close()/reopen. (#1153)
  let currentGen = 0;

  // Per-stream rate limiter state (#1606). Sliding 1-second bucket
  // keyed off Date.now() / 1000. When a runaway backend exceeds the
  // cap we drop the event and bump `droppedEventCount` for the UI.
  // Sibling fix to #1605 (seenIds cap) — both touch this merge path
  // and intentionally land together; do NOT duplicate #1605's scope
  // here.
  let eventsThisSecond = 0;
  let currentBucket = 0;
  let lastWarnAt = 0;

  function pushEvent(envelope: EventEnvelope): void {
    // Rate limit BEFORE doing any reactive work so a flood can't
    // trigger O(N) slice/splice churn on Vue's reactivity. (#1606)
    const now = Date.now();
    const bucket = Math.floor(now / 1000);
    if (bucket !== currentBucket) {
      currentBucket = bucket;
      eventsThisSecond = 0;
    }
    if (eventsThisSecond >= MAX_EVENTS_PER_SECOND_PER_STREAM) {
      droppedEventCount.value += 1;
      if (now - lastWarnAt >= RATE_LIMIT_WARN_INTERVAL_MS) {
        lastWarnAt = now;
        // Single throttled warn — we don't want a sustained flood to
        // also flood the console. Operators can read the precise
        // dropped count from `droppedEventCount` on the returned ref.
        // eslint-disable-next-line no-console
        console.warn(
          `[useEventStream] event rate cap hit (${MAX_EVENTS_PER_SECOND_PER_STREAM}/s); dropping events. total dropped=${droppedEventCount.value}`,
        );
      }
      return;
    }
    eventsThisSecond += 1;

    const next = events.value.slice();
    next.push(envelope);
    if (next.length > maxEvents) {
      next.splice(0, next.length - maxEvents);
    }
    events.value = next;
  }

  function handleMessage(msg: ParsedMessage): void {
    if (msg.id) {
      // Track last-seen id for `Last-Event-ID` on reconnect, even for
      // malformed payloads — the harness ring cares about the id, not
      // the body.
      lastEventId.value = msg.id;
    }
    if (msg.retryMs !== null) {
      serverRetryHintMs = msg.retryMs;
    }
    if (!msg.data) return;
    let parsed: EventEnvelope | null = null;
    try {
      parsed = JSON.parse(msg.data) as EventEnvelope;
    } catch {
      // Malformed JSON — drop the event but keep the stream alive. We
      // bump `parseFailureCount` so operators can observe the rate of
      // bad payloads without us tearing the connection down. (#1634)
      parseFailureCount.value += 1;
      return;
    }
    if (!parsed || typeof parsed !== "object") return;

    if (parsed.type === "stream.overrun") {
      // The harness closed our queue. The overrun payload carries a
      // `resume_id` pointing past the lost window; using it as the new
      // `Last-Event-ID` avoids the harness immediately re-emitting a
      // stream.gap on reconnect.
      const payload = (parsed.payload ?? {}) as { resume_id?: unknown };
      const resume = typeof payload.resume_id === "string" ? payload.resume_id : "";
      if (resume) {
        lastEventId.value = resume;
      }
      // Surface the overrun envelope itself into the events ring BEFORE
      // early-returning. Downstream listeners (notably
      // `useAlerts.handleStreamMarker`) key off the envelope to raise the
      // "stream caught up after reconnect" banner — swallowing the event
      // silently meant operators had no signal a gap had occurred.
      // (#1237)
      pushEvent(parsed);
      // Fall through and let the stream-end path schedule a reconnect.
      return;
    }

    if (parsed.type === "stream.gap") {
      pushEvent(makeSyntheticGapEvent(parsed));
      return;
    }

    pushEvent(parsed);
  }

  async function runOnce(gen: number): Promise<void> {
    if (gen !== currentGen) return;
    aborter = new AbortController();
    const headers: Record<string, string> = {
      Accept: "text/event-stream",
      "Cache-Control": "no-cache",
    };
    if (options.token) {
      headers.Authorization = `Bearer ${options.token}`;
    }
    if (lastEventId.value) {
      headers["Last-Event-ID"] = lastEventId.value;
    }

    let resp: Response;
    try {
      resp = await fetchImpl(url, {
        method: "GET",
        headers,
        signal: aborter.signal,
        // Tell browsers to keep the connection open; without this, some
        // HTTP/2 proxies will treat the request as idle and reset it.
        cache: "no-store",
      });
    } catch (e) {
      if (gen !== currentGen) return;
      if ((e as { name?: string }).name === "AbortError") {
        connected.value = false;
        return;
      }
      throw e;
    }

    if (gen !== currentGen) return;

    if (!resp.ok) {
      // Non-2xx — surface the status and let the reconnect ladder
      // decide how aggressively to retry.
      throw new Error(`stream HTTP ${resp.status}`);
    }

    const body = resp.body;
    if (!body) {
      throw new Error("stream body missing");
    }

    connected.value = true;
    reconnecting.value = false;
    error.value = "";
    attempt = 0;
    // Successful connect — reset the consecutive-failure terminal-state
    // counter so a transient outage doesn't permanently kill the
    // stream after a long uptime accrues failures. (#1615)
    consecutiveFailures = 0;

    const reader = body.getReader();
    const decoder = new TextDecoder("utf-8");
    let buffer = "";
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (gen !== currentGen) return;
        if (done) {
          // Mark the live-pill down immediately on clean close so the
          // UI doesn't show "connected" during the reconnect window.
          // (#1154)
          connected.value = false;
          break;
        }
        buffer += decoder.decode(value, { stream: true });
        const { messages, remainder } = __parseSSEChunk(buffer);
        buffer = remainder;
        for (const msg of messages) {
          handleMessage(msg);
        }
      }
    } finally {
      try {
        reader.releaseLock();
      } catch {
        // reader may already be released if the body errored.
      }
    }
  }

  function scheduleReconnect(gen: number): void {
    if (closed) return;
    if (gen !== currentGen) return;
    reconnecting.value = true;
    connected.value = false;

    // Prefer an explicit server `retry:` hint when present; otherwise
    // do exponential backoff with full jitter bounded by maxDelayMs.
    // Full jitter: ceiling = min(maxDelayMs, initialDelayMs * 2**attempt);
    // delay = random * ceiling. No additive floor. (#1152)
    // Terminal-failure gate (#1615): after MAX_CONSECUTIVE_FAILURES
    // consecutive failed reconnects, stop scheduling. The UI sees the
    // terminal state via the `error` ref already set in the catch path.
    consecutiveFailures += 1;
    if (consecutiveFailures > MAX_CONSECUTIVE_FAILURES) {
      reconnecting.value = false;
      if (!error.value) {
        error.value = `stream-failed: gave up after ${MAX_CONSECUTIVE_FAILURES} consecutive reconnect failures`;
      }
      return;
    }

    let delay: number;
    if (serverRetryHintMs !== null) {
      // Validate server `retry:` hint against MIN_TRUSTED_SERVER_RETRY_MS
      // (#1615). Below that threshold we treat the hint as suspect and
      // clamp it up to MIN_BACKOFF_MS — a buggy or malicious backend
      // sending `retry: 0` should not amplify into a reconnect storm.
      if (serverRetryHintMs < MIN_TRUSTED_SERVER_RETRY_MS) {
        const now = Date.now();
        if (now - lastRetryClampWarnAt > RATE_LIMIT_WARN_INTERVAL_MS) {
          lastRetryClampWarnAt = now;
          console.warn(
            `[useEventStream] server retry hint ${serverRetryHintMs}ms below trusted floor ${MIN_TRUSTED_SERVER_RETRY_MS}ms; clamping to ${MIN_BACKOFF_MS}ms`,
          );
        }
        delay = MIN_BACKOFF_MS;
      } else {
        delay = serverRetryHintMs;
      }
      serverRetryHintMs = null;
    } else {
      const ceiling = Math.min(
        maxDelayMs,
        initialDelayMs * Math.pow(2, Math.max(0, attempt)),
      );
      delay = Math.floor(Math.random() * ceiling);
    }
    // Apply MIN_BACKOFF_MS floor across BOTH branches (#1531, #1615).
    // Server-issued `retry:` hints below MIN_TRUSTED_SERVER_RETRY_MS are
    // already clamped above; this floor catches the jitter-branch case.
    delay = Math.max(MIN_BACKOFF_MS, delay);
    attempt += 1;

    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      if (gen !== currentGen) return;
      void loop(gen);
    }, delay);
  }

  async function loop(gen: number): Promise<void> {
    if (closed) return;
    if (gen !== currentGen) return;
    try {
      await runOnce(gen);
      if (gen !== currentGen) return;
      // Stream ended cleanly (server closed) — treat as a reconnect.
      if (!closed) {
        scheduleReconnect(gen);
      }
    } catch (e) {
      if (gen !== currentGen) return;
      if ((e as { name?: string }).name === "AbortError") {
        connected.value = false;
        return;
      }
      error.value = e instanceof Error ? e.message : String(e);
      scheduleReconnect(gen);
    }
  }

  function open(): void {
    if (closed) {
      // Reopen from a closed composable — reset the sentinel.
      closed = false;
    }
    if (aborter || reconnectTimer) return; // already running
    // Bump generation so any stale in-flight callback from a prior
    // open() session short-circuits. (#1153)
    currentGen += 1;
    const gen = currentGen;
    void loop(gen);
  }

  // neverOpen gates the autoConnect microtask so a synchronous close()
  // before the microtask runs prevents the connection from ever opening
  // (#1541). Distinct from `closed`: an explicit open() resets `closed`
  // to false (legitimate reopen from a stopped composable), but once
  // neverOpen is true the autoConnect shim stays suppressed — callers
  // that want to reopen still call open() explicitly.
  let neverOpen = false;

  function close(): void {
    closed = true;
    neverOpen = true;
    // Bump generation so any in-flight awaiters (fetch, reader.read,
    // pending reconnect timer) no-op when they wake. (#1153)
    currentGen += 1;
    connected.value = false;
    reconnecting.value = false;
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (aborter) {
      try {
        aborter.abort();
      } catch {
        // ignore
      }
      aborter = null;
    }
  }

  if (options.autoConnect !== false) {
    // Defer to microtask so callers can wire up watchers before the
    // first chunk lands. Unit tests that pass `autoConnect: false` get
    // deterministic control over when the fetch fires.
    queueMicrotask(() => {
      if (neverOpen || closed) return;
      open();
    });
  }

  // Best-effort auto-teardown — only register when we're inside a
  // component setup(). Outside a component (e.g. a long-lived Pinia
  // store, or a unit test calling directly), the store / caller is
  // responsible for calling `close()` explicitly.
  if (getCurrentInstance()) {
    onBeforeUnmount(() => close());
  }

  return {
    events,
    connected,
    reconnecting,
    error,
    lastEventId,
    droppedEventCount,
    parseFailureCount,
    open,
    close,
  };
}
