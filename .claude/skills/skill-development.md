---
name: skill-development
description: Guide the design and review of skills in this repository. Trigger when the user says "create a skill", "write a skill", "review a skill", "update a skill", "help with a skill", "audit skills", or "audit the skill families".
version: 1.4.0
---

# skill-development

Skills in this repository are plain English instruction files. They must be readable by any agent, human or AI, without knowledge of the underlying tools or services used to carry them out.

## Runtime Context

Two values are commonly needed in skills. Never hardcode them — resolve them at runtime:

**Agent name** — read from the `AGENT_NAME` environment variable. If not set, the agent is running as a local session and the name is `local-agent` (as defined in `AGENTS.md`).

**Repository** — derive from the git remote using the `gh` CLI:
```bash
gh repo view --json nameWithOwner -q .nameWithOwner
```
This works in any clone without hardcoding an owner or repo name. Never use `--repo` with a literal value in a skill.

**Repo root path** — use the `<repo-root>` placeholder (as defined in `AGENTS.md`) wherever a skill needs to reference a file by absolute path. Never hardcode a literal filesystem path such as `/Users/scott/...`.

**Skill name and version** — when a skill needs to record or report its own name and version (e.g. in a GitHub issue body), always reference the frontmatter of the skill file itself. Never hardcode the name or version as a literal string. Write "see the frontmatter of this file" rather than `skill-name v1.0.0`.

## Conventions

**Use domain language, not tool language.**

Skills speak in terms of the problem domain — not the tools used to implement it. Write "file the bug" and "close the bug", not "create a GitHub issue" or "run `gh issue close`". Implementation details belong only in the leaf skill that carries out the action.

**Delegate by action, not by skill name.**

When one skill needs another to do something, describe what needs to happen in plain English. Write "file the bug" — not "use the `github-issues` skill to file the bug". The agent resolves which skill handles "file the bug". This keeps cross-skill references stable when the underlying skill is renamed or replaced.

**Contain implementation details in leaf skills.**

Any skill that calls external tools (CLI commands, APIs, services) is a leaf skill. Only leaf skills may contain tool-specific commands. All other skills delegate to them using domain language. If `gh issue` commands need to change, only the leaf skill changes — nothing else does.

**Trigger phrases must be natural English.**

The `description` frontmatter field is what determines when a skill fires. Write trigger phrases a person would naturally say. Avoid tool names, command syntax, or jargon in trigger phrases.

**One skill, one concern.**

A skill should do one thing. If a skill is doing two unrelated things, split it.

**Version every change.**

Bump the version in the frontmatter whenever the skill's behavior changes. Use semantic versioning: patch for clarifications, minor for new steps or capabilities, major for breaking changes to the workflow.

**Use whole numbers for step numbering.**

Steps must be numbered with consecutive whole integers — Step 1, Step 2, Step 3. Never use fractional or inserted step numbers like Step 2.5 or Step 3b. When a new step is added between existing steps, renumber all subsequent steps to restore the whole-number sequence.

## Issue Type Taxonomy

Skills in this repository are organized around four first-class issue types. Each type has its own family of skills (discovery, refinement, approval, fix, github-issues, etc.) and follows the same structural pattern as bugs.

| Type | What it covers |
|------|---------------|
| **bugs** | Defects — code that is broken or behaves incorrectly at runtime |
| **risks** | Code quality issues — code that works today but is fragile, insecure, hard to maintain, or likely to break under foreseeable conditions |
| **gaps** | Missing behavior — functionality the system should have but does not |
| **features** | Intentional enhancements — new capabilities requested by stakeholders |

When creating or reviewing a skill for any of these types, treat the corresponding `bug-*` skill family as the reference implementation. Mirror its structure unless there is a specific reason to diverge, and document any divergences explicitly.

**bugs**, **risks**, **gaps**, and **features** are fully built out.

## Instructions

When the user asks to create or review a skill:

**Step 1: Understand the goal.**

Ask (or infer from context): What action does this skill perform? Who invokes it — a human, another skill, or an agent? What skills does it depend on?

**Step 2: Draft or review the skill.**

Apply the conventions above. Check:
- Do any steps use tool-specific language that belongs in a leaf skill instead?
- Are cross-skill calls written as domain actions ("close the bug") rather than skill names or commands?
- Are trigger phrases natural English?
- Is the skill focused on one concern?
- Is the version set correctly?

**Step 3: Identify leaf skill gaps.**

If the skill delegates an action that no existing leaf skill handles, note it. Either create the leaf skill or flag it for the user.

**Step 4: Write the skill file.**

Save the skill to `.claude/skills/<name>.md`. Use kebab-case for the filename.

---

## Auditing Skill Families

When the user asks to audit skills or skill families, check all built-out issue type families for consistency and compliance with the conventions above.

**Step 1: Identify the families to audit.**

If the user specifies a type (e.g. "audit the gap skills"), audit only that family. Otherwise audit all built-out families: **bugs**, **risks**, **gaps**, and any others marked as fully built out in the taxonomy table above.

For each family, the expected skills are: `<type>-discovery`, `<type>-refinement`, `<type>-approval`, `<type>-implement`, and `<type>-github-issues`.

**Step 2: Read every skill in each family.**

Read each skill file in full. Do not skip any.

**Step 3: Check each skill against the conventions.**

For every skill, verify:
- **Trigger phrases** — are they natural English? No tool names, command syntax, or jargon.
- **Domain language** — do steps speak in problem-domain terms, not tool terms? Tool-specific commands belong only in leaf skills.
- **Delegation style** — are cross-skill calls written as domain actions, not skill names or commands?
- **Leaf skill boundary** — does any non-leaf skill contain `gh`, CLI commands, or API calls it should not?
- **One concern** — is each skill focused on a single responsibility?
- **Version currency** — does the version reflect all changes made to the skill? If the behavior changed and the version did not bump, flag it.
- **Step numbering** — are all steps numbered with consecutive whole integers? Flag any fractional or non-sequential step numbers (e.g. Step 2.5, Step 3b) and renumber to restore the whole-number sequence.
- **Runtime context** — are agent name, repository, repo root, and skill name/version resolved at runtime rather than hardcoded?

**Step 4: Check cross-family consistency.**

Compare the families against each other and against the `bug-*` reference implementation:
- Do all families follow the same structural pattern (same phases, same step ordering)?
- Are divergences from the bug reference intentional and type-appropriate? Flag any that appear accidental.
- Are trigger phrases consistent in style and coverage across families?
- Do all `-github-issues` leaf skills handle the same set of operations (file, close, edit, comment, look up)?
- Do all `-implement` skills include the research step for web search?

**Step 5: Report findings and fix issues.**

List every finding — file, convention violated, and what needs to change. For each finding, fix it immediately unless it requires a judgment call, in which case flag it for the user. After all fixes are applied, bump the version of every modified skill.
