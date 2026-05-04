# Bootstrap

This repo is maintained by witwave agents running on a witwave cluster. This
document is the meta-loop: it walks through using the `ww` CLI plus a local
`.env` file to stand up the WitwaveWorkspace and WitwaveAgents that manage and
maintain *this* repo.

## Goal

A "witwave-self" ecosystem is one named WitwaveWorkspace plus one or more
WitwaveAgents, all bound to it, that share a working copy of this repo and
collaborate on maintaining it. Concretely, after this doc is fully implemented
the cluster is running:

- One **WitwaveWorkspace** (`witwave-self`) with one or more shared
  volumes (`source` for the working repo state, `memory` for long-term
  per-agent memory, more as concerns accrete) that every participating
  agent mounts at the same paths.
- Two **WitwaveAgent**s (`iris` and `kira`) with `Spec.WorkspaceRefs`
  pointing at `witwave-self` so they share the workspace. Iris owns
  source-tree initialization + release captaincy; kira owns
  documentation hygiene. Additional named agents (e.g. `nova`) can
  be added later in the same shape.
- A **gitSync** sidecar that keeps the shared volume in lockstep with this
  GitHub repo.

The doc is intentionally incremental — each section is a copy-pasteable
command. Sections are added as the bootstrap surface grows; if a step isn't
listed here yet, it isn't part of the bootstrap yet.

## Targets

This doc currently targets **Docker Desktop** (single-node local
Kubernetes).

Conventions used by every command in this doc:

- All commands are written with **long-form flags** (e.g. `--namespace`,
  not `-n`) so the intent is readable without consulting the CLI's flag
  table.
- Multi-flag commands are split across lines with `\` continuations,
  one flag per line, so each option is reviewable on its own.

## Prerequisites

### Cluster + tools

- A Kubernetes cluster reachable via your current kubeconfig context.
  Enable Kubernetes in Docker Desktop settings.
- The `ww` CLI installed (via the universal installer or Homebrew tap):

  ```bash
  curl -fsSL https://github.com/witwave-ai/witwave/releases/latest/download/install.sh | sh
  ```

  or:

  ```bash
  brew install witwave-ai/homebrew-ww/ww
  ```

### Environment

A local `.env` at the repo root holding the secrets the bootstrap consumes.
`.env` is gitignored — never commit it. Source it into your shell before
running any of the commands below. Every subsequent step assumes these
variables are present in the environment:

```bash
set --allexport
source .env
set +o allexport
```

Variables this walkthrough expects in `.env`:

```bash
# Claude OAuth token (the iris backend's LLM credential)
CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-replace_me

# Per-agent GitHub credentials — used for in-container git operations
# (commit/push/PRs) by each agent's claude backend. Agent-suffixed so
# every agent on the team carries its own credentials without collision.
# Pattern: each agent has a dedicated GitHub account named
# <name>-agent-witwave with a verified email <name>-agent@witwave.ai;
# the account is a collaborator on the primary repo with the access
# level appropriate to the agent's responsibilities.
GITHUB_TOKEN_IRIS=github_pat_replace_me
GITHUB_USER_IRIS=iris-agent-witwave

GITHUB_TOKEN_KIRA=github_pat_replace_me
GITHUB_USER_KIRA=kira-agent-witwave

