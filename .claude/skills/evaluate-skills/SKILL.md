---
name: evaluate-skills
description: >-
  Deep analysis of all skills and their workflow interactions to find logic
  errors, broken references, and consistency gaps — creates a GitHub Issue for
  each finding
---

Review every skill file and create GitHub Issues for every bug found in the
skill layer — logic errors, invalid CLI syntax, broken cross-skill references,
and workflow inconsistencies.

Steps:

1. Load all existing open skill issues from GitHub so you can avoid creating
   duplicates throughout the review. Run `/github-issue list type/skill` and keep
   the results in mind for every finding — if a finding is already covered by an
   open issue, skip it.

2. Read `<repo-root>/README.md` and `<repo-root>/AGENTS.md` to understand the
   conventions used across the project — especially `<repo-root>`, `<agent-name>`,
   label names, status values, and any other shared placeholders.

3. Discover all skills:

   - List every `SKILL.md` file under `<repo-root>/.claude/skills/`.
   - Read every one in full. Do not skim. Note the skill name, its subcommands
     (if any), the placeholders it uses, the external tools it calls (gh, bash
     commands, other skills), and any cross-skill references.

4. Build a complete map of the skill layer before drawing any conclusions:

   - Which skills call other skills (e.g. `work-bugs` calling `/github-issue`)?
   - Which subcommands does each skill expose?
   - Which placeholders are used (`<repo-root>`, `<agent-name>`, `$ARGUMENTS`,
     etc.) and how are they resolved?
   - Which GitHub labels, statuses, and issue fields are referenced?

5. Perform a deep skill bug review. For each skill, look for:

   - **Invalid CLI syntax** — flags or subcommands passed to `gh`, `bash`, or
     other tools that do not exist (e.g. `gh issue close --remove-label`)
   - **Broken cross-skill references** — a skill calls `/other-skill subcommand`
     but that subcommand is not defined in the target skill
   - **Missing subcommands** — a skill's argument-hint or description advertises
     a subcommand that has no implementation section
   - **Stale placeholders** — `<repo-root>`, `<agent-name>`, or other
     placeholders used inconsistently or resolved differently across skills
   - **Wrong label or status values** — label names or status values that do not
     match the actual GitHub labels in the repo
   - **Contradictory instructions** — two steps in the same skill that conflict
     with each other or produce an impossible state
   - **Silent skip conditions** — a step says "if X, skip" but never specifies
     what to do after skipping (infinite loop risk in autonomous runs)
   - **Orphaned skills** — do NOT flag any skill under `.claude/skills/` as
     an orphan. Every skill in that directory is available for direct user
     invocation regardless of whether another skill calls it. Standalone
     user-callable skills (e.g. `redeploy`, `remote`, `plan-features`,
     `evaluate-gaps`, `evaluate-risks`) are intentionally standalone.
   - **Inconsistent conventions** — the same concept handled differently across
     skills (e.g. one skill uses `status/wont-fix` where another uses a comment
     only, or `Created by` set differently)
   - **OS portability** — commands that use BSD-only or GNU-only coreutil syntax
     (e.g. `grep -P`, `sed -i` without an extension argument, `date -d`), tools
     only available on one platform (e.g. `brew` commands, `apt` commands), or
     shell features not portable across macOS (zsh/bash) and Linux (bash). Skills
     run on macOS when invoked locally and on Linux inside containerized agents —
     both paths are active and must work without OS-specific branching.

6. For each finding, cross-reference against the list loaded in step 1. If not
   already covered, also run `/github-issue search "<skill-name> <brief keyword>"`
   as a secondary check. Only proceed if no equivalent open issue exists.

7. For each new finding, run `/github-issue create task status/approved` and provide:

   - **Type:** `type/skill`
   - **Priority:** `priority/p2` by default; `priority/p1` if the bug would
     cause a skill to silently corrupt issue state or loop forever; `priority/p3`
     for cosmetic or low-impact inconsistencies
   - **Created by:** `<agent-name>` (value of `$AGENT_NAME` if set, otherwise
     `local-agent`)
   - **File:** specific skill file and line number
   - **Description:** start with a plain-language summary (1-2 sentences): what
     is broken and what would go wrong when the skill runs. Then provide the
     technical detail: the exact instruction that is wrong, why it fails, and
     what the correct behavior should be
   - **Acceptance criteria:** specific, verifiable conditions that define done
   - **Notes:** related skills, cross-skill call chains, or relevant context

8. Report a summary of all issues created (or skipped as duplicates).

9. Reflect on the review process itself. If you encountered any friction, gaps,
   or opportunities to improve this skill — missing bug categories, steps that
   were unclear, or patterns that would have been caught earlier with a different
   approach — create a GitHub Issue using `/github-issue create task status/approved`
   with type `type/code-quality`, describing the specific improvement and which
   step in this skill it affects.
