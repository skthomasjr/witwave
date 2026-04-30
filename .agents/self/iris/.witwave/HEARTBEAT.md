---
description: Liveness check — verifies iris's claude backend can reach the Anthropic API.
schedule: "* * * * *"
enabled: true
---

Respond with exactly: HEARTBEAT_OK

If you can't (token-budget hit, tool denial, anything else), respond
with HEARTBEAT_DEGRADED followed by one short sentence explaining
why. Keep the response under 20 tokens — this fires every minute.
