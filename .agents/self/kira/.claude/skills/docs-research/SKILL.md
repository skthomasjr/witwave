---
name: docs-research
description: Research-driven refresh of forward-looking Category C documents — `docs/competitive-landscape.md`, `docs/product-vision.md`, `docs/architecture.md`, and other industry-aware files. Walks the current content, follows links to verify they're still accurate, searches the web for new developments, and applies refinements as commits with explicit URL + access-date source citations. Designed to run on a weekly-ish schedule but also on demand. Delegates push to iris via `call-peer`. Trigger when the user says "research docs", "refresh competitive landscape", "update product vision", "rerun docs research", or via scheduled job.
version: 0.1.0
---

# docs-research

Periodic refresh of forward-looking documentation by reaching
outside the repo for current information. This skill is the only
docs skill that **edits prose substantively** based on external
sources — every other docs-* skill works only with what's already
in the repo or in the code.

The autonomy posture is therefore stricter than the other docs
skills, not more permissive: every change must be traceable, every
new claim must cite a source, and every commit must be small enough
that a human reviewer can revert it cleanly if anything looks
wrong.

## Target documents

The default list this skill refreshes:

- `docs/competitive-landscape.md` — industry positioning, comparable
  projects, who's adjacent in the agent / multi-agent / AI-DevOps
  space.
- `docs/product-vision.md` — forward-looking direction, "where
  this is going" framing, what the project aspires to be.
- `docs/architecture.md` — high-level architecture description.
  Less likely to need external research than the other two but
  benefits from periodic alignment with current industry
  vocabulary and patterns.

Other forward-looking docs under `docs/` may be added to this list
inline below as they appear (e.g. `docs/roadmap.md`,
`docs/research-notes.md`). Out-of-scope (do NOT touch from this
skill):

- Anything under `.agents/**` (Cat A — agent identity).
- Repo-root `CLAUDE.md` / `AGENTS.md` / `.claude/skills/**`
  (Cat B — local dev tooling).
- Per-subproject `README.md` files (handled by
  `docs-cleanup` / `docs-verify` / `docs-consistency`, not by
  research).
- `CHANGELOG.md`, `LICENSE`, `CONTRIBUTING.md`, `SECURITY.md` —
  these are factual project records, not forward-looking.

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path

### 1. Verify the source tree is in place

```sh
git -C <checkout> rev-parse --show-toplevel
git -C <checkout> status --porcelain
```

Stand down + log if missing or dirty (per CLAUDE.md →
Responsibilities → 1).

### 2. Pin git identity

Invoke the `git-identity` skill (idempotent). The commits in
step 7 fail otherwise.

### 3. Capture the pre-research ref

```sh
PRE_RESEARCH_SHA=$(git -C <checkout> rev-parse HEAD)
```

Used at step 8 to compute what landed and to phrase the push
delegation cleanly.

### 4. For each target document — research walk

Process targets one at a time. For each (call this `<doc>`):

#### 4a. Read current content + extract external references

Read `<doc>` in full. Extract every external reference — URLs,
GitHub repo references, project names mentioned by name (e.g.
"LangChain", "AutoGen", "CrewAI"), version-number claims about
external products, and any sentence that's a factual claim about
the state of the industry.

These are your **anchor points** — the facts the doc currently
asserts about the outside world. Each one is a candidate for
verification or refresh.

#### 4b. Verify each existing URL

For each URL in the doc:

```sh
curl -sfL --max-time 15 -o /dev/null -w "%{http_code}\n" "<url>"
```

- 2xx → URL is live; capture the page's title + first paragraph
  for sanity (use `curl ... | head` followed by HTML→text via a
  small awk or python helper).
- 4xx → URL is stale (redirected, removed, or moved). Find the
  current location via web search if reasonable; otherwise log
  the staleness.
- 5xx / timeout → transient; mark as unknown for this run, do
  not act on it.

#### 4c. Re-verify each named project / product

For each named project mentioned in the doc:

- Search the web for the project's current state — "<name>
  project status 2026", "<name> latest release", or similar.
- Check if the project is still active (recent commits / releases
  / blog posts within the last 12 months).
- Note any major direction changes that affect how the doc
  positions this project relative to ours.

Use the WebFetch / WebSearch tools provided by the Claude Agent
SDK if available; otherwise fall back to `curl` against
DuckDuckGo HTML or a similar search-friendly endpoint.

#### 4d. Discover new entries (competitive-landscape only)

For `docs/competitive-landscape.md` specifically: search for
projects in the same space that the doc DOESN'T mention yet.
Reasonable searches:

- "multi-agent platform Kubernetes 2026"
- "AI agent orchestration self-hosted"
- "LLM agent collaboration framework"

For each candidate found, evaluate fit:

- Does the project actually compete with or complement witwave?
  (Reject random adjacent results — e.g. consumer chatbots are
  not in our competitive set.)
- Is it active? (>50 stars, commit within last 6 months, etc.)
- Does it offer a meaningfully different design choice?

Add up to **3 new entries per run** to keep the diff bounded —
research-driven prose changes need to stay reviewable.

#### 4e. Compose proposed changes for this doc

