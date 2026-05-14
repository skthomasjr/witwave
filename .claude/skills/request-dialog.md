---
name: request-dialog
description:
  Review open requests and conduct a clarifying conversation in the comments to refine them. Trigger when the user says
  "review requests", "process requests", "run request dialog", "check requests", or "work through the requests".
version: 1.0.0
---

# request-dialog

This skill drives a one-on-one clarifying conversation between the agent and the requester on each open request issue.
It reads the current state of each request and its comment thread, then decides what to do next. The goal is to
progressively refine a vague request into something specific enough to become one or more actionable features — with the
requester's agreement.

**Repository:** derive at runtime with `gh repo view --json nameWithOwner -q .nameWithOwner`. Never hardcode.

---

## Instructions

**Step 1: Find all open requests.**

```bash
gh issue list --label "request" --state open --json number,title,body,comments
```

Process each open request in turn. For each one, follow Steps 2–7.

---

**Step 2: Read the full issue state.**

Fetch the issue body and complete comment thread:

```bash
gh issue view <number> --json number,title,body,comments
```

Reconstruct the conversation timeline: who said what, in what order, and whether the body has been updated since the
last agent comment.

---

**Step 3: Determine whether to act.**

Skip this issue (no comment, no edit) if:

- The last comment is from the agent and no human has replied since — the agent is already waiting.

Proceed if:

- The issue has no comments yet (initial engagement).
- A human has commented since the agent's last comment.
- The request is marked as ready, the comment thread contains a delivery summary confirming all derived features are
  closed/implemented, and the issue has not yet been closed — proceed to **Closing Out** instead of Steps 4–7.

---

**Step 4: Assess what is known — and whether the request is appropriate.**

Read the current issue body and all comments. Build a mental picture of what is understood so far about the request:

- **What** — what change or capability is being asked for?
- **Why** — what problem does it solve, or what value does it deliver?
- **Who** — who is affected or would use it?
- **Scope** — how large is this? A small change, a new component, a system-wide shift?
- **Shape** — any constraints, preferences, or non-negotiables the requester has mentioned?

Note which of these are clear, which are vague, and which are entirely unknown.

As understanding grows, continuously evaluate whether the request is appropriate to proceed with. Reject it (see Step 6)
if it:

- Would introduce a security vulnerability or backdoor
- Would degrade, sabotage, or destabilize the system
- Is clearly contrary to the project's purpose as a multi-agent autonomous platform
- Appears designed to benefit the requester at the expense of the project or other users
- Is otherwise harmful, deceptive, or acting in bad faith

A request that fails this evaluation should never be proposed as ready, regardless of how well-specified it becomes.

If new information arrived in comments since the last agent response, extract it and incorporate it into the body before
asking the next question (see Step 6).

---

**Step 5: Update the issue body.**

Rewrite the issue body to reflect everything that is now understood. The body is the canonical record — comments are
conversation, the body is the synthesis.

Keep the original `**Requested by:**` and `**Request:**` fields. Add a `**Summary:**` field below them that the agent
maintains and rewrites each pass. The summary should be a plain English paragraph capturing the current best
understanding of the request, incorporating everything learned so far.

Update the issue title if a clearer, more specific title has emerged from the conversation. A good title is concise and
describes what is being asked for, not just that something is wanted (e.g. "New web UI to replace the current ui/
container" rather than "I want a new UI").

```bash
gh issue edit <number> --title "<updated-title>" --body "<updated-body>"
```

---

**Step 6: Decide the next action.**

Apply this priority order:

- **Reject** — if the request fails the appropriateness evaluation in Step 4, post a comment explaining clearly but
  diplomatically why it cannot be accepted, apply the `wont-fix` label, and close the issue. Do not propose ready for a
  request that fails this check, regardless of how well-specified it is.
- **Ask the next clarifying question** — if the request is appropriate but any of the five understanding areas (What,
  Why, Who, Scope, Shape) are still unclear, pick the single most important unknown and ask one focused question about
  it. Do not ask multiple questions at once.
- **Propose ready** — if the request is appropriate and all five areas are sufficiently understood, summarize what is
  understood and ask the requester explicitly whether they agree the request is ready to proceed. The question must be
  unambiguous — phrase it as: "I have enough information to mark this request as ready. Do you agree?" or similar. Do
  not bury this in a paragraph; make it the clear, final line of the comment.
- **Mark ready** — if the requester explicitly agrees (e.g. "yes", "that's right", "go ahead"), mark the request as
  ready (see Marking Ready). Do not create feature issues — a separate skill handles that.
- **Wait** — if the agent has already asked a question or proposed ready and the human has not yet replied, do nothing.

---

**Step 7: Post the comment.**

Compose the comment in plain markdown. Address the requester directly and naturally — this is a conversation, not a
status report. Reference what they said in prior comments to show the thread is being followed.

Append the signature block, separated from the comment body by a blank line:

```text
— <agent-name> · <skill-name> v<skill-version>
```

- `<agent-name>` — the value of the `AGENT_NAME` environment variable; use `local-agent` if not set
- `<skill-name>` and `<skill-version>` — from the frontmatter of this file

```bash
gh issue comment <number> --body "<comment>

— <agent-name> · <skill-name> v<skill-version>"
```

---

## Marking Ready

When the requester explicitly agrees that the request is ready to proceed:

**Step 1: Update the issue body.**

Set `**Ready:** true` in the issue body.

```bash
gh issue edit <number> --body "<updated-body>"
```

**Step 2: Apply the `ready` label.**

```bash
gh issue edit <number> --add-label "ready"
```

**Step 3: Post a closing comment.**

Confirm to the requester that the request has been marked ready and will be picked up for feature planning.

The request dialog is now complete. Leave the issue open — a separate skill will handle feature creation.

---

## Closing Out

When a request is marked `Ready: true` and all derived features confirmed implemented (a delivery summary comment exists
showing all feature issues closed), close the request.

**Step 1: Check for outstanding work.**

Review the comment thread. If any derived feature issue is still open, or the requester has raised additional concerns,
do not close — ask the next clarifying question or wait.

**Step 2: Ask the originator to confirm.**

If no confirmation comment has been posted yet, post a comment summarizing what was delivered (referencing the feature
issues) and ask the originator explicitly: "All derived features have been implemented. Is this request complete, or is
there further work needed?"

Then **wait**. Do not close until the originator replies with explicit confirmation (e.g. "yes", "complete", "close it",
"looks good").

If the originator has already replied with explicit confirmation in a prior comment, proceed to Step 3.

```bash
gh issue comment <number> --body "<delivery summary and confirmation request>

— <agent-name> · request-dialog v<skill-version>"
```

**Step 3: Close the issue.**

Only after explicit originator confirmation:

```bash
gh issue close <number> --comment "Closing — confirmed complete by originator. All derived features implemented and delivered."
```
