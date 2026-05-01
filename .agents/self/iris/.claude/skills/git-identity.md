---
name: git-identity
description: Set the local git commit identity (user.name / user.email) for a checkout iris is about to commit from. Run once after a fresh clone, or any time `git log` shows commits attributed to the wrong author. Trigger when the user says "set git identity", "fix commit attribution", or before iris's first commit on a new checkout.
version: 0.1.0
---

# git-identity

Pin iris's git author identity on a local checkout so commits land with
a stable, recognisable name + email instead of falling through to the
PAT owner's default identity (which is usually whatever GitHub user
owns the token, not iris).

## Instructions

The values are operator-policy. Iris's identity is:

- `user.name`  = `iris`
- `user.email` = `iris@witwave.ai`

(If you ever need different values for a one-off, ask the user first —
don't guess.)

### Set identity on a checkout

Run from inside the checkout's working tree:

```sh
cd /workspaces/witwave-self/source/witwave
git config user.name  "iris"
git config user.email "iris@witwave.ai"
```

Local config (no `--global`) — confines the identity to this checkout
so a future agent or operator sharing the volume isn't surprised by
config bleed.

### Verify after setting

```sh
cd /workspaces/witwave-self/source/witwave
git config --get user.name
git config --get user.email
```

The two `git config --get` calls should print `iris` and
`iris@witwave.ai` exactly. Anything else means a previous setting
wasn't overwritten — re-run the set commands.

## When to invoke

- **After `sync-source` runs the first clone** on an empty volume.
  The clone itself doesn't carry identity; the very next commit
  would otherwise fall through to global config (usually empty in
  the container) and fail with `Please tell me who you are`.
- **Before iris's first commit on a checkout that's been there for
  a while** — verify the identity is still set; the local `.git/config`
  could have been wiped by a `--bare` reinit, a workspace volume
  reformat, or operator surgery.

## Out of scope

- Setting `--global` git config (don't — pollutes every other checkout
  on the volume)
- Setting GPG signing keys (separate skill if/when we adopt signed commits)
- Changing the identity for a single commit (use `git commit --author`
  for that — outside this skill)
