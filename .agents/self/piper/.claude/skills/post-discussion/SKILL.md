---
name: post-discussion
description:
  Publish a single post to a GitHub Discussion category in the primary repo via `gh api graphql`. Used by `team-pulse`
  after it has decided a post is warranted, drafted the title + body, and chosen the route (Announcements or Progress).
  Idempotent on retry within the same call (uses a clientMutationId derived from the body hash). Trigger when the user
  says "post a discussion", "publish to GitHub Discussions", or as the publish step inside `team-pulse`.
version: 0.1.0
---

# post-discussion

Single-shot publish of a draft post to one of the team's GitHub Discussion categories.

You don't decide WHEN to post — `team-pulse` does that scoring + cooldown work. You don't decide WHAT to write —
`team-pulse` drafts the title + body. Your only job: get the post on the wire safely.

## Inputs

- **`category`** — one of `announcements` / `progress`. Required.
- **`title`** — string, max 60 chars (GitHub allows 255 but the team voice rule prefers tight titles). Required.
- **`body`** — string, the markdown body of the post. Required. No internal markers, no marketing phrasing — see
  CLAUDE.md → "Voice".
- **`bundled_events`** _(optional)_ — list of short event references that this post represents (commit SHAs, PR numbers,
  escalation IDs). Stored alongside the post URL in `pulse_log.md` for audit.

## Instructions

### 1. Resolve the category ID

Read `/workspaces/witwave-self/memory/agents/piper/reference_gh_discussions.md` for the category IDs captured by your
initial setup pass:

```yaml
repo:
  owner: witwave-ai
  name: witwave
  id: <repository node ID — capture once via gh api graphql>
categories:
  announcements:
    id: <DIC_… node ID>
    slug: announcements
    name: Announcements
  progress:
    id: <DIC_… node ID>
    slug: progress
    name: Progress
```

If the file doesn't exist OR the requested category isn't in it, refresh:

```sh
gh api graphql -f query='
{
  repository(owner: "witwave-ai", name: "witwave") {
    id
    discussionCategories(first: 20) {
      nodes { id name slug }
    }
  }
}'
```

Update `reference_gh_discussions.md` with the freshly-fetched IDs. Then proceed.

### 2. Compose the GraphQL mutation

```sh
gh api graphql -f query='
mutation($repo: ID!, $cat: ID!, $title: String!, $body: String!) {
  createDiscussion(input: {
    repositoryId: $repo,
    categoryId: $cat,
    title: $title,
    body: $body
  }) {
    discussion {
      id
      number
      url
      createdAt
    }
  }
}' -F repo="$REPO_ID" -F cat="$CAT_ID" -f title="$TITLE" -f body="$BODY"
```

Capture the returned `url` — this is what gets logged in `pulse_log.md` and returned to the caller.

### 3. Handle errors

Common failure shapes and the right response:

- **HTTP 401 / 403** — PAT missing or lacks `discussion: write` scope. Log `[error: gh-auth-failed-on-post]` to
  `pulse_log.md`, surface to `/workspaces/witwave-self/memory/agents/piper/needs-human-review.md`. Do NOT retry —
  repeated 401s burn rate-limit budget without progress. The next tick will see the marker and skip posting until the
  user intervenes.
- **HTTP 422** (validation error) — title too long, body empty, etc. Surface the GitHub error verbatim. This is a
  `team-pulse` drafting bug; fix at the caller.
- **Category ID stale** (404 on the mutation) — refresh `reference_gh_discussions.md` per Step 1, retry ONCE. If still
  404, surface to `needs-human-review.md` and skip.
- **Rate-limited** (HTTP 429 / `secondary_rate_limit_exceeded`) — back off; defer to next tick. The team posts rarely so
  this shouldn't bite, but be defensive.
- **Network error** — retry once after a 2s pause. On second failure, log + skip.

### 4. Return the post URL

To the caller (`team-pulse`), return the discussion's `url` field (the human-friendly
`https://github.com/witwave-ai/witwave/discussions/<number>` shape) so it can be logged in `pulse_log.md`.

If the post was suppressed (auth failure, draft mode, etc.), return `null` AND a `reason` string so the caller can log
it instead of the URL.

## Draft mode (when GitHub identity isn't ready)

While the `piper-agent-witwave` GitHub account creation is still pending (per Piper's CLAUDE.md → Identity section),
your PAT won't exist and posting will 401. In that case:

- Don't attempt the `gh api graphql` write.
- Instead, write the would-have-posted draft to
  `/workspaces/witwave-self/memory/agents/piper/drafts/<timestamp>-<category>.md` with frontmatter: `category`, `title`,
  `bundled_events`, `route`, `would-have-posted-at: HH:MMZ`.
- Return `null` URL and `reason: "draft-mode-no-pat"` to the caller.

This way the user can review what Piper would have posted, validate voice + scoring, and only flip to live posting once
both identity + voice are verified.

## Out of scope for this skill

- **Drafting the post.** `team-pulse` does that.
- **Choosing the category.** `team-pulse` decides Announcements vs Progress.
- **Editing existing posts.** Posts are append-only in the team's policy; corrections happen via follow-up posts, not
  edits.
- **Replying to threads / handling mentions.** That's a future `read-discussion-thread` / `respond-to-mention` skill.
- **Cross-posting to other surfaces** (Twitter, Slack). v2 work.
