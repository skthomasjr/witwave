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
- One **WitwaveAgent** (`iris`) with `Spec.WorkspaceRefs` pointing at
  `witwave-self` so it sees the shared workspace. Additional named
  agents (e.g. `nova`, `kira`) can be added later in the same shape;
  the bootstrap walks through `iris` end-to-end first.
- A **gitSync** sidecar that keeps the shared volume in lockstep with this
  GitHub repo.

The doc is intentionally incremental — each section is a copy-pasteable
command. Sections are added as the bootstrap surface grows; if a step isn't
listed here yet, it isn't part of the bootstrap yet.

## Targets

This doc is currently written against **Docker Desktop** (single-node local
Kubernetes), since that's where the self-ecosystem is being brought up first.
A future cut will target **AWS EKS** for the production deployment. Where a
prerequisite differs between the two, the section calls out both paths
explicitly and the Docker Desktop path is the primary; EKS deltas appear as
sub-bullets.

Conventions used by every command in this doc:

- All commands are written with **long-form flags** (e.g. `--namespace`,
  not `-n`) so the intent is readable without consulting the CLI's flag
  table.
- Multi-flag commands are split across lines with `\` continuations,
  one flag per line, so each option is reviewable on its own.

## Prerequisites

### Cluster + tools

- A Kubernetes cluster reachable via your current kubeconfig context.
  - **Docker Desktop:** enable Kubernetes in Docker Desktop settings.
  - **EKS (future):** a cluster with at least one node group sized for the
    agent pods.
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

### Storage: an access mode the cluster can satisfy

The shared-volume requirement for a WitwaveWorkspace is "every binding
agent pod can mount the same PVC concurrently". On a multi-node cluster
this requires a `ReadWriteMany`-capable storage class. On a single-node
cluster `ReadWriteOnce` is sufficient — Kubernetes' RWO contract is "one
node at a time", and a single-node cluster only has the one node, so any
number of pods on it can share an RWO PVC.

**Docker Desktop (primary):** Docker Desktop ships a single `hostpath`
storage class which is `ReadWriteOnce`. No extra provisioner is needed —
the workspace just declares its volume with `:rwo` so the operator
provisions a hostpath PVC and every agent pod (all on the only node)
mounts it directly. The shared volume lives on Docker Desktop's underlying
Linux VM filesystem.

**EKS (future):** install the [EFS CSI driver][efs-csi] and create a
StorageClass backed by an EFS file system. EFS is natively `ReadWriteMany`
across nodes; the workspace volume declaration in Step 2 then drops the
`:rwo` suffix (RWM is the default) and adds `@<your-efs-class-name>` so the
operator provisions an EFS-backed PVC. Recipe to be added when the EKS
deployment lands.

[efs-csi]: https://github.com/kubernetes-sigs/aws-efs-csi-driver

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
Omitting `@<storageClass>` lets the cluster's default storage class win — on
Docker Desktop that's the bundled `hostpath` class. The `:rwo` suffix asks
for `ReadWriteOnce`, which is what `hostpath` supports and what works for
the single-node case (see the storage prerequisite above for the rationale).

On EKS, drop `:rwo` and add `@<your-efs-class-name>` to point at the EFS
storage class — RWM is the default mode and EFS supports it across nodes.

Verify:

```bash
ww workspace status witwave-self \
  --namespace witwave-self
```

The status output should show the workspace `Ready` with both volumes
provisioned. PVC names follow the pattern `<workspace>-vol-<volume>` —
`witwave-self-vol-source` and `witwave-self-vol-memory` here.

## Step 3 — Deploy iris

Iris is a WitwaveAgent. It lives in the same namespace as the
WitwaveWorkspace it binds to — `v1alpha1` only supports same-namespace
binding, and the `ww` CLI rejects cross-namespace asks loudly so users
see the limitation up-front. Additional agents (`nova`, `kira`, …) get
added later in the same shape; this step walks through `iris`
end-to-end first.

For the initial bootstrap iris runs **two echo backends** (`echo-1` and
`echo-2`) — echo is the zero-dependency stub backend that requires no API
keys and returns a canned response, and the two-backend shape is enough
to exercise the multi-backend wiring (one harness routing to N
backends, per-backend gitOps fan-out) without dragging in any of
`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, or `GOOGLE_API_KEY`. The
`<name>:<type>` form on `--backend` lets two backends share the same
type but stay independently addressable. Real LLM backends get
swapped in in a later step.

Iris's wiring on `ww agent create` covers three deliberately separate
concerns, each its own flag:

- `--workspace witwave-self` binds iris to the **shared workspace** —
  every workspace volume (source, memory, …) is mounted at the same
  path on every bound agent's pods. This is the
  `WitwaveWorkspace.Spec.WorkspaceRefs` channel covered in Step 2.
