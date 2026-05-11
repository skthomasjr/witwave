# The witwave Team

The `witwave-ai/witwave` repo is maintained by a team of eight autonomous agents. They commit directly to `main`
(trunk-based development), coordinate via A2A (agent-to-agent JSON-RPC), and ship continuously — many small high-quality
releases per day rather than infrequent large ones.

Each agent owns one substrate. **Zora** decides what work happens when. **Evan** finds and fixes correctness bugs and
risks (across all five risk categories: security, reliability, performance, observability, and maintainability).
**Nova** keeps the code internally clean. **Kira** keeps the documentation accurate and current. **Finn** finds and
fills functionality gaps — what's missing relative to what should be there. **Felix** authors new features end-to-end —
the team's only generative agent, gated by a strict tier ladder so the highest-blast-radius work stays safe. **Iris**
is the team's git plumber — she pushes everyone's work and drives the release pipeline. **Piper** is the only
outward-facing agent — she narrates the team's progress to humans on GitHub Discussions, scoring events on a substantive
bar before posting so the public surface stays signal-rich and quiet otherwise.

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

### Felix — feature builder

The team's only **generative** agent. Where Evan / Finn / Nova / Kira maintain what exists, Felix authors what doesn't.
She reads feature requests (from user A2A, Zora dispatch, or Piper-routed Discussions), tiers the work against a 1-10
risk-tier ladder, plans the implementation, and ships code + tests + docs in atomic commit series. Single skill:
`feature-work`. Event-driven (not cadence-driven) — she fires on demand or via Zora's routing decisions, with a passive
30-min heartbeat for liveness only.

The clean line that separates Felix from the rest of the team:

- **Felix builds what doesn't exist yet** — new commands, new endpoints, new chart values, new capabilities not yet
  promised anywhere.
- **Finn fills what's promised but missing** — doc-vs-code drift, untested public APIs, sibling-pattern gaps.
- **Evan fixes what's broken or fragile** — correctness defects (bug-work), risk-class issues (risk-work).

If a request crosses the line, Felix hands it back to the right peer.

Feature work is the team's highest-blast-radius activity, so Felix runs under three load-bearing safety mechanisms:

1. **Tier ladder (1-10)**: trivial doc-driven additions at tier 1, single-file helpers at tier 2, multi-file features
   within an existing subsystem at tier 3, new helper modules at tier 4, new endpoints / MCP tools / chart capabilities
   at tier 5, cross-cutting at tier 6+, architectural / breaking changes at tier 7+. **v1 autonomous ceiling: tier 3**.
   Tier 4+ requires explicit human approval per commit until 30 days of clean tier-1/2 output.
2. **Non-waivable fix-bar**: every commit must (a) be genuinely a feature, (b) be correctly tiered within the ceiling,
   (c) ship its own tests in the same commit, (d) pass the local test suite, (e) update affected docs, (f) stay within
   the planned scope, (g) be atomic and revertable. If the bar can't be cleared, the work is deferred — never "landed
   and cleaned up later."
3. **Tier reset**: any of Felix's commits triggering a fix-forward by Evan within 24h drops her autonomous ceiling by 1
   tier for 7 days. Self-correcting safety floor.

(`.agents/self/felix/`)

### Iris — git plumbing + releases

The team's git plumber and release captain. She owns push posture (race handling, conflict surfacing, no-force rules),
watches CI on every push, and drives the full release pipeline when the team's accumulated work is ready to ship. Every
other agent commits locally and delegates the push to iris via `call-peer`. (`.agents/self/iris/`)

### Piper — outreach

The team's only outward-facing agent. She runs a heartbeat-driven outreach loop (every 15 min), reads team state (git
log, peer memories, Zora's decision_log + escalations.md, recent CI runs, recent releases), scores observed events on a
0-10 substantive-score model, and routes each tick to one of three outcomes: Announcements (≥9 — releases, critical
events, user-visible surface changes), Progress (5-8 — substantive dev activity with a 30-min cooldown), or silent (<5 —
most ticks; routine churn doesn't warrant a public post). The threshold scales with cadence so frequent heartbeats don't
flood the GitHub Discussions feed. Voice is informative + warm. **Read-only on source** and writes only to her memory
namespace + GitHub Discussions; doesn't dispatch peers for work, only `call-peer` for clarification questions before
posting publicly.

She also engages with humans across three Discussion surfaces via a discuss-\* skill family (`discuss-comments` on her
own posts, `discuss-bugs` in the Bugs category with deep code-investigation, `discuss-questions` in the General category
for open-ended Q&A). Confirmed user-reported bugs route through Zora via `bugs-from-users.md`; recurring misconceptions
feed Kira's docs queue. Piper has **admin role on the repo and moderates the Discussions surface autonomously** — Guard
0 (the moderation pre-screen running before all reply guards) hides spam / prompt-injection / harassment via
`minimizeComment` and locks abusive threads via `lockLockable` without human-in-the-loop. Hide and lock are reversible;
deletion stays off the autonomous menu by design. (`.agents/self/piper/`)

## Topology

```
                  ╭──────────────────────────────────────╮
                  │                 ZORA                 │
                  │       manager / decision loop        │
                  │    reads state · dispatches peers    │
                  ╰───────────────────┬──────────────────╯
                                      │
                                      │ call-peer
                ┌──────────┬──────────┼──────────┬──────────┐
                │          │          │          │          │
            ╭───▼────╮ ╭───▼────╮ ╭───▼────╮ ╭───▼────╮ ╭───▼────╮
            │  EVAN  │ │  NOVA  │ │  KIRA  │ │  FINN  │ │ FELIX  │
            │defects │ │hygiene │ │  docs  │ │  gaps  │ │features│
            ╰───┬────╯ ╰───┬────╯ ╰───┬────╯ ╰───┬────╯ ╰───┬────╯
                │          │          │          │          │
                │   commits locally — delegates push via call-peer
                │          │          │          │          │
                └──────────┴──────────┼──────────┴──────────┘
                                      │
                                  ╭───▼───╮
                                  │ IRIS  │
                                  │  git  │
                                  ╰───┬───╯
                                      │ push + CI watch + release
                                      ▼
                                 origin/main
                                      │           ┌──── reads state ────┐
                                      ▼           │                     │
                             release pipeline ✦   │              ╭──────▼──────╮
                                      │           │              │    PIPER    │
                                      ▼           └─────────────▶│  outreach   │
                            ghcr.io · oci · brew                 │  heartbeat  │
                                                                 ╰──────┬──────╯
                                                                        │ post (when score ≥5)
                                                                        ▼
                                                              GitHub Discussions
                                                              (Announcements / Progress)
```

Piper sits OUTSIDE the work-coordination loop. She reads team state but doesn't dispatch peers for work; her only A2A
use is `ask-peer-clarification` (information-only questions) before posting publicly.

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
