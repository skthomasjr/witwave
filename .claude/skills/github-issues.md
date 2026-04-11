---
name: github-issues
description: File a bug as a GitHub issue on the skthomasjr/autonomous-agent repository.
version: 1.0.2
---

# github-issues

## Instructions

The user will describe a bug to file, e.g.:
- `/github-issues the codex executor leaks file handles on error`

**Step 1: Gather details.**

If the user provided a description, use it. If vague, ask for:
- Which component is affected (refer to the Components table in `README.md` if needed)
- What the expected behavior is
- What the actual behavior is

**Step 2: Check for duplicates.**

Search open bugs before filing:

```bash
gh issue list --repo skthomasjr/autonomous-agent --label "bug" --state open
```

Compare titles against the bug being filed. If a sufficiently similar issue already exists, report it to the user and stop. If it is a partial match, note the related issue in the new filing.

**Step 3: File a bug.**

Write a concise title (under 70 characters). The body should follow the bug issue template and must include:
- **Component** — which component is affected
- **Priority** — `critical`, `high`, `medium`, or `low`
- **Status** — `pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, or `wont-fix`
- **Expected** — what should happen
- **Actual** — what happens instead
- **Skill** — name and version of the skill that filed the issue (e.g. `github-issues v1.0.2`)

Once the body is written, read the `**Priority:**`, `**Status:**`, and `**Component:**` fields from it and derive the labels to apply. Always apply `bug`. Apply the priority and status values as labels directly. Apply the component as a label if it is a known component (`agent`, `a2-claude`, `a2-codex`, `a2-gemini`, `ui`); omit if cross-cutting or blank.

```bash
gh issue create --repo skthomasjr/autonomous-agent --title "<title>" --body "<body>" --label "bug" --label "<priority>" --label "<status>" --label "<component>"
```

**Step 4: Return the issue URL.**
