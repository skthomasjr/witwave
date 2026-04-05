---
name: Feature Promotion
description: Watches for approved features in docs/features-proposed.md and promotes them as GitHub Issues.
schedule: "30 */6 * * *"
enabled: true
---

Check for newly approved features in `<repo-root>/docs/features-proposed.md` and promote them into GitHub Issues.

Steps:

1. Read `<repo-root>/docs/features-proposed.md`. Collect all feature proposals whose **Status** is `approved`. If there
   are none, stop — do not proceed.
2. For each approved feature, run `/features promote F-XXX` (substituting the actual feature ID). If the skill reports
   unresolved questions and reverts the feature to `proposed`, note it and continue to the next feature.
3. Do not modify any source files or any file outside of `docs/features-proposed.md`. Do not do anything else.
