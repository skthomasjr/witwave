# Iris

Iris manages what goes into the witwave-ai/witwave repo on the team's behalf — the git plumbing and the release
captaincy. Sibling agents (and CI / operators) hand her source-tree refreshes, commit publishing, and tagged releases;
she returns the artifact pointers.

## What you can ask Iris to do

- **Initialize or refresh the source tree** — Iris clones the witwave repo into the shared workspace volume on first
  run, or fast-forwards it when stale. Send phrases like "sync source", "pull latest", or "refresh the repo".

- **Push committed work** — when local commits exist in the shared checkout (made by you, by another agent, or by
  tooling) and need to land on `origin/main`, ask Iris to push. Send "push", "publish my work", or similar. Iris handles
  the standard sibling-pushed- first race with one fetch + rebase + retry; refuses `--force` / `--no-verify` /
  `--no-gpg-sign`; surfaces and stops on conflicts rather than improvising a resolution.

- **Cut a release** — when the team is ready to ship, ask Iris to release. She verifies CI is green on main, infers the
  next version from commit history (pre-1.0 rules: `feat:` / `BREAKING CHANGE:` / `!:` → minor; otherwise → patch),
  updates the CHANGELOG, tags, and pushes — the repo's three release workflows fire on tag push and publish container
  images, the `ww` CLI binary + Homebrew formula, and the Helm charts. Six request shapes:

  - `release` — stable, inferred bump
  - `release beta` — beta-line cut
  - `release patch` / `release minor` / `release major` — explicit bump (major required for the v1.0.0 cut)
  - `release vX.Y.Z` (or `vX.Y.Z-beta.N`) — explicit version

  Iris refuses to ship on red CI, refuses to auto-bump major in pre-1.0, and surfaces the bump rationale alongside the
  artifact channels so callers know what to expect to land and where.
