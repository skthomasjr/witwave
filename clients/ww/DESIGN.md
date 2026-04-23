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
