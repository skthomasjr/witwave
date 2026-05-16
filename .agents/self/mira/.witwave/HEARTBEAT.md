---
description: >-
  Runs Mira's lightweight platform-health check. The check is read-only: observe operator status, agent readiness,
  release status, recent pod restarts, PVC/runtime-storage posture, and resource/anomaly signals. Problematic findings
  are distilled and sent to Zora for routing. Hourly cadence keeps the reliability loop warm without turning operations
  monitoring into a major token sink.
schedule: "17 * * * *"
enabled: true
---

Run your `platform-health` skill. This is one lightweight platform observation tick:

1. Check operator status and version alignment.
2. Check self-team agent readiness and recent activity.
3. Check recent release/CI state.
4. Look for pod restarts, degraded CRs, missing runtime storage, PVC pressure, release drift, or other anomalies.
5. Record the result in your memory namespace.
6. If a finding looks problematic, distill it and send it to Zora for routing.
7. Return a concise status: Green / Yellow / Red, what changed, and whether a Zora handoff was sent.

Do not mutate cluster state from the heartbeat. No restarts, patches, upgrades, rollbacks, tag pushes, or release reruns
unless a human explicitly approves that action in the triggering request. Your normal heartbeat action is observation +
Zora handoff, not repair.
