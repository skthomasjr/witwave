---
name: ask-peer-clarification
description:
  Ask a peer agent for a one-question clarification before posting publicly. Wraps `call-peer` with the
  framing that you're about to post to GitHub Discussions and need confirmation/context, not work.
  Returns the peer's reply for inclusion in the draft post. Trigger when team-pulse encounters something
  ambiguous in the team's state and needs the authoritative voice from the peer who owns the relevant
  domain. Especially Zora — she's the team-state oracle.
version: 0.1.0
---

# ask-peer-clarification

A focused wrapper around `call-peer` for the specific case where Piper is drafting a public post and
something in the team's state is ambiguous enough to risk misframing the post.

The distinction from `call-peer` matters: every other peer's `call-peer` use is for delegating WORK
("Iris, please push these commits"). Piper's use is purely for INFORMATION ("Iris, was the v0.23.4
release pipeline pending or failed at the time you cut the tag?"). The framing tells the recipient peer
to answer briefly with the fact, not to launch a skill or take an action.

## Inputs

- **`peer`** — one of `iris` / `kira` / `nova` / `evan` / `finn` / `zora`. Required.
- **`question`** — the one specific question. Required. Should be answerable in ≤ 50 words.
- **`context_for_post`** _(optional)_ — one-line summary of what Piper is about to post, so the peer
  understands why the answer matters and whether the framing they're hearing is correct. Helps the peer
  push back if Piper's framing is wrong.

## Instructions

### 1. Compose the call-peer prompt

Use this template — be explicit that you're requesting INFORMATION only, no action:

```
Hi <Peer> — Piper here, drafting a public post for GitHub Discussions. I need a quick clarification
before posting. Not asking you to do anything; just need the fact.

Context I'm about to post: <context_for_post>

Question: <question>

Please reply briefly (≤ 50 words). If my framing is wrong, push back. If you don't know the answer
authoritatively, say so — I'll defer the post rather than guess.
```

### 2. Dispatch via call-peer

```
call-peer peer=<peer> prompt=<the composed text above>
```

`call-peer` is synchronous over A2A; you'll get the peer's reply in the response. Default A2A timeout is
~5 min — at your 15min team-pulse cadence, that's one-third of a tick budget, so wrap in a shorter
ctx (`--timeout 60s` or similar at the call site so a slow peer doesn't block your tick beyond the
next heartbeat).

### 3. Handle the reply

Three useful shapes:

- **Direct answer** — the peer answered cleanly. Inline the answer (or an excerpt) into your draft post
  with attribution: "(per Iris's clarification: ...)". Don't quote the entire reply unless it's already
  short.
- **Pushback on framing** — the peer's reply says "your framing of X is wrong; the actual situation is
  Y". Update the draft with the correct framing. Optionally note in the post that the framing was
  clarified by the peer (transparency without dwelling on it).
- **"I don't know authoritatively"** — defer the post. Add a `[deferred: peer-couldnt-confirm]` entry
  to your draft (in `drafts/`) so a future tick can re-attempt with a different peer or after the peer
  has the answer.

### 4. Log the clarification round-trip

In your `pulse_log.md` tick entry, include:

```yaml
- clarification_asked: <peer>
- clarification_question: <question>
- clarification_response: <peer's-reply-summary, ≤ 30 words>
- clarification_outcome: <inline | reframed | deferred>
```

This way a human auditing your post-history can see what you asked, what you got, and how it shaped the
post.

## Use sparingly — read-first is your default

The point of this skill is to AVOID speculation in public posts. But every clarification round-trip
interrupts the peer (who is doing real work) and adds noise. **Default mode: dig the answer up
yourself.** Only invoke this skill after you've walked the read-first checklist and the answer is
genuinely missing.

**Read-first checklist** (verify at least one source is silent on the question before pinging):

1. The peer's own `MEMORY.md` index + relevant deferred-findings file (their audit trail often answers).
2. The commit message body of the relevant commit (`git show <sha>` — atomic per-finding commits
   typically explain the why).
3. Zora's `decision_log.md` for the relevant tick (her rationale is usually cited).
4. The relevant source code when the finding cites a file or symbol.
5. `escalations.md` for surrounding context if the question relates to an escalation.

**Then check three gates** — only invoke this skill if ALL THREE are true:

1. **The information is critical** — the framing of the public post meaningfully hinges on the answer,
   not just adds nice-to-have detail.
2. **You can't derive it from any read source** — the read-first checklist genuinely came up empty.
3. **The peer you'd ask is the authoritative source** — don't ask Iris about Evan's findings; ask Evan.
   Don't ask Evan about why a release pipeline failed; ask Iris.

If any gate fails, **defer the post** rather than ping. A delayed accurate post beats a timely-but-pinged
or timely-but-speculative post every time.

Most ticks will ask zero peers anything. That's the design — Piper is read-mostly, the team is
work-mostly, the channels are quiet by default.

## Special framing for Zora questions

Zora is the team-state oracle — her decision_log + escalations + team_state files are the canonical
source for "what's the team doing right now and why". When you `ask-peer-clarification peer=zora`, your
question should typically be about a decision or state-transition you saw in her log but can't fully
parse. E.g.:

- "I see escalation `release-workflow-stuck` opened at 14:30Z but no closure entry yet — is the
  pipeline still stuck or did it self-recover and you just haven't logged the close?"
- "Your decision_log shows you skipped Evan's bug-work cadence twice in a row; was that intentional
  (e.g., he's stuck) or a tick-priority quirk I should explain to humans?"

These are factual questions about her state, not about her decisions. Don't critique her decisions;
that's not your domain.

## Out of scope for this skill

- **Asking peers to do work.** That's `call-peer` directly, with a different framing — and it's not
  Piper's lane anyway. Only Zora dispatches work.
- **Multi-question conversations.** One question per call. If you need a second clarification after
  the first reply, that's a fresh `ask-peer-clarification` call (or skip and defer the post).
- **Polling peers for status.** Their MEMORY.md and findings files are the polled surface;
  `ask-peer-clarification` is for live questions about specific events. Don't use it as a "how are you
  doing?" check-in.
