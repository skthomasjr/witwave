"""Shared log-append utility used by all executor modules.

Provides a single implementation of _append_log with fcntl-based locking
and rotation so that bug fixes and enhancements are applied once rather
than across four separate copies.
"""
import fcntl
import logging
import os
import threading

logger = logging.getLogger(__name__)

MAX_LOG_BYTES = int(os.environ.get("MAX_LOG_BYTES", str(10 * 1024 * 1024)))
MAX_LOG_BACKUP_COUNT = int(os.environ.get("MAX_LOG_BACKUP_COUNT", "1"))

# Track writability state per log dir (#738, re-arm in #1035). -1 means
# the dir passed the probe. Any non-negative value is appends-since-warn.
_WRITABILITY_STATE: dict[str, int] = {}
_WRITABILITY_LOCK = threading.Lock()
_WRITABILITY_REARM_EVERY = int(os.environ.get("LOG_WRITABILITY_REARM_EVERY", "500"))

# Optional Prometheus counter surface. Callers set this to a Counter
# with .inc(); we bump on every re-warn.
writability_failed_total = None  # type: ignore[assignment]


def _check_writability(log_dir: str) -> None:
    """Probe log dir writability with periodic re-arm (#738/#1035).

    Re-emits the WARNING every ``LOG_WRITABILITY_REARM_EVERY`` subsequent
    appends after a failed probe so sustained readonly-mount outages
    stay visible.
    """
    with _WRITABILITY_LOCK:
        state = _WRITABILITY_STATE.get(log_dir)
        if state == -1:
            return
        if state is None:
            try:
                probe = os.path.join(log_dir, ".writability-probe")
                with open(probe, "a"):
                    pass
                os.unlink(probe)
            except OSError as exc:
                _WRITABILITY_STATE[log_dir] = 0
                logger.error(
                    "log_utils: directory %r is not writable (%s); log appends will "
                    "silently fail until the mount is fixed. Re-warning every %d "
                    "subsequent appends.",
                    log_dir, exc, _WRITABILITY_REARM_EVERY,
                )
                if writability_failed_total is not None:
                    try:
                        writability_failed_total.inc()
                    except Exception:
                        pass
                return
            _WRITABILITY_STATE[log_dir] = -1
            return
        state += 1
        if state >= _WRITABILITY_REARM_EVERY:
            try:
                probe = os.path.join(log_dir, ".writability-probe")
                with open(probe, "a"):
                    pass
                os.unlink(probe)
                _WRITABILITY_STATE[log_dir] = -1
                logger.info(
                    "log_utils: directory %r recovered and is now writable.",
                    log_dir,
                )
                return
            except OSError as exc:
                _WRITABILITY_STATE[log_dir] = 0
                logger.error(
                    "log_utils: directory %r is STILL not writable (%s); "
                    "%d silent append attempt(s) since the last warning.",
                    log_dir, exc, state,
                )
                if writability_failed_total is not None:
                    try:
                        writability_failed_total.inc()
                    except Exception:
                        pass
                return
        _WRITABILITY_STATE[log_dir] = state


def _append_log(path: str, line: str) -> None:
    """Append a single line to a log file using fcntl locking for multi-process safety.

    After writing, rotates the file if it exceeds MAX_LOG_BYTES.  Keeps up to
    MAX_LOG_BACKUP_COUNT numbered backups (<path>.1, <path>.2, …).

    A separate lock file (<path>.lock) is used so that the exclusive lock is
    held on a stable inode that is never renamed during rotation.  All writers
    must acquire this lock before opening <path>, ensuring that post-rotation
    opens are serialized and never race with a concurrent write.
    """
    log_dir = os.path.dirname(path)
    if log_dir:
        os.makedirs(log_dir, exist_ok=True)
        _check_writability(log_dir)
    lock_path = path + ".lock"
    with open(lock_path, "a") as lock_f:
        fcntl.flock(lock_f, fcntl.LOCK_EX)
        try:
            with open(path, "a", encoding="utf-8") as f:
                f.write(line + "\n")
                f.flush()
            if MAX_LOG_BACKUP_COUNT > 0 and os.path.getsize(path) >= MAX_LOG_BYTES:
                # Rotate: <path>.N → <path>.N+1, …, <path> → <path>.1
                for i in range(MAX_LOG_BACKUP_COUNT, 0, -1):
                    src = f"{path}.{i - 1}" if i > 1 else path
                    dst = f"{path}.{i}"
                    if os.path.exists(src):
                        if i == MAX_LOG_BACKUP_COUNT and os.path.exists(dst):
                            os.remove(dst)
                        os.rename(src, dst)
                logger.debug("Rotated log file %s", path)
        finally:
            fcntl.flock(lock_f, fcntl.LOCK_UN)