- `--gitops <url>[@<branch>]:<repo-path>` wires the agent's **own
  identity** — its prompts, HEARTBEAT.md, hooks, Claude/Codex/Gemini
  config, MCP wiring, skills — from a path inside the same repo.
  Per-agent, private to that one agent's pod, never shared with peer
  agents. Auto-populates the CR's `Spec.GitSyncs[]` and per-container
  `GitMappings[]` using a convention: `<repo-path>/.witwave/` lands at
  the harness's `/home/agent/.witwave/`, and `<repo-path>/.<backend>/`
  lands at each backend's `/home/agent/.<backend>/` — once per declared
  `--backend`. For iris with `echo-1` + `echo-2`, that fans out to
  three mappings: one for the harness, one for echo-1
  (`<repo-path>/.echo-1/`), and one for echo-2
  (`<repo-path>/.echo-2/`).
- `--persist <backend-name>=<size>[@<storage-class>]` provisions a
  **per-backend PVC for session + memory state** that survives pod
  reboot. Repeatable (one per backend that needs persistence). Auto-
  populates `Spec.Backends[].Storage` with type-derived default
  mounts: claude → `projects/`, `sessions/`, `backups/`, `memory/`
  under `/home/agent/.claude/`; codex → `memory/`, `sessions/` under
  `/home/agent/.codex/`; gemini → `memory/` under
  `/home/agent/.gemini/`. Echo is intentionally stateless — the
  flag is rejected for it. Distinct from the workspace `memory`
  volume: workspace memory is project-wide cross-agent knowledge;
  `--persist` is per-agent per-backend conversation history and
  SDK session state.

Together they make a single `ww agent create` the complete unit of
deploy: CR admitted, workspace bound, identity wired, persistence
provisioned.

```bash
ww agent create iris \
  --namespace witwave-self \
  --backend echo-1:echo \
  --backend echo-2:echo \
  --workspace witwave-self \
  --gitops https://github.com/witwave-ai/witwave.git@main:.agents/self/iris
```

`--persist` is intentionally absent from this command because both
backends are `echo` — echo doesn't have session state to persist and
the flag rejects with a clear error if you try. When iris swaps in a
real LLM backend in a later step, the command grows a `--persist`
line per stateful backend. For a forward look, the same agent with
one claude backend instead would be:

```bash
ww agent create iris \
  --namespace witwave-self \
  --backend claude \
  --workspace witwave-self \
  --gitops https://github.com/witwave-ai/witwave.git@main:.agents/self/iris \
  --persist claude=10Gi
```

That `--persist claude=10Gi` provisions a 10Gi PVC named
`iris-claude-data` and projects it via subPath into four mounts on
the claude container: `projects/`, `sessions/`, `backups/`,
`memory/` under `/home/agent/.claude/`. Conversation history and
Claude Code session state survive pod restart. Storage class
defaults to the cluster default — append `@<class>` to override
(e.g. `--persist claude=10Gi@gp3`).

### Long-hand equivalent (the explicit form)

`--gitops` is convention-driven sugar over two more general repeatable
flags that map 1:1 to the WitwaveAgent CRD's gitSync surface:

- `--gitsync <name>=<url>[@<branch>]` declares one cloned repo, named
  so mappings can reference it. Populates one `Spec.GitSyncs[]` entry.
- `--gitmap [<container>=]<gitsync-name>:<src>:<dest>` adds one
  GitMappings entry. `<container>` is `harness` (the default —
  populates `Spec.GitMappings[]`) or any backend name from `--backend`
  (populates that backend's `BackendSpec.GitMappings[]`).

Iris's `--gitops` line above is exactly equivalent to:

```bash
ww agent create iris \
  --namespace witwave-self \
  --backend echo-1:echo \
  --backend echo-2:echo \
  --workspace witwave-self \
  --gitsync witwave=https://github.com/witwave-ai/witwave.git@main \
  --gitmap witwave:.agents/self/iris/.witwave/:/home/agent/.witwave/ \
  --gitmap echo-1=witwave:.agents/self/iris/.echo-1/:/home/agent/.echo-1/ \
  --gitmap echo-2=witwave:.agents/self/iris/.echo-2/:/home/agent/.echo-2/
```

The two shapes **compose** — they aren't either/or. Pass `--gitops` for
the 95% case, then drop in extra `--gitmap` flags for paths that don't
follow the convention (e.g. mounting an additional skills repo, or
pointing a backend at a non-default subdirectory). The flag layer
merges everything into one `Spec.GitSyncs[]` and one flat
`Spec.GitMappings[]` set on the CR; duplicate gitSync names or
duplicate (container, dest) pairs are rejected at parse time.

Private-repo support uses `--gitsync-secret <name>=<k8s-secret>`,
which references a pre-created Kubernetes Secret holding the
gitSync credentials (typical keys: `GITSYNC_USERNAME` /
`GITSYNC_PASSWORD`, or `GITSYNC_SSH_KEY_FILE`). Same posture as
`--auth-secret` for backend auth — the CLI never accepts inline
tokens. The bootstrap repo is public, so this isn't needed in
this walkthrough.

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
