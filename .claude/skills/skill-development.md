---
name: skill-development
description: Guide the design and review of skills in this repository. Trigger when the user says "create a skill", "write a skill", "review a skill", "update a skill", or "help with a skill".
version: 1.0.1
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
