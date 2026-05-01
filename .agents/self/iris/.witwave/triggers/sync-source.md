---
name: sync-source
description: >-
  On-demand source sync — fire post-deploy or whenever the tree
  needs a forced refresh. POST /triggers/sync-source returns 202
  immediately and runs git-sync-source asynchronously.
endpoint: sync-source
enabled: true
---

Run the `git-sync-source` skill against the primary repo declared in
your CLAUDE.md. The skill is idempotent — first invocation clones,
subsequent invocations fast-forward.

Surface any errors or surprising diffs in your response so harness
logs capture them. If the skill reports `--ff-only` rejection (local
diverged from remote), do not force-resolve — surface the divergence
and stop.