# Shared gitSync credentials — used by every agent's gitSync sidecar
# to clone the config repo. Not agent-suffixed because the sidecar
# typically pulls from one shared config repo; one PAT serves the
# whole team. Override per-agent only when an agent points at a
# different private repo.
GITSYNC_USERNAME=iris-agent-witwave
GITSYNC_PASSWORD=github_pat_replace_me
```

The Step 3 / Step 4 commands lift these into each agent's containers
at the in-container env-var names each consumer expects:
`CLAUDE_CODE_OAUTH_TOKEN` lands on the claude container as-is;
`GITHUB_TOKEN_<NAME>` and `GITHUB_USER_<NAME>` are renamed to
`GITHUB_TOKEN` / `GITHUB_USER` inside that agent's claude container;
and `GITSYNC_USERNAME` / `GITSYNC_PASSWORD` are minted into a per-agent
`<name>-gitsync` Secret (keys `GITSYNC_USERNAME` / `GITSYNC_PASSWORD`)
and `envFrom`-wired to the gitSync sidecar.

For this bootstrap the repo (`witwave-ai/witwave`) is public and the
sidecar would clone anonymously without any creds — the
`--gitsync-secret-from-env` wiring is shown so the pattern carries over
verbatim when iris later points at a private config repo.

### Storage

Docker Desktop ships a single `hostpath` storage class which is
`ReadWriteOnce`. The workspace declares its volumes with `:rwo` so the
operator provisions hostpath PVCs and every agent pod (all on the only
node) mounts them directly. The shared volume lives on Docker Desktop's
underlying Linux VM filesystem.

## Step 1 — Install the witwave-operator

The operator reconciles all three CRDs (`WitwaveAgent`, `WitwavePrompt`,
`WitwaveWorkspace`) and is the prerequisite for every subsequent step. The
`ww` CLI ships the operator's Helm chart embedded so this single command is
all that's needed — no `helm repo add` and no Helm chart on disk:

```bash
ww operator install \
  --namespace witwave-system \
  --create-namespace
```

`--create-namespace` provisions `witwave-system` if it doesn't already
exist. Without the flag, `ww operator install` refuses on a missing
namespace so a typo in `--namespace` can't silently create a junk
namespace — same posture as `ww workspace create` and `ww agent create`.

Verify the operator pod is `Running` and the CRDs are registered:

```bash
ww operator status \
  --namespace witwave-system
```

The status output reports the Helm release, the operator deployment, and the
three CRDs. Investigate any non-`Ready` condition before continuing — the
remaining steps depend on the operator being healthy enough to serve
admission webhooks.

## Step 2 — Create the WitwaveWorkspace

The WitwaveWorkspace is the shared envelope every agent that maintains this
repo will bind to. It declares two shared volumes — `source` (the working
repo state) and `memory` (long-term per-agent memory) — each projected
onto every binding agent at `/workspaces/witwave-self/<volume-name>`.
Secret references and ConfigMap-backed files get added in later steps as
agents need them.

```bash
ww workspace create witwave-self \
  --namespace witwave-self \
  --create-namespace \
  --volume source=20Gi:rwo \
  --volume memory=1Gi:rwo
```

The `--volume` flag's form is `<name>=<size>[@<storageClass>][:<mode>]`.
Omitting `@<storageClass>` lets the cluster's default storage class win —
on Docker Desktop that's the bundled `hostpath` class. The `:rwo` suffix
asks for `ReadWriteOnce`, which is what `hostpath` supports.

Verify:

```bash
ww workspace status witwave-self \
  --namespace witwave-self
```

The status output should show the workspace `Ready` with both volumes
provisioned. PVC names follow the pattern `<workspace>-vol-<volume>` —
`witwave-self-vol-source` and `witwave-self-vol-memory` here.

## Step 3 — Deploy iris

Iris is a WitwaveAgent with one `claude` backend, bound to the
`witwave-self` workspace, with its identity (prompts, hooks, Claude
Code config) sourced from `.agents/self/iris/` in this repo, and
per-backend persistent storage for session + memory state. One
command:

```bash
ww agent create iris \
  --namespace witwave-self \
  --workspace witwave-self \
  --with-persistence \
  --backend claude \
  --harness-env TASK_TIMEOUT_SECONDS=2700 \
  --backend-env claude:TASK_TIMEOUT_SECONDS=2700 \
  --backend-secret-from-env claude=CLAUDE_CODE_OAUTH_TOKEN \
  --backend-secret-from-env claude=GITHUB_TOKEN_IRIS:GITHUB_TOKEN \
  --backend-secret-from-env claude=GITHUB_USER_IRIS:GITHUB_USER \
  --gitsync-bundle https://github.com/witwave-ai/witwave.git@main:.agents/self/iris \
  --gitsync-secret-from-env GITSYNC_USERNAME:GITSYNC_PASSWORD
