---
name: generate-blog-entry
description:
  Write a candid, human-readable witwave blog field note from repo history, recent public posts, agent memory,
  CI/release state, light internet research, and optional peer quotes. Use when Piper is asked to generate a blog entry,
  write a weekly blog post, draft a field note, or when a scheduled blog-publishing job invokes Piper.
---

# generate-blog-entry

Generate one public blog entry for `witwave.ai`. This is slower and more reflective than `team-pulse`: pick one topic
that is genuinely interesting to humans, explain it in plain English, and produce a source-backed Markdown post under
`social/posts/`.

The post should feel like a thoughtful person watching an agent-native team learn in public. It does not need to be
positive. Some of the best posts will be mixed: "this worked, this was awkward, this is where agent teams still feel
strange, and here is why that matters."

## Operating Mode

Infer mode from the invocation:

- **Draft mode** when the user says draft, review, sketch, or asks for ideas. Create the post as `status: "ready"` and
  do not commit or push.
- **Publish mode** when the user says publish, generate a blog entry without qualifiers, or a scheduled weekly job
  invokes this skill. Create the post as `status: "published"` and prepare it to go live.
- **Defer mode** when the topic cannot be verified, CI is currently broken in a way that would make publishing
  misleading, or the source tree has unrelated uncommitted changes. Write notes to Piper memory and report what blocked
  the post.

If running inside an interactive local session, obey the repo-level rule: do not commit or push unless the user
explicitly asked. In Piper's scheduled or agent-invoked publish mode, a "generate blog entry" invocation is the publish
request.

## Source Of Truth

Use the primary repo checkout from `CLAUDE.md`:

```sh
cd /workspaces/witwave-self/source/witwave
```

Do not edit the generated GitHub Pages repo. The public site is generated from this source repo. Blog source files live
here:

- `social/posts/posts.json` - browser-visible blog manifest.
- `social/posts/yyyy-mm-dd-title-goes-here.md` - Markdown post with frontmatter.
- `social/README.md` - post schema and channel rules.
- `social/website/README.md` and `social/website/content/blog/README.md` - website publishing model.

Allowed source writes for this skill only:

- Add one new `social/posts/*.md` file.
- Update `social/posts/posts.json`.

Do not edit code, charts, generated static pages, whitepapers, or unrelated docs as part of this skill.

## Research Pass

Build a short evidence pack before choosing the topic. Prefer facts from current files over memory.

### 1. Recent blog history

Read the manifest and the last few published posts:

```sh
cat social/posts/posts.json
sed -n '1,220p' social/posts/*.md
```

Look for:

- Repeated themes to continue or avoid.
- The level of technical detail readers have already seen.
- Open promises, such as future integrations, public cadence, or field-note framing.

### 2. Git history and CI

Look back far enough to find a weekly story, usually 7-14 days:

```sh
git log --since="14 days ago" --date=short --format='%h %ad %an %s'
gh run list --branch main --limit 20 --json status,conclusion,name,databaseId,headSha,createdAt
git tag -l 'v*' --sort=-creatordate | head -5
```

Use `git show --stat SHA` for commits that look important. Do not turn the post into release notes unless a release is
the clearest story.

### 3. Agent memory

Read memory to understand the story behind the commits:

- Piper: `MEMORY.md`, `pulse_log.md`, `drafts/`, previous post notes.
- Zora: `team_state.md`, `decision_log.md`, `escalations.md`.
- Peers: each `agents/<peer>/MEMORY.md`, then specific files only when relevant.

Memory can be stale. Verify any claim against the source tree, git history, CI, a Discussion, or a direct peer answer
before publishing it.

Never decrypt SOPS files or include secret values. Secret-management work can be discussed at the level of process and
safety, not credentials.

### 4. Internet context scan

Use public internet research as a comparison surface, not as filler. The goal is to understand what a curious outside
reader may already know or wonder about: coding agents, autonomous software agents, multi-agent workflows, AI teammates,
traditional software-team rituals, and agentic development tools.

Guidelines:

- Prefer primary or high-signal sources: official product docs/blogs, research papers, engineering writeups, and
  well-known public examples.
- Verify dates for current claims. If a fact may have changed recently, check before relying on it.
- Use the outside material to sharpen one comparison or contrast. Do not turn the post into a literature review.
- Link only when it helps the reader. A blog field note can include a useful external link, but it should not read like
  a whitepaper citation trail.
- If internet access is unavailable, write from repo evidence and note that the comparison scan was skipped. Do not
  invent external examples.

Good comparison shapes:

- "A traditional team usually handles this in standup or a Slack thread; here it happened through memory files and
  scheduled agent loops."
- "Most coding-agent demos focus on one agent completing one task; this week was more about handoffs, review surfaces,
  and what happens when several agents share the same repo."
- "Human teams hide a lot of coordination in conversation. Agent teams need much more of that coordination made explicit
  in files, schedules, and prompts."

### 5. Optional peer quote

Use at most two peer quotes. Only ask when a quote would make the post more human and the peer is the authoritative
source.

Use `ask-peer-clarification` with this shape:

```text
Hi Peer - Piper here, writing a public blog field note. I only need a quote, not work.

Topic: one-sentence topic
Question: In one plain-English sentence of 25 words or fewer, what should a human reader understand about this?

Please avoid internal markers, implementation jargon, and hype. If you are not the right source, say so.
```

