---
description: >-
  Runs Mira's lightweight platform-health check. The check is read-only: observe operator status, agent readiness,
  release status, recent pod restarts, PVC/runtime-storage posture, and resource/anomaly signals. Each run records a
  compact snapshot; once enough snapshots exist, Mira compares them for systemic patterns before escalating. Problematic
  findings are distilled and sent to Zora for routing. Hourly cadence keeps the reliability loop warm without turning
  operations monitoring into a major token sink.
schedule: "17 * * * *"
enabled: true
---

Run your `platform-health` skill. This is one lightweight platform observation tick plus historical comparison when
enough snapshots exist:

1. Check operator status and version alignment.
2. Check self-team agent readiness and recent activity.
3. Check recent release/CI state.
4. Look for pod restarts, degraded CRs, missing runtime storage, PVC pressure, release drift, or other anomalies.
5. Record a compact JSONL snapshot in your memory namespace.
6. If at least three snapshots exist, or the snapshot history spans at least 24 hours, compare history for repeated or
   worsening patterns.
7. Prioritize restart deltas first: if any pod/container restarted since the previous snapshot, inspect its Kubernetes
   status, events, termination reason, and previous/current logs before reporting.
8. If a finding looks systemic, repeated, problematic, or likely to need a fix, distill it and send it to Zora for
   routing. Do not leave fix-needed findings only in snapshot history.
9. Return a concise status: Green / Yellow / Red, what changed, whether history changed the assessment, and whether a
   Zora handoff was sent.

Do not mutate cluster state from the heartbeat. No restarts, patches, upgrades, rollbacks, tag pushes, or release reruns
unless a human explicitly approves that action in the triggering request. Your normal heartbeat action is observation +
Zora handoff, not repair.
