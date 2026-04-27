# Contributing

This project is an experiment in **AI-operated open source**. Every
line of code is written by AI. Every bug is diagnosed and fixed by AI.
Every question in an issue is answered by AI. Every PR is opened,
reviewed, and merged by AI. Blog posts, release notes, and — eventually —
outreach at a calibrated level are written by AI.

Humans open issues and make strategic calls. That is the shape of
participation in this repository, and it is intentional. It is not a
temporary constraint while the project bootstraps; it is one of the
things this project is trying to prove can work.

## Why

There are many agent frameworks. This project differs in a specific
way: it is the first test bed — at least that we know of — where the
agents maintain the platform they are built on. The code in this repo
is written by the same class of agent the repo deploys. We are eating
our own cooking: every feature shipped, every regression fixed, every
incident postmortem is a data point on whether autonomous agents can
actually maintain real software long-term.

If the thesis works, this becomes a reference for how other projects
can be maintained the same way. If it doesn't, we learn exactly where
it breaks.

## How to contribute

**File a GitHub issue.** That is the primary channel.

Two kinds of issues:

- **Questions** — "How does X work?" "Is Y a limitation?" "What's the
  design reason for Z?" An agent responds in-thread, answers, and
  closes the issue when resolved.
- **Requests** — feature requests, bug reports, documentation
  improvements, design proposals. An agent refines the request with
  you in the thread until it is actionable, then picks it up,
  implements it, opens a PR, carries it through review, and merges it.

You do not need to write code, tests, or documentation. You do not
need to debug. You do not need to review. Those are the agents' job.

What you *do* need to do: describe the need or the question clearly,
answer clarifying questions in the issue thread, and push back if the
direction drifts from what you meant.

## What not to do

**Do not open pull requests.** PRs authored by humans will be closed
without review. The design of this project is that all code changes
originate from an agent acting on an issue — not because human
contributions are unwelcome as ideas, but because the whole point is
testing whether agents can carry the development loop end-to-end. A
human PR short-circuits the thing we are trying to learn.

**Do not fork and parallel-ship.** If you've built something you think
should land here, file an issue describing the feature and let an
agent implement it from your description. If the agent implementation
differs from yours in ways that matter to you, tell the agent in the
issue thread.

**Do not expect instant replies.** Agents respond on schedules, not in
real time. A 24-hour response window is normal for questions; active
work on a request can take longer. This is by design — agents run on
heartbeat + job + task schedulers, not on a human's attention cadence.

## How AI handles the project

Concretely, today, the agents in this repo handle:

- **Code.** Every line. Implementation, refactoring, migration,
  scaffolding.
- **Bug fixes.** Triage, reproduction, root-cause analysis, fix, test,
  merge.
- **Testing.** Unit tests, integration tests, and the smoke-test spec
  are all agent-authored.
- **Documentation.** This file included. Agents write, audit, and
  refactor docs on the same cadence as code.
- **Release engineering.** Tags, releases, container images, chart
  publishing, CLI binaries, Homebrew cask — all automated via
  agent-written CI pipelines.
- **Issue triage and refinement.** Agents read new issues, classify
  them, ask clarifying questions, and either resolve or queue for
  implementation.

On the roadmap — not yet wired:

- **Blog posts and release writeups.** When the project ships a
  meaningful milestone, an agent drafts the announcement.
- **Outreach at a calibrated level.** Engagement on relevant threads,
  responses in communities where the project is discussed, replies on
  the project's own discussion surfaces — all within clearly-bounded
  policies the project will publish before activating.
- **GitHub Discussions** as a lower-friction channel for open-ended
  conversation that doesn't fit the Issues "question or request"
  taxonomy.
- **Support conversations.** Multi-turn dialogue with users working
  through deployment issues or unusual configurations.

## Current state vs. target

The distinction matters, because it tells you what to expect.

**Today:**
- One human contributor (the project owner).
- AI writes 100% of the code, but under direct human guidance — the
  human decides what to ask for, the agent executes.
- Issue triage, refinement, implementation, and merge are all manual
  steps that happen in a guided loop.
- External contributions are not yet open; the model above describes
  how participation will work once it is.
- No auto-closing of human PRs, no labeled state transitions between
  triage → refined → in-progress → review → merged, no automated
  outreach, no blog-post pipeline. These are on the roadmap.

**Target:**
- Multiple external humans filing issues.
- Agents autonomously triage, refine, implement, review, and merge.
- The human contributor's role shrinks to strategic direction and
  escalation — declining to do things, changing the positioning,
  approving model upgrades, resolving disputes between competing
  requests.
- Agents write the release notes, the blog posts, the examples, and
  the responses on community channels where the project is discussed.
- Humans never see a day-to-day bug fix unless they went looking for
  the commit.

**The gap between today and target is tooling, not design.** Agents
writing code is proven (that's the whole session history of this
repo). Agents triaging issues cleanly, running PR review, and carrying
multi-turn conversations at community quality is the work still ahead.

## Working with agents

Agents may push back. If an issue describes a feature the agent thinks
is out of scope, at odds with the platform's design, or better solved
a different way, the agent will say so and ask. Treat this the same
way you would treat a maintainer pushing back — it is not a bug, it
is the review step working.

Agents may decline. If a request conflicts with the project's
direction or safety posture, the agent will close the issue with an
explanation. You can escalate by opening a new issue that reframes
the ask, citing the prior close. The project owner reads escalations.

Agents may be wrong. If you believe an agent has closed, dismissed,
or misimplemented something incorrectly, say so in the issue thread.
Another agent pass will re-evaluate. If the disagreement persists
across passes, it becomes an escalation.

## Security

**Report security issues privately.** Use GitHub's "Report a
vulnerability" feature on the repository. Do not file public issues
for security bugs. An agent will triage and respond; the project
owner is CC'd on every security thread.

## Status of this document

The model described here is intent. Parts of it — the auto-close of
human PRs, labeled state transitions, blog pipeline, outreach
policies, multi-turn community support — do not yet exist as
automation. They will be added as the agent infrastructure matures.

In the meantime, the rules at the top of this file (*file an issue,
don't open a PR*) still apply. Any ambiguity resolves in favor of
the model described above, because that is the eventual state the
project is built toward.
