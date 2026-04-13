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


def _append_log(path: str, line: str) -> None:
    """Append a single line to a log file using fcntl locking for multi-process safety.

    After writing, rotates the file if it exceeds MAX_LOG_BYTES.  Keeps up to
    MAX_LOG_BACKUP_COUNT numbered backups (<path>.1, <path>.2, …).
    """
    log_dir = os.path.dirname(path)
    if log_dir:
        os.makedirs(log_dir, exist_ok=True)
    with open(path, "a", encoding="utf-8") as f:
        fcntl.flock(f, fcntl.LOCK_EX)
        try:
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
            fcntl.flock(f, fcntl.LOCK_UN)
