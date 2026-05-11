# social/

Outward-facing published content. Whitepapers, blog drafts, and posts destined for
public channels (X / LinkedIn / Mastodon / Threads / the team's eventual blog).

This folder sits outside `docs/` deliberately. `docs/` is project-internal reference
read by contributors; `social/` is content the team publishes to the world.

## What lives here

- **Posts** — short or medium-form content destined for one or more social channels.
  These follow the spec below.
- **Whitepapers** — long-form framework pieces (e.g.,
  `four-phases-of-ai-adoption.md`). These don't follow the post spec; they're
  standalone publications. Treat them as source material that posts can reference.
- **Drafts** — work-in-progress in either shape, marked `status: draft` in
  frontmatter.

## Post specification

Every post is a single `.md` file with YAML frontmatter and a markdown body. One
file per logical post, even when it ships to multiple surfaces — keeps voice and
facts in sync across variants.

### Frontmatter schema

```yaml
---
# Required
title: "Short descriptive title — for human reference, not necessarily the post headline"
status: draft # draft | ready | scheduled | published | archived
surfaces: [twitter, linkedin, blog] # subset of: twitter, linkedin, mastodon, threads, blog
created: 2026-05-11 # YYYY-MM-DD, the day this file was authored

# Scheduling (optional unless status = scheduled or published)
scheduled_for: null # ISO 8601 UTC, e.g. 2026-05-15T14:00:00Z
published_at: null # ISO 8601 UTC, set when status flips to published

# Published URLs — one entry per surface in `surfaces`, filled when status = published
published_urls:
  twitter: null
  linkedin: null
  mastodon: null
  threads: null
  blog: null

# Context (optional but encouraged)
tags: [] # topical tags, lowercase-kebab-case, e.g. [ai-adoption, framework]
audience: tech-leader # tech-leader | founder | community | mixed
related: [] # paths to related files in the repo, e.g. [social/four-phases-of-ai-adoption.md]
source: null # commit SHA, GitHub discussion #, escalation ref, or "organic"
thread_parent: null # path to parent post file if this is a follow-up
assets: [] # paths to image/video files referenced by this post
cta: none # link-to-whitepaper | link-to-blog | link-to-repo | mention | none
tone: conversational # formal | conversational | casual | technical
---
```

#### Field reference

| Field            | Type             | Notes                                                                                                                                                                                                                              |
| ---------------- | ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `title`          | string, required | For humans reading the repo, not necessarily the headline that ships. Keep ≤80 chars.                                                                                                                                              |
| `status`         | enum, required   | See "Status lifecycle" below.                                                                                                                                                                                                      |
| `surfaces`       | list, required   | Which channels this post targets. Each value must match a key in `published_urls`. Empty `[]` is invalid — use `status: archived` instead.                                                                                         |
| `created`        | date, required   | The day the file was authored, in `YYYY-MM-DD`.                                                                                                                                                                                    |
| `scheduled_for`  | ISO 8601 / null  | When the post should publish. Required when `status: scheduled`.                                                                                                                                                                   |
| `published_at`   | ISO 8601 / null  | When the post actually published. Required when `status: published`.                                                                                                                                                               |
| `published_urls` | map / null       | Per-surface URLs. Filled in when each variant publishes.                                                                                                                                                                           |
| `tags`           | list             | Lowercase-kebab-case topical tags.                                                                                                                                                                                                 |
| `audience`       | enum             | Who this post speaks to. Drives tone calibration.                                                                                                                                                                                  |
| `related`        | list             | Repo-relative paths to related content (other posts, whitepapers, docs).                                                                                                                                                           |
| `source`         | string / null    | What triggered this post. A commit SHA (`abcd1234`), a GitHub Discussion (`#1835`), an escalation marker, or `"organic"` if it's not tied to a specific trigger.                                                                   |
| `thread_parent`  | path / null      | If this post is a follow-up to another, point at the parent file. Threads of posts share a `thread_parent` chain.                                                                                                                  |
| `assets`         | list             | Paths to image/video files used by this post. Lives under `social/assets/<slug>/` when present.                                                                                                                                    |
| `cta`            | enum             | The call-to-action shape. Helps consistency across posts.                                                                                                                                                                          |
| `tone`           | enum             | Tone register. `conversational` is the witwave-house default; `technical` for engineering-detail posts (deep into APIs, internals); `casual` for off-the-cuff community-level posts; `formal` for press / investor / GTM contexts. |

### Body — multi-surface convention

The body is markdown, organised by surface. One H2 section per surface in
`surfaces`. An empty section means "this surface is in scope but the variant
hasn't been written yet." A missing section means "this surface is not in
scope" (and the entry should be removed from `surfaces`).

```markdown
## Twitter

Headline: <hook post, ≤280 chars>

1/ <opening, ≤280>
2/ <continues, ≤280>
3/ <CTA + link, ≤280>

## LinkedIn

<2–3 paragraph body, ≤3000 chars; the first ~140 chars are the visible preview, treat them as a hook>

CTA: <call-to-action line>

## Blog

<long-form treatment, no length cap; markdown freeform>
```

For posts that don't multi-surface, just use the one section that applies. The
file shape is the same.

### Tracked surfaces

Canonical list of values accepted in the `surfaces` field and the corresponding
keys in `published_urls`. Add a row here before adding a new value in a post.

| Surface             | `surfaces` enum     | URL pattern (filled in `published_urls` once live)         | Char limit                                  | Notes                                                                                                                                       |
| ------------------- | ------------------- | ---------------------------------------------------------- | ------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| X (Twitter)         | `twitter`           | `https://x.com/<user>/status/<id>`                         | 280 (Free); 25,000 (Premium)                | Threads are multiple posts. Number each `N/` for orientation. URLs count as 23 chars regardless of actual length.                           |
| LinkedIn            | `linkedin`          | `https://linkedin.com/posts/<slug>`                        | 3,000                                       | First ~140 chars are the visible preview before "see more" — treat them as the hook.                                                        |
| Mastodon            | `mastodon`          | `https://<instance>/@<user>/<id>`                          | 500 (default; instance-dependent)           | Federation-compatible across instances. 500 is the safe ceiling.                                                                            |
| Threads             | `threads`           | `https://threads.net/@<user>/post/<id>`                    | 500                                         | Meta's. Similar shape to X.                                                                                                                 |
| Bluesky             | `bluesky`           | `https://bsky.app/profile/<handle>/post/<id>`              | 300                                         | AT Protocol; federation in progress.                                                                                                        |
| Blog                | `blog`              | `https://witwave.ai/blog/<slug>` (eventual)                | No cap                                      | Project-owned long-form. Surface is forthcoming; until then, `published_urls.blog` stays `null`.                                            |
| GitHub Discussions  | `github-discussion` | `https://github.com/witwave-ai/witwave/discussions/<n>`    | No hard cap (practical: ~10,000 chars)      | Already in active use. For community-facing announcements that warrant a thread, not a shortform post.                                      |
| HackerNews          | `hn`                | `https://news.ycombinator.com/item?id=<n>`                 | Title only (80 chars)                       | Submission is a title + URL; no body. Body in the post file is the link-target text, usually the whitepaper or blog post being submitted.   |
| Newsletter          | `newsletter`        | `https://<provider>/issues/<id>` (Substack/Beehiiv/etc.)   | No cap                                      | Provider TBD; reserve enum value for future use.                                                                                            |

#### Per-post distribution snapshot

Posts may optionally include a `## Distribution` section at the bottom of the
body that mirrors `published_urls` in human-readable form. Useful when scanning
the file directly without parsing YAML. Example:

```markdown
## Distribution

- ✅ **Twitter** — https://x.com/skthomasjr/status/1234567890 (2026-05-15T14:00:00Z)
- ✅ **LinkedIn** — https://linkedin.com/posts/scott-thomas-launch (2026-05-15T14:05:00Z)
- ⏳ **Blog** — draft (blog surface not yet live)
- ❌ **Mastodon** — not posting (decision: too niche for this content)
```

The `## Distribution` section is optional. The frontmatter `published_urls` map
is the authoritative source; the body section is a reading aid.

### Filename convention

- **Slug-style, lowercase, hyphenated:** `four-phases-launch.md`,
  `phase-2-cliff-thread.md`, `release-v0.24-announcement.md`
- **No date prefix.** Frontmatter has `created`; the filename stays clean so
  it can become a future URL slug (`/blog/four-phases-launch`).
- **Action-or-topic naming, not chronological.** `four-phases-launch.md` reads
  better than `2026-05-11-post.md`.

### Status lifecycle

```
draft → ready → scheduled → published → archived
```

| Status      | Meaning                                                                                                                                                                                                                                                                                                                                            |
| ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `draft`     | Being written. Body may be incomplete; no schedule yet. Default starting state.                                                                                                                                                                                                                                                                    |
| `ready`     | Voice and content approved by the author. Awaiting a scheduling decision. A reviewer (Piper or human) might still want a final pass before scheduling.                                                                                                                                                                                             |
| `scheduled` | `scheduled_for` is set. Either a human or a future automation will publish it at that timestamp. Don't edit body content after `scheduled` without bumping back to `ready`.                                                                                                                                                                        |
| `published` | The post is live. `published_at` and `published_urls` are filled. Body should not change after this point — corrections happen via follow-up posts.                                                                                                                                                                                                |
| `archived`  | Post was retired before publishing (decision changed, no longer timely, voice didn't land) OR was published and is being kept for reference but no longer active. Body is frozen.                                                                                                                                                                  |

### Worked example

```markdown
---
title: "Four Phases of AI Adoption — launch announcement"
status: draft
surfaces: [twitter, linkedin]
created: 2026-05-11

scheduled_for: null
published_at: null

published_urls:
  twitter: null
  linkedin: null

tags: [ai-adoption, framework, launch]
audience: tech-leader
related:
  - social/four-phases-of-ai-adoption.md
source: organic
thread_parent: null
assets: []
cta: link-to-whitepaper
tone: conversational
---

## Twitter

Headline: New whitepaper on AI adoption — and why most teams will plateau at Phase 2.

1/ Most engineering teams adopting AI follow a four-phase progression: Co-Pilot →
Agent-Augmented → Agent-Native → Self-Improving. Most won't get past Phase 2.

2/ The cliff isn't technical. It's organizational. Phase 2 → 3 demolishes the
process scaffolding the team is built on — sprints, tickets, status meetings,
review queues. Nobody volunteers for that.

3/ Phase 3 → 4 by contrast is natural — same scaffolding pointed at the agents'
own codebase. The hard work was already done in the 2 → 3 cliff.

4/ Wrote up the framework + a 10-question self-assessment for where your team
sits. Link below. ⬇️

🔗 <whitepaper URL>

## LinkedIn

Most engineering teams adopting AI will plateau at Phase 2 — not because the tech blocks them, but because the process redesign required to reach Phase 3 demolishes the operational scaffolding their culture is built around.

I wrote up a four-phase framework (Co-Pilot → Agent-Augmented → Agent-Native → Self-Improving) and the asymmetric-transition insight that explains why the cliff between Phase 2 and 3 is real, why Phase 3 → 4 is natural by contrast, and what the rational positions are for teams currently at Phase 2.

Includes a 10-question self-assessment for where your team sits today.

🔗 <whitepaper URL>
```

## Whitepapers and longer pieces

Standalone publications (whitepapers, framework essays, long-form thought pieces)
don't follow the post spec — they're standalone documents. Conventions:

- File at the folder root: `social/four-phases-of-ai-adoption.md`
- H1 title, executive summary, body sections, conclusion
- Optional frontmatter (just `title:` and `created:` for minimal tracking)
- Posts can link to them via `related:` in the post's frontmatter

## Assets

Image / video / audio files referenced by posts live under
`social/assets/<post-slug>/`. Keeps each post's media co-located and prunable.

```
social/
├── README.md
├── four-phases-of-ai-adoption.md         (whitepaper)
├── four-phases-launch.md                  (post; status: draft)
└── assets/
    └── four-phases-launch/
        ├── header.png
        └── thread-diagram.png
```

## Out of scope for this folder

- **Project-internal reference docs** — those live in `docs/`. If a file's primary
  audience is contributors reading the repo to understand witwave, it belongs in
  `docs/`, not `social/`.
- **Agent identity / behavior files** — those live in `.agents/`. Even though
  Piper engages on the public surfaces this folder publishes to, her CLAUDE.md
  and skills are not "social content."
- **Code samples or working examples** — those live in `docs/examples.md` or in
  the relevant source tree. `social/` content references code; it doesn't host
  it.
