"""Outbound webhook subscription definitions — one Markdown file per subscription under WEBHOOKS_DIR."""

import logging
import os
from dataclasses import dataclass

from utils import parse_frontmatter

logger = logging.getLogger(__name__)

WEBHOOKS_DIR = os.environ.get("WEBHOOKS_DIR", "/home/agent/.nyx/webhooks")

_NOTIFY_WHEN_VALUES = {"always", "on_success", "on_error"}
_NOTIFY_WHEN_DEFAULT = "on_success"


@dataclass
class WebhookSubscription:
    path: str
    name: str
    url: str
    enabled: bool = True
    signing_secret: str | None = None
    notify_when: str = _NOTIFY_WHEN_DEFAULT
    description: str | None = None


def parse_webhook_file(path: str) -> WebhookSubscription | None:
    try:
        with open(path) as f:
            raw = f.read()

        fields, _ = parse_frontmatter(raw)

        enabled = True
        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() not in ("false", "")
        if not enabled:
            logger.info(f"Webhook file {path}: disabled, skipping.")
            return None

        url = fields.get("url") or None
        if not url:
            url_env_var = fields.get("url-env-var") or None
            if url_env_var:
                url = os.environ.get(url_env_var) or None
                if not url:
                    logger.warning(f"Webhook file {path}: env var {url_env_var!r} is unset or empty, skipping.")
                    return None
            else:
                logger.warning(f"Webhook file {path}: missing 'url' or 'url-env-var' in frontmatter, skipping.")
                return None

        if not (url.startswith("https://") or url.startswith("http://")):
            logger.warning(f"Webhook file {path}: invalid URL {url!r} (must start with http:// or https://), skipping.")
            return None

        notify_when_raw = fields.get("notify-when") or None
        if notify_when_raw and notify_when_raw not in _NOTIFY_WHEN_VALUES:
            logger.warning(
                f"Webhook file {path}: invalid notify-when {notify_when_raw!r} "
                f"(must be one of {sorted(_NOTIFY_WHEN_VALUES)}), using default {_NOTIFY_WHEN_DEFAULT!r}."
            )
            notify_when = _NOTIFY_WHEN_DEFAULT
        else:
            notify_when = notify_when_raw or _NOTIFY_WHEN_DEFAULT

        name = fields.get("name") or os.path.splitext(os.path.basename(path))[0]
        signing_secret_env_var = fields.get("signing-secret-env-var") or None
        signing_secret = os.environ.get(signing_secret_env_var) if signing_secret_env_var else None
        description = fields.get("description") or None

        return WebhookSubscription(
            path=path,
            name=name,
            url=url,
            enabled=enabled,
            signing_secret=signing_secret,
            notify_when=notify_when,
            description=description,
        )

    except Exception as e:
        logger.error(f"Webhook file {path}: failed to parse — {e}, skipping.")
        return None
