---
description: >-
  Passive liveness check only. Felix is event-driven, not cadence-driven — she does NOT initiate feature work from this
  heartbeat. Real work fires on direct user A2A, zora dispatch, or piper- routed Discussion request. The 30-min cadence
  is purely "confirm the backend is alive and the system-prompt loaded" — same shape as the team's other event-driven
  liveness pings.
schedule: "*/30 * * * *"
enabled: true
---

Respond with exactly: HEARTBEAT_OK felix

If you don't know your name from your system instructions, respond with HEARTBEAT_DEGRADED followed by one short
sentence explaining why. Keep the response under 20 tokens.

Do NOT initiate feature work from this heartbeat. Feature work is event-driven (A2A from user or zora; Piper-routed
Discussion request). This ping is liveness-only.
