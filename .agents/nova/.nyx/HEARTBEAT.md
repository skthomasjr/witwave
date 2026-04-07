---
description:
  Periodic check for urgent items, stale tasks, and team flags. Runs silently unless something needs attention.
schedule: "*/30 * * * *"
enabled: true
---

- Check shared memory for urgent flags or messages from teammates
- Flag anything that appears stale or overdue
- If nothing needs attention, append a single line to `~/.claude/memory/shared/heartbeat-test.md` with the current
  timestamp, your agent name, and the text "heartbeat OK", then respond HEARTBEAT_OK
