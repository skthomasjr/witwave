"""Shared log-append utility used by all executor modules.

Provides a single implementation of _append_log with fcntl-based locking
and rotation so that bug fixes and enhancements are applied once rather
than across four separate copies.
"""
import fcntl
import logging
import os

logger = logging.getLogger(__name__)

MAX_LOG_BYTES = int(os.environ.get("MAX_LOG_BYTES", str(10 * 1024 * 1024)))
MAX_LOG_BACKUP_COUNT = int(os.environ.get("MAX_LOG_BACKUP_COUNT", "1"))

# Track which log directories have already been probed so the per-append
# startup check runs once per process per unique dir (#738). Set to the
# probed path once we've verified it or emitted the warning.
_WRITABILITY_CHECKED: set[str] = set()


def _check_writability(log_dir: str) -> None:
    """Once-per-process probe that logs a loud warning when the mount is
    read-only (#738). Prevents silent per-write spinlock-style failures
    when the log volume is mounted read-only by a misconfigured
    PodSecurityContext or a stuck upstream writer.
    """
    if log_dir in _WRITABILITY_CHECKED:
        return
    _WRITABILITY_CHECKED.add(log_dir)
    try:
        probe = os.path.join(log_dir, ".writability-probe")
        with open(probe, "a"):
            pass
        os.unlink(probe)
    except OSError as exc:
        logger.error(
            "log_utils: directory %r is not writable (%s); log appends will "
            "silently fail until the mount is fixed.",
            log_dir, exc,
        )


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
