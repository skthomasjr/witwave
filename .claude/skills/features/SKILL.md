---
name: features
description: >-
  Research the competitive landscape and maintain a backlog of up to 25
  high-value feature proposals as GitHub Issues labelled type/feature
---

Maintain the feature backlog as GitHub Issues labelled `type/feature`. Each run
loads the current backlog, researches the competitive landscape, creates new
feature issues to fill any slots below 25 open proposals, and enriches or
updates existing issues with new evidence. Never exceed 25 open `type/feature`
issues — if already at 25, focus entirely on enriching existing ones.

Steps:

1. **Load existing context — do all of these before researching anything:**

   a. Read `<repo-root>/README.md`, `<repo-root>/AGENTS.md`,
      `<repo-root>/docs/product-vision.md`, and
      `<repo-root>/docs/competitive-landscape.md` to understand the project's
      current capabilities, target audience, and design principles.

   b. Read all Python source files under `<repo-root>/agent/` and
      `<repo-root>/agent/backends/` plus `Dockerfile` and `docker-compose.yml`
      to understand what is already built.

   c. Load all `type/feature` issues (open and closed) so you know what has
      already been proposed or implemented:

      ```bash
      gh issue list --state all --label "type/feature" --limit 100 \
        --json number,title,state,labels \
        --jq '.[] | "#\(.number) [\(.state)] \(.title)"'
      ```

      Use `--limit 100` to avoid the default 30-result cap. Count how many are
      currently open — this is the current backlog size. Note the issue numbers
      of all currently open issues — these are the only ones re-evaluated in
      step 6. Do not propose anything already tracked (open or closed).

2. **Research the competitive landscape.** Use WebSearch and WebFetch. Go to
   primary sources — GitHub repos, changelogs, release notes, issue trackers,
   official docs. Do not rely on marketing pages or summaries. Spend real time
   on each source; a shallow pass produces shallow proposals.

   **Primary targets — research these first and most deeply:**

   - **Claude Code** — Anthropic's own CLI agent. The closest model for what
     this project is building. Fetch the official changelog and GitHub
     Discussions. Search: `site:github.com/anthropics/claude-code`,
     "claude code changelog", "claude code new features", "claude code hooks
     site:github.com". Focus on: hooks, MCP integrations, memory, HITL
     patterns, slash commands, permission model, settings, session management.

   - **Codex CLI** — OpenAI's open-source CLI agent. Direct peer.
     Fetch `https://github.com/openai/codex` — read the README, CHANGELOG,
     open issues labeled "enhancement", and recent PRs. Focus on: approval
     modes, sandboxing, multi-file context, network/disk isolation,
     rollback, and any features that address operator trust.

   - **OpenHands** (formerly OpenDevin) — most feature-complete open-source
     autonomous agent. Fetch `https://github.com/All-Hands-AI/OpenHands` —
     read CHANGELOG, GitHub Discussions, and issues labeled "feature request".
     Search: "OpenHands runtime", "OpenHands evaluation", "OpenHands memory".
     Focus on: runtime isolation, evaluation harness, multi-agent routing,
     long-horizon task management.

   - **Any other app doing persistent autonomous agentic work** — search
     broadly: "persistent autonomous agent open source 2026",
     "self-directed coding agent GitHub", "always-on agent platform",
     "autonomous agent scheduler Kubernetes". Read repos that look genuinely
     similar to this project's architecture (containerized, persistent,
     schedule-driven, multi-agent).

   **Secondary targets — check for new developments, not a full re-read:**

   - **Devin** — focus on human-in-the-loop patterns, self-verification,
     and scheduling. Search: "Devin changelog 2026", "Devin new features".
   - **SWE-agent** — focus on ACI design and constraint patterns.
   - **CrewAI** — focus on multi-agent memory and coordination.
   - **LangGraph** — focus on durable execution and checkpoint/resume.

   **Community signal — this is where real demand lives:**

   - Search Hacker News: `site:news.ycombinator.com "autonomous agent"`,
     "claude code" site:news.ycombinator.com`, "agentic coding".
     Read top comment threads for pain points and feature requests.
   - Search Reddit: `site:reddit.com/r/LocalLLaMA autonomous agent`,
     `site:reddit.com/r/ClaudeAI "claude code"`. Look for recurring
     complaints and most-upvoted feature requests.
   - Search X/Twitter: "claude code feature request", "codex cli missing",
     "autonomous agent wishlist". Look for practitioners describing
     real workflow friction.
   - Check the Anthropic Discord and Claude Code GitHub Discussions for
     the most-requested features and unresolved pain points.

3. **Identify and record gaps.** Compare what competitors offer against what
   this project currently provides (from step 1). For each theme below, write
   1–3 sentences describing the most significant gap found — or "none found"
   if the project already covers it well.

   Write the gap map directly into `docs/competitive-landscape.md` under a
   `## Gap Analysis` section (create it if it doesn't exist, or replace the
   existing content). Format:

   ```markdown
   ## Gap Analysis

   _Last updated: <date> by <agent-name>_

   - **Memory and knowledge management:** <finding>
   - **Human-in-the-loop and approval gates:** <finding>
   - **Multi-agent coordination and delegation:** <finding>
   - **Scheduling and event-driven triggers:** <finding>
   - **Observability and debuggability:** <finding>
   - **Safety and guardrails:** <finding>
   - **Tooling and integrations (MCP, webhooks, APIs):** <finding>
   - **Cost and resource management:** <finding>
   ```

   This written gap map is the direct input to step 4 — only gaps documented
   here should become proposals.

