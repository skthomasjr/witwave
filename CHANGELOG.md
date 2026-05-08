# Changelog

All notable changes to this project are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The project is pre-1.0 — minor version bumps may introduce
user-visible behaviour changes; they are called out explicitly in the **Changed** section of each entry.

## [Unreleased]

## [0.18.0] — 2026-05-08

`ww` gains a `conversation` subcommand for cross-agent transcript inspection. Two `fix:` items close the v0.17.0
fallout: a Go 1.26 gofmt regression that had `CI — ww CLI` red across three commits, and the `go-git/v5` CVE bump
evan flagged on his risk-work sweep. The release-pipeline gate itself hardens — iris's pre-flight CI check now
covers every workflow's latest run on `main` (not just runs on the release commit), and the post-tag watch is
mandatory with a structured `[release-workflow-failed]` reply zora's Priority 1 handler can act on.

### Added

- **ww**: `ww conversation list` and `ww conversation show <session-id>` read agent LLM-exchange transcripts
  (prompt, reply, tool calls, model, tokens, trace ids) from each agent's harness `/conversations` endpoint.
  CLI handles port-forward + auth plumbing automatically — one command fans out across every agent in scope.
  `-n <ns>`/`-A` follow the DESIGN.md NS-1/NS-2/NS-3 convention; `--agent <name>` filters within scope; `--since`
  and `--limit` for `list`, `--format=text|json|jsonl` for `show`. Per-agent bearer comes from the
  `<agent>-claude` Secret's `CONVERSATIONS_AUTH_TOKEN` key with `--token` override. Partial-failure UX: an
  unreachable agent doesn't kill the list — what's reachable renders, with a footer naming the unreachable
  ones and why. Three new internal packages back this: `internal/portforward/` (generic SPDY port-forward
  helper, ephemeral local port via `:0`), `internal/conversation/` (typed client over `/conversations`,
  fan-out, `DiscoverAgents` via `dynamic.NewForConfig`), and `cmd/conversation.go` (cobra wiring). Live-tail
  via SSE deferred to v2 (lives on backend port 8001, not harness 8000; second port-forward shape required).

### Fixed

- **ww**: `shellQuoteSingle`'s doc comment rewritten to describe the POSIX escape sequence in prose rather
  than embed the backtick-wrapped `'\''` literal, which Go 1.26's gofmt was silently normalising to a Unicode
  smart-quote on every run. Closes the recurring `CI — ww CLI` failure that had been red since `e1e97efe`
  (2026-05-07's `ww update` self-upgrade landing). Function body, behaviour, and tests unchanged.
- **security**: `github.com/go-git/go-git/v5` bumped 5.13.2 → 5.18.0 (clients/ww), closing the three Medium
  CVEs evan flagged on his risk-work sweep. Completes evan's auto-fix WIP that stalled mid-bump on 2026-05-08
  before `go mod tidy` ran.

### Agent identity

- **iris**: release skill's pre-flight CI gate switches from "runs on this commit" to "latest run per
  workflow on `main`" — path-filtered workflows (e.g. `CI — ww CLI` only fires on `clients/ww/**` changes)
  no longer slip past the gate when the release commit doesn't trigger them. Closes the exact gap that let
  v0.17.0 cut on 2026-05-07 while `CI — ww CLI` had been red for ~1h45m on three earlier commits. Step 11
  watch-to-conclusion is now mandatory: the skill DOES NOT return success until every release workflow
  concludes; failures surface as a structured `[release-workflow-failed]` reply listing what published and
  what didn't. Iris does not auto-retry the failed workflow, doesn't delete the tag, doesn't try to recover
  — the contract is iris-reports / zora-decides.
- **zora**: "Never leave a broken build" load-bearing principle added right after Team Mission. Frames red
  CI as the team's fire alarm, outranking every other priority including critical CVEs. Author-agnostic;
  human commits and peer commits get the same treatment. Especially after a release: red CI on the next
  commit poisons every subsequent release until fixed. Extended to cover post-tag release-workflow failures
  with the same fire-alarm framing — freeze cadence, redirect to fix, surface visibly via `escalations.md`,
  hold release-warranted. `dispatch-team` Priority 1 grows two new handlers: red-CI auto-dispatches evan
  with the failing-job log + breaking commit (capped at 2 attempts before hard escalation), and failed-
  release-workflow branches into transient-infra (ask iris to `gh run rerun --failed`) vs real-bug-in-source
  (dispatch evan, then either iris re-run or cut vX.Y.Z+1). Stuck-peer escalation becomes time-bounded
  (T+30m iris auto-recovery, T+1h `[needs-human]` surface, T+2h pause-mode) so a single stalled peer can't
  freeze the team for hours waiting on human resolution. CI check now iterates `git rev-list v<latest>..origin/main`
  so failures on any commit since the latest tag count as red, regardless of how many commits later HEAD is.

## [0.17.0] — 2026-05-07

`ww` gains a working self-upgrade path for standalone-binary installs and an "all containers, prefixed" default for
`ww agent logs`, closing two of the more visible operator-experience gaps from the v0.16 line. A METRICS_ENABLED=0
startup crash in the harness is fixed. Backend images (codex, gemini) pick up Node.js 20 to match claude, and evan's
bug-work scope expands across the day-one toolchain table to cover charts and the dashboard.

### Added

- **ww**: `ww update` now upgrades standalone-binary installs by reusing the canonical `install.sh` pipeline with
  `--install-dir` pointed at the running binary's directory. Pre-flight write check surfaces "re-run with sudo" up-front
  rather than failing mid-download. Closes the gap surfaced after v0.16.5 cut autonomously and `ww update` reported "no
  automatic upgrade path." `ww update --check` now says "To upgrade: ww update" instead of pointing at the tarball URL.
- **ww**: `ww agent logs <name>` defaults to tailing every container in the pod (harness + backend(s) + git-sync)
  interleaved, with each line prefixed by `[<container>]`. Multi-pod scope (rollouts, `--pod` ambiguous match) widens
  the prefix to `[<short-pod-suffix>/<container>]`. `-c <name>` still filters to one container; unknown container names
  surface a clean error listing what's available.
- **backends**: `backends/codex` and `backends/gemini` Dockerfiles install Node.js 20 (claude already had it), so any
  agent on any backend can run any skill that shells out to a Node-based CLI.

### Fixed

- **harness**: METRICS_ENABLED=0 startup no longer crashes with `NameError: name 'app' is not defined`. The lifespan
  closure's metrics-disabled branch was calling `app.router.add_route(...)` at lines 2227/2232; the closure parameter
  had been renamed to `_app` upstream (commit `e027504c`, #924) but the rename wasn't propagated. Both call sites now
  reference `_app`. Surfaced by evan's bug-work depth=5 sweep at 2026-05-07T18:00Z.

### Agent identity

- **evan**: bug-work day-one toolchain table picks up `charts/witwave` and `charts/witwave-operator`
  (helm lint + yamllint) and `clients/dashboard` (vue-tsc --noEmit + hadolint). The "deferred to v2" list shrinks to
  empty — every section the repo currently exposes is now in evan's scan scope. `all-day-one` count: 14 → 17. Chart and
  dashboard toolchains were already installed in the backend images; the SKILL was just stale.
- **nova, kira, zora**: findings-marker schema adopted team-wide. `code-verify`, `docs-verify`, and `docs-consistency`
  SKILLs now end findings bullets with `[pending]` / `[flagged: <reason>]` / `[fixed: <SHA>]` markers — matching the
  schema evan was already using. Zora's `dispatch-team` per-peer backlog adapter reframes accordingly: marker count on
  sections dated 2026-05-07 onward, narrative-bullet count only on legacy pre-cutoff sections. Once legacy sections age
  out, the adapter degenerates to a uniform marker count across all peers.

## [0.16.5] — 2026-05-07

Closes the harness `-32603` empty-response bug that had been corrupting synchronous JSON-RPC replies whenever an agent
returned a clean empty string (observed across evan empty-sweeps, kira docs work, and nova code-cleanup runs since
2026-05-06). Alongside that fix, zora's policy gains depth-control across evan / nova / kira so polish-tier sweeps
escalate when cheap-pass tiers exhaust, and the team picks up its first per-agent self-maintenance loop with `self-tidy`
running once per 24h on a staggered cron across all five peers.

### Fixed

- **harness**: `executor.execute()` now always enqueues a final A2A Message event, defaulting to empty text on empty
  responses, so the SDK's `DefaultRequestHandler` always has at least one event to serialise into a JSON-RPC success
  reply. Previously the `if _response:` gate skipped the enqueue on empty-string returns, leaving the SDK with nothing
  to serialise and surfacing `-32603` to the caller despite a clean execute. Adds `logger.exception()` at the catch site
  so future exception paths capture full traceback instead of getting re-raised opaquely. Scoped to
  `harness/executor.py:execute()`; zero changes to backend interfaces or A2A SDK pinning.

### Documentation

- **repo-wide**: markdownlint + prettier auto-fixes applied across the docs surface — formatting-only, no semantic
  changes.

### Agent identity

- **team**: `self-tidy` skill added — each peer runs a byte-identical per-agent daily self-maintenance pass
  (memory-index consolidation, cross-agent awareness refresh, agent-card drift check) on a staggered 24h cron (iris
  02:15Z, kira 06:30Z, nova 10:45Z, evan 15:00Z, zora 19:15Z). Sibling to zora's `team-tidy` (cross-cutting, zora-only);
  self-tidy is per-agent and stays scoped to the running agent's own files. Cap: 1 commit per agent per day, ≤50 lines,
  atomic. Same iris-delegated push contract every other peer follows.
- **zora**: direct `gh` CI-read auth wired (`zora-claude` secret now carries `GITHUB_TOKEN` + `GITHUB_USER`) so the
  release-warranted check can call `gh run list --branch main` directly instead of inferring CI state from indirect
  signals; gh writes stay strictly in iris's lane. Per-peer backlog adapter added to step 2c so evan's canonical
  `[pending]` / `[flagged: ...]` schema and nova/kira's narrative tally formats both feed the dispatch tiebreaker.
- **zora**: polish-tier depth control extended across the team. evan dispatches now pick the depth tier per cadence
  (reset to 3 on fresh source, advance 3 → 5 → 7 → 9 after 2 consecutive 0/0/0 runs, hold otherwise) and the polish
  baseline raises 3 → 5 once cheap-pass ground has been swept. Same advance/reset/hold logic extends to nova
  (`code-cleanup` ↔ `code-document` one-shot) and kira (`docs-cleanup` ↔ `docs-research` one-shot) — deeper passes fire
  as one-shots when the default tier returns 0/0/0 for 2 consecutive runs on stable source, then flip back. State
  tracked per skill in `team_state.md`. Dispatches/hour cap raised 5 → 8 to give the tightened cadence floors breathing
  room. kira `docs-cleanup` cadence floor tightens 24h → 6h so docs sweeps keep pace with the team's commit rate.

## [0.16.4] — 2026-05-07

Operator-regen drift closure plus a team-cadence sharpening. The operator-section probes (evan bug-work, evan risk-work,
nova code-cleanup) had been dirtying the working tree on every run because two committed files diverged from
current-toolchain output, costing the team's dispatch cap on motion-without-progress; this release commits the
regenerated state and adds a working-tree restore around the probes themselves so the failure can't recur. Alongside
that, zora's release policy moves from a count-based daily cap to a velocity-driven release-warranted check, and evan's
bug-class scope broadens to pull pyflakes-class items off nova's deferred shelf.

### Fixed

- **operator**: `operator/go.mod` and `operator/config/webhook/manifests.yaml` regenerated against the pinned
  `controller-gen v0.18.0` + `go mod tidy` so committed state matches what the toolchain produces. `go-logr/logr` flips
  from indirect to direct (operator imports it directly); webhook manifest is a pure YAML emitter-style flip
  (sequence-dash positioning, 81/81 lines, zero semantic change). Combined with the probe-restore wrap below, this
  closes the regen-drift escalation that had been burning the team's dispatch cap.

### Agent identity

- **team**: each peer's CLAUDE.md gains a one-line cross-reference to `.agents/self/TEAM.md` right after the team-roster
  block, so the canonical roster + topology + future-roles overview is reachable from any agent's identity file instead
  of being browse-only. Unblocks zora's `team-tidy` skill, which had been escalation-blocked for ~10h on this exact gap.
- **evan**: bug-work probes wrap operator-section toolchain runs (`make manifests`, `go vet`, `go mod` side effects)
  with `git checkout -- operator/go.mod operator/config/` after capturing drift results, so probe residue can't dirty
  the tree for the next peer. Bug-class scope broadens from `B` to `B,F` — pyflakes (F821 undefined-name, F811
  redefinition, F823 referenced-before-assignment, F841 unused-variable-masking-typos) IS bug-class and was being
  filtered out incorrectly; items currently sitting in nova's deferred-findings memory under those rules will flow
  through evan's fix-bar pipeline on his next sweep.
- **zora**: release policy switches from a count-based cap (max 4/day, ≥1h floor) to a velocity-driven release-warranted
  check that fires when weighted commits since the latest tag cross 3.0 (feat=2.0, fix=1.0, docs=0.5,
  chore/style/refactor/test=0.25) or a `fix(security):` / critical commit lands. Hygiene floor of 15 min between
  releases prevents same-tick double-fires; hard cap of 20 releases/day acts as a runaway guard only. Heartbeat tightens
  30 min → 15 min so release latency tracks the new velocity policy. Cadence floors for evan bug-work (6h → 3h), evan
  risk-work (12h → 8h), and nova code-cleanup (12h → 8h) tighten alongside so the produce side keeps pace with the
  publish side.

## [0.16.3] — 2026-05-07

CI hygiene patch. Single fix from evan's bug-work catalogue (SP-4 unused-loop-var rename) in the install-script CI
workflow.

### Fixed

- **workflows**: unused loop variable `i` renamed to `_i` in the install-script CI poll loop
  (`.github/workflows/ci-install-script.yml`) — count-controlled retry idiom; pure variable rename in unused position,
  no runtime effect.

## [0.16.2] — 2026-05-07

Documentation-only release. The team's self-agent roster gets a written reference: a new `TEAM.md` under `.agents/self/`
captures the five-agent team's shape (iris, kira, nova, evan, zora) and adds a priority-ordered "Proposed future
members" section sketching where the team might grow next (CTO, devops, security, architecture, testing, PR roles).

### Agent identity

- **team**: `.agents/self/TEAM.md` added — overview of the current five-agent team plus a proposed-future-members
  roster, reordered by priority and extended over the cycle to cover CTO, devops, security, architecture, testing, and
  PR roles.

## [0.16.1] — 2026-05-07

Manager arc: the team gains a fifth self-agent — **zora** — owning team-level dispatching and release cadence across the
four domain-specialist peers (iris, kira, nova, evan). She decides what work happens when, not how; domain decisions
stay with each peer. Initial responsibilities are scaffolded alongside a `team-tidy` skill that keeps the team's shared
posture (CLAUDE.md tone, skill structure, memory hygiene) consistent over time. A nova-driven `ruff format` pass sweeps
the Python sources clean as a baseline.

### Agent identity

- **zora**: scaffolded as the team's fifth self-agent and team coordinator — identity (CLAUDE.md + agent-card),
  team-participation files, and the standard cross-agent skills inherited from the family. Sits above the four domain
  peers and dispatches work via A2A; all peers' CLAUDE.md updated to describe her as a valid caller into their skills,
  not a gate — direct user invocation still works.
- **zora/team-tidy**: skill scaffolded to keep team-shared posture consistent — anchored on the team's overall mission
  so consistency-and-self-improvement work targets the right shape rather than drifting into local cleanup.

### Changed

- **python-sources**: `ruff format` pass across the Python tree (nova) — formatting-only, no behaviour change.

## [0.16.0] — 2026-05-07

Risk-work arc: evan gains a sibling skill to `bug-work` — `risk-work` — owning identification of exploitable risk
(vulnerabilities, secrets, supply-chain exposure) across the project's source. The supporting backend image work
installs the day-one risk toolchain (govulncheck, gosec, pip-audit, bandit, gitleaks, trivy) uniformly across all three
backends so risk scans run identically wherever evan is deployed. Bug-work itself gains a safe-pattern catalogue so
auto-fixes at depth ≥ 5 land on rails for shapes the team has already vetted.

### Agent identity

- **evan/risk-work**: scaffolded as bug-work's sibling — same 7-step shape (scan, validate, reason as set, decide
  fix-vs-flag, fix with web-search + scoped local tests, log, push + CI watch through iris), same depth dial, same
  batch-revert posture on red CI, but the discovery target is exploitable risk rather than correctness defects. Skill
  scaffold landed first, then the procedural body was fleshed out and the toolchain installed across the backends.
- **evan/bug-work**: safe-pattern catalogue added to the step-4 fix-bar — a curated list of "we've seen this shape
  before and the canonical fix is X" patterns (B904 chain-vs-suppress, B010 setattr-with-constant, SC2086
  quote-GOOS/GOARCH, SC2034 rename-unused-loop-var, etc.) so depth-≥5 auto-fixes land on rails instead of asking the web
  every time. Companion changes: `bug-sweep` renamed to `bug-work` (leading the verb pair); depth reframed as "how hard
  we hunt"; Step 0.5 added to recover stuck commits before scanning; CLAUDE.md + SKILL.md refactor for clarity;
  fix-forward semantics on local-test + CI failures; candidate list persists to memory immediately after scan so a
  crashed pass doesn't lose its work.

