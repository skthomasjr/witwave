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

- One **WitwaveWorkspace** (`witwave-self`) with a shared RWM volume that
  every participating agent mounts at the same path.
- One **WitwaveAgent** per named operator-of-record (`iris`, `nova`, `kira`),
  each with `Spec.WorkspaceRefs` pointing at `witwave-self` so they all see
  the same source tree.
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
repo will bind to. It declares one shared RWM volume (`source`) that every
agent mounts at `/workspaces/witwave-self/source`. Secret references and
ConfigMap-backed files get added in later steps as agents need them.

```bash
ww workspace create witwave-self \
  --namespace witwave-self \
  --create-namespace \
  --volume source=20Gi:rwo
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

The status output should show the workspace `Ready` with one provisioned
volume. The PVC name follows the pattern `<workspace>-vol-<volume>`
(`witwave-self-vol-source` here).
