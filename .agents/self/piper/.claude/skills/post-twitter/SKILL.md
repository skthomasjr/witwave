---
name: post-twitter
description:
  Publish a single post (or a chained thread) to X / Twitter via the X API v2. Used by `team-pulse` after it has decided
  a post is warranted, drafted the text, and chosen X as a surface. Authenticates with OAuth 1.0a user-context using the
  four credentials stored in the `piper-x-credentials` Secret. Single posts are ≤280 chars; threads chain N posts via
  `reply.in_reply_to_tweet_id`. Trigger when the user says "post to X", "post to Twitter", "publish a tweet", or as the
  X publish step inside `team-pulse`.
version: 0.1.0
---

# post-twitter

Single-shot publish of a draft post (or thread) to the team's X account.

You don't decide WHEN to post — `team-pulse` does that scoring + cooldown work. You don't decide WHAT to write —
`team-pulse` drafts the text and chooses whether it should be a single post or a thread. Your only job: get the post on
the wire safely.

## Inputs

- **`text`** — string, the body of a single post (≤280 chars on Free/Basic tier). Required for single-post mode.
- **`thread`** — list of strings, each ≤280 chars, for thread mode. Required for thread mode. Mutually exclusive with
  `text` — exactly one of `text` or `thread` is set.
- **`reply_to_tweet_id`** _(optional)_ — string. When set, this post is a reply rather than a standalone post. Used for
  follow-up posts on a previously published Piper tweet.
- **`bundled_events`** _(optional)_ — list of short event references this post represents (commit SHAs, PR numbers,
  escalation IDs). Stored alongside the post URL in `pulse_log.md` for audit.

Exactly one of `text` or `thread` must be set. Both empty → return `null` with `reason: "no-content"`.

## Pre-flight checks (run before any API call)

### 1. Credentials present

The four OAuth 1.0a credentials are mounted from the `piper-x-credentials` Secret into the claude container as env vars:

- `X_API_KEY` (a.k.a. Consumer Key)
- `X_API_SECRET` (a.k.a. Consumer Secret)
- `X_ACCESS_TOKEN`
- `X_ACCESS_TOKEN_SECRET`

If any of the four are missing or empty, do NOT attempt the API call. Fall through to **Draft mode** (see below). The
bot account / API setup is incomplete.

### 2. Character-limit validation

Reject before the wire to avoid pointless 4xx round-trips:

- **Single post mode (`text`):** if `len(text) > 280`, return `null` with `reason: "exceeds-280-chars (got <N>)"`. URLs
  count as 23 chars regardless of actual length — adjust your counting accordingly. (X auto-shortens URLs via t.co.)
- **Thread mode (`thread`):** check each entry. On any over-limit entry, return `null` with
  `reason: "thread-entry-N-exceeds-280-chars"` so the caller can fix that specific entry.

### 3. Idempotency guard (light)

Before posting, hash the `text` (or the concatenation of `thread` entries) and check
`/workspaces/witwave-self/memory/agents/piper/twitter_posts.md` for the same hash within the last 24h. If a match is
found, return `null` with `reason: "duplicate-content-recent"`. Prevents accidental re-posts when `team-pulse`
mis-fires.

## Instructions

### 1. Authenticate (OAuth 1.0a user-context)

Use Python with `requests-oauthlib` (installable via pip if not present in the container):

```python
import os
from requests_oauthlib import OAuth1Session

auth = OAuth1Session(
    client_key=os.environ["X_API_KEY"],
    client_secret=os.environ["X_API_SECRET"],
    resource_owner_key=os.environ["X_ACCESS_TOKEN"],
    resource_owner_secret=os.environ["X_ACCESS_TOKEN_SECRET"],
)
```

OAuth 1.0a signing is handled by the library. Don't hand-roll the signature; it's error-prone and the X API rejects
malformed signatures with cryptic 401s.

### 2. Single-post mode

```python
payload = {"text": text}
if reply_to_tweet_id:
    payload["reply"] = {"in_reply_to_tweet_id": reply_to_tweet_id}

r = auth.post("https://api.x.com/2/tweets", json=payload)
r.raise_for_status()
result = r.json()  # {"data": {"id": "...", "text": "..."}}
post_id = result["data"]["id"]
post_url = f"https://x.com/<bot-handle>/status/{post_id}"
```

Replace `<bot-handle>` with the actual account handle from `reference_x_identity.md` (see Step 4).

### 3. Thread mode

Post the first entry standalone, then post each subsequent entry as a reply to the _previous_ entry (NOT to the first —
X UI threads correctly only if each post replies to the immediately-prior one):

```python
prev_id = reply_to_tweet_id  # None for a brand-new thread
post_ids = []
post_urls = []

for entry in thread:
    payload = {"text": entry}
    if prev_id:
        payload["reply"] = {"in_reply_to_tweet_id": prev_id}
    r = auth.post("https://api.x.com/2/tweets", json=payload)
    r.raise_for_status()
    data = r.json()["data"]
    prev_id = data["id"]
    post_ids.append(data["id"])
    post_urls.append(f"https://x.com/<bot-handle>/status/{data['id']}")
```