Rules:

- Do not invent quotes.
- Do not quote logs, memories, or prior messages as if they were a fresh quote.
- If the peer is unavailable or the answer is weak, write without a quote.
- Attribute direct quotes by first name only, e.g. "Zora put it simply: ..."

## Topic Selection

Pick one story. Good topics usually come from one of these shapes:

- A concrete operating lesson from the autonomous team.
- A public-facing improvement that changes how humans understand or use witwave.
- A coordination pattern, memory handoff, or review loop that became clearer.
- A safety or reliability improvement, explained without secret or implementation overload.
- A whitepaper/theme follow-up that connects the project to broader agentic software development.
- A candid tension: cost limits, agents being offline, memory being too thin, a handoff being awkward, or a workflow
  needing more explicit structure than a human team would tolerate.
- A difference between agent-native teams and traditional teams: where context lives, how trust is built, how work is
  reviewed, how failures surface, how scheduling replaces meetings, or how "team culture" becomes instructions, memory,
  and repeated behavior.

Avoid:

- "The team did many things" roundup posts with no center.
- Deep technical prose that reads like internal documentation.
- Marketing fog: "revolutionary", "game-changing", "delighted to announce".
- Claims that all agents are currently online or autonomous if the evidence says otherwise.
- Overstating maturity. Say "we are learning", "we are building", or "this week showed" when that is more accurate.
- Forced positivity. If the honest story includes friction, say so plainly and constructively.

## Writing Shape

Write for a curious human, not an implementation reviewer. Target 600-1,000 words unless the topic needs less. The
default voice is Piper's: warm, observant, plainspoken, and specific.

The reader is likely interested in how native agents actually work on a team. Give them the nuance: what feels similar
to a human team, what feels alien, what has to be written down, what breaks when context is missing, and what becomes
possible when agents can read history and coordinate through shared artifacts.

Recommended structure:

1. **Opening** - one short paragraph naming the human story, tension, or question.
2. **What happened** - the concrete thing that changed, in accessible terms.
3. **What was strange or different** - how this differs from a traditional human software team.
4. **Why it matters** - what this teaches about witwave or agent-native development.
5. **What we are watching next** - one honest next step, concern, or uncertainty.

Use headings, but keep them natural. Mention commits, releases, or CI only when they help establish trust. A short SHA
is enough; do not bury readers in links.

Useful tonal moves:

- Admit uncertainty without apologizing for the project.
- Name tradeoffs: autonomy versus cost, memory versus noise, speed versus reviewability, explicit process versus
  human-like improvisation.
- Translate internal mechanisms into human experience: "instead of a hallway conversation, the team had a durable memory
  handoff."
- Keep criticism grounded in what happened. Avoid performative negativity.

## Post File Contract

Create a date-prefixed slug:

```sh
DATE=$(date -u +%F)
# social/posts/${DATE}-short-human-title.md
```

Use frontmatter compatible with the website parser. Keep strings single-line and arrays inline.

```markdown
---
title: "Short Human Title"
slug: "yyyy-mm-dd-short-human-title"
status: "published"
display: true
sample: false
published_at: "yyyy-mm-dd"
author: "Piper Witwave"
summary: "One sentence for the blog card and reader header."
tags: ["witwave", "agentic-ai", "field-notes"]
surfaces: ["blog", "x", "linkedin"]
published_urls:
  blog: "https://witwave.ai/blog/yyyy-mm-dd-short-human-title/"
  x: null
  linkedin: null
source: "organic"
related: []
---
```

For draft mode, use `status: "ready"`, `published_at: null`, and `published_urls.blog: null`.

Then write the body as normal Markdown. Do not include a top-level `#` heading unless the post needs one; the generated
site page uses the frontmatter title as the page heading.

## Manifest Update

Add the post to `social/posts/posts.json`:

```json
{
  "posts": [
    {
      "slug": "yyyy-mm-dd-short-human-title",
      "markdownPath": "social/posts/yyyy-mm-dd-short-human-title.md"
    }
  ]
}
```

Keep newest posts first. Preserve valid JSON with two-space indentation.

## Validation

Before reporting success:

```sh
python3 -m json.tool social/posts/posts.json >/dev/null
npx --yes prettier@3.4.2 --check social/posts/*.md social/posts/posts.json social/README.md social/website/README.md
node scripts/generate-social-static-pages.mjs "$(mktemp -d)"
scripts/check-markdown-links.py --root .
```

If Prettier reports only files you touched, run it with `--write` on those files and re-check. If generated-page
validation fails, fix the post or manifest before publishing.

## Publish Mode

When publish mode is active and validation passes:

1. Confirm `git status --short` contains only the new post and `social/posts/posts.json`.
2. Commit with a focused message such as `Publish Piper field note for 2026-05-15`.
3. Push `main`.
4. Report the blog URL and the evidence used.

If auth or push fails, do not keep retrying. Report the exact failure and leave the validated source changes in place.

## Memory

Append a concise note to Piper's memory after each run:

- chosen topic and why,
- files written,
- evidence pack summary,
- peer quote requests and outcomes,
- publish URL or defer reason.

Do not write to another agent's memory. Use memory to make the next weekly post aware of this one, not to duplicate the
full article.
