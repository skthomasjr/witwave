"""Producer-side contract test for the /heartbeat HTTP handler shape.

Pins the response shape that ``harness/main.py:heartbeat_handler``
returns so it stays in lockstep with the ww CLI parser (the consumer).
Companion to ``clients/ww/cmd/snapshot_test.go``
TestParseSnapshotSingle_FlatHeartbeat{Enabled,Disabled}.

Background — the 2026-05-16 06:30Z bug-work run:

The harness ``/heartbeat`` endpoint has returned a flat JSON object
since 31c798ed (April 2026) — never wrapped in an envelope.  The ww
CLI's snapshot parser, authored later in 34653b49, was written
against an envelope contract (``{"heartbeat": {...}}``) that the
handler never delivered.  ``ww heartbeat`` fell over with
"unexpected snapshot shape" once an operator (Mira) first invoked it
in earnest at 05:10Z / 05:14Z / 05:17Z.  Fix landed on the CLI side
(parseSnapshotSingle); this test pins the producer side so a future
restructure of heartbeat_handler can't accidentally re-introduce the
drift.

The test is source-inspection only — same pattern as
``test_backends_a2a_readerror_retry.py`` and other
configuration-drift regression suites in this directory.  We don't
spin up the Starlette app (heartbeat_handler is a closure inside
``async def main()``); we assert on the literal response keys built
inline in main.py.

Run with::

    PYTHONPATH=harness:shared pytest harness/test_heartbeat_handler_shape.py
"""

from __future__ import annotations

import re
from pathlib import Path

HARNESS = Path(__file__).parent
MAIN_PATH = HARNESS / "main.py"

# The exact set of top-level keys the CLI's parseSnapshotSingle
# decoder + view-render path expects from /heartbeat.  Adding a key
# is forward-compatible (the parser preserves unknown keys via the
# snapshotEntry map-typed alias), but dropping or renaming any of
# these breaks the CLI's "no heartbeat configured" detection and/or
# its KV view output.
REQUIRED_KEYS = frozenset(
    {
        "enabled",
        "schedule",
        "model",
        "backend_id",
        "consensus",
        "max_tokens",
        "next_fire",
        "last_fire",
        "last_success",
    }
)


def _extract_handler_source() -> str:
    """Return the source of the ``heartbeat_handler`` closure.

    The handler lives inside ``async def main()`` so it isn't
    importable as a top-level symbol — we slice it out by name and
    walk forward until the next sibling definition.
    """
    source = MAIN_PATH.read_text()
    start_re = re.compile(r"^    async def heartbeat_handler\b", re.MULTILINE)
    match = start_re.search(source)
    assert match, "could not locate heartbeat_handler in harness/main.py"
    tail = source[match.start() :]
    # Stop at the next sibling `async def` / `def` at the same 4-space
    # indent level.  Conservative — if no sibling exists we just keep
    # the whole tail.
    end_re = re.compile(r"^    (?:async def|def) \w+", re.MULTILINE)
    sibling = end_re.search(tail, pos=1)  # skip the handler's own def line
    return tail[: sibling.start()] if sibling else tail


def test_heartbeat_handler_returns_flat_object_not_envelope() -> None:
    """The handler must call ``JSONResponse({...})`` directly — not
    wrap the payload in ``{"heartbeat": {...}}`` or ``{"items": [...]}``.

    A future refactor that introduces an envelope would silently
    break the ww CLI consumer (and any dashboard / monitoring tool
    that's been written against the flat shape since April 2026).
    """
    body = _extract_handler_source()
    # The handler should NOT contain an envelope key wrapping the
    # payload.  These are the four envelope shapes the CLI's
    # parseSnapshot recognises for the other snapshot endpoints —
    # if any of them appear at the top of the handler's response
    # the CLI parser would suddenly start unwrapping it differently.
    for envelope in ('"heartbeat":', '"items":', '"jobs":', '"tasks":'):
        # Allow the envelope-key string inside comments / docstrings
        # by requiring it to appear inside a JSONResponse(...) call.
        bad = re.compile(
            r"JSONResponse\s*\(\s*\{[^}]*"
            + re.escape(envelope),
            re.DOTALL,
        )
        assert not bad.search(body), (
            f"heartbeat_handler wraps its response in {envelope!r} envelope; "
            f"the ww CLI parser (clients/ww/cmd/snapshot.go:parseSnapshotSingle) "
            f"expects a flat object. If this is intentional, update the CLI "
            f"parser and the sibling Go test in lockstep."
        )


