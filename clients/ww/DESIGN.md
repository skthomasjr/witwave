# ww CLI — design rules

This document codifies the design invariants for the `ww` CLI. Each rule is numbered,
stands alone, and carries a one-line rationale when the reasoning isn't self-evident.

## How to use this document

- Rules are numbered within each section (`KC-1`, `KC-2`, …) so they can be cited
  in issues, PRs, and code review comments.
- Rules are additive. When a rule is superseded, keep the number, strike the text,
  and add a pointer to the replacement — the history matters more than the churn.
- Add new rules when a design decision is made that would otherwise need to be
  re-litigated. Don't write rules for things that are obvious from the code.
- If a rule needs a full rationale (multiple paragraphs, trade-off analysis),
  write an ADR under `clients/ww/docs/adr/` and link to it from the rule.

---

## Command taxonomy

Every subcommand is either **cluster-touching** or **local-only**. The distinction
drives whether kubeconfig resolution runs, whether `--context` is meaningful, and
whether the command works on an airplane.

- **TAX-1.** Local-only commands MUST NOT call `k8s.NewResolver` or otherwise
  trigger kubeconfig loading. Rationale: `helm template` is the gold standard —
  pure local commands should succeed with zero kubeconfig on the machine.
- **TAX-2.** Cluster-touching commands MUST resolve their target through
  `k8s.NewResolver` and print a preflight banner before any mutating operation
  (see KC-4).
- **TAX-3.** A command's taxonomy is part of its contract. Do not silently
  promote a local-only command to cluster-touching across releases — that breaks
  CI pipelines and airgap workflows.

---

## Kubeconfig and context

Kubeconfig resolution follows client-go's standard loader chain. `ww` inherits
kubectl/helm/flux semantics for free and must not deviate.

- **KC-1.** Discovery precedence is `--kubeconfig` flag → `$KUBECONFIG` env →
  `~/.kube/config` → in-cluster config. Do not introduce a `ww`-specific
  kubeconfig path or env var. Rationale: users expect one mental model across
  every K8s tool on their machine.
- **KC-2.** Exactly one context is active per invocation. Selection precedence
  is `--context` flag → `current-context:` in the resolved kubeconfig.
- **KC-3.** Never auto-pick a context. If `current-context` is unset and
  `--context` is absent, the command MUST exit non-zero with a diagnostic that
  lists the available contexts. Rationale: picking "the first one" silently
  mutates the wrong cluster; an explicit error is cheaper than a postmortem.
- **KC-4.** Every cluster-touching command MUST print a preflight banner
  (`Target` struct: context, cluster, server URL, user, namespace) before any
  mutating API call. Read-only commands MAY suppress the banner with a quiet
  flag but default to printing it.
- **KC-5.** `--kubeconfig` and `--context` MUST be persistent flags on the
  root command. They identify the target cluster, which is a uniform concept
  across every command. Harness-only commands (`ww tail`, `ww send`, …)
  inherit them harmlessly — the flags are inert unless a cluster-touching
  subtree consumes them. Rationale: cluster identity is the same question
  regardless of what you're doing on the cluster; forcing users to remember
  which subtree the flag lives on is friction with no upside.
- **KC-6.** `--namespace` / `-n` MUST be a persistent flag on each
  cluster-touching subtree, with a default that reflects that subtree's
  semantics. It MUST NOT be a global flag on root. Rationale: namespace
  meaning shifts by subtree — `ww operator` acts on the operator install
  namespace (default `witwave-system`); future `ww agent` / `ww prompt`
  subtrees act on tenant CR namespaces (default the context's namespace,
  then `default`). A single global default would be wrong for at least one
  subtree. Mutating commands that act on tenant resources MUST require an
  explicit `-n` rather than silently picking `default`.
- **KC-7.** `$KUBECONFIG` with multiple files merges them, but `current-context`
  comes from the first file that sets it — not the last. Document this
  explicitly in user-facing docs; do not "fix" it by changing precedence.
- **KC-8.** In-cluster mode (pod with `KUBERNETES_SERVICE_HOST` set) is
  supported via client-go's default loader. The preflight banner will show
  `Context: ""` and `Server: https://kubernetes.default.svc` — that's expected;
  don't special-case it unless it causes a concrete UX problem.

---

## Port assignment (agent pods)

Every agent pod is a single Kubernetes pod with N+1 containers (harness +
N backend sidecars). Containers in the same pod share one network
namespace, so **ports MUST be distinct** — only one container can bind a
given TCP port at a time.

- **PORT-1.** The harness container listens on port **8000**. Hard-coded
  because dashboards, NetworkPolicy, and the operator's default Service
  template all assume it. Changing this invalidates a lot of downstream
  wiring; don't.
