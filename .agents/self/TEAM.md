# The witwave Team

The `witwave-ai/witwave` repo is maintained by a team of five autonomous agents. They commit directly to `main`
(trunk-based development), coordinate via A2A (agent-to-agent JSON-RPC), and ship continuously — many small
high-quality releases per day rather than infrequent large ones.

Each agent owns one substrate. **Zora** decides what work happens when. **Evan** finds and fixes correctness bugs
and security risks. **Nova** keeps the code internally clean. **Kira** keeps the documentation accurate and
current. **Iris** is the team's git plumber — she pushes everyone's work and drives the release pipeline.

The mission: **continuously improve and release the witwave platform — autonomously, around the clock, with quality
gates that catch problems before they land on `main`.**

## The team

### Zora — manager
The team's coordinator. She runs a continuous decision loop driven by a 30-minute heartbeat: reads team state,
decides who works on what next via call-peer, and decides when accumulated commits + green CI warrant a release.
She doesn't write code — she dispatches the right peer at the right time. (`.agents/self/zora/`)

### Evan — code defects
Finds and fixes code defects. Two skills: `bug-work` (correctness defects — unchecked errors, null derefs,
race smells, format-string mismatches) and `risk-work` (security defects — CVEs in dependencies, secrets in
source, insecure patterns). His fixes pass through a strict fix-bar; risky candidates flag for human review
instead of auto-fixing. (`.agents/self/evan/`)

### Nova — code hygiene
Keeps the code internally clean. She formats Python with ruff, Go with gofmt + goimports, JSON/YAML/TS/Vue with
prettier; lints shell with shellcheck and Dockerfiles with hadolint; authors missing docstrings, godoc, and
helm-docs comments on undocumented exports. (`.agents/self/nova/`)

### Kira — documentation
Maintains the documentation surface — root README, CHANGELOG, every per-subproject README, the `docs/` tree.
She validates prose against current code state (`docs-verify`), refreshes forward-looking docs against industry
reality (`docs-research`), and catches drift between what the project claims and what it does. (`.agents/self/kira/`)

### Iris — git plumbing + releases
The team's git plumber and release captain. She owns push posture (race handling, conflict surfacing, no-force
rules), watches CI on every push, and drives the full release pipeline when the team's accumulated work is ready
to ship. Every other agent commits locally and delegates the push to iris via `call-peer`. (`.agents/self/iris/`)

## Topology

```
            ┌──────────────────────────────────┐
            │              ZORA                │
            │     manager / decision loop      │
            │  reads state · dispatches peers  │
            └────────────────┬─────────────────┘
                             │
                             │ call-peer
                ┌────────────┼────────────┐
                │            │            │
            ╭───▼───╮    ╭───▼───╮    ╭───▼───╮
            │ EVAN  │    │ NOVA  │    │ KIRA  │
            │defects│    │hygiene│    │ docs  │
            ╰───┬───╯    ╰───┬───╯    ╰───┬───╯
                │            │            │
                │ commits locally — delegates push via call-peer
                │            │            │
                └────────────┼────────────┘
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

The team is designed to grow. These roles aren't built yet but are queued in the design pipeline — names below are
tentative and likely to be revisited before scaffolding.

### (next up) gap-fixer — likely **owen** or **finn**
Sibling to evan, but with a different lens: instead of finding *what's wrong* in code, this agent finds *what's
missing*. Architectural gaps, unimplemented spec promises, untested code paths, missing error handling that the
existing analyzers don't flag because they don't know what *should* be there. Skill: `gap-work`. Same single-pass
shape as `bug-work`/`risk-work` — find, validate, fix-or-flag, commit, delegate push to iris.

### feature builder — likely **liam** or **felix**
Builds new features end-to-end. Reads requirements (from issues, discussions, design docs), implements the change
across code + tests + docs, commits in atomic pieces, delegates push to iris. Skill: `feature-work`. Distinct from
the defect-finding agents (evan, gap-fixer) because feature delivery is *creative authorship* rather than
*defect remediation* — different shape of work, different safety bar.

### community liaison — likely **sage** or **ezra**
Talks with humans on GitHub Discussions. Reads new threads, answers questions, negotiates feature scope with
external requesters, surfaces actionable bugs/features back to the team. Coordinates with zora on prioritisation
("a discussion thread is asking for X — when can we fit it?"). Adds a *human-facing voice* the team currently
lacks; today external requests have no team-facing channel.

### agent resources — likely **luna** or **dora**
Infra-level management of the agents themselves. Scales pods up/down (e.g., scales evan to zero overnight if no
backlog; spins kira down on weekends if docs are quiet), watches resource budgets (LLM cost, CPU, memory), tunes
configuration like `TASK_TIMEOUT_SECONDS` per-agent based on observed run times, knows who's available when. Like
HR but for agents — operational lifecycle rather than substantive work. Coordinates with zora on team capacity but
operates one layer below (zora dispatches *work* to agents; agent-resources keeps the agents *runnable*).

## How the loop closes

1. **Zora's heartbeat fires** every 30 min → reads team state → applies priority policy.
2. **Zora dispatches a peer** (urgent first, then cadence floor, then team-tidy, then backlog-weighted) via
   `call-peer`.
3. **The peer does its domain work** — finds bugs, formats code, refreshes docs, etc. Commits locally with a focused
   message.
4. **The peer delegates the push to Iris** via `call-peer`. Iris pushes; watches CI on the resulting commit.
5. **Iris reports back** to the originating peer with the CI conclusion. Red → fix-forward then revert. Green →
   work landed.
6. **Zora's next tick** sees the new commit on `origin/main`. Independent of peer dispatching, she runs a
   release-warranted check: commits since latest tag + CI green + ≥1h since last release + no critical findings →
   asks Iris to cut a release.
7. **Iris cuts the release** — pre-flight, CHANGELOG, tag, push. The three release workflows fire on the tag.
   Container images, Helm charts, ww CLI artifacts publish.
8. **Loop continues** — there's always more to find, more to fix, more to ship.

## Reading further

- Per-agent identity + skills: `.agents/self/<name>/.claude/CLAUDE.md`
- Per-agent public capability surface: `.agents/self/<name>/.{claude,witwave}/agent-card.md`
- Bootstrap (deploying the team to a cluster): `docs/bootstrap.md`
- Project-level architecture: `docs/architecture.md`, `AGENTS.md`
