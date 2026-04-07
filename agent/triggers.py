"""Inbound trigger definitions — one Markdown file per trigger under TRIGGERS_DIR."""

import logging
import os
import re
import uuid
from dataclasses import dataclass

from utils import parse_frontmatter

logger = logging.getLogger(__name__)

TRIGGERS_DIR = os.environ.get("TRIGGERS_DIR", "/home/agent/.nyx/triggers")
AGENT_NAME = os.environ.get("AGENT_NAME", "nyx-agent")

# Valid endpoint: non-empty, alphanumeric and hyphens only, no leading/trailing hyphens
_SLUG_RE = re.compile(r"^[a-z0-9]([a-z0-9-]*[a-z0-9])?$")


@dataclass
class TriggerItem:
    path: str
    name: str
    endpoint: str
    session_id: str
    content: str
    enabled: bool = True
    secret_env_var: str | None = None
    agenda_item: str | None = None
    model: str | None = None
    description: str | None = None


def parse_trigger_file(path: str) -> TriggerItem | None:
    try:
        with open(path) as f:
            raw = f.read()

        fields, content = parse_frontmatter(raw)

        enabled = True
        if "enabled" in fields:
            enabled = str(fields["enabled"]).lower() != "false"
        if not enabled:
            logger.info(f"Trigger file {path}: disabled, skipping.")
            return None

        endpoint = fields.get("endpoint") or None
        if not endpoint:
            logger.warning(f"Trigger file {path}: missing 'endpoint' in frontmatter, skipping.")
            return None
        if not _SLUG_RE.match(endpoint):
            logger.warning(f"Trigger file {path}: invalid endpoint slug {endpoint!r} (must be lowercase alphanumeric + hyphens), skipping.")
            return None

        name = fields.get("name") or endpoint
        session_str = fields.get("session") or None
        session_id = session_str or str(uuid.uuid5(uuid.NAMESPACE_URL, f"{AGENT_NAME}.trigger.{endpoint}"))
        model = fields.get("model") or None
        secret_env_var = fields.get("secret-env-var") or None
        agenda_item = fields.get("agenda-item") or None
        description = fields.get("description") or None

        return TriggerItem(
            path=path,
            name=name,
            endpoint=endpoint,
            session_id=session_id,
            content=content,
            enabled=enabled,
            secret_env_var=secret_env_var,
            agenda_item=agenda_item,
            model=model,
            description=description,
        )

    except Exception as e:
        logger.error(f"Trigger file {path}: failed to parse — {e}, skipping.")
        return None