4. **Apply a high bar.** Before proposing a feature, ask:

   - Is there strong evidence users actually want this (not just that it is
     technically interesting)?
   - Would it meaningfully improve agent autonomy, reliability, or usefulness
     for the target audience (individual practitioners and enterprise teams)?
   - Does it align with the design principles: infrastructure-grade reliability,
     file-based configuration, standard protocols, complexity is opt-in?
   - Does it fit the Kubernetes-first deployment target?
   - Can it be implemented without a major architectural rewrite?

   Discard speculative, cosmetic, or nice-to-have features. Add at most 3 new
   proposals per run — and only if each one is genuinely compelling: strong
   evidence of demand, clear implementation path, and meaningful impact on
   agent autonomy, reliability, or usefulness. It is better to add 1 excellent
   proposal than 3 mediocre ones. If the open `type/feature` count is already
   at 25, skip to step 6.

5. **Create new feature issues.** For each new proposal, run
   `/github-issue create task status/pending` and provide:

   - **Type:** `type/feature`
   - **Priority:** `priority/p0` through `priority/p3`
   - **Created by:** `<agent-name>`
   - **Description:** must include all of the following sections:

     **Confidence:** `high | medium | low` — one sentence justification

     > high = well-understood technically, strong demand evidence, clear scope
     > medium = mostly clear, but at least one open question or uncertain dependency
     > low = speculative, depends on unresolved design decisions, or demand is thin

     **Value:** Why it matters, what problem it solves, evidence of user demand
     (cite a specific source: competitor feature, GitHub issue, changelog entry,
     community thread), which audience it serves. 2–4 sentences. Vague
     "evidence of demand" without a citation is not acceptable.

     **Implementation:** Which files change, what new components are added, what
     the operator-facing interface looks like. Specific enough that an engineer
     could start without additional research. 3–6 sentences.

     **Risk:** Low / Medium / High — one sentence justification.

     **Questions:** Specific unresolved questions, or `none`.

   - **Acceptance criteria:** specific, verifiable conditions that define done
   - **Notes:** related files, competitor references, or relevant context

   Write complete, specific issues — not stubs. A proposal is only useful if
   an engineer can read it and understand exactly what to build and why.

6. **Re-evaluate all open `type/feature` issues** that existed before this run
   (the issue numbers noted in step 1c). Do not re-evaluate issues created in
   step 5. For each pre-existing open issue:

   a. Fetch the full body: `/github-issue view <number>`

   b. Ask:
      - **Already implemented?** Check the source files from step 1b. If so,
        fetch the current body (`gh issue view <number> --json body --jq '.body'`),
        update the `**Status:**` line to `status/implemented`, then apply the
        updated body and label:
        `gh issue edit <number> --body "<updated body>" --add-label "status/implemented" --remove-label "status/pending,status/approved,status/in-progress"`
        Then close: `/github-issue close <number> "Implemented — <summary>"`
      - **Superseded or obsolete?** Update the body to note why, post a comment
        explaining the change, then fetch the current body
        (`gh issue view <number> --json body --jq '.body'`), update the
        `**Status:**` line to `status/wont-fix`, and apply the updated body and label:
        `gh issue edit <number> --body "<updated body>" --add-label "status/wont-fix" --remove-label "status/pending,status/approved,status/in-progress"`
        Then close: `/github-issue close <number> "Superseded — <one sentence reason>"`
      - **Implementation details stale?** Do file paths, API names, env vars,
        or SDK APIs still match the current codebase? Update the issue body
        directly with corrected details, then post a brief comment noting
        what changed and why.
      - **Priority wrong?** Has new evidence shifted importance? Update the
        label: `/github-issue relabel <number> add priority/pX remove priority/pY`
        and post a comment citing the evidence that drove the change.
      - **Confidence changed?** Has the feature become clearer or murkier?
        Update the Confidence field in the issue body directly.
      - **Value or Implementation thin?** If research surfaced concrete evidence
        (with a citation) that would strengthen the case, update the issue body
        directly. Do not pad — only add content that genuinely helps.
      - **Questions answered?** If open questions have been resolved by research,
        update the Questions field in the body and post a comment with the
        source that answered them.

   Only update issues where something has actually changed. Record every
   update made (issue number, what changed, why) for the summary in step 7.

7. **Update docs** if research surfaced significant new information:

   - `docs/competitive-landscape.md` — the `## Gap Analysis` section was
     already written in step 3. Additionally update any competitor section
     where new facts, features, or positioning have emerged. Update the
     `Last updated:` line at the top. Only add or update — do not delete
     existing content.
   - `docs/product-vision.md` — if research revealed a shift in what the
     market values, a pattern this project should adopt, or a strategic
     direction worth capturing, update the vision to reflect it. Be
     conservative — only update if the insight is substantial and durable,
     not just a passing trend.

   After making any doc changes, commit and push:

   ```bash
   git add docs/competitive-landscape.md docs/product-vision.md
   git commit -m "$(git diff --cached --name-only | tr '\n' ' ' | sed 's/ $//'): update from features research pass"
   git push origin main || (git pull --rebase origin main && git push origin main)
   ```

   Only commit files that were actually modified. Skip this if no doc changes
   were made.

8. **Report a summary:**
   - New feature issues created (number, title)
   - Existing issues updated (number, what changed, reason)
   - Issues closed as implemented or obsolete (number, reason)
   - Features discarded as duplicates (title, one-line reason)
   - Total open `type/feature` issue count (should be ≤ 25)

9. **Reflect on the research process itself.** If you encountered any friction,
   gaps, or opportunities to improve this skill — search queries that produced
   poor signal, steps that were unclear, sources that should be added or
   removed, or patterns that would have surfaced better proposals with a
   different approach — create a GitHub Issue using
   `/github-issue create task status/approved` with type `type/code-quality`,
   describing the specific improvement and which step in this skill it affects.
