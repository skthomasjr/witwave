---
name: sync-source
description: Clone or update the witwave repo on iris's shared source volume. Run before any task that reads from or commits to the source tree, and any time the local checkout might be stale. Trigger when the user says "sync source", "pull latest", "refresh the repo", or before starting any code-modification task.
version: 0.1.0
---

# sync-source

Bring `/workspaces/witwave-self/source/witwave` to a known-current state
matching the GitHub `main` branch. Idempotent: works whether the directory
is empty (first run) or already a checkout (later runs).

## Instructions

The shared source volume mounts at `/workspaces/witwave-self/source/`.
The witwave repo's working copy lives at
`/workspaces/witwave-self/source/witwave/`.

Auth comes from the container's environment — `$GITHUB_USER` and
`$GITHUB_TOKEN` are wired by the bootstrap. Don't echo them; pass via
the URL or a credential helper.

### First run (clone)

When `/workspaces/witwave-self/source/witwave/.git` doesn't exist, clone:

```sh
git clone "https://${GITHUB_USER}:${GITHUB_TOKEN}@github.com/witwave-ai/witwave.git" \
  /workspaces/witwave-self/source/witwave
```

After a fresh clone, invoke the **git-identity** skill to pin local
`user.name` / `user.email` on this checkout. Don't inline that here —
the policy lives there so a single edit propagates if iris's identity
ever changes.

### Subsequent runs (refresh)

When the checkout already exists, fetch + fast-forward only:

```sh
cd /workspaces/witwave-self/source/witwave
git fetch origin
git pull --ff-only origin main
```

`--ff-only` is intentional. It refuses to merge if `main` has diverged
from your local — which only happens if you have unpushed local commits
or someone force-pushed upstream. Either way, **stop and ask the user
what they want to do** rather than silently merging or force-resetting.

## Failure handling

- **Auth failure** (401, 403): surface the error verbatim and stop.
  Don't retry without instruction.
- **`pull --ff-only` rejected**: report local vs. remote HEAD and ask
  the user. Never `git reset --hard` or `git pull --no-ff` on your own.
- **Network error**: retry once after a short pause; on second failure
  surface and stop.

## Out of scope for this skill

- Committing or pushing changes (use a separate workflow / skill for
  that — this skill is read-only-against-remote)
- Branch operations beyond `main`
- Cleaning up untracked files (don't `git clean`; surface and ask)
