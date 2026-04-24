# ww walkthrough — from zero to gitOps-wired multi-backend agent

A step-by-step tour of `ww`. Follow it top to bottom; every command is
copy-pasteable, every section builds on the last. At the end you'll
have a running agent with multiple backends, wired to a git repo, with
all the surfaces for observing and evolving it exercised.

If you already have a working agent and just want to look up a specific
verb, [`README.md`](README.md) is the reference. This file tells the
story; the README lists the flags.

---

## 0. What you need before you start

1. **A Kubernetes cluster you can write to.** Docker Desktop,
   Rancher Desktop, kind, or a remote cluster all work. The `ww`
   CLI detects the cluster type via kubeconfig context names and
   skips destructive prompts on local ones — production-looking
   contexts (EKS, GKE, AKS, anything not on the local-cluster
   allowlist) prompt before mutating.

2. **`ww` on your `PATH`.** If you don't have it yet:

   ```bash
   brew install witwave-ai/homebrew-ww/ww
   # or:
   go install github.com/skthomasjr/witwave/clients/ww@latest
   ```

3. **(Optional) a git repo you can push to.** You'll use this in
   section 5 when we wire gitOps. Your machine's existing git
   credentials (`gh auth login`, git credential helper, or ssh-agent)
   is all `ww` needs — there's no ww-specific credential store.

Verify you're ready:

```bash
ww version                            # should print 0.7.7 or later
kubectl config current-context        # confirm you're on the right cluster
```

---

## 1. Install the operator (one-time per cluster)

The operator reconciles `WitwaveAgent` custom resources into running
pods. Nothing else you do in `ww` works without it.

```bash
ww operator install \
    --if-missing \
    --yes
```

`--if-missing` makes the install idempotent: if the operator is already
installed on this cluster, the command logs a one-liner and exits 0.
Safe to leave in scripts.

What you should see:

```
Target cluster:  docker-desktop  (context: docker-desktop)
Namespace:       witwave-system
Action:          install witwave-operator (embedded chart)
Chart:           witwave-operator 0.7.7 (appVersion 0.7.7)
Installing witwave-operator 0.7.7 into namespace witwave-system …
Installed witwave-operator revision 1 (deployed).
```

Confirm it's healthy:

```bash
ww operator status
```

You should see one operator pod Running + the two CRDs
(`witwaveagents.witwave.ai`, `witwaveprompts.witwave.ai`) reported as
present.

**What just happened:** ww shipped with the operator's Helm chart
embedded via `go:embed`, so no Helm or repo configuration is needed.
The chart installed into the `witwave-system` namespace (the fixed,
operator-scoped namespace per DESIGN.md KC-6) and registered the CRDs
cluster-wide.

---

## 2. Your first agent (hello world)

```bash
ww agent create hello \
    --namespace witwave \
    --create-namespace
```

Expected output:

```
Target cluster:  docker-desktop  (context: docker-desktop)
Namespace:       witwave
Action:          create WitwaveAgent "hello"
Backends:        echo/8001
Harness image:   ghcr.io/skthomasjr/images/harness:0.7.7

Created namespace witwave (labelled app.kubernetes.io/managed-by=ww).
Created WitwaveAgent hello in namespace witwave (uid=...)
Waiting up to 2m0s for agent to report Ready...
  phase: Pending
  phase: Ready

Agent hello is ready.
```

**What just happened:**

- `--namespace witwave` pinned the target namespace explicitly. `witwave`
  is also ww's own fallback when neither `--namespace` nor your kubeconfig
  context pins one — so dropping the flag would land in the same place.
  The explicit form is the habit we recommend: production changes belong
  in a namespace you named on purpose, not one that quietly defaulted.
  When `--namespace` is omitted, ww prints a
  `Using namespace: <ns> (from kubeconfig context)` or
  `Using namespace: <ns> (ww default)` line at the top of every command
  so you can tell an inherited namespace from a quiet fallback.
- `--create-namespace` provisioned the `witwave` namespace on first use,
  carrying the `app.kubernetes.io/managed-by: ww` label so teardown
  tooling can tell ww-created namespaces from hand-authored ones.
  Subsequent runs skip the creation (idempotent).
- The CR declared one backend — the `echo` stub, which needs no API
  keys — on port 8001.
- The operator reconciled the CR into a pod with two containers
  (`harness` on port 8000 + `echo` on port 8001) and flipped the
  agent to Ready within ~15 seconds.

