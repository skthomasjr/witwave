---
description:
  Periodic check for urgent items, stale tasks, and team flags. Runs silently unless something needs attention.
schedule: "*/30 * * * *"
enabled: true
---

- Check shared memory for urgent flags or messages from teammates
- Flag anything that appears stale or overdue
- If nothing needs attention, respond HEARTBEAT_OK
