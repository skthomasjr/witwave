"""Derive the backend-internal session identifier from caller-supplied input.

Defense-in-depth for #710 / #733: a caller-supplied ``session_id`` acts as a
bearer secret — anyone who obtains one can resume that session's
conversation state on subsequent A2A calls. The historical derivation
(:func:`uuid.uuid5` with :data:`uuid.NAMESPACE_URL` and the raw id) is
deterministic across callers, so a second principal who observes or
guesses another's raw id can address the same persisted session.

This module introduces an **opt-in** binding that combines the caller's
identity with the raw id before hashing. When ``SESSION_ID_SECRET`` is
set on the backend and the request carries a caller identity (passed in
by the harness via ``metadata.caller_id`` or derived from the bearer
token on caller-authenticated endpoints like ``/mcp``), the backend
stores state under an HMAC-derived, per-caller session id so two
callers presenting the same raw id end up in disjoint sessions.

Behaviour:

* ``SESSION_ID_SECRET`` unset → identical to the legacy ``uuid5``
  derivation. Backward compatible.
* ``SESSION_ID_SECRET`` set + ``caller_identity`` supplied → derive
  ``uuid5(NAMESPACE_URL, HMAC-SHA256(secret, caller || "\\0" || raw))``.
* ``SESSION_ID_SECRET`` set + ``caller_identity`` missing → fall back to
  the legacy derivation and emit a **one-shot WARNING** so operators
  notice their multi-tenant config isn't fully wired.

The same ``caller_identity`` + ``raw_sid`` pair yields the same
derived session id, so session resumption works as long as the caller
consistently provides the same identity.

Set the env var on each backend container in multi-tenant deployments.
The harness is expected to stamp a ``caller_id`` field in A2A
``metadata`` that hashes the upstream principal (e.g. a bearer-token
fingerprint) before forwarding, or backends can derive it themselves
when they terminate auth directly (e.g. on the ``/mcp`` endpoint).
"""

from __future__ import annotations

import hashlib
import hmac
import logging
import os
import threading
import uuid

logger = logging.getLogger(__name__)

_ENV_VAR = "SESSION_ID_SECRET"

# One-shot warning guard: when the secret is set but caller_identity is
# missing on the very first request, we log once per process and then
# stay quiet. Logging once per request would drown the signal under
# normal traffic.
_missing_caller_warned = False
_warn_lock = threading.Lock()


def _legacy_derive(raw_sid: str) -> str:
    """Current/pre-#710 derivation: deterministic uuid5 over the raw id."""
    try:
        uuid.UUID(raw_sid)
        return raw_sid
    except ValueError:
        return str(uuid.uuid5(uuid.NAMESPACE_URL, raw_sid))


def _hash_caller(caller_identity: str) -> str:
    """Hash caller identity with sha256 so we don't HMAC over raw bearer
    tokens. Keeps log traces and span attributes free of the token plain
    text if caller_identity happens to be the token itself.
    """
    return hashlib.sha256(caller_identity.encode("utf-8")).hexdigest()


def derive_session_id(
    raw_sid: str,
    caller_identity: str | None = None,
    *,
    secret: str | None = None,
) -> str:
    """Resolve the backend-internal session_id for a given raw input.

    :param raw_sid: the caller-supplied session identifier (already
        sanitised: control chars stripped, trimmed, length-capped).
        Empty / falsy values fall back to a fresh uuid4 — the historical
        "no resumption key" behaviour.
    :param caller_identity: opaque string identifying the calling
        principal. Supplied by the harness via ``metadata.caller_id`` or
        derived from a bearer-token fingerprint on caller-authenticated
        endpoints. May be ``None``.
    :param secret: HMAC key. ``None`` (the default) reads
        ``os.environ[SESSION_ID_SECRET]``. Passed explicitly by tests so
        the function stays stateless.

    Derivation rules (see module docstring for rationale):

    * No raw id and no prior state → fresh random uuid4.
    * No secret set → legacy uuid5 derivation.
    * Secret set + caller provided → HMAC-bound derivation.
    * Secret set + no caller → one-shot warning + legacy derivation.
    """
    if not raw_sid:
        return str(uuid.uuid4())

    if secret is None:
        secret = os.environ.get(_ENV_VAR, "")

    if not secret:
        return _legacy_derive(raw_sid)

    if not caller_identity:
        global _missing_caller_warned
        with _warn_lock:
            if not _missing_caller_warned:
                _missing_caller_warned = True
                logger.warning(
                    "SESSION_ID_SECRET is set but no caller_identity is available "
                    "on this request — session_id binding cannot be applied and "
                    "the legacy (uuid5) derivation is in use. Ensure the harness "
                    "stamps metadata.caller_id for multi-tenant deployments. "
                    "This warning is logged once per process."
                )
        return _legacy_derive(raw_sid)

    caller_hash = _hash_caller(caller_identity)
    mac = hmac.new(
        secret.encode("utf-8"),
        msg=f"{caller_hash}\x00{raw_sid}".encode("utf-8"),
        digestmod=hashlib.sha256,
    ).digest()
    # Fold HMAC bytes back into a uuid so downstream code (session file
    # paths, SQLiteSession row keys, LRU dict) sees the same shape it
    # always has. uuid5 over an hmac-derived stable input gives us a
    # deterministic, per-caller, collision-resistant session id.
    return str(uuid.uuid5(uuid.NAMESPACE_URL, mac.hex()))