Poke at it — every `ww agent *` verb takes `--namespace`, and every
example in this walkthrough passes it explicitly:

```bash
# Table view with phase + ready count.
ww agent list \
    --namespace witwave

# Curated describe: phase, reconcile history.
ww agent status hello \
    --namespace witwave

# Harness container logs.
ww agent logs hello \
    --namespace witwave \
    --no-follow \
    --tail 20

# Echo sidecar logs.
ww agent logs hello \
    --namespace witwave \
    --container echo \
    --no-follow \
    --tail 5

# CR + pod events.
ww agent events hello \
    --namespace witwave
```

And the headline move — talk to it:

```bash
ww agent send hello "ping from the walkthrough" \
    --namespace witwave
```

Expected response:

```
echo backend — no LLM configured.

You said: ping from the walkthrough

This agent is running the echo backend, which returns canned responses
so you can deploy and exercise an agent without any API keys. To swap
in a real backend (claude, codex, or gemini), see `ww agent backend set --help`.
```

`ww agent send` hits the agent's harness via the Kubernetes apiserver's
built-in Service proxy — no port-forwarding, no external LoadBalancer.
Works against any ClusterIP service.

---

## 3. Multi-backend agents

Echo is fine for hello-world, but the framework's headline feature is
running **multiple backends on one agent** — e.g. claude + codex
reaching consensus, or two echo instances to prove the dispatch path
handles N-way routing before you wire real LLMs.

Two shapes for the repeatable `--backend` flag:

