# The witwave Team

The `witwave-ai/witwave` repo is maintained by a team of six autonomous agents. They commit directly to `main`
(trunk-based development), coordinate via A2A (agent-to-agent JSON-RPC), and ship continuously — many small high-quality
releases per day rather than infrequent large ones.

Each agent owns one substrate. **Zora** decides what work happens when. **Evan** finds and fixes correctness bugs and
risks (across all five risk categories: security, reliability, performance, observability, and maintainability).
**Nova** keeps the code internally clean. **Kira** keeps the documentation accurate and current. **Finn** finds and
fills functionality gaps — what's missing relative to what should be there. **Iris** is the team's git plumber — she
pushes everyone's work and drives the release pipeline.

The mission: **continuously improve and release the witwave platform — autonomously, around the clock, with quality
gates that catch problems before they land on `main`.**

## The team

### Zora — manager

The team's coordinator. She runs a continuous decision loop driven by a 30-minute heartbeat: reads team state, decides
who works on what next via call-peer, and decides when accumulated commits + green CI warrant a release. She doesn't
write code — she dispatches the right peer at the right time. (`.agents/self/zora/`)

### Evan — code defects + risks

Finds and fixes code defects and risks. Two skills: `bug-work` (correctness defects — unchecked errors, null derefs,
format-string mismatches, idempotency gaps) and `risk-work` (code that works today but is fragile under foreseeable
conditions — five categories: security CVEs / secrets / insecure patterns, reliability missing-timeouts / no-retries /
silent-degradation, performance unbounded-growth / blocking-in-async, observability silent-failures /
swallowed-error-context, plus maintainability deep-coupling / undocumented-invariants which is flag-only). His fixes
pass through a strict fix-bar; risky candidates flag for human review instead of auto-fixing. (`.agents/self/evan/`)

### Nova — code hygiene

Keeps the code internally clean. She formats Python with ruff, Go with gofmt + goimports, JSON/YAML/TS/Vue with
prettier; lints shell with shellcheck and Dockerfiles with hadolint; authors missing docstrings, godoc, and helm-docs
comments on undocumented exports. (`.agents/self/nova/`)

### Kira — documentation

Maintains the documentation surface — root README, CHANGELOG, every per-subproject README, the `docs/` tree. She
validates prose against current code state (`docs-verify`), refreshes forward-looking docs against industry reality
(`docs-research`), and catches drift between what the project claims and what it does. (`.agents/self/kira/`)

### Finn — functionality gaps

Finds and fills what's _missing_ relative to what should be there. Eleven gap-source categories per run — doc-vs-code
promises, untested public APIs, TODO/FIXME triage, architectural sibling-pattern gaps, convention drift,
operator↔helm-chart parity, CLI↔dashboard parity, environment-variable claims, helper-module unfinished surface,
configuration claims vs operator behavior, missing error handling. Single skill `gap-work`, single-pass shape parallel
to evan's. **Risk-tier 1-10 ladder** is the load-bearing autonomous-safety knob — starts at tier 1 (cosmetic / orphan
removal, near-zero blast) and walks up `1 → 3 → 5 → 7 → 9` only as each tier's gap pool exhausts clean. Bolder fills
happen later, after low-tier territory is verified safe.

Reliability / performance / observability mitigations are NOT finn's lane — those are **risks**, not gaps, and live in
evan's broadened `risk-work`. The clean line: finn fills what's missing per existing claims; evan addresses what's wrong
(bugs) or fragile (risks). (`.agents/self/finn/`)

### Iris — git plumbing + releases

The team's git plumber and release captain. She owns push posture (race handling, conflict surfacing, no-force rules),
watches CI on every push, and drives the full release pipeline when the team's accumulated work is ready to ship. Every
other agent commits locally and delegates the push to iris via `call-peer`. (`.agents/self/iris/`)

## Topology