- **PORT-2.** Backend sidecars listen on ports **8001–8050**, offset by
  their index in `spec.backends[]` (0-based). The CRD caps
  `spec.backends` at 50 entries (`maxItems: 50`), so 8001–8050 is an
  exact fit — one port per possible backend slot. Ship a new backend
  type and you don't have to think about port allocation; `ww agent
  create` / `ww agent backend add` pick the next free port automatically.
- **PORT-3.** The Prometheus metrics listener lives on port **9000**
  across every container, on a dedicated listener that `shared/
  metrics_server.py` manages. Intentionally outside the app-port range
  so NetworkPolicy rules can diverge between app traffic and
  monitoring scrapes.
- **PORT-4.** Callers can override any of these via explicit
  `spec.image.port` / `spec.backends[].port` fields on the CR. ww only
  enforces PORT-1..3 when it's the one generating the CR — hand-authored
  YAML is the user's responsibility. If a legacy backend insists on port
  8000 (collides with the harness), the user is expected to move it, not
  the harness.

---

## Subsystem enablement (dormant-by-default)

The harness has six optional subsystems — heartbeat, jobs, tasks,
triggers, continuations, webhooks — each keyed on a well-known path
under `.witwave/`. An agent's enabled subsystems are expressed through
**file presence, not CRD fields**. This is a deliberate architectural
choice, not an accident.

- **SUB-1.** A harness subsystem is enabled iff its content exists in
  the agent pod's filesystem:
  - `HEARTBEAT.md` enables heartbeat
  - `jobs/*.md` enables jobs (directory + at least one `.md` file)
  - `tasks/*.md` enables tasks
  - `triggers/*.md` enables triggers
  - `continuations/*.md` enables continuations
  - `webhooks/*.md` enables webhooks

  Content can land via any mount path the operator supports (gitSync,
  `spec.config` inline entries, mounted ConfigMaps/Secrets, emptyDir +
  init container, …). The harness doesn't care how content arrives —
  only that it's present.
- **SUB-2.** The absence of a subsystem's content is a **normal,
  expected state**. It means "this agent intentionally does not use
  this subsystem." A fresh agent is dormant on every optional
  subsystem by default; it answers A2A requests and nothing else.
- **SUB-3.** The harness MUST NOT emit INFO-level logs for missing
  subsystem content. Missing content is a DEBUG signal — visible under
  `-v` for diagnostics, silent at default levels. The
  transition *missing → present* (content materialised, e.g. via a
  gitSync pull or a later ConfigMap mount) IS an INFO signal: operators
  want to see the moment a subsystem comes online.
- **SUB-4.** Neither CRD fields nor CLI flags exist to toggle
  subsystem enablement explicitly. Future CLI verbs that enable a
  subsystem (e.g. `ww agent add-job <file>`) do so by materialising
  content under the corresponding path — no bit-flipping, no
  redundant fields. Rationale: two ways to express enablement
  (file presence + a CRD field) drift apart over time; one source of
  truth keeps the mental model clean.

---

## Namespace handling (tenant subtrees)

Rules governing how `ww agent` (and future tenant-scoped subtrees like
`ww prompt`) resolve `-n/--namespace`. Operator-scoped subtrees use
fixed per-subtree defaults per KC-6 and are exempt from NS-1..3.

- **NS-1.** Tenant-subtree commands with no `-n` flag MUST default to the
  kubeconfig context's namespace. If the context has no namespace set, fall
  back to `"default"`. This matches kubectl's precedence exactly.
- **NS-2.** Every tenant-subtree command MUST print the resolved namespace
  at the top of its output when `-n` was not explicitly supplied. A single
  line — e.g. `Using namespace: prod-agents (from kubeconfig context)` — is
  enough. Rationale: operators forget `-n`; the echo is their only fallback
  visibility for where a mutation actually landed. Never silently act on a
  defaulted namespace.
- **NS-3.** `-A/--all-namespaces` is valid only on **read** verbs (list,
  status-across-all-agents, etc.). Never on mutating verbs (create, delete,
  update). Rationale: `-A` multiplies blast radius; a single misplaced `-A`
  on a mutating verb is a cross-namespace incident.
- **NS-4.** `create` is the one mutating verb exempt from the "must specify
  `-n`" discipline other tenant-scoped CLIs enforce. It MAY land in the
  context's namespace by default because (a) hello-world ergonomics outrank
  purity for the onboarding path, and (b) `create` is idempotent — a
  re-run against an already-created name surfaces `AlreadyExists` cleanly.
  NS-2's print-the-resolved-ns rule still applies.

---

## Flags

*To be populated as flag conventions are established. Reserve:*

- Cluster-identity flags on root (KC-5): `--kubeconfig`, `--context`.
- Namespace flag per cluster-touching subtree (KC-6): `--namespace` / `-n`
  with subtree-specific defaults.
- Output format: `-o/--output` for read commands.
- Quiet/verbose: `-q/--quiet`, `-v/--verbose`.

---

## Output

*To be populated as output conventions are established. Reserve:*

- Default to human-readable on TTY, plain on pipe.
- `-o json` / `-o yaml` on any command that emits structured data.

---

## Exit codes

*To be populated. Reserve:*

- `0` success.
- Non-zero codes split by category (usage error, config missing, cluster
  unreachable, etc.) once the classes have been enumerated.
