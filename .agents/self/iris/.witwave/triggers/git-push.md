---
name: git-push
description: >-
  On-demand push of local commits — fire from a sibling agent
  (delegate-push pattern), CI (post-build attribution), or operator
  (manual publish). POST /triggers/git-push returns 202 immediately
  and runs git-push asynchronously.
endpoint: git-push
enabled: true
---

Run the `git-push` skill against the primary repo declared in your
CLAUDE.md. The skill is push-only — it assumes the commits to publish
already exist in the local checkout's history.

Idempotent (no-op when nothing to push). Refuses force / no-verify
flags as a hard rule. Handles the sibling-pushed-first race with
fetch + rebase + retry once; on a second rejection or rebase conflicts,
stops and surfaces the state rather than retrying indefinitely.

Surface the result of the push (commits landed, no-op, or stopped-at)
in your response so harness logs capture the outcome.
