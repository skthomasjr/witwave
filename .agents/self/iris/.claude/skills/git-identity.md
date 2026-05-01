---
name: git-identity
description: Set the local git commit identity (user.name / user.email) on a checkout this agent is about to commit from. Run once after a fresh clone, or any time `git log` shows commits attributed to the wrong author. Trigger when the user says "set git identity", "fix commit attribution", or before the first commit on a new checkout.
version: 0.4.0
---

# git-identity

Pin your git author identity on a local checkout so commits land
with a stable, recognisable name + email instead of falling through
to the PAT owner's default.

The values come from your **Identity** section in CLAUDE.md (the
agent's system prompt). Same skill works across the self-agent
family because each agent's CLAUDE.md owns its own values; this
file just describes the procedure.

## Instructions

Read `user.name` and `user.email` from CLAUDE.md's Identity section,
then run from inside the checkout's working tree, substituting
those literal values into the commands:

```sh
cd /workspaces/witwave-self/source/witwave
git config user.name  "<your user.name from CLAUDE.md>"
git config user.email "<your user.email from CLAUDE.md>"
```

For example, if your CLAUDE.md says `user.name: iris` and
`user.email: iris@witwave.ai`, run:

```sh
git config user.name  "iris"
git config user.email "iris@witwave.ai"
```

Local config (no `--global`) — confines the identity to this checkout
so a future agent or operator sharing the volume isn't surprised by
config bleed.

### Verify

```sh
cd /workspaces/witwave-self/source/witwave
git config --get user.name
git config --get user.email
```

The two `git config --get` calls should print exactly what your
CLAUDE.md says. Anything else means a previous setting wasn't
overwritten — re-run the set commands.

## When to invoke

- **After `sync-source` runs the first clone** on an empty volume.
  The clone itself doesn't carry identity; the very next commit
  would otherwise fall through to global config (usually empty in
  the container) and fail with `Please tell me who you are`.
- **Before this agent's first commit on a checkout that's been there
  for a while** — verify the identity is still set; the local
  `.git/config` could have been wiped by a `--bare` reinit, a
  workspace volume reformat, or operator surgery.

## Out of scope

- Setting `--global` git config (don't — pollutes every other checkout
  on the volume)
- Setting GPG signing keys (separate skill if/when we adopt signed commits)
- Changing the identity for a single commit (use `git commit --author`
  for that — outside this skill)
- Improvising identity values that aren't in CLAUDE.md (always ask
  the user; the system prompt is the source of truth)
