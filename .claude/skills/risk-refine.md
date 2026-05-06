---
name: risk-refine
description:
  Analyze pending risks holistically, identify dependencies and conflicts, update stale information, and produce an
  execution order for mitigating. Optionally scoped to a specific component; if no component is specified, processes all
  components including cross-cutting risks with no component assigned. Trigger when the user says "refine risks", "run
  risk refine", "analyze risks", "clean up risks", "prioritize risks", or "order risks for mitigation".
version: 1.3.0
---

# risk-refine

Analyze pending risks as a set — not individually — to identify dependencies, conflicts, redundancies, and stale
information. The output is a clean, ordered, dependency-aware work queue ready for mitigation.

## Instructions

**Step 1: Gather all pending risks.**

The user may optionally specify a component (e.g. "refine risks for codex") or say "all" to process everything. If no
component is specified, process all pending risks across all components.

Retrieve all open pending risks, filtered by component if specified. Read each one in full — component, file, line
number, category, condition, impact, suggested mitigation, and any existing depends-on relationships.

Note: some risks are cross-cutting and have no component assigned. When processing all components, always include
cross-cutting risks. When filtering to a specific component, exclude cross-cutting risks unless the user explicitly asks
to include them.

**Step 2: Verify each risk against the current code.**

Read the **full source file**, not just the cited line range. False-positive risks survive into refine when both
discover and refine focus only on the cited lines and miss surrounding context — or mitigation at a different layer —
that already addresses the concern.

For each risk, confirm:

- The risk still exists at the referenced location
- If the code has shifted (refactor, lines moved), update the file and line reference to reflect the current location
- If the surrounding code has changed, re-evaluate whether the condition and mitigation still apply correctly to the
  current code — not just whether the risk is still present
- If a partial mitigation was already applied, note what remains and update the description accordingly

Then run the same intentional-design checklist that risk-discover Step 5 runs, in case it was missed at filing time.
Specifically verify:

- **No inline `#NNNN` comment within ~10 lines** documents the choice as intentional or references a prior issue that
  already accepted/closed this risk
- **No mitigation already in place at a different layer** (caller-side timeout, controller-runtime backoff,
  `with_kube_retry` wrapper, HTTP-client retry policy, asyncio `wait_for` higher up the call stack)
- **No cap / sweeper / bound exists elsewhere** (`MAX_*` constant, periodic sweeper task, LRU eviction, chart resource
  limits, caller-side rate limit)
- **No metric counter or structured log** at the failure site that would surface the silent-failure concern
- **The "insecure default" isn't a documented escape hatch** (loud-warn `*_DISABLED=true` env vars are intentional dev
  affordances, not security risks)
- **The "maintainability" concern isn't a documented design choice** (claude-as-superset metric pattern,
  single-source-of-truth shared modules)

If any of these resolve the concern, **close the risk as `wont-fix`** with a note explaining what the discovery framing
missed and where the mitigation actually lives. Do not let an invalid risk propagate to approve and implement — that
wastes effort across both downstream phases.

If a risk is no longer present (the code was actually mitigated since filing), close it with a note explaining what
changed.

**Step 3: Validate uncertain findings.**

Before updating records or identifying relationships, flag any risks where you are not confident the risk is real, the
severity is correctly assigned, or the mitigation is sound. For those, do a web search to check whether the risk is a
recognized problem in the industry and whether the proposed mitigation is standard practice. A finding is uncertain if
any of the following apply:

- The category or priority feels inconsistent with similar risks in the set
- The mitigation is vague, contested, or depends on an approach you are not sure is applicable here
- You are not sure whether the condition could actually occur given this architecture

If a search confirms the finding and severity, proceed. If the search suggests the priority or category is wrong, update
the record accordingly and note what you found. If the search shows the risk does not apply, close it with a note
explaining why.

**Step 4: Identify relationships across risks.**

Read all affected files together and look for:

- **Dependencies** — risk A must be mitigated before risk B because B's mitigation assumes A's mitigation is in place,
  or mitigating B first would be invalidated by A's mitigation
- **Conflicts** — two risks touch the same code in ways that would cause one mitigation to break the other
- **Common root cause** — two or more risks that stem from the same underlying issue and should be mitigated together in
  a single change
- **Cascading risk** — a mitigation for one risk that increases or decreases the risk profile of another

**Step 5: Update risk records.**

For every risk processed, add a comment documenting:

- Whether the risk was verified, stale, or already partially mitigated
- Any changes made to the file, line, condition, or mitigation description
- Any dependencies identified and why
- If nothing changed, explicitly state that the record was reviewed by this skill (name and version, see frontmatter)
  and is current as of this run
- The name and version of this skill (see the frontmatter of this file)

For risks where relationships were found, also:

- Populate the `Depends on` field with any risk numbers that must be resolved first
- Apply the `blocked-by` label to any risk that has dependencies
- Update the `Mitigation` field if the suggested mitigation needs revision based on current code or discovered
  relationships

**Step 6: Produce an execution order.**

Output a recommended mitigation order based on dependencies, priority, and category:

- Risks with no dependencies and high priority come first
- Security risks take precedence over other categories at equal priority
- Blocked risks come after their dependencies
- Where risks share a common root cause, group them together
- Note any risks that should be mitigated in the same commit or PR to avoid intermediate broken states

The execution order is a recommendation, not a constraint — but it should reflect the safest, most efficient path
through the pending work queue.