```

`TASK_TIMEOUT_SECONDS=2700` is set on **both** the harness and
the claude backend so the timeout headroom (45 minutes vs the
5-minute default) applies end-to-end. The harness uses it to
size the A2A relay's read timeout (`_HTTP_TIMEOUT_SECONDS =
TASK_TIMEOUT_SECONDS - 10` in `harness/backends/a2a.py`); the
backend uses it as the per-task LLM-call timeout. Mismatch
between them causes confusing failures — the harness gives up
mid-call and retries, leaving the backend running and producing
duplicate work. The headroom matters when iris watches release
workflows (the container-build job alone can take ~25 minutes)
or delegates a long-running task to a sibling.

Verify iris is `Ready` and bound to the workspace:

```bash
ww agent list \
  --namespace witwave-self
```

```bash
ww workspace status witwave-self \
  --namespace witwave-self
```

`ww agent list` should show one row (`iris`) in state `Ready`.
`ww workspace status` should report `iris` under the bound-agents
section. The pod has the workspace's `source` and `memory` volumes
mounted at `/workspaces/witwave-self/source` and
`/workspaces/witwave-self/memory` on every backend container.

## Step 4 — Deploy kira

Kira is the team's documentation-hygiene agent — periodic scans
for typos, dead links, stale paths, markdown formatting drift, and
other mechanical doc issues. Same shape as iris (one `claude`
backend, bound to `witwave-self`, identity sourced from
`.agents/self/kira/`); she pushes her own commits via her
`git-push` skill rather than handing off to iris. One command:

```bash
ww agent create kira \
  --namespace witwave-self \
  --workspace witwave-self \
  --with-persistence \
  --backend claude \
  --harness-env TASK_TIMEOUT_SECONDS=2700 \
  --backend-env claude:TASK_TIMEOUT_SECONDS=2700 \
  --backend-secret-from-env claude=CLAUDE_CODE_OAUTH_TOKEN \
  --backend-secret-from-env claude=GITHUB_TOKEN_KIRA:GITHUB_TOKEN \
  --backend-secret-from-env claude=GITHUB_USER_KIRA:GITHUB_USER \
  --gitsync-bundle https://github.com/witwave-ai/witwave.git@main:.agents/self/kira \
  --gitsync-secret-from-env GITSYNC_USERNAME:GITSYNC_PASSWORD
```

Same paired-timeout lift as iris (harness + backend both set to
2700s). Kira's full docs scans walk every markdown file in the
repo and on first run also npx-download prettier and
markdownlint-cli — that combination eats through the default
5-minute timeout fast on a cold container. 45 minutes is generous
headroom; dial it back later if scans typically finish well under
the ceiling.

Verify both agents are now bound to the workspace:

```bash
ww agent list \
  --namespace witwave-self
```

`ww agent list` should now show two rows (`iris`, `kira`) both in
state `Ready`. `ww workspace status witwave-self` should report
both under the bound-agents section.

## Tear it down

Reverse order: agent → workspace → operator → namespaces. Each
command is destructive and cascade-deletes everything it owns.

Delete each agent (cascades the pod, Service, per-backend PVC
`<name>-claude-data`, and the ww-managed `<name>-claude` Secret;
`--delete-git-secret` also reaps the per-agent `<name>-gitsync`
Secret minted by `--gitsync-secret-from-env`):

```bash
ww agent delete kira \
  --namespace witwave-self \
  --delete-git-secret \
  --yes
```

```bash
ww agent delete iris \
  --namespace witwave-self \
  --delete-git-secret \
  --yes
```

Delete the workspace (removes both `source` and `memory` PVCs —
data is gone unless the volume's reclaim policy is `Retain`):

```bash
ww workspace delete witwave-self \
  --namespace witwave-self \
  --yes
```

Uninstall the operator and drop the CRDs:

```bash
ww operator uninstall \
  --namespace witwave-system \
  --delete-crds \
  --yes
```

Drop the namespaces:

```bash
kubectl delete namespace witwave-self witwave-system
```