```
            ┌──────────────────────────────────┐
            │              ZORA                │
            │     manager / decision loop      │
            │  reads state · dispatches peers  │
            └────────────────┬─────────────────┘
                             │
                             │ call-peer
              ┌──────────┬───┼───┬──────────┐
              │          │   │   │          │
          ╭───▼───╮  ╭───▼───╮  ╭───▼───╮  ╭───▼───╮
          │ EVAN  │  │ NOVA  │  │ KIRA  │  │ FINN  │
          │defects│  │hygiene│  │ docs  │  │ gaps  │
          ╰───┬───╯  ╰───┬───╯  ╰───┬───╯  ╰───┬───╯
              │          │          │          │
              │ commits locally — delegates push via call-peer
              │          │          │          │
              └──────────┴────┬─────┴──────────┘
                              │
                          ╭───▼───╮
                          │ IRIS  │
                          │  git  │
                          ╰───┬───╯
                             │ push + CI watch + release
                             ▼
                        origin/main
                             │
                             ▼
                    release pipeline ✦
                             │
                             ▼
                  ghcr.io · oci · brew
```

## Proposed future members

The team is designed to grow. These roles aren't built yet but are queued in the design pipeline. Names below are
tentative and likely to be revisited before scaffolding. **Listed in recommended implementation order** — earlier
entries are closer to existing patterns (lower build risk, faster ROI); later entries are more speculative or depend on
the team being more mature first.

### 1. devops — likely **otto** or **dale**

Owns the build, CI/CD, and observability infrastructure of the witwave platform itself. Improves Dockerfile build times,
evolves GitHub Actions workflows, tunes Prometheus alerts, watches Grafana dashboards, fixes broken pipelines. Distinct
from iris (who _uses_ the build process to publish releases) and agent-resources (who manages _agent_ infra) — devops
owns the **platform's own infra**: the build/deploy/monitor surface that ships witwave to its users. **Second priority**
because the team's own velocity depends directly on a healthy build/release pipeline; every hour devops shaves off CI is
multiplied across every other agent's work.

### 2. agent-resources — likely **luna** or **dora**

Infra-level management of the agents themselves. Scales pods up/down (e.g., scales evan to zero overnight if no backlog;
spins kira down on weekends if docs are quiet), watches resource budgets (LLM cost, CPU, memory), tunes configuration
like `TASK_TIMEOUT_SECONDS` per-agent based on observed run times, knows who's available when. Like HR but for agents —
operational lifecycle rather than substantive work. Coordinates with zora on team capacity but operates one layer below
(zora dispatches _work_ to agents; agent-resources keeps the agents _runnable_). **Third priority** because as the team
grows past 5 agents the manual lifecycle tuning starts to dominate operator time.

### 3. security — likely **vera** or **maya**

Higher-level security work that goes beyond evan's automated `risk-work` lens. Threat modeling against the architecture,
manual audit response, RBAC posture review, supply-chain analysis, secret rotation policy, compliance gap-finding.
Distinct from evan: evan automates CVE/secret/insecure-pattern detection across the codebase; security-agent reasons
about the _system's overall threat posture_ — the work that requires architectural understanding rather than scanner
output. **Fourth priority**: evan covers the high-volume automated surface today; the architectural-security gap is real
but rarer-firing.

### 4. testing — name + scope TBD

At least one testing-focused agent is on the roadmap, but the scope needs a design discussion before scaffolding —
possibilities span "writes new tests where evan's fix-bar flagged untested code paths," "runs existing suites and
surfaces flakiness/regressions," "mutation testing to evaluate test quality," "property-based test generation," "E2E
test maintenance." Each is a different shape of work. **Fifth priority** because the value is high but the design
discussion has to land first — until we pick a shape, scaffolding is premature.

### 5. software-architecture — likely **theo** or **lyra**

Watches the _shape_ of the system rather than individual files. Detects module-boundary erosion, cross-cutting refactor
opportunities, design-pattern drift, scalability/performance architecture concerns. Distinct from nova (line-level
hygiene) and evan (defect-level fixing) — architecture-agent looks at how the system fits together across components,
surfacing changes that no single file or function would reveal. Many of her findings will be flag-only; substantive
refactor proposals deserve human review before landing. **Sixth priority** because the findings are mostly
flag-for-human (low autonomy yield) and overlap with what a CTO-level role can also surface.

### 6. CTO — likely **rhea** or **aria**

