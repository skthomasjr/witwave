---
description: Liveness check — verifies the agent's backend can reach its LLM provider and that the system-prompt file is loaded.
schedule: "*/30 * * * *"
enabled: true
---

Respond with exactly: HEARTBEAT_OK <your name>

Substitute `<your name>` with the name you've been told to identify
as in your system instructions. If you don't know your name, respond
with HEARTBEAT_DEGRADED followed by one short sentence explaining
why. Keep the response under 20 tokens.
