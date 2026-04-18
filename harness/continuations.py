import asyncio
import logging
import os
import time
from dataclasses import asdict, dataclass, field
from fnmatch import fnmatch
from pathlib import Path

from bus import Message, MessageBus
from metrics import (
    harness_continuation_fanin_evictions_total,
    harness_continuation_fires_shed_total,
    harness_continuation_fires_total,
    harness_continuation_items_registered,
    harness_continuation_parse_errors_total,
    harness_continuation_reloads_total,
    harness_continuation_runs_total,
    harness_continuation_throttled_total,
    harness_file_watcher_restarts_total,
    harness_watcher_events_total,
)
from utils import (
    ConsensusEntry,
    parse_consensus,
    parse_duration,
    parse_frontmatter,
    parse_frontmatter_raw,
    run_awatch_loop,
)

logger = logging.getLogger(__name__)

CONTINUATIONS_DIR = os.environ.get("CONTINUATIONS_DIR", "/home/agent/.nyx/continuations")

# Sentinel returned by parse_continuation_file() when the file is explicitly
# disabled (enabled: false).  Distinct from None (parse error) so that
# _register() can unregister a disabled continuation rather than preserving it.
_DISABLED = object()

# Global default cap on concurrent in-flight fires per continuation.
# Overridable per-continuation via the max-concurrent-fires frontmatter field.
CONTINUATION_MAX_CONCURRENT_FIRES = int(os.environ.get("CONTINUATION_MAX_CONCURRENT_FIRES", "5"))
# Global concurrency cap across *all* continuations (#781). Mirrors
# WEBHOOK_MAX_CONCURRENT_DELIVERIES so N continuations sharing an
# upstream can't fan out 5×N in-flight fires and starve the harness
# event loop. Set to 0 to disable (not recommended). Default is 5×the
# per-continuation cap, matching the webhook pattern.
CONTINUATION_MAX_CONCURRENT_FIRES_GLOBAL = int(
    os.environ.get("CONTINUATION_MAX_CONCURRENT_FIRES_GLOBAL",
                   str(CONTINUATION_MAX_CONCURRENT_FIRES * 5))
)

# TTL (seconds) for partial fan-in state entries in `_fanin_state`.  Prevents
# unbounded growth when one of several required upstreams never fires for a
# given session (#557).  Default 1 hour; override via env.
CONTINUATION_FANIN_TTL = float(os.environ.get("CONTINUATION_FANIN_TTL", "3600"))


@dataclass
class ContinuationItem:
    path: str
    name: str
    continues_after: list[str]  # one or more upstream kind patterns (fnmatch); all must complete (fan-in)
    content: str              # prompt body
    on_success: bool = True   # fire on successful upstream completion
    on_error: bool = False    # fire on upstream error
    trigger_when: str | None = None   # only fire if upstream response contains this string
    delay: float | None = None        # seconds to wait before firing
    session_id: str | None = None     # if None, inherit upstream session_id at fire time
    model: str | None = None
    backend_id: str | None = None
    description: str | None = None
    consensus: list[ConsensusEntry] = field(default_factory=list)
    max_tokens: int | None = None
    max_concurrent_fires: int = CONTINUATION_MAX_CONCURRENT_FIRES
    # When False, the continuation is listed in /continuations for
    # dashboard visibility but does not subscribe to upstream events —
    # no fires. Flipping enabled:true re-arms on reload.
    enabled: bool = True