Return the FIRST post's URL as the canonical thread URL (that's what humans share when linking to a thread).

If any post in the thread fails partway through, log the partial state to `twitter_posts.md` with
`[partial-thread: posted N of M]` and surface to `needs-human-review.md`. Do NOT attempt to delete the already-posted
entries — deletion isn't in your tool surface and the partial thread is preferable to silent loss.

### 4. Identity reference

`/workspaces/witwave-self/memory/agents/piper/reference_x_identity.md` holds the X account handle and any other identity
metadata:

```yaml
account:
  handle: piper-witwave # the @ — used to construct status URLs
  display_name: Piper
  automated_label: applied # X "Automated" label status
  operator: skthomasjr # human responsible per X automation rules
  account_id: <numeric ID> # for reply addressing and verification
```

Refresh this file via a self-lookup (`GET /2/users/me`) on first run; cache the result. Bot handle changes are rare but
possible.

### 5. Log the post

Append to `/workspaces/witwave-self/memory/agents/piper/twitter_posts.md`:

```markdown
## YYYY-MM-DDTHH:MMZ — post

- mode: single | thread | reply
- content_hash: <sha256 prefix 12 chars of text-or-thread-concatenation>
- post_ids: [<id-1>, <id-2>, ...]
- urls: [<url-1>, <url-2>, ...]
- canonical_url: <first-url>
- reply_to: <reply_to_tweet_id or null>
- bundled_events: [<commit-sha-or-event-ref>, ...]
- chars_used: <int> | thread: [<n1>, <n2>, ...]
```

The append-only log is read by future ticks for the idempotency-guard hash check, and by humans auditing what Piper has
posted.

### 6. Return

To the caller (`team-pulse`):

- On success: return `canonical_url` (the first post's URL) plus `post_ids` (the full list for thread tracking).
- On any failure: return `null` plus `reason` string.

## Error handling

Common failure shapes and the right response:

- **HTTP 401** — OAuth credentials wrong, expired, or revoked. Log `[error: x-auth-failed]` to `twitter_posts.md`,
  surface to `needs-human-review.md`. Do NOT retry — repeated 401s burn rate-limit budget. The next tick will see the
  marker and skip posting until the user rotates credentials.
- **HTTP 403 — `unauthorized`** — your account permissions on the App are Read-only, not Read+Write. Surface to
  `needs-human-review.md` with a hint: _"Check developer.x.com → App → User authentication settings → Permissions = Read
  and Write."_
- **HTTP 403 — `automated-label-required`** — X is enforcing the Automated label and the bot account doesn't have it
  applied yet. Surface to `needs-human-review.md` with: _"Apply the Automated label via Account settings → Account
  Information → Automated on the bot account."_
- **HTTP 429 — rate-limited** — back off. Log the `x-rate-limit-reset` header value; defer next post until after that
  timestamp. The team posts rarely so this shouldn't bite, but be defensive especially during launch bursts.
- **HTTP 422 — content validation** — duplicate post content within X's duplicate-content window, or banned content. X
  is opaque about which. Surface verbatim error to caller; do NOT retry the same content.
- **Network error / timeout** — retry once after a 2s pause. On second failure, log + skip.

## Draft mode (when X credentials aren't ready)

While the X account + API setup is in progress (`piper-x-credentials` Secret doesn't exist OR env vars are empty), don't
attempt the API call. Instead:

- Write the would-have-posted text to `/workspaces/witwave-self/memory/agents/piper/drafts/<timestamp>-twitter.md` with
  frontmatter: `mode` (single|thread|reply), `text`-or-`thread`, `reply_to_tweet_id`, `bundled_events`,
  `would-have-posted-at: HH:MMZ`.
- Return `null` URL and `reason: "draft-mode-no-x-credentials"` to the caller.

This lets you review what Piper would have posted, validate voice + scoring, and flip to live posting once credentials
land.

## Out of scope for this skill

- **Drafting the post text.** `team-pulse` does that.
- **Deciding to post.** `team-pulse` decides based on the substantive-score.
- **Choosing single-post vs thread.** `team-pulse` decides based on content length and structure (a release announcement
  might be single; a multi-event weekly digest might be a thread).
- **Media attachments.** Image / video / GIF uploads are a v2 capability — requires the `/2/media/upload` endpoint and a
  different auth flow. Text-only in v1.
- **Polls / quotes / retweets.** Out of scope; text posts only.
- **Editing posts.** X supports edit windows on Premium accounts; we don't use them. Corrections happen via follow-up
  posts, same as the Discussions surface.
- **Deleting posts.** Irreversible action, not in the autonomous tool surface — parallel to the moderation posture in
  CLAUDE.md → Guard 0 (deletion is never autonomous on any surface Piper writes to).
- **Replying to non-Piper posts / DMs / mentions.** That's a future `discuss-twitter` skill (parallel to
  `discuss-comments`), not this one.

## When to invoke

- **From `team-pulse`** when the scoring decision routes content to X as a surface.
- **On demand** — user sends "post to X: <text>" via A2A. Same flow.