- `--backend <type>` — name = type (shortcut when there's one per type)
- `--backend <name>:<type>` — explicit name + type (needed when the
  same type appears twice)

```bash
# Cleanup from section 2.
ww agent delete hello \
    --namespace witwave \
    --yes

# Create a two-backend agent in the same namespace.
ww agent create consensus \
    --namespace witwave \
    --backend echo-1:echo \
    --backend echo-2:echo
```

Expected:

```
Backends:        echo-1:echo/8001, echo-2:echo/8002
...
Created WitwaveAgent consensus ...
```

Inspect the pod:

```bash
kubectl get pods \
    --namespace witwave \
    --selector app.kubernetes.io/name=consensus \
    --output wide
# NAME                       READY   STATUS
# consensus-xxxxxxxxxx-yyyy  3/3     Running

kubectl get pods \
    --namespace witwave \
    --selector app.kubernetes.io/name=consensus \
    --output jsonpath='{.items[0].spec.containers[*].name}{"\n"}'
# harness echo-1 echo-2
```

Three containers: harness on port 8000, echo-1 on 8001, echo-2 on 8002.
Every backend's name becomes the container name, the folder name in
the gitOps repo, the mount path in the pod, and the routing id in
`backend.yaml`. Ports follow the DESIGN.md PORT-2 convention
(8001–8050).

### Default routing

A2A round-trip still works — every concern (a2a, heartbeat, job, etc.)
routes to the **first** backend by default:

```bash
# Lands on echo-1 (first backend = default for every concern).
ww agent send consensus "who am I talking to" \
    --namespace witwave
```

You'll see echo-1's canned response. To redistribute routing across
backends, you edit `backend.yaml` — which we'll do once gitOps is
wired.

### Multi-model consensus for real

When you have API keys, the shape is the same:

```bash
ww agent create research \
    --namespace witwave \
    --backend claude \
    --backend codex
# (requires ANTHROPIC_API_KEY + OPENAI_API_KEY Secrets per-backend;
#  future `ww agent backend set --api-key-secret ...` verb will make
#  this one command.)
```

---

## 4. gitOps — scaffold a repo, wire it to the agent

Two phases. Scaffold shapes the repo; wire attaches the running agent
to pull from it. They're separate verbs because they have different
failure modes (scaffold: git push issues; wire: CR patch issues) and
different trust boundaries (scaffold uses your laptop's git creds;
wire creates a K8s Secret the operator reads).

### 4a. Scaffold the repo structure

Using your own private or public repo (example assumes you have
`<you>/my-witwave-config` that's empty or you don't mind new files
in). Empty repos are handled — ww bootstraps the initial commit.

```bash
ww agent scaffold consensus \
    --repo <you>/my-witwave-config \
    --backend echo-1:echo \
    --backend echo-2:echo
```

Expected:

```
Action:        scaffold agent "consensus"
Repo:          <you>/my-witwave-config
Branch:        main
Backends:      echo-1:echo/8001, echo-2:echo/8002
Files:         5
  - .agents/consensus/README.md
  - .agents/consensus/.witwave/backend.yaml
  - .agents/consensus/.witwave/HEARTBEAT.md
  - .agents/consensus/.echo-1/agent-card.md
  - .agents/consensus/.echo-2/agent-card.md

Cloning <you>/my-witwave-config …
Added 5 file(s):
  + .agents/consensus/.echo-1/agent-card.md
  + .agents/consensus/.echo-2/agent-card.md
  + .agents/consensus/.witwave/HEARTBEAT.md
  + .agents/consensus/.witwave/backend.yaml
  + .agents/consensus/README.md
Committed abc1234: Scaffold agent consensus
Pushing main to origin …
Pushed main.
```

**What just happened:**

- ww cloned your repo to a temp dir (using your system git
  credentials — env token, `gh auth token`, git credential helper,
  or ssh-agent in that precedence order).
- It wrote a minimum-viable skeleton with one folder per backend,
  a `.witwave/backend.yaml` listing every backend and routing
  everything to the first, and an `HEARTBEAT.md` that fires every
  hour (`schedule: "0 * * * *"`). Dormant subsystems (jobs, tasks,
  triggers, continuations, webhooks) are **not** scaffolded — per
  SUB-1..4, their absence is how an agent expresses "I don't use
  this feature." They're one-file drops away when you want them.
- Committed + pushed with `Scaffolded-by: ww agent scaffold` in the
  commit trailer so future-you can tell scaffolded commits from
  hand-authored ones.

If you re-run the scaffold on an existing directory:

```bash
ww agent scaffold consensus \
    --repo <you>/my-witwave-config \
    --backend echo-1:echo \
    --backend echo-2:echo
```

You'll see it **merge** rather than overwrite — missing files land,
identical files are silent, drifted files are preserved with a "pass
`--force` to overwrite" hint. That makes scaffold safe to re-run as
templates evolve.

### 4b. Wire the running agent to the repo

Now the cluster side. `ww agent git add` patches the CR to add a
gitSync sidecar that pulls from your repo on a timer.

Three ways to supply auth — pick **one** based on how you sign into
GitHub:

```bash
# Option A: local `gh` already authenticated.
ww agent git add consensus \
    --namespace witwave \
    --repo <you>/my-witwave-config \
    --auth-from-gh

# Option B: named env var holds a token (CI / .env workflows).
GITHUB_TOKEN=ghp_... ww agent git add consensus \
    --namespace witwave \
    --repo <you>/my-witwave-config \
    --auth-from-env GITHUB_TOKEN

# Option C: a K8s Secret you already created.
ww agent git add consensus \
    --namespace witwave \
    --repo <you>/my-witwave-config \
    --auth-secret my-github-pat
```

Expected output (Option A):

```
Action:    attach gitSync "my-witwave-config" to WitwaveAgent "consensus" in witwave
  gitSync  "my-witwave-config"  repo=<you>/my-witwave-config  ref=<remote default>  period=60s
    credentials: minted Secret "consensus-git-credentials" from `gh auth token`
  mapping   .agents/consensus/.witwave/ → /home/agent/.witwave/ (harness)
  mapping   .agents/consensus/.echo-1/ → /home/agent/.echo-1/ (backend)
  mapping   .agents/consensus/.echo-2/ → /home/agent/.echo-2/ (backend)

Attached gitSync "my-witwave-config" to WitwaveAgent witwave/consensus.
The operator will reconcile a git-sync sidecar shortly.
```

The operator adds a `git-sync-<sync-name>` sidecar to the pod. After
~60 seconds:

```bash
kubectl get pods \
    --namespace witwave \
    --selector app.kubernetes.io/name=consensus
# consensus-xxxxxxxxxx-zzzz  4/4  Running  (harness + echo-1 + echo-2 + git-sync)

ww agent git list consensus \
    --namespace witwave
```

Confirm the content reached the pod:

```bash
POD=$(kubectl get pods \
    --namespace witwave \
    --selector app.kubernetes.io/name=consensus \
    --output jsonpath='{.items[0].metadata.name}')

kubectl exec "$POD" \
    --namespace witwave \
    --container harness \
    -- ls -la /home/agent/.witwave/
# HEARTBEAT.md
# backend.yaml

kubectl exec "$POD" \
    --namespace witwave \
    --container echo-1 \
    -- ls -la /home/agent/.echo-1/
# agent-card.md
```

### 4c. Watch an edit propagate

This is the moment the whole gitOps loop is supposed to prove. Edit a
file on the repo, wait ≤60 seconds, confirm the pod sees the edit:

```bash
# On your local clone of the repo:
echo "Custom prose added on $(date)" >> .agents/consensus/README.md
git add .agents/consensus/README.md
git commit --message "docs: add timestamp to consensus README"
git push

# Wait ~60s, then:
kubectl exec "$POD" \
    --namespace witwave \
    --container harness \
    -- cat /home/agent/.witwave/../README.md | tail
```

The git-sync sidecar's exechook detected the new commit, rsync'd the
changed file into the pod, and the harness now sees the edit —
without a pod restart.

---

## 5. Backend lifecycle — rename, remove

As agents evolve, backends get renamed (`echo` was a placeholder,
rename to `smoke-test`) or removed (you're swapping echo for a real
backend). Two verbs, both atomic across CR + repo:

### 5a. Rename

```bash
ww agent backend rename consensus echo-2 echo-backup \
    --namespace witwave
```

Expected:

```
Action:    rename backend "echo-2" → "echo-backup" on WitwaveAgent "consensus" in witwave
  CR:     spec.backends[].name + gitMappings + inline backend.yaml
  Repo:   <you>/my-witwave-config/echo-2/ → <you>/my-witwave-config/echo-backup/  on ... (branch main)

Renamed backend "echo-2" → "echo-backup" on WitwaveAgent witwave/consensus.
Cloning ... Committed ... Pushing main ...
```

**What happened across three places, atomically:**

1. CR: `spec.backends[1].name` = `echo-backup` + every `gitMappings[]`
   dest path `/home/agent/.echo-2/` → `/home/agent/.echo-backup/`.
2. Inline `spec.config[0]` `backend.yaml` regenerated with new name.
3. Repo: `git mv .agents/consensus/.echo-2/ .agents/consensus/.echo-backup/`
   + rewrote `.agents/consensus/.witwave/backend.yaml`. One commit
   captures both moves. Pushed.

If you want to rename only the CR and handle the repo yourself, pass
`--no-repo-rename`.

### 5b. Remove

Remove a backend from both the CR and the repo:

```bash
ww agent backend remove consensus echo-backup \
    --namespace witwave \
    --remove-repo-folder
```

Expected:

```
Action:    remove backend "echo-backup" from WitwaveAgent "consensus" in witwave
  backend   "echo-backup" (removed)
  remaining [echo-1]
  backend.yaml (gitSync-managed) — edit the repo's file manually to drop references to echo-backup

Removed backend "echo-backup" from WitwaveAgent witwave/consensus.
Cloning ... Committed: Remove backend echo-backup for agent consensus ... Pushed main.
```

`--remove-repo-folder` extends the operation to the repo: `git rm -r`
the backend folder and rewrite `backend.yaml` so `agents:` no longer
lists the removed entry. Without the flag, the CR is updated but the
repo is untouched — useful when you want the backend's config
preserved for later re-attach.

`ww agent backend remove` refuses to remove the last backend on an
agent — the CRD requires at least one. If you want to delete the
agent entirely, use `ww agent delete`.

---

## 6. Cleanup — order matters

Removing gitOps wiring before deleting the agent avoids a race where
the operator's owner-ref cascade catches the gitSync sidecar
mid-pull:

```bash
ww agent git remove consensus \
    --namespace witwave \
    --delete-secret \
    --yes

ww agent delete consensus \
    --namespace witwave \
    --yes
```

`--delete-secret` removes the K8s Secret ww minted for the gitSync
sidecar. User-provided Secrets (referenced via `--auth-secret`) are
preserved regardless — ww gates deletion on the
`app.kubernetes.io/managed-by: ww` label, so hand-rolled Secrets
sharing the default name never get clobbered.

If you want to clean up the repo folder too:

```bash
# Local clone of the repo:
git rm --recursive .agents/consensus
git commit --message "chore: remove consensus config"
git push
```

There's no `ww agent delete --purge-repo` today — we deliberately
kept repo deletion manual so a `delete` typo can't destroy config
history.

---

## 7. Useful flags across every mutating verb

Every verb that mutates state honours the same discipline, so your
muscle memory carries across them:

| Flag | What it does |
|---|---|
| `--dry-run` | Print the plan and exit. Touches nothing — no API call, no disk write, no git push. |
| `--yes` | Skip confirmation prompts on production-looking clusters. Also via `WW_ASSUME_YES=true`. |
| `--no-wait` | (create, some operator verbs) Return as soon as the CR is accepted. Useful in CI. |
| `--timeout 2m` | Bound how long we wait for Ready (create) or git push (scaffold). |

Verbs that touch git additionally support:

| Flag | What it does |
|---|---|
| `--branch <name>` | Target a non-default branch. Defaults to the remote's HEAD symref (`main` when empty repo). |
| `--commit-message "..."` | Override the auto-generated commit subject. |
| `--no-push` / `--no-repo-rename` / `--no-repo-remove` | Stop after the local operation; handle push yourself. |

The full flag surface is in each command's `--help`. The README is
the reference doc.

---

## 8. When things don't work

The three debugging verbs, in increasing "I've been at this a while"
order:

```bash
# CR phase + reconcile history + backend summary.
ww agent status <name> \
    --namespace witwave

# Recent Kubernetes events on the CR + pods.
ww agent events <name> \
    --namespace witwave

# Harness container logs (pass --container <name> for sidecars).
ww agent logs <name> \
    --namespace witwave
```

Common situations:

- **Phase stuck at `Degraded`** — check `ww agent status`'s
  reconcile-history column; operator errors land there. Check
  `ww agent logs <name> --namespace witwave --no-follow --tail 50`
  for harness-level failures (missing `backend.yaml` routes,
  unreachable backends).

- **Pod stuck at `Init:Error` / `CrashLoopBackOff`** — usually a
  sidecar issue. `kubectl describe pod --namespace witwave <pod>`
  shows which init container failed;
  `kubectl logs --namespace witwave <pod> --container <init-container-name>`
  has the error. For git-sync issues, the most common fault is auth:
  rerun `ww agent git add` with a different `--auth-*` path.

- **gitSync attached but content isn't in the pod** — wait ~60s
  (default sync period) then check again. If still empty, the
  sync's `src:` path probably doesn't match a real folder in the
  repo. `ww agent git list` prints the mapping; a default-scaffolded
  layout uses `.agents/<agent>/.<backend>/` — if the repo is organised
  differently, pass an explicit `--repo-path` to `ww agent git add`.

- **`ww agent send` returns "the server is currently unable to
  handle the request"** — the apiserver's Service proxy can't
  reach the harness. Usually means the pod isn't Ready yet. Run
  `ww agent status` and wait.

---

## 9. What's next

Verbs already shipped — fully documented above:

- **Operator:** `install` (with `--if-missing`), `upgrade`, `uninstall`, `status`, `logs`, `events`
- **Agent lifecycle:** `create`, `list`, `status`, `delete`
- **Interaction:** `send`, `logs`, `events`
- **Scaffold:** `scaffold` (single + multi-backend, `--no-heartbeat`, merge-on-existing)
- **GitOps:** `git add / list / remove` (three auth modes, `--delete-secret` on remove)
- **Backend lifecycle:** `backend remove / rename` (with optional `--remove-repo-folder` / `--no-repo-rename`)

Verbs on the roadmap (shapes sketched in DESIGN.md, not yet
implemented):

- **`ww agent backend add`** — mint a new backend on an existing
  agent (the inverse of backend remove).
- **`ww agent add-job <file>`** / `add-task` / `add-trigger` etc. —
  materialise dormant-subsystem content into the repo with the
  right frontmatter shape, eliminating the need to hand-author
  scheduler files.
- **`ww prompt` subtree** — manage `WitwavePrompt` CRs (one prompt,
  bound to one or many agents) declaratively from the CLI.

The full design of each verb lives in [`DESIGN.md`](DESIGN.md) under
the rule tables (KC-*, SUB-*, PORT-*, NS-*, TAX-*). That's the
contributor-facing doc; this walkthrough is the user-facing one.

---

## Where to go from here

- **Reference for any specific verb**: [`README.md`](README.md) — every
  flag, every default, every exit-code-carrying condition.
- **Design rules the codebase follows**: [`DESIGN.md`](DESIGN.md) —
  codified so future-you doesn't have to re-derive them.
- **Smoke test script**: [`scripts/smoke-ww-agent.sh`](../../scripts/smoke-ww-agent.sh) —
  an automated version of sections 2 and 7 of this walkthrough, for
  verifying a fresh release.
- **Issue tracker / feature requests**:
  [github.com/skthomasjr/witwave/issues](https://github.com/skthomasjr/witwave/issues).

If you worked through this whole doc, you've exercised every
user-facing surface `ww` ships today. Future walkthrough sections
will land here as new verbs come online.