Build a list of edits to apply to `<doc>`:

- **Update**: existing entry has stale info; rewrite with current
  source.
- **Add**: new project / claim worth including.
- **Mark stale**: entry references a project that's clearly
  abandoned; don't delete (preserves history) — instead annotate
  the entry with a `(no longer active as of YYYY-MM-DD, source:
  <url>)` parenthetical.
- **Remove**: only when the entry is factually wrong AND a
  current source disproves it. Removals must include the
  contradicting source in the commit body.

### 5. Apply edits with citation discipline

For every prose change, the new content must end with an inline
citation of the form:

```
(source: <url>, accessed YYYY-MM-DD)
```

For multi-source claims:

```
(sources: <url1> and <url2>, accessed YYYY-MM-DD)
```

This is non-negotiable. If you can't cite a source, you don't
have evidence for the claim, and the claim doesn't go in the
doc — even if it "feels right" or you remember it from training.
A pattern-matched but uncited claim is exactly the failure mode
this discipline exists to prevent.

Apply edits via standard Read / Edit tool calls — line-by-line,
preserving the surrounding prose voice and structure. Don't
rewrite whole sections wholesale; tweaks only.

### 6. Commit per document

After all edits to a single `<doc>` are applied:

```sh
git -C <checkout> add <doc>
git -C <checkout> commit -F - <<COMMIT_MSG
docs(research): refresh <doc> against current industry state

<one-paragraph summary of what changed and why>

Sources consulted this run:
- <url1> (accessed YYYY-MM-DD)
- <url2> (accessed YYYY-MM-DD)
- ...

Edits applied:
- <one-line summary per edit>
COMMIT_MSG
```

One commit per doc, so a human reviewer can revert any single
file's research run independently. **Do not bundle multiple docs
into one commit** — even if all the changes ride together
naturally, separate commits keep the audit trail clean.

If a doc had no proposed changes (everything verified clean,
nothing new to add), don't commit anything for it. Silence is the
right output.

### 7. Loop back to step 4 for the next target

Process all targets. Each gets its own commit (or no commit if
clean).

### 8. Decide whether to push

Compare HEAD to `PRE_RESEARCH_SHA`:

```sh
git -C <checkout> log --oneline ${PRE_RESEARCH_SHA}..HEAD
```

- **No commits** → research found nothing actionable. Report
  "research run clean, no changes" and exit.
- **One or more commits** → proceed to step 9.

### 9. Delegate the push to iris via `call-peer`

Same pattern as `docs-cleanup`. Send iris a self-contained
prompt:

> *"docs-research batch ready to publish. <N> commit(s) on
> `<branch>` since `<PRE_RESEARCH_SHA>`. Each commit refreshes
> one target doc with cited research. Subjects:*
>
> - *<commit subject 1>*
> - *<commit subject 2>*
>
> *Please run `git-push` to land them on origin/<branch>."*

Capture iris's reply for the report.

### 10. Report

Return a structured summary:

- Pre-research SHA / post-research SHA (after iris's push)
- Per-target outcome:
  - clean (no changes), OR
  - changed (commit subject + summary of what was refreshed)
- Total external sources consulted across all targets
- Any URLs found stale (with their previous + current
  destination if discoverable)
- New entries added (for competitive-landscape)
- Iris's push outcome (success with commit range, or verbatim
  failure)

## When to invoke

- **Scheduled** — a job in `.witwave/jobs/` fires this skill on
  a weekly-ish cadence (configured separately; the cron entry is
  a follow-up to this skill landing). Keeps forward-looking docs
  drifting on their own clock.
- **On demand** — the user or a sibling agent sends "research
  docs", "refresh competitive landscape", "update product vision",
  "rerun docs research", or similar via A2A.
- **After major industry events** — a release announcement from a
  competitor, a new project entering the space, etc. Triggered
  manually rather than waiting for the next scheduled run.

## Failure handling

- **No internet access** — if every URL probe times out or fails,
  exit early with "research unavailable; skipping" rather than
  producing uncited content. The whole skill depends on external
  reachability.
- **Web search returns junk** — if results are clearly low-
  quality (SEO spam, AI-generated junk, off-topic), don't include
  them. Better to skip a refresh round than poison the doc with a
  bad source.
- **Iris unreachable at push time** — local commits stay
  unpushed; report the situation. Next run picks them up
  naturally.

## Out of scope for this skill

- **Cat A / Cat B docs** — agent identity and local dev tooling
  are handled by other docs skills (or not at all autonomously).
  This skill never touches them.
- **Per-subproject READMEs** (e.g. `clients/ww/README.md`) —
  those are factual descriptions of subprojects, not
  forward-looking research targets. `docs-cleanup` handles them.
- **CHANGELOG / LICENSE / SECURITY / CONTRIBUTING** — factual
  project records; not research targets.
- **Removing or rewriting existing content without a source** —
  the citation discipline is hard. If you can't cite, don't
  change.
- **Bundling multiple docs into one commit** — separate commits
  per doc preserve the per-file revert path.
- **Inferring beyond what sources support** — restate what
  sources say; don't extrapolate. The doc's job is to stay
  current with the industry, not to advance kira's opinions.
