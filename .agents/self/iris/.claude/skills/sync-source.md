---
name: sync-source
description: Clone or update your primary repo onto its workspace source volume. Run before any task that reads from or commits to the source tree, and any time the local checkout might be stale. Trigger when the user says "sync source", "pull latest", "refresh the repo", or before starting any code-modification task.
version: 0.2.0
---

# sync-source

Bring your primary repo's local checkout to a known-current state
matching its default branch. Idempotent: works whether the directory
is empty (first run) or already a checkout (later runs).

The repo URL, the local checkout path, and the default branch all
come from your **Primary repository** section in CLAUDE.md. This
skill describes the procedure; the values are agent-specific. Same
file works for iris/nova/kira because each agent's CLAUDE.md owns
its own primary-repo facts.

## Instructions

Read the following from CLAUDE.md's Primary repository section:

- **`<repo-url>`** — the HTTPS clone URL
- **`<checkout>`** — the local working-tree path (the volume mount;
  `.git/` lives directly under this, no nested subdirectory)
- **`<branch>`** — the default branch (typically `main`)

Auth comes from the container env — `$GITHUB_USER` and `$GITHUB_TOKEN`
are wired by the bootstrap. Don't echo them; pass via the URL.

Substitute the literal values from CLAUDE.md into the commands below
when running them.

### First run (clone)

When `<checkout>/.git` doesn't exist, clone **into the directory**.
The mount point already exists from PVC provisioning, so use the
`cd && clone .` form rather than a target-path argument that would
error on the existing directory:

```sh
cd <checkout>
git clone "https://${GITHUB_USER}:${GITHUB_TOKEN}@<repo-url-without-https-prefix>" .
```

(If `<repo-url>` from CLAUDE.md is `https://github.com/witwave-ai/witwave`,
the substitution becomes
`https://${GITHUB_USER}:${GITHUB_TOKEN}@github.com/witwave-ai/witwave.git`.
Append `.git` if the URL doesn't already end in it.)

If the directory has stray contents (e.g., `lost+found` from
filesystem provisioning) and `git clone .` refuses, list them first
and ask the user before deleting — never `rm -rf` an unfamiliar
directory unprompted.

After a fresh clone, invoke the **git-identity** skill to pin local
`user.name` / `user.email` on this checkout.

### Subsequent runs (refresh)

When the checkout already exists, fetch + fast-forward only:

```sh
cd <checkout>
git fetch origin
git pull --ff-only origin <branch>
```

`--ff-only` is intentional. It refuses to merge if the branch has
diverged from your local — which only happens if you have unpushed
local commits or someone force-pushed upstream. Either way, **stop
and ask the user what they want to do** rather than silently merging
or force-resetting.

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
- Branch operations beyond the default branch
- Cleaning up untracked files (don't `git clean`; surface and ask)
- Switching to a different repo (update CLAUDE.md's Primary repository
  section first; this skill follows whatever's declared there)
