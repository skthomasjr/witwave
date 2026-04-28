# Bootstrap

This repo is maintained by witwave agents running on a witwave cluster. This
document is the meta-loop: it walks through using the `ww` CLI plus a local
`.env` file to stand up the WitwaveWorkspace and WitwaveAgents that manage and
maintain *this* repo.

The doc is intentionally incremental — each section is a copy-pasteable
command. Sections are added as the bootstrap surface grows; if a step isn't
listed here yet, it isn't part of the bootstrap yet.

## Prerequisites

- A Kubernetes cluster reachable via your current kubeconfig context.
- The witwave-operator installed (`ww operator install`).
- A local `.env` at the repo root containing the secrets the bootstrap
  consumes. `.env` is gitignored — never commit it.

Source the `.env` into your shell before running any of the commands below.
Every subsequent step assumes these variables are present in the environment:

```bash
set -a; source .env; set +a
```

## Step 1 — Create the WitwaveWorkspace

The WitwaveWorkspace is the shared envelope every agent that maintains this repo
will bind to. It starts empty; volumes and Secret references get added in
later steps as agents need them.

```bash
ww workspace create witwave-self -n witwave-self --create-namespace
```

Verify:

```bash
ww workspace status witwave-self -n witwave-self
```
