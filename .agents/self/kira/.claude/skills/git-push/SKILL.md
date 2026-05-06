---
name: git-push
description:
  Push already-made local commits to the remote default branch. Idempotent (no-op when nothing to push), refuses
  force/no-verify flags, handles the standard sibling-pushed-first race via pull --rebase + retry. Trigger when the user
  says "push", "push the commits", "publish my work", or after any local commit that's ready to share.
version: 0.1.0
---

# git-push

Publish local commits that are already in the local checkout's history to the remote default branch. **This skill does
not stage, commit, or write commit messages** — it assumes the commits already exist (made elsewhere, by you in a prior
step, by a tool, or hand-rolled). Its only job is the safe push.

The checkout path and default branch come from your **Primary repository** section in CLAUDE.md.

## Instructions

Read the following from CLAUDE.md's Primary repository section:

- **`<checkout>`** — the local working-tree path
- **`<branch>`** — the default branch (the remote tracking ref is `origin/<branch>`)

### 1. Sanity check — anything to push?

```sh
git -C <checkout> rev-list --count <branch>..origin/<branch>
git -C <checkout> rev-list --count origin/<branch>..<branch>
```

The first count is "how many commits on the remote you don't have locally" — usually 0 unless you're behind. The second
count is "how many local commits aren't yet on the remote." If the second count is 0, **stop and report "nothing to
push"** — the skill is a no-op.

### 2. Refuse footguns

Before any push, verify the user/caller has **not** asked for any of: `--force`, `-f`, `--force-with-lease`,
`--no-verify`, `--no-gpg-sign`. These are explicitly disallowed. If a caller's prompt includes any of those flags,
surface the rule and refuse — don't run the command. The no-flags rule is non-negotiable; ask the user before granting
an exception, never improvise one.

### 3. Push

```sh
git -C <checkout> push origin <branch>
```

If push succeeds: report success with the commit range that landed
(`git log origin/<branch>@{1}..origin/<branch> --oneline` to list the just-pushed commits).

### 4. Handle the rejection-and-retry race

If push is rejected with `! [rejected] (non-fast-forward)` — a sibling agent (or a human collaborator) pushed to
`<branch>` between your last sync and your push — fetch + rebase + retry once:

```sh
git -C <checkout> fetch origin
git -C <checkout> rebase origin/<branch>
git -C <checkout> push origin <branch>
```

If the rebase is clean and the second push succeeds: report success. If either step fails — rebase has conflicts, or the
second push is also rejected — **stop and surface the state** to the user. Do NOT:

- Retry a third time (a steady stream of rejections means something more interesting than a one-off race; surface and
  ask)
- `git rebase --abort` and try a different strategy
- Force-push to "win" the race
- Reset local HEAD to remote and lose your local commits

### 5. Final verification

```sh
git -C <checkout> log origin/<branch>..<branch>
```

This should print nothing — meaning every local commit is now on the remote. If anything appears, surface the unpushed
commits and stop; something went wrong silently.

## Failure modes worth surfacing explicitly

- **Auth failure (401/403)**: the remote rejected the credentials. Surface the error verbatim and stop. The credentials
  live in the container env (`$GITHUB_USER` / `$GITHUB_TOKEN`); they may have rotated, expired, or been wired wrong.
- **Network error**: retry once after a short pause. On second failure surface and stop.
- **Branch protection rules**: GitHub may reject pushes that don't meet branch protection (required reviewers, status
  checks, etc.). Surface the GitHub error message verbatim — those rules are operator/repo policy, not something the
  skill can or should bypass.

## Out of scope for this skill

- Staging files (`git add`)
- Writing commit messages
- Making commits (`git commit`)
- Resolving merge conflicts during rebase
- Force-push of any flavor
- Pushing to branches other than `<branch>` from CLAUDE.md
- Tagging or releasing