Picks big direction changes. Reads the team's accumulated state — open issues, recurring pain points, drift between what
the platform claims and what users want, market/ecosystem shifts (new MCP servers, new model capabilities, adjacent OSS
projects) — and proposes _strategic_ moves: "we should pivot to X," "the next quarter's theme is Y," "this whole
subsystem deserves a rewrite." Output is high-leverage, low-frequency, mostly human-review: design memos, prioritisation
proposals, deprecation calls, "let's stop investing in Z." Distinct from zora (who decides _which peer dispatches next_
on the 30-min cadence) and software-architecture (who flags structural decay): CTO sets the _direction_ both of them
then execute against. **Seventh priority** because direction-setting is highest-leverage but also requires the most
accumulated context — better once the team has months of state to reason over and the platform has real users with real
friction points.

### 7. public relations — likely **piper** or **nora**

The team's outbound voice. Maintains a deep working relationship with every other agent — reads their memory, their
commits, their findings, their decisions — and turns the team's lived reality into _stories worth telling_. Cadence: a
blog entry every other day or so chronicling the trials, tribulations, and forward growth of running software
development this way: what's working, what broke and how the team recovered, what surprised everyone, what the team
learned this week. Publishes on behalf of the witwave team to wherever the project's public surface lives (blog, social,
mailing list). Distinct from community-liaison (who _responds_ to inbound threads): PR is _outbound_ storytelling —
proactive narrative, not reactive support. **Eighth priority** because the role only works once the team has accumulated
enough lived history to be interesting; spinning it up too early produces empty, hand-wavy content. Once the team's been
running for a quarter or two, the material practically writes itself.

### 8. community liaison — likely **sage** or **ezra**

Talks with humans on GitHub Discussions. Reads new threads, answers questions, negotiates feature scope with external
requesters, surfaces actionable bugs/features back to the team. Coordinates with zora on prioritisation ("a discussion
thread is asking for X — when can we fit it?"). Adds a _human-facing voice_ the team currently lacks; today external
requests have no team-facing channel. **Ninth priority** because it depends on actually having a community generating
threads — and that community is partly what PR exists to grow, so PR comes first.

### 9. feature builder — likely **liam** or **felix**

Builds new features end-to-end. Reads requirements (from issues, discussions, design docs), implements the change across
code + tests + docs, commits in atomic pieces, delegates push to iris. Skill: `feature-work`. Distinct from the
defect-finding agents (evan, gap-fixer) because feature delivery is _creative authorship_ rather than _defect
remediation_ — different shape of work, different safety bar. **Last priority** because creative authorship has the
highest blast radius if the safety bar is wrong; ideally we have CTO-level direction-setting _and_ a mature testing
agent in place before letting an agent author net-new features autonomously.

## How the loop closes

1. **Zora's heartbeat fires** every 30 min → reads team state → applies priority policy.
2. **Zora dispatches a peer** (urgent first, then cadence floor, then team-tidy, then backlog-weighted) via `call-peer`.
3. **The peer does its domain work** — finds bugs, formats code, refreshes docs, etc. Commits locally with a focused
   message.
4. **The peer delegates the push to Iris** via `call-peer`. Iris pushes; watches CI on the resulting commit.
5. **Iris reports back** to the originating peer with the CI conclusion. Red → fix-forward then revert. Green → work
   landed.
6. **Zora's next tick** sees the new commit on `origin/main`. Independent of peer dispatching, she runs a
   release-warranted check: commits since latest tag + CI green + ≥1h since last release + no critical findings → asks
   Iris to cut a release.
7. **Iris cuts the release** — pre-flight, CHANGELOG, tag, push. The three release workflows fire on the tag. Container
   images, Helm charts, ww CLI artifacts publish.
8. **Loop continues** — there's always more to find, more to fix, more to ship.

## Reading further

- Per-agent identity + skills: `.agents/self/<name>/.claude/CLAUDE.md`
- Per-agent public capability surface: `.agents/self/<name>/.{claude,witwave}/agent-card.md`
- Bootstrap (deploying the team to a cluster): `docs/bootstrap.md`
- Project-level architecture: `docs/architecture.md`, `AGENTS.md`