### Added

- **backends**: evan's risk-work toolchain installed uniformly in claude, codex, and gemini images — govulncheck and
  gosec (Go), pip-audit and bandit (Python), gitleaks (secret scanning), trivy (filesystem + container scans). Sits
  alongside the existing bug-work toolchain from v0.15.0. Echo image stays minimal by design.

### Fixed

Tonight's evan bug-work pass closed five issues; the morning's pass had landed a larger batch which was reverted on red
CI (`ci-ww.yml`) and then reworked and re-landed cleanly.

- **harness**: TimeoutError chain suppressed when raising QueueFull (B904). Int-conversion context suppressed when
  re-raising as ValueError (B904). Unused loop variable renamed in `_resolve_host_to_private_check` (SC2034-equivalent
  Python).
- **tools/helm**: bash `pipefail` SHELL set so pipe failures surface in helm-chart make targets (DL4006). idna failure
  chained when raising HelmError on bad hostname (B904).
- **tools/kubernetes**: loop variables bound at iteration time in apply-commit lambda (closure-over-loop-var).
- **backends/claude**: NameError context suppressed when re-raising primary lifespan error (B904); setattr-with-constant
  replaced with direct attribute assignment (B010, two call sites).
- **backends/codex**: TimeoutExpired chained when raising ShellTimeoutError (B904).
- **backends/gemini**: setattr-with-constant replaced with direct attribute assignment (B010).
- **workflows**: GOOS/GOARCH expansion quoted in `ci-ww.yml` build step (SC2086). Option parsing terminated in the SLSA
  archive `sha256sum` invocation (SC2035) so subjects with leading dashes are preserved verbatim. SC2016 false positive
  suppressed on the bcrypt htpasswd single-quoted literal in `release.yaml`. SC2034 silenced on the unused index in the
  fake-dist server poll loop (rename to `_`).

### Changed

- **ww**: embedded operator chart values resynced to track upstream chart edits.

## [0.15.0] — 2026-05-06

Bug-discovery arc: evan lands as the team's fourth self-agent — owner of correctness across the project's source code,
with a single-pass `bug-sweep` skill and a 17-section addressable namespace driving where he looks. The supporting
backend image work installs his Go and Python bug-class toolchains uniformly across all three backends so day-one
analyzer + local-test gating runs identically wherever evan is deployed.

### Agent identity

- **evan**: scaffolded as the team's fourth self-agent — owner of correctness bug discovery across Python, Go,
  Dockerfile, shell, and GitHub Actions sources. Identity (CLAUDE.md + agent-card), team-participation files, and four
  skills: `bug-sweep` (the work skill — 7-step end-to-end pass: scan, validate through an eight-concern
  intentional-design gauntlet, reason as set, decide fix-vs-flag, fix with web-search + scoped local tests, log, push +
  CI watch — both delegated to iris), and `git-identity` / `call-peer` / `discover-peers` (cross-agent skills copied
  byte-identical from kira/nova). 17-section addressable namespace (14 day-one + 3 deferred to v2) with aliases
  (`all-python`, `all-go`, `all-backends`, `all-tools`, `all-day-one`); single 1-10 depth dial gates both step-2
  validation rigor and step-4 fix-bar stringency, with auto-fix only at depth ≥ 5. Batch-revert posture on red CI per
  trunk-based-dev contract. State lives in commits + memory only — no GitHub issues, no labels, no funnel.
- **evan → iris CI-watch delegation**: framed as team contract, not workaround. Step 7 of `bug-sweep` delegates BOTH the
  push AND the CI watch to iris in one `call-peer` round-trip; iris reports back, evan acts on the report. The same
  shape applies to future siblings (security, test-coverage, etc.) — iris owns all git/GitHub authority for the team,
  every other agent stays focused on its domain.

### Added

- **backends**: evan's bug-class Go toolchain installed uniformly in claude, codex, and gemini images — staticcheck
  2024.1.1, errcheck v1.7.0, ineffassign v0.1.0, controller-gen v0.16.5. Consolidates with nova's existing goimports
  install into a single five-binary `go install` step. ~80–120 MB image growth per backend. Echo image stays minimal by
  design.
- **backends**: evan's bug-class Python toolchain installed alongside nova's existing ruff/yamllint pip block in claude,
  codex, and gemini images — pytest 8.3.3, pytest-asyncio 0.24.0, httpx 0.27.2, python-kubernetes 31.0.0. Lets
  `bug-sweep` run scoped local tests (`pytest <section>/`) without per-workflow ad-hoc installs.

### Fixed

- **evan**: closed five gaps from end-to-end review of the v1 design — pytest tooling missing from backend images;
  GitHub PAT placeholder breaks Step 7 CI watch (now delegated to iris by design); controller-gen drift command
  regenerated only deepcopy files, replaced with `make manifests` + git diff against `operator/config/crd/bases/`;
  memory directory parent might not exist on first run, added idempotent `mkdir -p`; CLAUDE.md / SKILL.md cd-path
  inconsistency for scoped tests aligned around `<checkout>/<section>`.

### Changed

- **tooling**: `.prettierignore` excludes controller-gen output (`charts/witwave-operator/crds/`,
  `operator/config/crd/bases/`, `operator/config/rbac/role.yaml`) so nova's code-format passes stop reverting it on
  every run. `.editorconfig` pins shell-script indent to 2-space (matching 7 of 9 `scripts/*.sh`); `install.sh` and
  `smoke-ww-agent.sh` keep 4-space via per-pattern override to avoid ~700-line reflows.

## [0.14.0] — 2026-05-06

Team-participation arc: every self-agent gains generic A2A peer discovery + call skills, nova lands as the third
self-agent (full code-hygiene identity, skill set, and image toolchain), kira's docs surface grows a research skill plus
Tier 2 verify/consistency checks under a new `docs-cleanup` orchestrator, and `ww` gains a `--harness-env` flag that
closes the cross-agent timeout trap where `TASK_TIMEOUT_SECONDS` set on the backend alone left the harness retrying
long-running relays at the 5-minute default.

### Added

- **ww**: `--harness-env <KEY>=<VALUE>` flag on `ww agent create`, repeatable. Stamps `spec.env[]` on the harness
  container so settings like `TASK_TIMEOUT_SECONDS` propagate to the harness's A2A relay timeout (read at startup as
  `max(TASK_TIMEOUT_SECONDS - 10, 10)`). Closes the failure mode where iris→kira docs-scan calls retried mid-relay and
  produced duplicate work because the backend's longer timeout wasn't matched on the harness.
- **backends**: nova's code-hygiene toolchain installed uniformly in claude, codex, and gemini images — Go 1.23.4 +
  goimports, shfmt 3.10.0, shellcheck, actionlint 1.7.7, hadolint 2.12.0, helm 3.16.3, ruff 0.6.9, yamllint 1.35.1. ~205
  MB image growth (Go is the bulk). Echo image stays minimal by design.

### Agent identity

- **self (iris/kira/nova)**: `discover-peers` + `call-peer` skills installed identically across all three agents.
  Discovery introspects Kubernetes-injected service env vars (`<NAME>_SERVICE_HOST` / `_PORT`) and probes each
  candidate's `/.well-known/agent.json`, caching confirmed peers as reference-type memory entries. `call-peer` builds a
  JSON-RPC `message/send` envelope (blocking, fresh messageId), POSTs, parses the response, and surfaces typed
  diagnostics for the standard `-32001` / `-32603` error codes. Same-namespace only; pod restart required to discover
  newly-deployed siblings.
- **nova**: scaffolded as the team's third self-agent — owner of the CODE-INTERNAL COMPREHENSION SUBSTRATE (code
  comments as the way future contributors and AI agents understand existing behaviour). Identity (CLAUDE.md +
  agent-card), team-participation files (HEARTBEAT, backend.yaml, git-identity, git-push), and four code-domain skills:
  `code-format` (Tier 1 mechanical fixes via ruff/gofmt/goimports/shfmt/prettier/yamllint), `code-verify` (Tier 2 read-
  only cross-checks of docstrings vs signatures, godoc vs exported APIs, helm-docs comments vs template usage),
  `code-document` (Tier 3 grounded authoring of missing comments — every claim must be derivable from the code body),
  and `code-cleanup` (Tier 1 + 2 orchestrator that delegates publishing to iris). Helm chart values are first-class
  targets. Infrastructure source — Dockerfiles, shell scripts, GitHub Actions workflows — is included in scope across
  every nova skill (shellcheck, hadolint, actionlint added to Tier 1; new check classes D/E/F in Tier 2; new authoring
  categories D/E/F in Tier 3).
- **kira**: `docs-research` skill — the only docs skill that reaches outside the repo. Targets forward-looking Cat C
  documents (`competitive-landscape.md`, `product-vision.md`, `architecture.md`); verifies existing URLs still resolve,
  rechecks named projects for currency, and adds up to 3 new competitive-landscape entries per run. Citation discipline
  is the load-bearing rule: every new claim must end with `(source: <url>, accessed YYYY-MM-DD)`. One commit per target
  doc; push delegated to iris.
- **kira**: Tier 2 docs skills — `docs-verify` cross-checks Cat C documentation against current code (broken paths,
  command examples, env-var names, version numbers, identifiers); `docs-consistency` runs cross-doc agreement checks on
  Cat C (versions, subproject README claims, command-surface drift). Both are memory-log only — every finding is an
  "update doc OR fix code" judgment call. New `docs-cleanup` orchestrator runs the full sweep (Tier 1 on all `.md`, Tier
  2 on Cat C only) and delegates publishing to iris. `docs-validate` extended to wrap long YAML frontmatter
  `description:` strings via folded scalars so the `printWidth=120` convention applies there too. `docs-scan` updated to
  delegate push via `call-peer` instead of invoking `git-push` directly — kira-commits / iris-pushes contract now
  applied uniformly across both orchestrators.
- **kira**: identity tightened around the docs-as-communication-channel framing (CLAUDE.md + agent-card). Two distinct
  audiences — humans reading the repo and downstream automated processes that ingest forward-looking docs for feature
  discovery / planning — make drift propagate downstream as wrong code changes or miscalibrated planning. Stale claims
  dropped from agent-card ("every 6 hours" cadence, "pushes the batch herself", AGENTS.md/CLAUDE.md autofix scope).
- **iris**: identity tightened around the choke-point framing (CLAUDE.md + agent-card). Iris is the team's choke point
  for everything reaching `origin/main` — every commit by any agent reaches origin through iris; every tagged release
  happens through iris. The kira-commits / iris-pushes `call-peer` pattern is now made explicit in Responsibility 2.
  agent-card rewritten end-to-end to drop the source-tree-as-user-capability framing (iris owns it autonomously as a
  precondition).

### Documentation

- **bootstrap**: Step 5 added for nova deploy — mirrors Step 4 (kira) with nova-specific env-var lifts
  (`GITHUB_TOKEN_NOVA` / `GITHUB_USER_NOVA`) and the paired `TASK_TIMEOUT_SECONDS=2700` on both harness and backend.
  Goal section, env-vars block, verify section ("three rows"), and teardown updated for the three-agent footprint.

## [0.13.0] — 2026-05-02

A2A correctness arc: blocking-call response truncation fixed, hook-event field corrected, and the streaming capability
flag flipped to match actual wire behaviour. `ww` gains `--backend-env` for non-secret per-backend env overrides and
stops clobbering operator-overlaid `backend.yaml` when a harness gitMapping owns the `.witwave/` directory. Kira's
identity scaffolding fleshes out with Tier 1 docs skills, a liveness heartbeat, and a docs-drift sibling.

### Added

- **ww**: `--backend-env <backend>:<KEY>=<VALUE>` flag on `ww agent create`, repeatable per (backend, KEY). Stamps
  `spec.backends[].env[]` directly on the CR for non-sensitive tunables (`TASK_TIMEOUT_SECONDS`, `LOG_LEVEL`,
  `STREAM_CHUNK_TIMEOUT_SECONDS`, etc.). Secret values continue to flow through `--auth-set` /
  `--backend-secret-from-env`.

### Fixed

