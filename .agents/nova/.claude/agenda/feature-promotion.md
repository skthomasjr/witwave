---
name: Feature Promotion
description:
  Watches for approved features in docs/features-proposed.md and promotes them as concrete tasks into TODO.md under a
  Enhancements section.
schedule: "30 */6 * * *"
enabled: true
---

Check for newly approved features in `~/workspace/source/autonomous-agent/docs/features-proposed.md` and promote them
into `~/workspace/source/autonomous-agent/TODO.md`.

Steps:

1. Read `~/workspace/source/autonomous-agent/docs/features-proposed.md`. Collect all feature proposals whose **Status**
   is `approved`. If there are none, stop — do not proceed.
2. Run `/todo lock promoting nova` to acquire the lock. If the skill reports the file is locked by another agent, abort
   — do not proceed.
3. For each approved feature, run `/features promote F-XXX` (substituting the actual feature ID). If the skill reports
   unresolved questions and reverts the feature to `proposed`, note it and continue to the next feature.
4. Run `/todo unlock nova` to release the lock.
5. Do not modify any source files or any file outside of `TODO.md` and `docs/features-proposed.md`. Do not do anything
   else.