def test_heartbeat_handler_carries_required_keys() -> None:
    """Every key the CLI consumes must appear inline in the handler
    response.  Catches a future refactor that drops a key (e.g.
    a structural rename of ``backend_id`` to ``agent`` would break
    ``ww heartbeat``'s "BACKEND" column).
    """
    body = _extract_handler_source()
    missing = sorted(k for k in REQUIRED_KEYS if f'"{k}"' not in body)
    # next_fire / last_fire / last_success arrive via ``**_fire_snap``
    # (the heartbeat.snapshot() dict-splat), not as inline string
    # literals — assert the splat is present and that snapshot()
    # itself returns those three keys.
    splatted = {"next_fire", "last_fire", "last_success"}
    still_missing = [k for k in missing if k not in splatted]
    assert "**_fire_snap" in body, (
        "heartbeat_handler no longer splats _fire_snap into its response; "
        "the next_fire / last_fire / last_success bookkeeping (#1087) is now "
        "missing from the /heartbeat payload — the ww CLI's `LAST FIRE` "
        "column will render as `-` for every state."
    )
    assert not still_missing, (
        f"heartbeat_handler response is missing required keys {still_missing}; "
        f"the ww CLI parser (clients/ww/cmd/snapshot.go) and view renderer "
        f"expect all of {sorted(REQUIRED_KEYS)} on every response."
    )


def test_snapshot_function_returns_fire_bookkeeping_keys() -> None:
    """The ``snapshot()`` helper in heartbeat.py must return the
    next_fire / last_fire / last_success trio the handler splats into
    its response.  Pins the contract that ``_fire_snap`` actually
    delivers what the CLI's view layer prints (#1087).

    Source-inspection rather than ``import heartbeat`` — keeps this
    suite runnable in environments that don't have the full harness
    dep stack (croniter, watchfiles, etc.) installed, matching the
    rest of the file's style.
    """
    hb_source = (HARNESS / "heartbeat.py").read_text()
    snap_re = re.compile(
        r"def\s+snapshot\s*\([^)]*\)\s*->\s*[^:]+:.*?return\s*\{(.*?)\}",
        re.DOTALL,
    )
    match = snap_re.search(hb_source)
    assert match, "could not locate snapshot() definition in harness/heartbeat.py"
    snap_body = match.group(1)
    for k in ("next_fire", "last_fire", "last_success"):
        assert f'"{k}"' in snap_body, (
            f"heartbeat.snapshot() no longer returns key {k!r}; drift here "
            f"would silently strip that field from the /heartbeat HTTP "
            f"response (handler builds it via **_fire_snap) and the ww CLI "
            f"`LAST FIRE` column would render as `-` for every state."
        )
    # And nothing else — the handler also relies on this trio being
    # the COMPLETE set of splatted keys (not a superset that overlaps
    # the inline enabled/schedule/etc.).  Count the keys via the
    # presence of "next_" / "last_" prefixes — anything else is a
    # red flag worth flagging in a future bug-work pass.
    extra_keys = re.findall(r'"([a-z_]+)"\s*:', snap_body)
    assert set(extra_keys) == {"next_fire", "last_fire", "last_success"}, (
        f"heartbeat.snapshot() returns unexpected keys {sorted(extra_keys)}; "
        f"expected exactly next_fire / last_fire / last_success. New keys "
        f"would land in /heartbeat via **_fire_snap and the ww CLI parser "
        f"would surface them — coordinate with clients/ww/cmd/snapshot.go."
    )


def test_disabled_branch_keeps_same_keys() -> None:
    """The not-loaded branch (HEARTBEAT.md missing or disabled) must
    return the SAME flat shape as the enabled branch — populated with
    null/false defaults rather than an empty body or a 404.  The CLI
    distinguishes "no heartbeat configured" via ``enabled: false``,
    not via response truthiness.
    """
    body = _extract_handler_source()
    # The handler has two JSONResponse({...}) calls: one for the
    # not-loaded branch (returns enabled=False) and one for the loaded
    # branch (returns enabled=True).  Both must have all 6 inline
    # keys + the **_fire_snap splat.
    not_loaded = re.search(
        r"if not loaded:.*?JSONResponse\s*\(\s*\{(.*?)\}",
        body,
        re.DOTALL,
    )
    assert not_loaded, "could not locate the `if not loaded:` JSONResponse block"
    not_loaded_block = not_loaded.group(1)
    assert '"enabled": False' in not_loaded_block, (
        "not-loaded branch must return enabled=False; the CLI's "
        "heartbeatEnabled() helper relies on this sentinel to print "
        "\"no heartbeat configured\" rather than a half-rendered view."
    )
    for k in ("schedule", "model", "backend_id", "consensus", "max_tokens"):
        assert f'"{k}"' in not_loaded_block, (
            f"not-loaded branch is missing inline key {k!r}; the CLI "
            f"view renderer expects every key present on every response "
            f"so it can render a uniform table."
        )
    assert "**_fire_snap" in not_loaded_block, (
        "not-loaded branch must splat _fire_snap so next_fire / last_fire / "
        "last_success appear (as null) even when no heartbeat is configured — "
        "dashboards graph the absence."
    )


if __name__ == "__main__":  # pragma: no cover
    import sys

    import pytest

    sys.exit(pytest.main([__file__, "-v"]))