def parse_continuation_file(path: str) -> "ContinuationItem | object | None":
    """Parse a continuation file. Returns:
    - ContinuationItem on success
    - _DISABLED sentinel when enabled: false or continues-after is missing/empty
    - None on parse error (caller should preserve last known good registration)
    """
    try:
        with open(path) as f:
            raw = f.read()

        fields, content = parse_frontmatter(raw)
        raw_fields, _ = parse_frontmatter_raw(raw)

        enabled = True
        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() not in ("false", "")

        continues_after_raw = fields.get("continues-after") or ""
        # Accepts a single string or a YAML list.  A comma-separated string is
        # also accepted as a convenience shorthand (e.g. "job:a, job:b").
        if isinstance(continues_after_raw, list):
            continues_after = [p.strip() for p in continues_after_raw if str(p).strip()]
        else:
            text = str(continues_after_raw).strip()
            if not text:
                continues_after = []
            else:
                continues_after = [p.strip() for p in text.split(",") if p.strip()]
        # Missing continues-after is a hard parse failure for ENABLED
        # continuations (nothing to subscribe to). For disabled ones it
        # just means the display shows "—" — the user is parking the
        # file while figuring out what it should chain off of.
        if not continues_after and enabled:
            logger.warning(f"Continuation file {path}: missing required 'continues-after' field, skipping.")
            return _DISABLED

        filename = Path(path).stem
        name = fields.get("name") or filename

        on_success = True
        if "on-success" in fields:
            on_success = str(fields["on-success"]).lower() not in ("false", "")

        on_error = False
        if "on-error" in fields:
            on_error = str(fields["on-error"]).lower() not in ("false", "")

        trigger_when = fields.get("trigger-when") or None

        delay: float | None = None
        delay_raw = fields.get("delay")
        if delay_raw:
            try:
                delay = parse_duration(str(delay_raw))
            except ValueError as e:
                logger.warning(f"Continuation file {path}: invalid 'delay': {e}, ignoring.")

        session_id = fields.get("session") or None
        model = fields.get("model") or None
        backend_id = fields.get("agent") or None
        description = fields.get("description") or None
        consensus = parse_consensus(raw_fields.get("consensus"))
        max_tokens: int | None = None
        max_tokens_raw = fields.get("max-tokens") or fields.get("max_tokens")
        if max_tokens_raw is not None:
            try:
                max_tokens = max(1, int(max_tokens_raw))
            except (ValueError, TypeError):
                logger.warning(f"Continuation file {path}: invalid 'max-tokens' value {max_tokens_raw!r}, ignoring.")

        max_concurrent_fires = CONTINUATION_MAX_CONCURRENT_FIRES
        max_fires_raw = fields.get("max-concurrent-fires") or fields.get("max_concurrent_fires")
        if max_fires_raw is not None:
            try:
                max_concurrent_fires = max(1, int(max_fires_raw))
            except (ValueError, TypeError):
                logger.warning(
                    f"Continuation file {path}: invalid 'max-concurrent-fires' value {max_fires_raw!r}, "
                    f"using default {CONTINUATION_MAX_CONCURRENT_FIRES}."
                )

        return ContinuationItem(
            path=path,
            name=name,
            continues_after=continues_after,
            content=content,
            on_success=on_success,
            on_error=on_error,
            trigger_when=trigger_when,
            delay=delay,
            session_id=session_id,
            model=model,
            backend_id=backend_id,
            description=description,
            consensus=consensus,
            max_tokens=max_tokens,
            max_concurrent_fires=max_concurrent_fires,
            enabled=enabled,
        )

    except Exception as e:
        if harness_continuation_parse_errors_total is not None:
            harness_continuation_parse_errors_total.inc()
        logger.error(f"Continuation file {path}: failed to parse — {e}, skipping.")
        return None


async def _fire(item: ContinuationItem, session_id: str, bus: MessageBus) -> None:
    if item.delay is not None:
        await asyncio.sleep(item.delay)
    from prompt_env import resolve_prompt_env  # noqa: E402 — scoped import keeps startup simple

    prompt = resolve_prompt_env(f"Continuation: {item.name}\n\n{item.content}")
    resolved_session = item.session_id or session_id
    try:
        response = await bus.send(Message(
            prompt=prompt,
            session_id=resolved_session,
            kind=f"continuation:{item.name}",
            model=item.model,
            backend_id=item.backend_id,
            consensus=item.consensus,
            max_tokens=item.max_tokens,
        ))
        if harness_continuation_runs_total is not None:
            harness_continuation_runs_total.labels(name=item.name, status="success").inc()
        logger.info(f"Continuation '{item.name}' completed successfully. Response: {response!r}")
    except Exception as e:
        if harness_continuation_runs_total is not None:
            harness_continuation_runs_total.labels(name=item.name, status="error").inc()
        logger.error(f"Continuation '{item.name}' error: {e}")


