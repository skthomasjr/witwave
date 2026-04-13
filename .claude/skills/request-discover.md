---
name: request-discover
description: Research the platform, competitive landscape, and product vision to surface compelling new requests. Trigger when the user says "discover requests", "find requests", "generate requests", "run request discover", "surface new requests", "discover requests for features", "find requests for features", or "generate requests for features".
version: 1.1.0
---

# request-discover

Research the platform's current capabilities, product vision, and competitive landscape to identify and file compelling new requests — written as a thoughtful human stakeholder would write them.

The goal is to maintain a live queue of at most five open requests at any time. This skill surfaces what is most worth building next, not an exhaustive wish list.

## Instructions

**Step 1: Understand the platform's current state.**

Read the following in full:

- `<repo-root>/README.md` and `<repo-root>/AGENTS.md` — architecture, components, and current capabilities
- `<repo-root>/docs/product-vision.md` — where the platform is headed and what it is trying to become
- `<repo-root>/docs/competitive-landscape.md` — who the competition is and how this platform differentiates
- `<repo-root>/docs/architecture.md` — how the system is structured and what it can do today

Then read the source files for any component relevant to ideas you are forming. Do not generate requests based on documentation alone — understand what the code actually does.

---

**Step 2: Research the competitive landscape.**

Do a web search to understand the current state of the autonomous agent and multi-agent platform space. Look for:

- What capabilities are competitors offering that this platform does not yet have?
- What patterns or integrations are becoming standard in the industry?
- What are users of similar platforms asking for most often?
- What differentiating capabilities could this platform develop that competitors are missing or doing poorly?

Use this research alongside the competitive landscape document — the document reflects a point in time, and the web reflects what is happening now.

---

**Step 3: Identify candidate requests.**

Synthesize everything from Steps 1 and 2 into a set of candidate requests. A good candidate:

- Is compelling — it would meaningfully improve the platform's value, usability, or differentiation
- Is grounded in the platform's architecture — it is something this system could plausibly build
- Is written at a human level of specificity — general enough that a dialog skill can refine it further, not a full specification
- Is not already covered by an existing open request or feature issue

Generate as many candidates as your research supports, then rank them by how compelling and well-grounded they are. Keep only the top candidates needed to bring the open request count to five.

---

**Step 4: Check existing open requests.**

Look up all open requests. Count how many are open. Subtract from five — that is how many new requests to file. If five or more are already open, stop and report the current queue without filing anything.

---

**Step 5: Replace stale orphaned requests if needed.**

If you need to file new requests but the queue is already at five, look for orphaned requests — open requests that have no comments at all and no activity since filing. These represent ideas that never generated engagement.

For each orphaned request, compare it against your top-ranked candidate. If the candidate is clearly more compelling, close the orphaned request with a brief note explaining it is being replaced by a higher-priority idea, then file the candidate in its place.

Only close orphaned requests — never close a request that has any comments or activity. If no orphaned requests exist and the queue is full, stop without filing.

---

**Step 6: File the new requests.**

For each request to file, write it as a human stakeholder would — conversational, expressing genuine interest in a capability, with enough context to understand the motivation but not so much detail that it forecloses the clarifying conversation. Use `<agent-name>` (from the `AGENT_NAME` environment variable; `local-agent` if not set) as the requester.

File each request. After filing, report the full list of open requests — existing and newly filed — so the current queue is visible.