- **backends**: blocking A2A callers (`message/send` with `configuration.blocking=true`) now receive the agent's
  complete output instead of just the first turn. The A2A SDK's result aggregator treats every `Message` event as
  terminal; reverting per-chunk emission to a single final aggregated `Message` closes the truncation. (Follow-up to
  #430 — chunks belong on `TaskStatusUpdateEvent` if streaming consumers ever appear.)
- **backends**: hook-decision events send the backend id (`claude` / `codex` / `gemini`) instead of the named-agent name
  (`iris` / `kira`). Ends the per-turn `hook.decision SSE drop: unknown agent` warning that was swallowing real policy
  signal. (#1149)
- **backends**: `AgentCapabilities.streaming=False` on agent cards, matching the post-revert wire behaviour. Clients
  querying `/.well-known/agent.json` no longer see a dishonest streaming flag. Per-chunk dashboard drill-down is
  unaffected — that's a separate `_sess_stream` SSE channel.
- **ww**: `ww agent create` skips emitting the inline `backend.yaml` Config entry when a harness-level `gitMapping`
  covers `/home/agent/.witwave/`. Closes the rsync-delete race where the gitSync sidecar's `rsync --delete` was wiping
  operator-overlaid `backend.yaml` on every pull (the operator subPath wasn't present in the synced source, so rsync
  removed it from the dest).

### Changed

- **codex**: removed the now-unreachable drop-recovery machinery in `executor.py` (`_attempted_texts`, `_emitted_texts`,
  `_stream_state`, and the elif drop-recovery branches in `execute()`). With per-chunk emission gone, the chunk-enqueue
  timeout it guarded against can no longer fire. ~60 lines deleted.

### Agent identity

- **kira**: Tier 1 docs skills scaffolded and named in CLAUDE.md; `docs-validate` skill fixed to invoke
  `markdownlint-cli` (no `2`). Doc-categories section added to CLAUDE.md.
- **kira**: 30-minute heartbeat added — liveness signal only, does not trigger a docs scan.
- **kira**: docs-drift sibling identity scaffolded.
- **iris**: identity updated to the `iris-agent-witwave` GitHub account; commits now link to that account via the
  matching verified email.
- **iris**: Memory section added to CLAUDE.md — file-based memory on the shared workspace volume with private
  (`agents/iris/`) and team (top-level) namespaces.
- **iris**: `backend.yaml` lands as gitSync content under `.agents/self/iris/.witwave/`. Pairs with the `ww` fix above
  so iris no longer needs an operator-overlaid `backend.yaml`.

### Documentation

- **bootstrap**: kira deploy step added.

## [0.12.0] — 2026-05-01

This release is entirely infrastructure for the self-agent ecosystem: iris gains a full release-captain build-out, all
three self-agents are promoted to `bypassPermissions` mode, and gh CLI lands in the backend images to enable
workflow-query tooling. No changes to `ww`, the operator, the harness, or backend runtime behaviour.

### Added

- **backends**: gh CLI installed in claude, codex, and gemini images, enabling `gh run list` and other GitHub workflow
  queries inside backend containers. (`GH_PROMPT_DISABLED=1` was already set in anticipation of this install.)

### Agent identity

- **iris**: `release` skill added — covers CI-green verification, bump inference (patch / minor / explicit), CHANGELOG
  generation, annotated tagging, and tag push. Pre-1.0 semantics: `feat:` and breaking markers fold into a minor bump;
  major is reserved for the deliberate `v1.0.0` cut. Skill gaps closed post-scaffolding: git identity is pinned before
  the changelog commit, stable-tag filtering ensures beta-cycle commits aren't lost on graduation, and
  `BREAKING CHANGE:` entries fold into **Changed** without a bold prefix in the pre-1.0 period.
- **iris**: `git-push` skill added — narrow push-only skill for publishing already-made commits to `main`; handles the
  sibling- pushed-first race via pull-rebase + one retry.
- **iris**: Identity contract moved to CLAUDE.md — `user.name` / `user.email` declared per-agent; all skills read values
  from the agent's own prose so the same skill files work across iris, nova, and kira without modification.
- **iris**: Skills reorganised into folder-per-skill layout; HTTP triggers removed in favour of A2A as the exclusive
  inter-agent channel. `sync-source` renamed to `git-sync-source`.
- **iris**: A2A agent-card rewritten to surface actual capabilities (git plumber + release captain); Responsibilities
  section added to CLAUDE.md scoping the role explicitly.
- **agents**: iris, nova, and kira promoted to `bypassPermissions` mode; `settings.json` trimmed to just `defaultMode`.

### Documentation

- Changelog backfilled from commit history covering v0.8.2 through v0.11.16.

## [0.11.16] — 2026-04-30

Single-fix patch on `ww agent upgrade` — closes a regression where the merge-patch path dropped existing
`spec.backends[].image.repository` on push, causing admission to reject with "spec.backends[i].image .repository:
Required value."

### Fixed

- **ww**: `ww agent upgrade` swaps merge-patch → Update with the full CR. Preserves every field on the existing CR
  (image repository, env, volumes, gitMappings) so admission sees a valid object. Verified end-to-end on a live cluster.

## [0.11.15] — 2026-04-30

First-cut `ww agent upgrade` — in-place image-tag rollout for a single agent.

### Added

- **ww**: `ww agent upgrade <name>` patches `spec.image.tag` (harness) and each `spec.backends[].image.tag` on the
  WitwaveAgent CR. The operator reconciles the change and rolls the Deployment via the standard kubelet rollout —
  pod-local PVC state survives the roll. Tag resolution priority: `--tag X` (everything) > `--harness-tag` /
  `--backend-tag <name>=X` (per-container) > brewed CLI's own version. Idempotent fast-path (no-op when tags match),
  `--force` to roll regardless, dev-build guard, unknown-backend guard, event dump on timeout. Bulk paths (`--all`,
  `--all-namespaces`) deferred to a follow-up.

## [0.11.14] — 2026-04-30

Operator + cross-component fixes to make hook events flow correctly out of the box. Metrics enabled by default on every
operator- provisioned agent; new CLI opt-out.

### Added

- **shared**: `shared/env.py` boundary-safe parsers — `parse_bool_env`, `parse_int_env`, `parse_float_env`. Centralises
  env-var parsing across every Python component with case- insensitive truthy/falsy vocabulary and loud-failure on
  typos.
- **ww**: `--no-metrics` flag on `ww agent create` to opt out of the new default-on metrics posture.

### Changed

- **operator**: `MetricsSpec.Enabled` switched to `*bool` with kubebuilder default `true`. Every agent the operator
  provisions has metrics on by default; explicit `false` opts out.
- **charts**: `metrics.enabled` default flipped from `false` → `true` for chart-installed agents (parity with operator
  default).
- **All Python components**: 16 sites of `bool(os.environ.get(...))` replaced with `parse_bool_env`. The classic Python
  anti-pattern (`bool("false") == True`) had been silently flipping `METRICS_ENABLED=false` to `True` everywhere.

### Fixed

- **operator**: `HARNESS_EVENTS_URL` stamped at the metrics port when metrics are enabled, matching where the
  `/internal/events/{hook-decision,publish}` routes actually live under #924's NetworkPolicy posture. Combined with the
  bool-parse fix above, closes the silent 404 storm on hook event delivery (#1781).
- **shared**: import env helpers via top-level name (`from env import ...`) — the previous `from shared.env import` form
  was broken because `shared/` is on PYTHONPATH but isn't a package.

### Agent identity

- **iris**: heartbeat probes CLAUDE.md loading via name-substitution check (HEARTBEAT_OK <name>); throttled back to
  every 30 minutes after validation runs.

## [0.11.13] — 2026-04-30

Operator-side auto-mint of the per-agent internal-state Secret. Closes the silent-401 storm on
`/internal/events/hook-decision` POSTs.

### Added

- **operator**: per-agent `<agent>-internal` Secret reconciler. Always-on, idempotent — generates a random
  `HOOK_EVENTS_AUTH_TOKEN` on first reconcile (32 bytes, base64url-encoded; ~43 chars) and preserves it across
  subsequent reconciles. Cascade-deletes with the agent via OwnerReference. Stamped as envFrom on harness + every
  backend container so user-supplied envFrom can still override on key collision.

### Agent identity

- **iris**: per-minute liveness heartbeat (later throttled back to `*/30 * * * *` after validation).

### Chore

- **ww**: gofmt fix on `gitsync_auth_env_test.go`.

## [0.11.12] — 2026-04-30

`--gitsync*` flag-family closure on `ww agent create`. Six commits flatten the long-form into a single coherent prefix.

### Added

- **ww**: `--gitsync-from-env <USER_VAR>:<PASS_VAR>` (then renamed to `--gitsync-secret-from-env`) — lifts shell vars
  into a per-agent gitSync credential Secret. Closes the .env-driven gap for private-repo gitSync.
- **ww**: `--timeout` default on `ww agent create` bumped from 2m → 5m; recent CR + pod events dumped on timeout for
  actionable diagnostics.

### Changed

- **ww**: `--gitops` → `--gitsync-bundle`. Convention-driven sugar joins the `--gitsync*` family.
- **ww**: `--gitmap` → `--gitsync-map`. The flag is conceptually subordinate to a `--gitsync` entry; the prefix reflects
  the hierarchy.
- **ww**: `--gitsync-from-env` → `--gitsync-secret-from-env`. Symmetry with the backend half.
- **ww**: `--secret-from-env` → `--backend-secret-from-env`. Target type now in the flag name; mirrors
  `--gitsync-secret-from-env`.

## [0.11.11] — 2026-04-30

Backend-credential Secret cleanup on agent delete is default-on. CLI sugar additions for gitOps and persistence.

### Added

- **ww**: `--gitops` short-form on `ww agent create` — convention- driven sugar over `--gitsync` + N `--gitmap` entries.
- **ww**: `--with-persistence` on `ww agent create` — fans out type- derived persistence defaults to every declared
  backend without per-backend `--persist` enumeration.
- **ww**: `--delete-backend-secrets` on `ww agent delete` (later flipped to default-on in this same release).

### Changed

- **ww**: backend-Secret cleanup on `ww agent delete` is default-on; `--keep-backend-secrets` opts out. Same label-gated
  safety regardless: Secrets without `app.kubernetes.io/managed-by=ww` are never touched.
- **ww**: minted backend Secret name drops the `-credentials` suffix. `iris-claude` instead of
  `iris-claude-credentials`. The suffix oversold what's in the Secret (arbitrary envFrom material via
  `--secret-from-env`, not just credentials) and collided visually with the operator-side
  `<agent>-<backend>-backend- credentials` naming for the inline-CR path.

### Documentation

- **bootstrap**: collapsed to a down-and-dirty single-claude walkthrough; dropped EKS / future-platform commitments;
  added a Tear-it-down section.

### Agent identity

- **iris**: removed stale `.echo-1` / `.echo-2` scaffolds.

## [0.11.10] — 2026-04-30

`--auth-from-env` rename and rename-form support on `ww agent create`.

### Added

- **ww**: `--auth-from-env` accepts the `<SRC>:<DEST>` rename form so agent-suffixed shell vars
  (`GITHUB_TOKEN_IRIS:GITHUB_TOKEN`) can land as stable in-container names without the per-agent prefix.

### Changed

- **ww**: `--auth-from-env` → `--secret-from-env` family-wide. Multi- line accumulation supported (multiple
  `--secret-from-env` entries for the same backend merge into one resolver). Old name dropped without deprecation
  aliases.

### Documentation

- **bootstrap**: switched to `--secret-from-env`; demonstrates combined and multi-line forms; uses real iris-suffixed
  shell vars from `.env`.

## [0.11.9] — 2026-04-29

### Added

- **ww**: `--persist-mount <name>=<subpath>:<mountpath>` on `ww agent create` for explicit PVC mount overrides.
  Replace-on- presence — any `--persist-mount` for a backend takes ownership of its full mount list (type-derived
  defaults are skipped).

### Documentation

- **bootstrap**: `--persist-mount` documented in the long-hand block.

## [0.11.8] — 2026-04-29

### Added

- **ww**: `--persist` accepts echo backends with a symbolic `memory` mount.

### Documentation

- **bootstrap**: `--persist` flag documented in Step 3 with a forward-looking concrete example.

## [0.11.7] — 2026-04-29

### Added

- **ww**: `--persist <name>=<size>[@<storage-class>]` on `ww agent create` — provisions a per-backend PVC for
  session/memory persistence (`<agent>-<backend>-data`).

### Agent identity

- **iris**: dropped `sync-test.md` scratch file.

## [0.11.6] — 2026-04-29

### Added

- **ww**: `--gitsync` / `--gitmap` / `--gitsync-secret` long-form on `ww agent create` for per-entry gitSync wiring.

### Fixed

- **operator**: probe selection now uses image-repo-basename instead of the backend's Name. `--backend echo-1:echo` no
  longer gets stamped with `/health/start` 404s because the operator now recognises the underlying image type.

### Documentation

- **bootstrap**: Step 3 focuses on iris with two backends; Step 2 prose aligned with the two-volume workspace.

## [0.11.5] — 2026-04-29

Reverts the workspace-mount-on-harness change from v0.11.4.

### Reverted

- **operator**: workspace volumes no longer mounted on the harness container; they stay scoped to backend containers
  only.

### Fixed

- **echo**: `WORKDIR /home/agent/workspace` to match the other backends.

## [0.11.4] — 2026-04-29

### Fixed

- **harness**: `/health/ready` probe falls back to `/health` on 404 — supports echo backends that don't expose the
  readiness endpoint.
- **operator**: stamped workspace mounts onto the harness container (subsequently reverted in v0.11.5).

## [0.11.3] — 2026-04-29

### Added

- **ww**: `--workspace` flag on `ww agent create` — bind to a WitwaveWorkspace at creation time; equivalent to a
  follow-up `ww workspace bind`.

### Documentation

- **bootstrap**: documented `--gitsync` / `--gitmap` explicit form; collapsed Step 3 to one command per agent.

### Chore

- **scripts**: embedded-chart drift check uses `--checksum` so mtime drift no longer false-positives.

## [0.11.2] — 2026-04-28

Multi-arch container images.

### Added

- **release**: container-image builds now produce `linux/amd64` + `linux/arm64` manifest lists. Fixes
  `ImagePullBackOff: no matching manifest for linux/arm64/v8` on Apple Silicon and AWS Graviton clusters.

### Fixed

- **ww**: `ww operator status` enumerates all three CRDs (WitwaveAgent, WitwavePrompt, WitwaveWorkspace).

### Changed

- **layout**: `.agents/active/` renamed to `.agents/self/`.

## [0.11.1] — 2026-04-28

### Added

- **ww**: `--create-namespace` on `ww operator install` — provisions the target namespace if missing. Parity with
  `ww workspace create` and `ww agent create`.

### Documentation

- **bootstrap**: Step 3 walkthrough for iris/nova/kira deployment.

## [0.11.0] — 2026-04-28

WitwaveWorkspace CRD — shared per-namespace volume / Secret / ConfigMap envelope every agent in the namespace can bind
to. End- to-end CRD + operator + admission webhook + chart + CLI surface.

### Added

- **operator**: WitwaveWorkspace CRD with shared volumes (PVC- backed, per-volume access modes), Secret projection
  (existingSecret pass-through), and ConfigMap-backed file rendering. Inverted-index `Status.BoundAgents` reconciled
  from each agent's `Spec.WorkspaceRefs`. Same-namespace binding only in v1alpha1.
- **operator**: WitwaveWorkspace admission webhook gates the CR shape.
- **operator**: WitwaveAgent grows `Spec.WorkspaceRefs[]` for declarative binding; agent reconcile stamps workspace
  volumes / envFrom / configmap mounts on the pod.
- **operator**: ReadWriteOncePod accepted alongside ReadWriteOnce for single-node clusters (Docker Desktop, kind).
- **ww**: `ww workspace { create, list, get, status, delete, bind, unbind }` subcommand tree.
- **charts**: WitwaveWorkspace CRD bundled, RBAC + webhook config plumbed.

### Fixed

- **mcp-kubernetes**: retry `apply()` and `delete()` on 401 token rotation (#1816, #1817).
- **operator**: defensive logging on workspace-watch and agent-list errors (#1805, #1806, #1810).
- **harness**: re-raise TimeoutError from `_bounded` so callers see timeouts (#1769); consolidate `bus.send` cleanup
  (#1770).
- **codex**: log when `_release_mcp_stack` matches no tracked stack (#1781) or refcount underflows (#1780).
- **shared**: best-effort graceful shutdown for the metrics-server thread (#1818).
- **ww**: guard `Target()` against nil context value (#1800); close stdout pipe when `cmd.Start` fails in credential
  fill (#1796).

### Documentation

- **bootstrap**: new `docs/bootstrap.md` for self-hosting witwave on witwave (the witwave-self ecosystem).
- **workflow**: project formally adopts trunk-based development — commits land directly on main, no feature branches,
  fix or revert immediately on break.

### Agent identity

- **layout**: scaffolded iris, nova, kira directories under `.agents/self/`.

### Skills

- Bake intentional-design checklist into bug-discover, bug-refine, risk, and gap skills.

## [0.10.0] — 2026-04-27

Cycle 17 — operator hardening, observability finishing touches, and security follow-ups across MCP tools, backends, and
the dashboard.

### Added

- **operator**: per-agent PrometheusRule reconciled from chart defaults (#1746); sibling NetworkPolicies for MCP tools +
  dashboard (#1743); dashboard Ingress + auth annotations (#1741); MCPToolSpec CRD additions for security parity
  (#1737); CORS\_\* env stamped on harness (#1748).
- **harness**: `_MD_CACHE_EVICTIONS` exported as Prometheus counter (#1747); `PROMPT_ENV_MAX_BYTES` cap on resolved
  bodies (#1744).

### Fixed

- **gemini**: hot-reload revision gauge registered (#1751); `_pre_tool_use_gate` engine signature corrected (#1724);
  per- logger + tool-audit metrics wired (#1755, #1756); MAX_SESSIONS clamped to ≥1 (#1718); chunk metric increment
  timing (#1721); skip tool_duration histogram for prefix-paired AFC frames (#1727); inbound prompt UTF-8 byte length
  capped at A2A entry (#1730); `mcp_command_args_safe` invoked on MCP config load (#1734).
- **claude**: `mcp_command_args_safe` on MCP config load (#1734); `task_last_success_timestamp` gated on budget_exceeded
  (#1729).
- **codex**: `mcp_command_args_safe` on MCP config load (#1734); `MCP_CONFIG_PATH` realpath allow-list (#1731); observe
  `backend_sqlite_task_store_lock_wait_seconds` (#1753).
- **operator**: backend three-probe model with echo carve-out (#1719); paginate WitwavePromptReconciler ConfigMap GC
  List (#1726) and applyBackendPVCs cleanup List (#1725); paginatedList routed through APIReader (#1738);
  renew_failures_total increment on involuntary lease loss (#1739); MCP tool Service exposes the metrics port (#1722);
  hardened security context + SA fields stamped on MCP tool pods (#1737).
- **harness**: stream-read `/validate` bodies via shared cap helper (#1736); cross-backend timestamp normalised in
  `/trace` proxy sort (#1728).
- **ww**: timeout-free `streamHC` for SSE callers (#1733); prerequisite Service Get bounded by `--timeout` (#1720).
- **mcp**: `/info.features.read_only` honours per-tool env (#1759).
- **dashboard**: CSV formula injection neutralised in `csvEscape` (#1732).
- **shared**: schedule session_stream sweeper + LRU-cap registry insertion (#1735).
- **charts**: unified mcp-tools Service/Pod metrics gate (#1723).

### Documentation

- **harness**: drop unimplemented `POST /triggers/{name}/run` references (#1745).
- **supply-chain**: per-artifact verification recipes + supply-chain overview (#1598 item 5).
- **echo**: three-probe health model added to intentional-non-scope list.
- **layout**: removed stale Docker Compose references — K8s-only platform now.

## [0.9.6] — 2026-04-27

### Added

- **release**: SLSA L3 provenance for ww binaries + embedded chart bridge (#1598 items 2 + 4).

## [0.9.5] — 2026-04-27

### Fixed

- **CI**: docker login alongside helm login for cosign chart sign (#1598).

## [0.9.4] — 2026-04-27

### Added

- **release**: cosign-sign published Helm charts (#1598 item 3).

## [0.9.3] — 2026-04-27

### Fixed

- **CI**: buildx setup before image build — provenance / SBOM prereq (#1598).

## [0.9.2] — 2026-04-27

### Added

- **release**: SLSA provenance + SBOM emitted for every published image (#1598).

### Changed

- **CI**: prettier + markdownlint enforced on changed `*.md` files (#1481). MD018 disabled — `#NNN` issue refs at line
  start are project convention.

## [0.9.1] — 2026-04-27

### Changed

- **ww**: goreleaser `brews` migrated to `homebrew_casks` (#1446).

### Fixed

- **CI**: guard `release.extra_files` against parent-dir globs.

## [0.9.0] — 2026-04-27

Cycle 16 — universal curl installer, supply-chain hardening groundwork, ~15 metrics + observability fixes across harness
/ backends / operator, and broad test coverage across cli / operator / dashboard / shared.

### Added

- **ww**: universal curl installer + post-release validation. Polish parity with uv-class installers — existing-install
  detection, SHELL-aware advice, post-install version exec. `-o yaml` output parity for snapshot commands (#1707).
- **charts**: #1416 harness env vars plumbed as first-class values (#1691); MCP tool env vars documented under
  `mcpTools.*` (#1692).
- **backends**: metric superset parity placeholders for codex + gemini (#1687); `/health/start` probe added to claude /
  codex / gemini (#1686).

### Fixed

- **operator**: admission gates for existingSecret presence + inline- creds RBAC (#1683, #1685); cross-watches gated
  between WitwaveAgent and WitwavePrompt (#1684); WitwavePrompt ConfigMap name made injective (#1676);
  `ObservedGeneration` preserved when spec moves mid-retry (#1677).
- **harness**: `tasks.py` duration metric covers `resolve_prompt_env` (#1675); YAML-list `continues-after` parses
  correctly (#1689).
- **mcp**: surface describe events-fetch failures to callers (#1680); redact stdin-delivered helm values in error
  messages (#1681).
- **codex / gemini**: bound `/mcp` body size on the wire (#1673, #1674).
- **dashboard**: clear stale terminal-failed error on `open()` (#1702); always overwrite `error.value` on
  terminal-failed state.
- **charts**: CI checks for alert↔runbook coverage (#1698) and PrometheusRule metric names declared in source (#1682);
  operator namespace Role monitoring verbs gated on `metrics.enabled` (#1678).
- **CI**: skip SBOM and sign steps in snapshot-install goreleaser run; fetch tag history; shellcheck/gofmt drift in
  curl-installer follow-ups.

### Changed

- **terminology**: replaced "agenda item" with "prompt" across documentation and wire contracts.

## [0.8.2] — 2026-04-26

Brand asset refresh; dashboard test fixture follow-on.

### Added

- **brand**: witwave brand assets bundled.

### Fixed

- **dashboard**: always overwrite `error.value` on terminal-failed state. Test timeouts adjusted after #1605 + #1615
  production changes.

### Documentation

- **README**: witwave logo embedded (then reverted to non-inline form) at top; FUNDING.yml updated with Buy Me A Coffee
  sponsorship link.

## [0.8.1] — 2026-04-26

Patch release re-shipping the artifacts that v0.8.0 missed.

v0.8.0's ww binaries + helm charts published cleanly, but the container-image matrix (harness, claude, codex, gemini,
echo, dashboard, operator, mcp-\*, git-sync) was cancelled when the dashboard's Docker build failed on a vue-tsc type
error. The fake stream fixture in `clients/dashboard/tests/unit/timelineStore.spec.ts` was missing the
`droppedEventCount` and `parseFailureCount` fields added in cycle-1 #1606 and cycle-2 #1634; local vitest runs would
have caught it, but vitest was sandbox-blocked on the cycle commits and the production code still type-checked.

This release re-runs the full matrix with the fixture fixed.

### Fixed

- **dashboard test fixture**: FakeStream now satisfies `UseEventStreamReturn` (#1606 + #1634 follow-on); unblocks Docker
  build → unblocks container image publication.

### Changed

- **CI**: removed `gitleaks-action` workflow. `gitleaks-action@v2` requires a paid license for organization repos;
  relying on GitHub-native secret scanning + Push Protection instead. See commit message for re-enablement options.

### Chore

- **ww**: gofmt re-sort imports after the v0.8.0 module-path rewrite (alphabetical: `spf13/cobra` now sorts before
  `witwave-ai/witwave/...`).

## [0.8.0] — 2026-04-26

First release under the `witwave-ai/witwave` org (transferred from `skthomasjr/witwave` on 2026-04-26). New container
images and helm charts are published to `ghcr.io/witwave-ai/...`; the old GHCR namespace becomes a frozen archive going
forward. The `ww` CLI's update-check now points at the new Releases endpoint. Existing clones can follow GitHub's HTTP
redirect or run `git remote set-url origin git@github.com:witwave-ai/witwave.git`.

Autonomous bug + risk cycle output (74 commits, 73 closed issues across 10 cycles). The work was driven by the develop
skill's discover/refine/approve/implement loop applied to bugs and risks only — gaps, features, and docs phases were
skipped per the session scope. Issues are grouped by component below; full provenance lives in the GitHub issue history
(#1599–#1672) and linked commits.

### Security

- **mcp-prometheus: refuse to start on cloud-metadata bearer** (#1652). When `PROMETHEUS_BEARER_TOKEN` is set and
  `PROMETHEUS_URL` host resolves to a cloud-provider instance- metadata endpoint (169.254.169.254, fd00:ec2::254,
  metadata.google.internal, metadata.azure.com), startup raises — regardless of `PROMETHEUS_ALLOW_PLAINTEXT_BEARER`. The
  metadata IP is privileged regardless of transport.
- **mcp-prometheus: response body redacted from non-200 logs** (#1639). The WARN log emitted on upstream errors no
  longer includes the body snippet (kept the status code + byte count).
- **mcp-helm: validate `--repo` URL scheme on install / upgrade / diff** (#1638, #1664). Rejects file://, javascript://,
  and other non-http(s) schemes that previously slipped past `_reject_flag_like()`.
- **mcp-helm: port-aware allowlist matching for `repo_add`** (#1601). `MCP_HELM_REPO_URL_ALLOWLIST` entries now match
  hostname AND port; bare-host entries match URLs with no explicit port or the scheme's default; `host:port` entries
  require exact match. Backwards compatible for default-port URLs.
- **mcp tool Dockerfiles: HEALTHCHECK switched to exec-form** (#1651). Removes shell-form interpolation of `MCP_PORT` so
  a malformed env value can't escape into a shell context.
- **mcp shared: `mcp-prometheus` added to `DEFAULT_MCP_ALLOWED_COMMANDS`** (#1640). All three shipped MCP tool binaries
  are now accepted by default.
- **claude / shared: bearer-token decode hardened to `errors='strict'`** (#1617). Invalid UTF-8 returns a JSON-RPC 400
  instead of silently coalescing distinct token byte sequences onto the same caller-identity hash.
- **codex: Chromium `--no-sandbox` is now opt-in** via `CHROMIUM_SANDBOX_DISABLED` (#1619). Default-off so the host
  kernel sandbox runs where supported.
- **codex: prompt-size cap (`MAX_PROMPT_BYTES`, default 10 MiB)** rejecting oversized requests at the executor entry
  (#1620). Mirrored in echo at 1 MiB (#1650).
- **claude: `/mcp` body cap streams instead of trusting `Content-Length`** (#1609). New `MCP_MAX_BODY_BYTES` env
  (default 4 MiB); HTTP 413 on overflow with a clean JSON-RPC error body.
- **gemini: `MCP_CONFIG_PATH` realpath-prefix validation** (#1610). Refuses to load files outside
  `MCP_CONFIG_PATH_ALLOWED_PREFIX` (default `/home/agent/`).
- **shared: `prompt_env.substitutions_total` cardinality bounded** (#1668). Dropped the attacker-influenced `var` label;
  per-var detail still surfaces in WARN logs at miss/deny time.
- **ww: `ww config get` redacts secret-key values** (#1646). Mirrors the existing `ww config set` posture; secret keys
  print `<redacted>` to stdout.
- **ww: config Save uses atomic CreateTemp + Chmod + Rename** (#1607, #1654). Bearer tokens never observable at any mode
  but 0o600.
- **ww: brew/git/gh shell-outs wrapped in `context.WithTimeout`** (#1616) and credential env vars stripped before exec
  via `sanitizeShellEnv`.
- **operator: ClusterRole RBAC split for Secrets read/write** resolved at the canonical source (#1613). Scripts/ added a
  drift-check helper.
- **operator: validating webhook emits warning on inline-credential use under restrictive RBAC posture** (#1623).
- **operator chart: monitoring CRDs RBAC gated on `metrics.enabled`** (#1659). No verbs requested when metrics are off.
- **operator chart: CRDs carry `helm.sh/resource-policy: keep` at the canonical source** (#1614, #1647).
  `helm uninstall` no longer cascades into deletion of WitwaveAgent / WitwavePrompt CRs.
- **agent chart: image digest pinning supported across harness, backends, and dashboard** (#1612, #1665). Mirrors the
  existing mcpTools digest support.
- **agent chart: ingress basic-auth empty-htpasswd validation** (#1626). Render fails fast when auth is enabled but no
  htpasswd / existingSecret is provided.

### Reliability

- **claude / codex / gemini: split `/health` (liveness, always 200 once up) from `/health/ready` (readiness, 503 while
  initialising or boot-degraded)** (#1608 claude, #1672 codex + gemini). Operators using K8s readinessProbe should point
  at `/health/ready`; `/health` remains for livenessProbe. The agent chart's backend probes were updated to match
  (cycle-10 follow-on); the harness's own readiness gate now probes backend `/health/ready` (cycle-9 follow-on).
- **claude: bounded sub-app lifespan shutdown wait** via `SUB_APP_SHUTDOWN_TIMEOUT_SEC` (default 10s) (#1618). Faulty
  sub-apps no longer stall pod termination.
- **claude: `SqliteTaskStore.close()` race fix** (#1649). New `_closing` sentinel prevents `_get_conn()` from spawning a
  fresh connection during teardown.
- **codex: `MAX_SESSIONS=0` no longer crashes the LRU eviction path** (#1629). Clamped at parse to `max(1, ...)`.
- **codex: MCP watcher normal-exit drops readiness** (#1630). Mirrors the claude readiness-gate pattern.
- **codex: `backend_task_last_success_timestamp_seconds` gated on `_budget_exceeded`** (#1662). Budget-exceeded
  responses no longer mask as success in the gauge.
- **codex: token-budget check uses `total_tokens` only** (#1600). Dropped the `output_tokens` fallback that
  under-counted prompt
  - cached-input tokens against the cap.
- **gemini: `max_tokens=0` no longer raises ZeroDivisionError** (#1602). Tightened guard to also require `> 0`.
- **gemini: API-key rotation atomicity** (#1621). Build-then-swap the new client; on failure preserve the
  previously-cached one so transient credential blips don't take down the backend.
- **gemini: session-history file unlink races eviction** (#1611). Backpressure branch waits up to
  `_EVICT_BACKPRESSURE_SAVE_WAIT_SEC` (default 30s) on the per- session done-event before attempting removal.
- **gemini: history-save force-split fallback** (#1622). When no safe AFC boundary exists in the trim window, cut at the
  earliest user-role entry so long mid-AFC tails can't keep the on-disk file oversized indefinitely.
- **harness: `A2A_SESSION_CONTEXT_CACHE_MAX` validated at module import** (#1648). Non-int or `< 1` fails fast with a
  CRITICAL log.
- **harness: rate-limited WARN on background-task shed path** (#1644). New `BACKGROUND_SHED_LOG_WINDOW_SEC` (default
  10s). Sustained drops surface to operator logs without spam.
- **harness: shed-path `coro.close()` exception logged** (#1670). Replaces the silent `except Exception: pass` with a
  WARN that carries the source label and exception repr.
- **operator: `teardownDisabledAgent` cleans up NetworkPolicy and MCP tool resources** (#1635). Disabling a CR no longer
  leaves stale per-agent NetworkPolicy + mcp-`<tool>` Deployments to await OwnerRef GC.
- **operator: dashboard reconcile no longer panics when dashboard is disabled** (#1660). Nil-guard mirrors the ConfigMap
  and Service paths.
- **operator: WitwavePrompt status retry refreshes `reconciledGeneration` after re-Get** (#1636). Fixes a one-cycle
  observedGeneration lag under concurrent spec writes + 409 conflicts.
- **operator: List operations on cleanup paths now paginate** via a shared `paginatedList` helper (`Limit=500` +
  `Continue`) (#1656). Bounds memory + apiserver load on namespaces with many agents.
- **operator: leader-election timing flags validated at startup** (#1657). `leaseDuration > renewDeadline > retryPeriod`
  is now enforced; misconfigs fail fast instead of silently deadlocking election.
- **operator: validating webhook enforces port upper bounds** (#1669). Rejects configs whose metrics-port reservation
  (port + 1000) would overflow 1..65535.
- **operator: webhook validating `failurePolicy=Ignore` by default** (#1624). Mutating webhook stays on Fail (sets
  reconciler-critical defaults). New `webhooks.validatingFailurePolicy` knob.
- **operator: WitwaveAgent CR teardown adds IsControlledBy guard on shared manifest CM** (#1599). Foreign-owned CMs are
  no longer deleted on the empty-membership branch.
- **agent chart: `podDisruptionBudget.enabled=true` by default** (#1625). Safe at `replicaCount=1` (PDB delays drain
  until reschedule); behaviour change for new installs only.
- **operator chart: same default flip on `podDisruptionBudget.enabled`** (#1628). Operator is a control- plane
  component; PDBs ship on by default.
- **operator chart: `probes.startup.failureThreshold` raised 30 → 60** (#1627, #1642). 600s grace covers cold-start
  under leader-election + multi-replica.
- **agent chart: MCP tool pods get default resource requests/limits** (#1658). Removes the QoS=BestEffort eviction
  vulnerability.
- **dashboard: SSE reconnect floor raised 50ms → 500ms** (#1615); added `MIN_TRUSTED_SERVER_RETRY_MS=100` for clamping
  suspect server `retry:` hints; added `MAX_CONSECUTIVE_FAILURES=30` terminal-failed state with reset on `open()`
  (#1653).
- **dashboard: SSE per-stream rate cap (200 evts/sec)** with reactive `droppedEventCount` for observability (#1606); new
  `parseFailureCount` for malformed payload visibility (#1634).
- **dashboard: `useChat.loadHistory` clears `loadingHistory` on every error path** (#1633). No more stuck spinner.
- **dashboard: `ConversationsView` watchers stopped in `teardownStream`** (#1661). Fixes stale callbacks firing on
  closed streams during filter switches.
- **dashboard: `seenIds` Set bounded at 5000 entries with LRU-ish eviction** (#1605). Long-lived tabs no longer
  accumulate unbounded dedup state.
- **dashboard: DOMPurify link-rel hook regression test** (#1604). Pins `target=_blank rel="noopener noreferrer"`
  injection so a future `removeAllHooks()` call breaks CI rather than silently regressing the phishing mitigation.
- **ww: TUI modal lifecycle context plumbed end-to-end** (#1631). Quitting the app cancels in-flight
  create/delete/scaffold/send operations instead of letting them dangle.
- **ww: TUI log-tail goroutines drained on cycle/close** (#1663). No more apiserver-connection leak on rapid 'c' presses
  in aggregate mode.
- **ww: TUI send-modal stale-frame draw fix** (#1603). Active flag gates the QueueUpdateDraw.
- **ww: TUI preflight banner rendered before agent list per DESIGN.md KC-4** (#1632).
- **ww: helm-uninstall ctx-cancel observability** (#1655). 60s background waiter logs WARN if the in-flight goroutine
  doesn't settle.

### Changed (operator-visible behaviour)

- **K8s probes: agent + operator chart defaults updated** (see Reliability above). Operators with custom values that
  override `readinessProbe.path` to `/health` should review — the chart now ships `/health/ready` for backends
  post-#1672.
- **`acknowledgeInsecureInline=true` triggers an admission warning in the operator validating webhook** (#1623). Guides
  operators toward `existingSecret` references when the operator's `secretsWrite` permissions are restricted.
- **`MCP_HELM_REPO_URL_ALLOWLIST` entry format extended** (#1601). `host:port` syntax now supported; bare-host entries
  unchanged but only match default-port URLs.

### Operator chart RBAC drift checker

`scripts/check-rbac-drift.sh` added (#1613). Renders the chart ClusterRole and diffs against
`operator/config/rbac/role.yaml` to catch future regressions of the `secretsWrite` split. Manual + CI ready (`chmod +x`
once after pulling).

### Removed

(none)

## [0.7.19] — 2026-04-25

### Added

- **`ww tui` create modal — Secret KEY fields are now combo boxes** with autocomplete suggesting the conventional
  env-var names for the selected backend type. Type "AUTH" → AWS*\*, AZURE*\*, GITHUB_AUTH_TOKEN-style entries surface;
  pick one with ↑↓+Enter or keep typing for a custom name (the suggestions are hints, never constraints). Powered by
  tview's `InputField.SetAutocompleteFunc`; substring match (case- insensitive) so credential names sharing common stems
  are easy to discover.

  Built-in catalog ships per backend type:

  - claude: `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
    `AWS_SESSION_TOKEN`, `AWS_REGION`
  - codex: `OPENAI_API_KEY`, `OPENAI_ORG`, `OPENAI_PROJECT`, `AZURE_OPENAI_API_KEY`, `AZURE_OPENAI_ENDPOINT`,
    `AZURE_OPENAI_API_VERSION`
  - gemini: `GEMINI_API_KEY`, `GOOGLE_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`, `GOOGLE_CLOUD_PROJECT`
  - echo: nothing — popup stays hidden for the no-credentials hello-world case

  User-extensible via a new `[tui.expected_env_vars]` block in `~/.witwave/config.toml`:

      [tui.expected_env_vars]
      claude = ["MY_CUSTOM_VAR"]
      codex  = ["MY_OPENAI_PROXY_KEY"]

  Custom entries MERGE with the built-ins (dedup + sort) — adding your own can never accidentally drop the canonical
  suggestions. Removing built-ins isn't supported yet (block-list semantics would land if anyone asks).

  The autocomplete closure reads `cf.state.backend` on every keystroke, so changing the Backend dropdown updates
  suggestions live for any unfocused KEY field — no rebuild required.

## [0.7.18] — 2026-04-25

### Changed

- **`ww tui` create modal — secrets section is now dynamic per-pair** (Phase 2). Replaces the multi-line "Backend
  secrets" TextArea with a list of editable `KEY` / `VALUE` InputField pairs that grows and shrinks at runtime. Two new
  buttons in the form's button row:

  - `[+ Secret]` appends a fresh empty pair and lands focus on the new pair's KEY field so the user can type
    immediately.
  - `[− Secret]` pops the trailing pair (no-op when empty) and lands focus on the previous pair's VALUE — or on the
    Existing-Secret field when no pairs remain.

  Per-pair removal beyond the tail: clear the row's KEY field; empty-KEY pairs are silently skipped at submit so the row
  effectively disappears from the resulting Secret without needing a tear-down.

  Values prefixed with `$` still mean "lift from shell env" — same convention Phase 1 introduced. Empty value on a
  non-empty KEY is refused with a hint pointing at "clear the KEY to drop the pair."

  On-disk shape changes: `[tui.create_defaults]` now stores secrets as a TOML list of `"KEY=VALUE"` strings
  (`secrets = ["KEY1=value1", "KEY2=$VAR"]`) instead of the previous `secrets_block` multi-line string. Hand-editable,
  round-trips cleanly. Existing config files with the old `secrets_block` key are silently ignored on read; users get
  fresh state on next successful create. Pre-1.0; the migration surface is small.

  resolveTUISecrets walks the typed pairs slice instead of parsing a string block. Submit-time validation: empty KEY =
  drop, empty value on non-empty KEY = error, duplicate KEY across pairs = error, `$VAR` = env-lift with actionable
  error on unset.

## [0.7.17] — 2026-04-25

### Changed

- **`ww tui` create modal — secrets redesign (Phase 1)**. Dropped the Auth mode dropdown entirely. The four old modes
  (none / profile / from-env / existing-secret / set-inline) collapse into two more focused fields:

  - **Existing Secret name (optional)** — single-line. When set, references a pre-built K8s Secret as-is (verified,
    never modified). Wins over the secrets block.
  - **Backend secrets** — multi-line. One `KEY=VALUE` per line. Values prefixed with `$` are lifted from the shell
    environment at submit time; everything else is literal. Empty in both fields = no Secret minted (legitimate for
    echo).

  Examples in the placeholder cover both shapes:

      ANTHROPIC_API_KEY=sk-ant-literal-value
      GITHUB_TOKEN=$GITHUB_PAT     (leading $ → read from shell env)
      CUSTOM_HEADER=hello-world

  Old `[tui.create_defaults]` schema keys (`auth_mode`, `auth_value`) are no longer read. Users with a saved file from
  earlier versions get fresh fallback defaults on next launch and new state on next successful create. Pre-1.0; the
  migration surface is small. The `WW_TUI_DEFAULT_AUTH_MODE` and `WW_TUI_DEFAULT_AUTH_VALUE` env vars are removed; new
  `WW_TUI_DEFAULT_EXISTING_SECRET` env var pins the new field. `WW_TUI_DEFAULT_SECRETS_BLOCK` deliberately not added —
  multi- line values don't pair well with shell env vars; users wanting a pinned set of secrets edit
  `~/.witwave/config.toml` directly.

  Phase 2 — per-row UI with checkbox + env-var dropdown — remains on the roadmap as its own follow-up.

## [0.7.16] — 2026-04-25

### Changed

- **`ww tui` create modal — Auth value field is now a multi-line TextArea** (4 rows tall) instead of a single-line
  InputField. Set-inline mode naturally takes one `KEY=VALUE` per line — no more cramming five pairs into a
  comma-separated single line that scrolls horizontally. Parser accepts BOTH newlines (the natural shape with the
  multi-line field) and commas (back-compat with the earlier single-line shape, and convenient when pasting a
  dotenv-style snippet from another doc); blank lines and trailing separators are trimmed. Placeholder text refreshed to
  a multi- line example so the expected shape is visible on first open. Modal height bumped 30 → 34 to fit the taller
  field without form-internal scroll.

## [0.7.15] — 2026-04-25

### Added

- **`ww agent send --backend <name>`** — bypasses the harness's default routing and dispatches the prompt directly to
  the named backend sidecar via the A2A `metadata.backend_id` hint. Empty flag preserves the existing
  no-metadata-no-routing-hint behaviour so calls without it are bit-for-bit identical to before.
  `agent.SendOptions.BackendID` carries the field for programmatic callers.

- **`ww tui` send modal (`s` on the list)** — keybinding opens a scoped send-message modal for the selected agent. Form
  has a Target dropdown (`(agent — harness routes)` first, then each declared backend), a prompt input with placeholder
  hint, and a scrollable response view that fills the lower half of the modal. Long replies open at the top so the lede
  is visible without scrolling. In-flight Send is guarded by a mutex + sending flag — impatient Enter-mashing can't
  stack parallel goroutines against a hung apiserver proxy. Errors stay inline with an ERROR: marker so the user can
  adjust + retry without re-typing the prompt. Same arrow-key translation the create/delete modals use; ESC and Cancel
  both close cleanly.

  Footer on the list updated to include the new key:
  `↑/↓ · a add · d delete · s send · l logs · r refresh · ↵ details (soon) · q/esc quit`.

## [0.7.14] — 2026-04-25

### Added

- **`--auth-set` — fourth backend-credential mode** alongside the existing `--auth` (named profile), `--auth-from-env`
  (lift from shell), and `--auth-secret` (reference existing Secret). Stamps literal `KEY=VALUE` pairs onto the
  backend's credential Secret at command time. Wired on both `ww agent create` (form `<backend>:<KEY>=<VALUE>` since
  multi-backend per command) and `ww agent backend add` (form `<KEY>=<VALUE>` since the backend is already positional).
  Repeatable per `(backend, KEY)`; duplicate-KEY-within-same-backend is a hard error rather than silent last-write-wins.
  Mutually exclusive with the other three auth modes per backend. SECURITY: command-line values land in shell history +
  ps output; for production tokens prefer `--auth-secret` (pre-create with `kubectl create secret --from-env-file`) or
  `--auth-from-env` (lift from a sourced env file). The minted Secret's `created-by` annotation records key NAMES only —
  values never leak into metadata that `kubectl get secret -o yaml` would surface.
- **`ww tui` create-modal `set-inline` mode** — TUI parity for `--auth-set`. The Auth-mode dropdown grows a fifth option
  (`set-inline`); the Auth-value field accepts a comma-separated list of `KEY=VALUE` pairs, equivalent to one
  `--auth-set <backend>:KEY=VALUE` per pair on the CLI side. Same dup-key rejection + empty-value rejection as the CLI
  parser.

### Documentation

- README backend-credentials section grows from three paths to four; cheatsheet line gains a `--auth-set` example.
- WALKTHROUGH § 3 (Multi-model consensus for real) credentials table updated to four modes; § 5a (Backend Add) gains a
  `--auth-set` snippet showing the no-prefix form. § 9 (What's next) lists the `ww tui` surface for the first time and
  adds the planned `ww agent backend auth set/unset/list/show` subtree to the roadmap.
- README adds a new **Interactive TUI** section covering keymap + the layered defaults (env > saved > fallback) the
  create modal uses.

## [0.7.13] — 2026-04-24

### Added

- **`ww tui` · delete modal (`d` on the list)** — orange-bordered confirmation dialog naming the target agent +
  namespace, with three checkboxes mapping directly to the CLI flags: `Remove repo folder`,
  `Delete ww-managed credential Secret(s)`, and `Purge` (the convenience superset). Ticking `Purge` auto-ticks the two
  granular flags so the form reflects the actual blast radius. Submit invokes `agent.Delete` asynchronously; the list's
  poll loop renders the row disappearing within milliseconds (refreshNow ping) rather than on the next 2s tick.

### Changed

- **`ww tui` · long-form create modal** — the 5-field skeleton grows three more (Auth mode dropdown, Auth value input,
  GitOps repo input). When `--repo` is set, submit runs three sequential phases — `agent.Create` → `agent.Scaffold` →
  `agent.GitAdd` — each with its own banner state ("creating CR…", "scaffolding repo…", "attaching gitSync…") so the
  user sees progress. Failures short-circuit and the error strip names the failing phase plus a CLI command to retry the
  rest from. Modal height bumped 18 → 22 so the new fields fit without form-internal scroll.

- **`ww tui` · `l` opens logs, Enter reserved for details** — re-bound the list keymap so Enter is free for the upcoming
  per-agent details view (status / events / conversation log / send-prompt). `l` takes over the one-shot "tail logs"
  action; matches the k9s convention of lowercase verbs for one-shot actions and Enter for "drill in." Until the details
  view lands, Enter flashes a 3-second hint in the footer naming the agent + pointing at `l` and `ww agent status` from
  the CLI.

### Fixed

- **`ww tui` · ESC in the logs view returns to the list** instead of quitting the app. The app-level `SetInputCapture`
  was catching `KeyEscape` and calling `app.Stop()` before per-page handlers could run; ESC is now page-local (logs view
  → back to list, create / delete modal → cancel). Ctrl-C remains the app-level emergency bail; `q` still quits from
  anywhere.

## [0.7.12] — 2026-04-24

### Changed

- **`ww tui` · logs default to aggregate across all containers** — Enter on a row now opens in "all containers" mode
  rather than dropping straight into harness. Fans out one tail goroutine per real container (harness + each declared
  backend); each writes through an `io.Writer` decorator that prepends `[<container>]` so the interleaved body reads as
  `[harness] routing → echo` / `[echo] received prompt "ping"`. `c` still cycles, but the rotation now starts with the
  aggregate view and then proceeds through each individual container (`all → harness → echo → … → all`). One shared
  cancel context tears every tail down atomically on ESC / cycle — no goroutine leaks on rapid key-mashing. Error
  reporting stamps the failing container name in aggregate mode so you can tell which tail died when the others are
  healthy.

## [0.7.11] — 2026-04-24

### Added

- **`ww tui` · per-agent logs drill-down (Enter on a row)** — replaces the "drill down (soon)" stub with a live
  log-tailing view. Header shows the agent identity + current container + stream status; body autoscrolls new log lines
  from the apiserver's `/logs?follow=true` stream; footer lists the two navigation keys. `c` cycles through the agent's
  containers (harness + each declared backend); ESC cancels the stream and returns to the list with the
  previously-selected row still highlighted. Reuses `agent.Logs` under the hood so buffer size, SinceTime/TailLines, and
  multi-pod fan-in semantics match the CLI exactly. Writes from the log goroutine are copied out of bufio's reusable
  scanner buffer and queued onto tview's UI thread via QueueUpdateDraw — no interleaved rendering. MaxLines capped at
  5000 for multi-hour tails; above that, `kubectl logs --since` is the right tool.

- **`ww agent backend add <agent> <name>[:<type>]`** — completes the backend-lifecycle trio (add / remove / rename) that
  was missing its third leg. Appends a backend to a running agent without the delete+recreate dance that used to lose
  gitSync wiring, team membership, and credentials.

  Reuses the `BackendAuthResolver` + profile catalog from `ww agent create --auth`. The three auth flags on
  `backend add` drop the `<backend>=<value>` prefix because the backend is already named positionally: `--auth oauth`,
  `--auth-from-env VAR[,VAR2]`, `--auth-secret <name>`. Missing credentials on an LLM backend surfaces a warning in the
  preflight banner rather than silently allowing a broken pod.

  Port picking: auto-assigns the first free slot in [8001, 8050]; fills gaps in sparse layouts rather than appending at
  end. CRD `MaxItems: 50` cap caught with a nicer diagnostic than the apiserver's schema-validation blob.

  When the agent has exactly one gitSync wired, also scaffolds `.agents/<…>/.<name>/agent-card.md` (+ behavioural stub
  for LLM backends) to the repo and regenerates `.witwave/backend.yaml` to list the new backend. Routing stays put — new
  backend is present but idle until the user redistributes. Pass `--no-repo-folder` for a CR-only change. 11 fake-client
  tests cover the full matrix (happy path, duplicate-name, unknown type, invalid name, missing agent, dry-run
  non-mutation, auth profile mint, no-auth LLM warning, sparse-port gap fill, inline backend.yaml regeneration,
  50-backend cap).

### Changed

- Drop duplicate `Cloning <repo> …` log line in four repo-touching verbs (`backend add`, `backend remove`,
  `backend rename`, `agent delete`). `cloneOrInit()` already prints it; every caller was re-printing the same message,
  producing doubled log output. No information lost — `scope.repoDisplay` and `ref.Display` resolve to the same string.

### Fixed

- **claude backend: SyntaxError on import at `executor.py:2546`** — #1491's rebind fix placed `global ALLOWED_TOOLS`
  mid-function (inside `settings_watcher`), AFTER earlier reads of the name. Python requires any `global` to come before
  every reference in the same scope, so the module failed to parse at all and every claude sidecar built from the #1491
  merge crash-looped at container start. Hoisted the declaration to the top of `settings_watcher` (just after the
  docstring) and dropped the duplicate at the rebind site. `py_compile` clean; new `claude:latest` image built from this
  release picks up the fix.

## [0.7.10] — 2026-04-24

### Added

- **`ww tui` · `a` to add an agent** — keybinding opens a centered modal form (Name / Namespace / Backend / Team /
  Create namespace if missing). Submit invokes `agent.Create()` asynchronously (Wait=false) so the TUI doesn't freeze;
  the list's poll loop shows the new row appearing and its Pending → Ready transition live. DNS-1123 validation on
  name + team surfaces inline; apiserver errors (AlreadyExists, RBAC denied, etc.) populate an error strip above the
  form so the user can fix and resubmit without retyping. ESC/Cancel closes cleanly; form state is reset on every open
  so a cancelled submission doesn't leak values into the next one. Footer updated:
  `↑/↓ move · a add · r refresh · ↵ drill down (soon) · q/esc quit`.

  Design notes: `AssumeYes=true` because `k8s.Confirm` can't prompt over a tview canvas; banner chatter discarded via a
  local writer so `agent.Create` stdout doesn't leak under the surface. Auth
  (`--auth / --auth-from-env / --auth-secret`) deliberately NOT on the form — typing tokens into a TUI form is the wrong
  UX; users who need credentials stay on the CLI until a richer credential picker lands alongside the drill-down view.

## [0.7.9] — 2026-04-24

### Added

- **`ww tui` — live agent list** (replaces the welcome stub). Polls the apiserver every 2 seconds and renders
  WitwaveAgents across every namespace the caller can read, in a `k9s`-style table with `NAMESPACE`, `TEAM`, `NAME`,
  `PHASE`, `READY`, `BACKENDS`, `AGE` columns. Agents created / deleted / transitioning out-of-band (via another CLI
  session, kubectl, Helm) update in place without a keystroke. Header strip shows cluster + context + a rollup
  (`Ready N · Degraded N · Pending N`); footer shows keybindings. `r` forces an immediate refresh; selection survives
  each snapshot swap by `(namespace, name)` identity so the highlighted row doesn't jump when rows shift above it. Empty
  / no-cluster / fetch-error states all render inline — never a black screen. Drill-down (Enter on a row) is still a
  stub pointing at #1450 — per-agent logs/events/send panels land in a follow-up. `agent.ListAgents()` shipped alongside
  as the render-ready data path shared by CLI and TUI.

- **`ww agent create --auth / --auth-from-env / --auth-secret`** — three repeatable, per-backend credential flags that
  close the last "CLI-only" gap. Previously users had to `kubectl create secret` and `kubectl patch` the CR after
  `ww agent create`; now a Claude agent with an OAuth token or API key is a single invocation:
  `ww agent create iris --backend claude --auth claude=oauth` reads `$CLAUDE_CODE_OAUTH_TOKEN` from the shell, mints a
  ww-labelled Secret in the namespace, and stamps `spec.backends[].credentials. existingSecret` on the CR so the
  operator wires it into the backend container's envFrom at reconcile time. Profiles ship for claude (`api-key`,
  `oauth`); more per-backend profiles and Vertex/Bedrock shapes land as follow-ups. Pre-existing Secrets are referenced
  verbatim via `--auth-secret` (verified, never modified). Arbitrary env vars are liftable via `--auth-from-env` for
  custom setups not covered by a named profile.

- **`ww agent team {join, leave, list, show}`** — first-class CLI surface for the operator's existing team-membership
  plumbing. Team membership is the label `witwave.ai/team` on the WitwaveAgent CR; the operator reconciles one
  `witwave-manifest-<team>` ConfigMap per distinct value and mounts it at `/home/agent/manifest.json`. Verbs are a pure
  label patch — no CRD schema change, no pod restart. `join` is idempotent for same-team joins and explicit about
  cross-team moves (was → now); `leave` drops the label so the agent falls back to the namespace-wide manifest; `list`
  renders a tree of teams → members (with an `(ungrouped)` bucket); `show` prints an agent's team + sorted teammates.
- **`ww agent create --team <team>`** — stamp `witwave.ai/team=<team>` at creation time. Avoids the race where a
  follow-up `team join` briefly drops the agent into the namespace-wide manifest before landing the label. Low-key flag:
  no default, not promoted in onboarding docs.

### Changed

- **`ww agent list` now defaults to cluster-wide scope** (was: context namespace only). The `kubectl get pods -A` idiom
  most operators reach for anyway is now the default; narrow to a single namespace with `--namespace`. The NAMESPACE
  column is always shown regardless of scope so grep/sort pipelines work uniformly. `-A` is preserved for kubectl parity
  but is now functionally redundant. DESIGN.md NS-3 updated to codify the read-verb carve-out from NS-1's context-first
  resolution.
- DESIGN.md gains a **TEAM-1..5 rules block** codifying the team-membership contract: label-based (not a CRD field), no
  default team, per-namespace scope, operator-owned cleanup, `--team` deliberately not a prominent flag.
- README.md agent-cheatsheet backfilled to cover the full verb surface (git, backend, team, delete with `--purge`), plus
  the `default` → `witwave` namespace fallback correction (was stale since 0.7.8) and a `--create-namespace` mention
  that was missing from the prose.

## [0.7.8] — 2026-04-24

### Added

- **`ww agent create --create-namespace`** — mirrors `helm install --create-namespace`. Provisions the target namespace
  before the CR apply when it doesn't exist (labelled `app.kubernetes.io/managed-by: ww` so teardown tooling can tell
  ww-created namespaces from hand-authored ones); no-op otherwise. Lets a virgin cluster go zero-to-agent in a single
  invocation.
- **`ww agent delete --remove-repo-folder`** — clones the (single) wired gitSync repo, `git rm -r`s the agent's
  `.agents/<…>/` subtree, commits, pushes. Runs BEFORE the CR delete so a repo-side failure leaves cluster state intact
  and the user can retry. Hard- fails on multi-gitSync ambiguity; soft-skips when no gitSync is wired.
- **`ww agent delete --delete-git-secret`** — after the CR is gone, reaps every ww-managed credential Secret referenced
  by the CR's gitSyncs[]. User-created Secrets preserved via the managed-by label gate.
- **`ww agent delete --purge`** — convenience flag: `--remove-repo-folder --delete-git-secret`. For decommissioning an
  agent permanently in one command.
- **End-to-end walkthrough** (`clients/ww/WALKTHROUGH.md`) — zero-to- gitOps-wired-multi-backend-agent narrative with
  every verb exercised. Long-form flags, multi-line snippets, copy-pasteable throughout.
- **Smoke Phase 5 (gitOps round-trip)** in `scripts/smoke-ww-agent.sh` — gated on `WW_SMOKE_GITHUB_REPO`. Exercises
  scaffold → create multi-backend → git add → rename → remove `--remove-repo-folder` → delete `--purge` end-to-end
  against a real repo.
- Fake-client unit tests for every CR-mutation verb (`GitAdd`, `GitList`, `GitRemove`, `BackendRemove`, `BackendRename`,
  `Delete` including all its new cleanup modes).

### Changed

- **Default namespace is now `witwave`** (was `default`). When neither `--namespace` nor the kubeconfig context pins
  one, every `ww agent *` verb falls back to `witwave` via the new `agent.DefaultAgentNamespace` constant. Rationale:
  ww-managed resources benefit from a dedicated blast radius, and landing in `default` by accident invites cross-tenancy
  incidents on shared clusters. Breaks kubectl parity by design — see DESIGN.md NS-1.
- **Namespace-source log line** — the `Using namespace: <ns> (<source>)` banner now distinguishes
  `(from kubeconfig context)` from `(ww default)` so operators can tell an inherited namespace from a quiet fallback
  (DESIGN.md NS-2).
- DESIGN.md NS-1/NS-2 rewritten; new NS-5 codifies the `--create-namespace` contract.

### Unlocks

Virgin cluster + virgin repo to a fully-wired agent, then full teardown, in flat flags:

```bash
ww agent create consensus \
    --namespace witwave \
    --create-namespace \
    --backend echo-1:echo \
    --backend echo-2:echo

ww agent scaffold consensus --repo <you>/my-witwave-config \
    --backend echo-1:echo \
    --backend echo-2:echo

ww agent git add consensus \
    --namespace witwave \
    --repo <you>/my-witwave-config \
    --auth-from-gh

# Later, when you're done:
ww agent delete consensus \
    --namespace witwave \
    --purge \
    --yes
```

## [0.7.7] — 2026-04-23

### Added

- **`ww agent backend remove <agent> <backend>`** — drops a backend from `spec.backends[]`, regenerates the inline
  `spec.config` backend.yaml when ww owns it (agents: list + routing no longer reference the removed entry), and refuses
  to remove the last backend (CRD minItems: 1). Pass `--remove-repo-folder` to also delete the corresponding
  `.agents/<…>/.<backend>/` folder from the gitSync repo and rewrite the repo's backend.yaml to drop the removed entry —
  one atomic commit, one push, same auth story as `ww agent scaffold`.
- **`ww agent backend rename <agent> <old> <new>`** — renames a backend atomically across the CR, harness + per-backend
  gitMappings, inline backend.yaml, AND the gitSync repo. The repo-side move uses git's native rename detection
  (`git mv`), regenerates the repo's `.witwave/backend.yaml` with the new name, and pushes in a single commit.
  `--no-repo-rename` skips the repo phase. Refuses on DNS-1123 violations, same-name no-ops, and collisions with an
  existing backend of the target name.
- Repo-side cleanup + rename for both verbs is best-effort from the user's perspective: the CR update lands first, so a
  push failure prints a manual-recovery recipe instead of reverting cluster state.

### Unlocks

Lifecycle management on multi-backend agents without hand-editing either the CR or the repo:

```bash
ww agent create consensus --backend claude --backend codex --backend echo
ww agent backend rename consensus echo smoke                   # echo → smoke
ww agent backend remove consensus smoke --remove-repo-folder   # drop it cleanly
```

## [0.7.6] — 2026-04-23

### Added

- **Multi-backend agents** — `ww agent create` and `ww agent scaffold` both gain a repeatable `--backend` flag. Two
  shapes accepted per entry:

  - `<type>` — name = type (e.g. `--backend claude`), the single- backend shortcut
  - `<name>:<type>` — explicit name + type pair (e.g. `--backend echo-1:echo --backend echo-2:echo`), required when two
    backends of the same type must coexist on one agent Each declared backend gets a distinct container name, distinct
    port (8001, 8002, …), and a distinct folder under `.agents/<agent>/.<name>/` in the gitOps repo. The generated
    `backend.yaml` enumerates every backend under `agents:` and routes every concern (a2a, heartbeat, jobs, tasks,
    triggers, continuations) to the **first** backend by default — operators redistribute routing by editing the file
    post-scaffold. This unlocks the multi-model consensus pattern the framework has always supported at the CRD level
    but that the CLI couldn't express until now:

  ```
  ww agent create consensus --backend claude --backend codex
  ```

### Backward compat

- `ww agent create hello` (no flags) and `ww agent scaffold hello --repo owner/repo` continue to produce a single
  default-echo backend — identical CR + repo output to 0.7.5.
- `--backend echo` (single bare type) still works for users who don't need multi-backend naming.
- `ww agent git add` needed no changes — its mapping generator already walked `spec.backends[]` by name, so
  multi-backend agents auto-derive per-backend gitMappings on attach.

## [0.7.5] — 2026-04-23

### Changed

- **`ww agent git add` gitSync default name now derives from the repo.** Previously the default `gitSyncs[].name` was
  the hardcoded label `witwave`, producing `/git/witwave/` on the pod regardless of which repo was wired. The new
  default sanitises the repo's basename to DNS-1123 (lowercase, `.`/`_`/`+` → `-`, trim hyphens), so
  `--repo skthomasjr/witwave-test` produces a gitSync named `witwave-test` and a filesystem at `/git/witwave-test/…` —
  matching what the user typed. Pass `--sync-name <name>` explicitly when wiring two repos with the same basename, or
  two branches of the same repo. The literal `witwave` label remains as a terminal fallback (exposed as
  `FallbackGitSyncName`) when sanitisation produces an empty string.
- **`ww agent git remove` auto-selects the sole gitSync.** When the agent has exactly one sync configured and
  `--sync-name` isn't passed, remove picks that one automatically. Zero → "nothing to remove" error. Multiple → refuse
  with the list of names so the caller can disambiguate. Eliminates the "what was that sync-name I used?" round-trip via
  `git list`.

## [0.7.4] — 2026-04-23

### Fixed

- **Operator git-sync wiring was broken for CR-based gitOps.** The mapping helper anchored rsync sources at
  `/git/<gs.Name>/<src>` but the init + sidecar containers never told git-sync to symlink at that path. git-sync v4
  defaults `--link` to `HEAD`, so the actual symlink lived at `/git/HEAD/` and every mapping hit ENOENT → `git-map-init`
  crash-looped indefinitely. Fix: pass `--link=<gs.Name>` in both args builders so the init's and sidecar's symlink
  names match the path the helper constructs.

## [0.7.3] — 2026-04-23

### Added

- **`ww agent scaffold <name> --repo <…>`** — seeds a ww-conformant agent directory structure on a remote git repo using
  the user's existing system git credentials. No ww-managed credential store; auth resolution walks env tokens →
  `gh auth token` → `git credential fill` → ssh-agent in that order. Empty-repo bootstrap is handled (go- git's
  `PlainInit` path). Phase 1 of the gitOps wiring plan.
- **Scaffold seeds hourly `HEARTBEAT.md`** by default — gives every scaffolded agent a self-exercising proof-of-life
  signal from the moment it's wired up. Pass `--no-heartbeat` to opt out. Documented exception to DESIGN.md SUB-4; every
  other dormant subsystem remains absent.
- **Scaffold branch auto-detection** — `--branch` defaults to empty; scaffold queries the remote's HEAD symref
  (`git ls-remote --symref`) and uses the repo's real default branch. Covers `master`/`develop`/ `trunk` without
  requiring the flag. Falls back to `main` on empty repos.
- **Scaffold merges on existing agents** — re-running `ww agent scaffold <existing>` no longer refuses. Missing files
  land; identical files are silent; drifted files are **preserved** (kubectl-apply-style merge). `--force` overwrites
  drifted files only — never touches user-added content outside the skeleton list.
- **`ww agent git {add,list,remove}`** — Phase 2 gitOps verbs. `git add` attaches a gitSync sidecar +
  harness/per-backend gitMappings to an existing WitwaveAgent CR. Three mutually-exclusive auth paths:
  `--auth-secret <name>` (reference pre-created K8s Secret, production), `--auth-from-gh` (mint from `gh auth token`,
  dev laptops), `--auth-from-env <VAR>` (mint from a named env var, CI/CD / .env). ww-minted Secrets carry
  `app.kubernetes.io/managed-by: ww` so `remove --delete-secret` can distinguish them from user-managed Secrets and
  refuse to clobber the latter.

### Fixed

- **Release pipeline built the operator with `DefaultImageTag=v<ver>`** (the raw `github.ref_name`) while
  `docker-metadata-action` published images with the `v` stripped. The operator then rendered pods that requested e.g.
  `git-sync:v0.7.2` and got GHCR 404 → ImagePullBackOff. `.github/workflows/release.yaml` now derives a stripped version
  (`${GITHUB_REF_NAME#v}`) and passes that as the `VERSION` build-arg. Non-tag runs (branch pushes) have no `v` prefix
  so the strip is a no-op.

## [0.7.2] — 2026-04-23

### Changed

- **Harness watchers go quiet when their directories are absent.** The five optional subsystems (jobs, tasks, triggers,
  continuations, webhooks) used to INFO-log `"<name> directory not found — retrying in 10s"` every 10 seconds forever
  when content was missing. That's 30 lines/minute of noise on a hello-world agent that legitimately uses none of those
  subsystems. Missing-directory logs now fire at DEBUG (visible under `-v`, silent by default). The _missing → present_
  transition — when content actually materialises, e.g. via a gitSync pull or a later ConfigMap mount — is preserved as
  a single INFO line so operators see the moment a subsystem comes online.

  The readiness gate (`/health/ready`) is unchanged: it continues to depend on backend routing config, not on
  optional-subsystem content. A dormant agent is now correctly both quiet AND schema-Ready.

### Added

- **DESIGN.md — SUB-1..4** codify the "file-presence-as-enablement" architectural property. An agent's enabled
  subsystems are expressed through content on disk under `.witwave/`, not through CRD fields. The absence of content is
  a normal, expected state that means "this agent intentionally does not use this subsystem." Future CLI verbs that
  enable a subsystem (e.g. `ww agent add-job <file>`) will do so by materialising content — no CRD bit-flipping, one
  source of truth.
- **`harness/test_run_awatch_loop_logging.py`** — 3 tests covering the dormant-directory contract: DEBUG-only on every
  miss, INFO exactly once on transition missing → present, and no transition-INFO when the directory exists on the first
  iteration (boot).

## [0.7.1] — 2026-04-23

### Fixed

- **`ww agent create` produced unhealthy pods.** The CR builder put both the harness and backend sidecar on port 8000.
  Pods share one network namespace, so one container's readiness probe hit the other's HTTP server and failed. Fixed by
  offsetting backends to 8001-8050 (one port per CRD-allowed backend slot). Codified as DESIGN.md PORT-1..4.
- **Harness never flipped Ready without an inline routing config.** The minimal CR from `ww agent create` didn't include
  `.witwave/backend.yaml`, so `/health/ready` stayed 503 with reason `no-backends-configured` (harness/main.py:524-534).
  The builder now stamps an inline config entry rendering a single-backend routing YAML that points the harness at the
  sidecar.

### Added

- **`ww operator install --if-missing`** — new flag that makes install idempotent. When the operator is already
  installed, logs a one-line no-op instead of refusing with `ErrPreflightRefused`. Useful for "ensure the operator is
  here" flows in scripts and CI.
- **`scripts/smoke-ww-agent.sh`** self-heals via `--if-missing` if the operator isn't installed when the smoke begins.
  Smoke is now truly turn-key: just `./scripts/smoke-ww-agent.sh` against any cluster.

### Design rules

DESIGN.md gains **PORT-1..4** codifying the agent-pod port contract: harness on 8000 (hard-coded), backends on 8001-8050
(CRD cap fits the range exactly), metrics on 9000 (dedicated listener), callers may override via explicit CR fields but
ww's builder enforces PORT-1..3 on generated CRs.

## [0.7.0] — 2026-04-23

### Added

- **`ww agent` subtree** — new command family for managing WitwaveAgent custom resources from the CLI. Closes the
  hello-world loop: a new user can go from zero to a working agent round-trip in two commands
  (`ww operator install && ww agent create hello && ww agent send hello "ping"`) with no API keys required.
  - `ww agent create <name>` — apply a minimum-viable WitwaveAgent CR. Defaults to the echo backend (no credentials
    required). Waits up to `--timeout` (default 2m) for the operator to report Ready; `--no-wait` opts out.
    `--backend echo|claude|codex|gemini` selects the backend; `--dry-run` renders the preflight banner and exits.
  - `ww agent list [-A]` — kubectl-style table with phase, ready count, backends, age. `-A` lists cluster-wide.
  - `ww agent status <name>` — curated describe: phase, ready replicas, backend summary, last-5 reconcile history.
  - `ww agent delete <name>` — deletes the CR; the operator cascades pod/Service cleanup via owner refs.
  - `ww agent send <name> "<prompt>"` — A2A `message/send` round-trip via the Kubernetes apiserver's built-in Service
    proxy. Works with any ClusterIP Service (no port-forward lifecycle, no external LoadBalancer required). `--raw`
    prints the full JSON-RPC envelope.
  - `ww agent logs <name>` — multi-pod container log streaming. Default container `harness`; `-c <name>` for
    backend/sidecar containers.
  - `ww agent events <name>` — scoped event snapshot: CR events + events on pods matching
    `app.kubernetes.io/name=<agent-name>`. `--warnings`, `--since`.
- **DESIGN.md — namespace rules NS-1..4** codify tenant-subtree namespace handling: default to the context's namespace
  with fallback to `default` (NS-1), always print the resolved namespace (NS-2), `-A` only on read verbs (NS-3),
  `create` exempt from the "explicit `-n` required for mutations" discipline for hello-world ergonomics (NS-4).

### Implementation notes

- Package layout mirrors `internal/operator/`: one file per concern (create, list, status, delete, send, logs, events)
  plus pure helpers (types, defaults, validate, build). Uses dynamic client + `unstructured.Unstructured` rather than a
  typed generated client — same pattern as `internal/operator/install.go`, avoids cross-module dependency on
  `operator/api/v1alpha1`.
- 30+ test assertions cover pure-function surface: DNS-1123 name validation + 50-char length cap, image-ref resolution
  (release / dev / empty versions, port-in-registry edge case), namespace precedence, CR builder invariants.

## [0.6.0] — 2026-04-23

### Added

- **`echo` backend** — a fourth backend image (`backends/echo/`) that ships as a zero-dependency stub A2A server.
  Returns a canned response quoting the caller's prompt; requires no API keys or external services. Serves two purposes:
  (1) the hello-world default for `ww agent create` so a new user can deploy a live agent with "access to a Kubernetes
  cluster and the CLI" as the only prerequisites, and (2) a reference implementation of the common A2A backend contract
  — demonstrates the dedicated-port metrics listener, the common `backend_*` metric baseline, and the
  contract-conformance pytest template for future backend types. See `backends/echo/README.md` for the in-scope vs
  intentional-non-scope list.
- **Release matrix** (`.github/workflows/release.yaml`) now publishes `ghcr.io/witwave-ai/images/echo` on every tag.
- **Chart integration** — `charts/witwave/values.yaml` defines proportionally small resource defaults for echo (~1/10th
  the envelope of an LLM-backed sidecar) and includes a commented `backends[]` example.
  `operator/config/samples/witwave_v1alpha1_witwaveagent.yaml` and the operator chart README now reference echo as a
  valid backend.
- **Events schema** (`docs/events/events.schema.json`) extended the `HookDecision.backend` and `AgentLifecycle.backend`
  enums to accept `echo` — prevents runtime validation rejection of echo-sourced events.
- **Dashboard** — `BackendType` now accepts `echo`; `BackendBubble.vue` and `tokens.css` carry a neutral slate palette
  entry for echo (`--witwave-brand-echo`), visually distinguishing it from the vendor-branded LLM backends.

## [0.5.8] — 2026-04-20

### Added

- **`ww tui` subcommand** (#1450 stub). Launches an interactive terminal UI: welcome banner, "what's coming" bullets,
  tracking-issue pointer, and live confirmation of the target kubeconfig context (cluster, context, namespace).
  Kubeconfig resolution is best-effort — if it fails the TUI still launches and shows a "No cluster configured"
  diagnostic in place of the context block. Exit with `q`, `esc`, or `ctrl-c`. No feature panels yet; the point of this
  release is to establish the framework. `--kubeconfig` / `--context` / `--namespace` flags mirror the `ww operator *`
  surface.

### Changed

- **TUI framework locked in as `rivo/tview`.** Shipped the stub on `charmbracelet/bubbletea` in one intermediate commit,
  then switched to tview before release on reflection that the long-term UX target is k9s-style (agent list → drill in →
  watch logs/events/sessions), and k9s runs on tview. Matching the framework means users who know k9s carry muscle
  memory across for free. The stub is small enough that the swap was nearly free; the same swap after real feature
  panels shipped would have been expensive.
- **Competitive landscape doc updated** to reflect fresh research on OpenClaw (20+ chat-platform integrations, macOS
  menu-bar companion with voice wake, TypeScript/Node.js implementation, MIT license, calendar-versioned release
  cadence) and to capture the "witwave is OpenClaw for teams with Kubernetes clusters" positioning frame in the
  Reference Products entry. Marked OpenClaw explicitly as "primary open-source competitor" in the section heading and
  restructured Relative-standing into explicit differentiator lists (5 in witwave's favor, 4 in OpenClaw's).

### Deps

- Added: `github.com/rivo/tview`, `github.com/gdamore/tcell/v2` (tview + the low-level terminal library it builds on).
- Net go.sum reduction — tview's transitive graph is lighter than the Charm ecosystem chain we temporarily added and
  then removed.

## [0.5.7] — 2026-04-20

Docs-only release. No code changes, no behaviour changes. Closes out the doc audit + prettier/markdownlint conformance
work that surfaced during session wrap-up.

### Fixed

- Documentation audit across the 8 most-read markdown files (README, SECURITY, CHANGELOG, operator + chart READMEs, ww +
  dashboard READMEs, runbooks): 12 stale-version / broken-link / wrong- endpoint / docs-vs-code-drift issues corrected.
  Notably: README Helm-install chart version 0.5.2 → 0.5.6; `ww operator status` sample output switched to `<X.Y.Z>`
  placeholders so it no longer goes stale every release; `docs/runbooks.md` `/tool-audit` → `/trace?decision=deny` (the
  former endpoint doesn't exist); `harness/README.md` AGENT_NAME default corrected to `witwave` (code default, not the
  documented `local-agent`).
- Table column realignment on the `tools/kubernetes/README.md` Tools table after the `read_secret_value` row was added
  in commit 423ae13.

### Changed

- Applied `prettier --write` + `markdownlint` across all 8 audit-pass files. Respects the repo's `.prettierrc.yaml`
  (proseWrap: always, printWidth: 120) and `.markdownlint.yaml` (MD013 line length, MD034 bare URLs, MD040 fenced-code
  language tags, MD051 link fragments). Largest diffs come from reflowing paragraphs that had been manually wrapped at
  ~80 chars; no content change beyond the six markdownlint fixes listed in the commit.
- New request filed: #1481 (enforce markdown linting in CI). The tools sat as standards-documents rather than gates,
  which is how the drift accumulated. Tracking issue captures the design trade-offs (pre-merge vs main-only scans,
  repo-wide cleanup vs incremental enforcement).

## [0.5.6] — 2026-04-20

Follow-up to v0.5.5. Three real changes — the LLM-billing defensive work, a cosign verify-recipe fix surfaced during
v0.5.5 validation, and the chart ↔ operator migration documentation.

### Added

- **A2A retry-policy guard** (#1457): new `A2A_RETRY_POLICY=fast-only|always|never` env (default `fast-only`) +
  `A2A_RETRY_FAST_ONLY_MS` threshold (default 5000ms). Under the default, 5xx responses that came back AFTER the
  threshold are refused instead of retried — the theory being that a slow 5xx almost always means the backend's LLM call
  ran to completion and only failed on the return path, so retrying would bill the prompt a second time.
  `harness_a2a_backend_slow_5xx_no_retry_total{backend,status}` counts every refused retry. Policy `always` restores
  legacy behaviour; `never` is the strictest no-double-bill posture.
- **Outer-timeout cancellation observability** (#1457): structured WARN on `asyncio.TimeoutError` with `session_id`,
  `backend`, `elapsed_seconds`, `prompt_bytes`, `trace_id` — the fields operators need to audit LLM billing for
  potential double-charges. New dedicated counter `harness_task_outer_timeout_cancel_total{backend}` distinct from the
  generic `harness_tasks_total{status="timeout"}`.
- **Chart ↔ operator migration documentation** (#1478): full seven-step procedure in `operator/README.md`, pointer in
  `charts/witwave/README.md`. Covers PVC preservation, CR authoring with `existingClaim`, verification, and the explicit
  "pick one" constraint.
- **Reduced-RBAC footgun callout** (#1461 Option A): explicit ⚠ callout in the operator chart README naming the
  `rbac.secretsWrite: false` + inline credentials incompatibility that today produces a silent reconcile loop, with
  concrete `existingSecret` guidance and a forward-reference to #1461 for the apply-time surfacing work.

### Fixed

- **Cosign verify recipe in SECURITY.md**: images strip the leading `v` from release tags (docker/metadata-action@v5
  default semver normalisation), so `v0.5.5` in the image path returned MANIFEST_UNKNOWN. Recipe now uses the bare
  `0.5.5` tag and includes an inline comment explaining the convention.
- **ConnectionError propagation** in `harness/backends/a2a.py`: added a narrow `except ConnectionError: raise` above the
  generic exception handler so our deliberate error surfaces (slow-5xx guard, response-size cap) propagate with their
  intended metric labels instead of being reclassified as `"unexpected error"`.

### Follow-ups tracked

- **#1479** Idempotency-Key header on A2A retries for backend-side dedupe (closes the remaining fast-5xx double-bill
  window; needs backend cooperation).
- **#1480** A2A cancellation verb (closes the outer-timeout window; blocked on upstream A2A spec work).
- **#1461** Options B (reconciler short-circuit) and C (admission webhook) remain deferred as the security-posture UX
  improvements they are, not bugs.

## [0.5.5] — 2026-04-20

Substantial follow-up to v0.5.4 — ten issues closed across security, observability, operator scale-readiness, and docs.
No user-visible behaviour changes on the happy path; all additions are opt-in or silent hardening.

### Added

- **Cosign keyless signing on every container image** (#1460). All ten images published under
  `ghcr.io/witwave-ai/images/*` on a tag release are now signed via Sigstore's OIDC flow — no long-lived signing key in
  the repo. Verification is opt-in; `docker pull` / `helm install` / `ww operator install` continue to work identically.
  See SECURITY.md § Verifying signed release artefacts for the `cosign verify` recipe.
- **Gitleaks pre-merge secrets scan** (#1462). New workflow `.github/workflows/ci-secrets.yml` runs on every PR + main
  push plus a weekly cron sweep. Allow-list lives at `.gitleaks.toml`; policy is zero tolerance for real secrets.
- **Four new Prometheus alerts** (#1465, #1467, #1469, plus #1466): `WitwavePVCFillWarning` + `WitwavePVCFillCritical`
  (70% / 90% kubelet_volume_stats thresholds), `WitwaveA2ALatencyHigh` (p99 harness → backend latency),
  `WitwaveEventValidationErrors` (non-zero schema validation failure rate), and `WitwaveWebhookRetryBytesHalfFull`
  (early warning before retry-bytes shedding begins). All eleven alerts (five existing + six new across this release)
  now carry `runbook_url` annotations pointing at `docs/runbooks.md` (#1468).
- **Webhook retry-bytes in-flight gauges** (#1466). `harness_webhooks_retry_bytes_in_flight{subscription}`,
  `harness_webhooks_retry_bytes_in_flight_total`, and `harness_webhooks_retry_bytes_budget_bytes{scope}` — the gauge
  signal that was missing when the shed counter was the only observable retry-bytes signal.
- **Operator leader-election flag surface + metric** (#1475). New `--leader-election-lease-duration` /
  `--leader-election-renew-deadline` / `--leader-election-retry-period` flags exposed by the operator and plumbed
  through `charts/witwave-operator/values.yaml`. New metric `witwaveagent_leader_election_renew_failures_total` declared
  for the alert-key slot; wiring to the renewal-error hook is a follow-up.
- **CRD `MaxItems` / `MaxProperties` caps** (#1471) on `WitwaveAgent.spec.Backends`, `GitSyncs`, `Config`,
  `PodAnnotations`, `PodLabels`, and `WitwavePrompt.spec.AgentRefs` — apiserver-side rejection of pathological CRs
  before they hit etcd's 1MB object-size ceiling.
- **Credential-watch secondary indexer** (#1474) — `WitwaveAgentCredentialSecretRefsIndex` field indexer narrows a
  Secret rotation's enqueue set to exactly the agents that reference it. At 100+ agents + bulk rotation, eliminates the
  reconcile thundering herd the old full-List mapper produced.
- **`checksum/config` + `checksum/manifest` pod annotations** on the harness Deployment (#1476). `kubectl edit cm` /
  `helm upgrade` with ConfigMap-only changes now roll the pod, instead of silently no-op'ing from the pod's perspective
  on config files the backends_watcher doesn't hot-reload.
- **CHANGELOG.md** (#1472) at the repo root, format follows Keep a Changelog 1.1.0. Backfilled entries for v0.4.0 →
  v0.5.5.
- **Token + secret rotation procedures** in SECURITY.md (#1463, #1464). Covers `HOMEBREW_TAP_GITHUB_TOKEN`
  (release-to-tap PAT; 90-day rotation cadence) and `SESSION_ID_SECRET` (MCP session-ID HMAC binding; two-secret
  grace-window rotation).
- **Event schema versioning policy** in docs/events/README.md (#1473). Documents additive-vs-breaking bump rules, compat
  windows across major-version transitions, and the subscriber contract.
- **`operator/MIGRATION.md`** (#1470) — CRD deprecation policy (2 minor-version served-version overlap),
  conversion-webhook architecture, manual `jq`-based fallback, test matrix contract. Sets expectations now so the first
  `v1alpha1` → `v1beta1` transition doesn't surprise anyone.
- **`docs/runbooks.md`** (#1468) — on-call playbook per alert.

### Fixed

- Gitleaks config schema (`.gitleaks.toml`) — original commit used `[[allowlist]]` (array of tables) syntax from a newer
  gitleaks release; gitleaks 8.24 bundled by `gitleaks-action@v2` wants a single `[allowlist]` map. Config refactored to
  match.

## [0.5.4] — 2026-04-20

### Fixed

- Operator startup log: "Failed to initialize metrics certificate watcher" now reads as a proper verb phrase rather than
  the grammar-broken "to initialize …" that made grep patterns and structured logging noisier. Duplicate `err` field in
  the same log call removed (closes #58ec91b in part).
- `charts/witwave-operator/Chart.yaml` `home:` URL retargeted from a non-existent `witwave-ai/witwave-operator` repo to
  the canonical `skthomasjr/witwave` source. Helm clients and artifacthub now render a working link.

### Changed

- `operator/README.md` and `charts/witwave/README.md` status blurbs updated to reflect shipped reality — the "first
  pass" / "work in progress" notices were written before the v0.4 chart releases and the operator's CRD + ww-CLI
  plumbing all landed.

## [0.5.3] — 2026-04-20

### Added

- `ww operator logs` — tails pods matching `app.kubernetes.io/name=witwave-operator` in the operator namespace.
  `--tail N` (default 100), `--since DUR`, `--no-follow`, `--pod NAME`. Scanner buffer bumped to 1 MiB so
  controller-runtime's long structured-log lines don't truncate.
- `ww operator events` — renders three event sources merged into one sorted table: events on `WitwaveAgent` CRs
  (cluster-wide), events on `WitwavePrompt` CRs, and events in the operator's own namespace (covers pod scheduling
  failures, image-pull errors, crash loops). `--watch` / `-w`, `--warnings`, `--since DUR` (default 1h).

## [0.5.2] — 2026-04-20

### Fixed

- `ww operator status` now reports the real chart version and appVersion — embedded chart's `Chart.yaml` is rewritten by
  a goreleaser pre-hook (`scripts/bump-embedded-chart-version.sh`) before `go:embed` bakes the binary, so the shipped ww
  reports the release tag instead of the canonical `0.1.0` placeholder on main.

## [0.5.1] — 2026-04-20

### Fixed

- `ww update` on Homebrew installs now runs `brew update` before `brew upgrade ww` and verifies via
  `brew list --versions ww` that the installed version actually changed. Previously, `HOMEBREW_NO_AUTO_UPDATE=1` or a
  "recent" brew cache could leave the user on the old version while `ww` printed a lying "Upgraded." line.

## [0.5.0] — 2026-04-20

### Added

- `ww operator install / upgrade / status / uninstall` — first-class Kubernetes management in the CLI. Operator chart is
  embedded via `go:embed`; no `helm` required on PATH. Singleton detection refuses installs when a release exists
  cluster-wide; `--adopt` takes over a cluster whose CRDs were installed manually. RBAC preflight via
  `SelfSubjectAccessReview` fails fast with a readable missing-verbs list. `upgrade` server-side-applies CRDs **before**
  `helm upgrade --skip-crds`, working around Helm's "crds/ is install-only" semantics so new CRD fields land before the
  Deployment rolls. `uninstall` preserves CRDs + CRs by default; `--delete-crds` + `--force` cascade-delete live CRs
  with a loud banner warning.
- Preflight confirmation banner with a local-vs-production cluster heuristic. Skips the prompt on `kind-*` / `minikube`
  / `docker-desktop` / `rancher-desktop` / `orbstack` / `k3d-*` / `colima` / localhost servers; always prompts on EKS /
  GKE / AKS ARNs. `--yes` / `WW_ASSUME_YES=true` / `--dry-run` overrides.
- `scripts/sync-embedded-chart.sh [--check]` — CI-guarded drift check so the embedded copy can't silently diverge from
  the canonical chart.

### Changed

- **Behaviour change:** when a backend's `auth_env` is configured but the referenced env var is empty, harness now
  raises `ConnectionError` at request time with a clear diagnostic. Previously sent unauthenticated requests silently,
  producing "backend instability" dashboards when the real cause was a token misconfig. Existing deployments with a
  latent token-env-var typo will start failing fast after upgrading; clear `auth_env` in `backend.yaml` if auth isn't
  required, otherwise set the referenced env var to a real token.

## [0.4.4] — 2026-04-20

### Fixed

- A2A response buffering now streams via `client.stream()` + `aiter_bytes()` with a configurable cap
  (`A2A_MAX_RESPONSE_BYTES`, default 256 MiB). Prevents harness OOM from a pathological backend streaming a multi-GB
  response.
- Harness proxy fetch endpoints (`/conversations`, `/trace`, `/tool-audit`) gained a matching
  `HARNESS_PROXY_MAX_RESPONSE_BYTES` (default 64 MiB) via a `_capped_get_json` streaming helper.
- `_extract_text` emits a structured WARN log surfacing the response's top-level keys when no A2A fallback shape
  matched, so SDK schema drift is diagnosable without enabling debug logs.
- Operator seeds `Replicas = minReplicas` on first-install when autoscaling is enabled — previously Kubernetes defaulted
  to 1 during the window between Deployment create and HPA create, producing transient under-provisioning.

### Added

- `harness_consensus_backend_errors_total` carries a bounded `reason` label (`timeout|connection|backend_error|other`)
  so dashboards can separate deadline hits from network blips.
- Dashboard `Agent` TypeScript interface gained `model?: string` — closes a silent schema drift vs
  `harness/main.py:team_handler`.

## [0.4.3] — 2026-04-19

### Fixed

- `ww update` cache no longer pins users to the previously-cached `latest` tag for 24h after upgrading. When
  `cached_latest == current`, the check re-fetches instead of returning a stale "you're current" answer.

## [0.4.2] — 2026-04-19

### Fixed

- `dashboard-ci` workflow added — 112 dashboard unit tests now run on every PR instead of being silently skipped.
- Dashboard `TeamView` test isolation — module-level state in `useTeam` now reset between tests via an
  `__resetForTesting` export.
- `charts-ci` fix: `/tmp/charts/` is created before `helm template` redirects into it.

## [0.4.1] — 2026-04-19

### Added

- `ww update` subcommand with `--force` and `--check` flags; in-place self-upgrade via Homebrew or `go install`.
  Configurable via `update.mode = off | notify | prompt | auto` in `ww`'s config file.

### Fixed

- `ww` config writer now pre-creates the file with `O_CREATE|O_EXCL|0o600` to close a chmod race on first config write.
- `WW_PROFILE=<typo>` now emits a stderr warning listing known profiles instead of silently falling back to defaults.

## [0.4.0] — 2026-04-19

### Added

- Homebrew distribution via the [witwave-ai/homebrew-ww](https://github.com/witwave-ai/homebrew-ww) tap.
  `brew install witwave-ai/homebrew-ww/ww` is now the primary install path.
- Operator chart + controller shipped to GHCR at every tag. `ww` and chart version numbers track each release.
- Cosign signing on `ww` CLI binaries via OIDC (GitHub Actions). Container images remain unsigned for now (see #1460).

### Changed

- Brand rename from the prior experimental name to **witwave**. All Go module paths, Python imports, chart names,
  directory references (`.witwave/`), and environment variables (`WITWAVE_*`) migrated in one sweep on 2026-04-19
  (commit b966b40).

[Unreleased]: https://github.com/witwave-ai/witwave/compare/v0.5.8...HEAD
[0.5.8]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.8
[0.5.7]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.7
[0.5.6]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.6
[0.5.5]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.5
[0.5.4]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.4
[0.5.3]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.3
[0.5.2]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.2
[0.5.1]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.1
[0.5.0]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.0
[0.4.4]: https://github.com/witwave-ai/witwave/releases/tag/v0.4.4
[0.4.3]: https://github.com/witwave-ai/witwave/releases/tag/v0.4.3
[0.4.2]: https://github.com/witwave-ai/witwave/releases/tag/v0.4.2
[0.4.1]: https://github.com/witwave-ai/witwave/releases/tag/v0.4.1
[0.4.0]: https://github.com/witwave-ai/witwave/releases/tag/v0.4.0