class ContinuationRunner:
    def __init__(self):
        self._items: dict[str, ContinuationItem] = {}
        self._active_fires: set[asyncio.Task] = set()
        # Per-continuation in-flight tasks, keyed by continuation name.
        self._fires_by_name: dict[str, set[asyncio.Task]] = {}
        # Fan-in state: maps (continuation_name, session_id) -> (monotonic_ts, set
        # of upstream kind patterns satisfied so far).  Cleared on each fire;
        # stale partial entries are evicted after CONTINUATION_FANIN_TTL to
        # prevent unbounded growth when a required upstream never completes
        # (#557).
        self._fanin_state: dict[tuple[str, str], tuple[float, set[str]]] = {}

    def _register(self, path: str) -> None:
        result = parse_continuation_file(path)
        if result is _DISABLED:
            # parse returned _DISABLED for "hard unregisterable" reasons
            # (missing continues-after on an enabled continuation). Pull
            # any previous registration.
            self._unregister(path)
            return
        if result is None:
            # Parse error — preserve the last known good registration.
            return
        item = result
        self._unregister(path)
        self._items[path] = item
        # registered-count metric tracks only enabled continuations; the
        # dispatcher filters on enabled so disabled entries never fire.
        if harness_continuation_items_registered is not None:
            harness_continuation_items_registered.set(
                sum(1 for i in self._items.values() if i.enabled)
            )
        if item.enabled:
            logger.info(f"Continuation '{item.name}' registered (continues-after: {item.continues_after}).")
        else:
            logger.info(f"Continuation '{item.name}' disabled — listed but not subscribing.")

    def _unregister(self, path: str) -> None:
        existing = self._items.pop(path, None)
        if existing is not None:
            logger.info(f"Continuation '{existing.name}' unregistered.")
            # Drop any in-progress fan-in state for this continuation.
            stale = [k for k in self._fanin_state if k[0] == existing.name]
            for k in stale:
                del self._fanin_state[k]
        if harness_continuation_items_registered is not None:
            harness_continuation_items_registered.set(len(self._items))

    def _evict_stale_fanin(self, now: float | None = None) -> int:
        """Evict fan-in state entries older than CONTINUATION_FANIN_TTL.

        Returns the number of evicted entries.  Records each eviction on the
        harness_continuation_fanin_evictions_total counter, labelled by
        continuation name.  Called opportunistically from notify() so stale
        partial fan-ins can never accumulate unboundedly (#557).
        """
        if CONTINUATION_FANIN_TTL <= 0:
            return 0
        if now is None:
            now = time.monotonic()
        cutoff = now - CONTINUATION_FANIN_TTL
        stale_keys = [k for k, (ts, _seen) in self._fanin_state.items() if ts < cutoff]
        for key in stale_keys:
            name, _session = key
            del self._fanin_state[key]
            logger.info(
                f"Continuation '{name}': evicted stale fan-in state "
                f"(session={_session}) after {CONTINUATION_FANIN_TTL}s TTL."
            )
            if harness_continuation_fanin_evictions_total is not None:
                harness_continuation_fanin_evictions_total.labels(name=name).inc()
        return len(stale_keys)

    async def _scan(self) -> None:
        if not os.path.isdir(CONTINUATIONS_DIR):
            return
        try:
            filenames = os.listdir(CONTINUATIONS_DIR)
        except OSError:
            return
        for filename in filenames:
            if filename.endswith(".md"):
                self._register(os.path.join(CONTINUATIONS_DIR, filename))

    def items(self) -> list[dict]:
        """Return a serializable snapshot of all continuation items (enabled + disabled)."""
        result = []
        for item in self._items.values():
            # Serialize continues_after: list when >1 entry, single
            # string when 1, None for disabled entries that have no
            # upstream specified (legal only when enabled=False).
            if not item.continues_after:
                ca: str | list[str] | None = None
            elif len(item.continues_after) > 1:
                ca = item.continues_after
            else:
                ca = item.continues_after[0]
            result.append({
                "name": item.name,
                "continues_after": ca,
                "on_success": item.on_success,
                "on_error": item.on_error,
                "trigger_when": item.trigger_when,
                "delay": item.delay,
                "description": item.description,
                "backend_id": item.backend_id,
                "model": item.model,
                "consensus": [asdict(e) for e in item.consensus],
                "max_tokens": item.max_tokens,
                "max_concurrent_fires": item.max_concurrent_fires,
                "active_fires": len(self._fires_by_name.get(item.name, set())),
                "enabled": item.enabled,
            })
        return result

    def notify(self, kind: str, session_id: str, success: bool, response: str, bus: MessageBus) -> None:
        """Called by on_prompt_completed() when an upstream completes. Non-blocking."""
        # Evict any stale partial fan-in state before processing this event so
        # sessions whose required upstream never fires cannot leak memory (#557).
        self._evict_stale_fanin()
        for item in list(self._items.values()):
            # Disabled continuations stay in _items for listing purposes
            # only — never subscribe to upstream events.
            if not item.enabled:
                continue
            outcome_matches = (success and item.on_success) or (not success and item.on_error)
            content_matches = item.trigger_when is None or item.trigger_when in response

            # Find which pattern(s) in continues_after this upstream kind satisfies.
            matched_patterns = [
                p for p in item.continues_after
                if p == "*" or fnmatch(kind, p)
            ]
            if not matched_patterns or not outcome_matches or not content_matches:
                continue

            fanin_key: tuple[str, str] | None = None
            if len(item.continues_after) == 1:
                # Single-upstream fast path — fire immediately (original behaviour).
                ready = True
            else:
                # Fan-in: record which patterns have been satisfied for this session
                # and fire only once all required patterns have been seen.
                key = (item.name, session_id)
                entry = self._fanin_state.get(key)
                if entry is None:
                    seen: set[str] = set()
                else:
                    _ts, seen = entry
                seen.update(matched_patterns)
                # Refresh timestamp on every update so active fan-ins are kept
                # alive and only truly stale partial state ages out (#557).
                self._fanin_state[key] = (time.monotonic(), seen)
                # Check whether every required pattern has been satisfied by at
                # least one upstream completion in this session.
                ready = all(
                    any(p2 == "*" or fnmatch(p2, p) or p == p2 for p2 in seen)
                    for p in item.continues_after
                )
                if ready:
                    # Defer state clear until after the throttle check passes
                    # (#656). Clearing before the throttle dropped accumulated
                    # pattern state for shed fires, leaving the fan-in with no
                    # replay path.
                    fanin_key = key

            if not ready:
                continue

            # Global throttle (#781): shed the fire if the process-wide
            # in-flight continuation count is at or above
            # CONTINUATION_MAX_CONCURRENT_FIRES_GLOBAL. Mirrors the
            # webhook runner's WEBHOOK_MAX_CONCURRENT_DELIVERIES cap so
            # N continuations sharing an upstream can't fan out 5×N
            # in-flight fires and starve the harness event loop.
            if (
                CONTINUATION_MAX_CONCURRENT_FIRES_GLOBAL > 0
                and len(self._active_fires) >= CONTINUATION_MAX_CONCURRENT_FIRES_GLOBAL
            ):
                logger.warning(
                    f"Continuation '{item.name}': global in-flight cap "
                    f"({CONTINUATION_MAX_CONCURRENT_FIRES_GLOBAL}) reached — "
                    f"shedding fire for upstream '{kind}'."
                )
                if harness_continuation_fires_shed_total is not None:
                    try:
                        harness_continuation_fires_shed_total.labels(name=item.name).inc()
                    except Exception:
                        pass
                # Fan-in state intentionally preserved — same as the
                # per-continuation throttle path — so a future upstream
                # event can re-enter once the cap drops.
                continue

            # Throttle: skip this fire if the per-continuation in-flight
            # count already equals max_concurrent_fires.
            fires = self._fires_by_name.setdefault(item.name, set())
            if len(fires) >= item.max_concurrent_fires:
                logger.warning(
                    f"Continuation '{item.name}': max_concurrent_fires ({item.max_concurrent_fires}) "
                    f"reached — skipping fire for upstream '{kind}'."
                )
                if harness_continuation_throttled_total is not None:
                    harness_continuation_throttled_total.labels(name=item.name).inc()
                # Fan-in state intentionally preserved — a subsequent
                # upstream event can re-enter the ready branch once the
                # in-flight count drops below max_concurrent_fires.
                continue

            # Throttle passed — now it's safe to clear fan-in state so the
            # next round of upstream events starts fresh.
            if fanin_key is not None:
                self._fanin_state.pop(fanin_key, None)
            if harness_continuation_fires_total is not None:
                harness_continuation_fires_total.labels(upstream_kind=kind).inc()
            _t = asyncio.ensure_future(_fire(item, session_id, bus))
            self._active_fires.add(_t)
            fires.add(_t)
            def _cleanup(t: asyncio.Task, _name: str = item.name) -> None:
                self._active_fires.discard(t)
                # Pop-when-empty: drop the per-name set once it's drained so
                # entries for unregistered/renamed continuations don't linger
                # across hot reloads (#507).
                _fires = self._fires_by_name.get(_name)
                if _fires is not None:
                    _fires.discard(t)
                    if not _fires:
                        self._fires_by_name.pop(_name, None)
            _t.add_done_callback(_cleanup)

    async def run(self) -> None:
        logger.info(f"Continuation runner watching {CONTINUATIONS_DIR}")

        def _on_change(path: str) -> None:
            logger.info(f"Continuation file changed: {path}")
            if harness_continuation_reloads_total is not None:
                harness_continuation_reloads_total.inc()
            self._register(path)

        def _on_delete(path: str) -> None:
            logger.info(f"Continuation file removed: {path}")
            if harness_continuation_reloads_total is not None:
                harness_continuation_reloads_total.inc()
            self._unregister(path)

        def _cleanup() -> None:
            for path in list(self._items.keys()):
                self._unregister(path)

        await run_awatch_loop(
            directory=CONTINUATIONS_DIR,
            watcher_name="continuations",
            scan=self._scan,
            on_change=_on_change,
            on_delete=_on_delete,
            cleanup=_cleanup,
            logger_=logger,
            not_found_message="Continuations directory not found — retrying in 10s.",
            watcher_exited_message="Continuations directory watcher exited — directory deleted or unavailable. Retrying in 10s.",
            watcher_events_metric=harness_watcher_events_total,
            file_watcher_restarts_metric=harness_file_watcher_restarts_total,
        )
