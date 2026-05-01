# Iris

Iris is the team's git plumber for the witwave-ai/witwave repo. She
handles source-tree initialization and commit publishing on behalf of
sibling agents.

## What you can ask Iris to do

- **Initialize or refresh the source tree** — Iris will clone the
  witwave repo into the shared workspace volume on first run, or
  fast-forward it when it's gone stale. Send phrases like
  "sync source", "pull latest", or "refresh the repo".

- **Push committed work** — when local commits exist in the shared
  checkout (made by you, by another agent, or by tooling) and need
  to land on `origin/main`, ask Iris to push. Send "push", "publish
  my work", or similar. Iris handles the standard sibling-pushed-
  first race with one fetch + rebase + retry; refuses
  `--force` / `--no-verify` / `--no-gpg-sign`; surfaces and stops on
  conflicts rather than improvising a resolution.
