---
name: gap-github-issues
description: File, close, edit, comment on, or look up a gap (not a bug, risk, or feature). Trigger when the user says "file a gap", "report a gap", "close the gap", "close gap #N", "update the gap", "edit gap #N", "look up a gap", "find gap #N", "check if a gap exists", "list gaps", "show all gaps", "comment on gap #N", or "add a comment to the gap".
version: 1.0.0
---

# gap-github-issues

This is a leaf skill. It contains all the commands needed to file, close, edit, and look up gaps. Other skills delegate to it using plain English — "file the gap", "close the gap", "update the gap's status" — and this skill carries out the action.

**Repository:** derive at runtime with `gh repo view --json nameWithOwner -q .nameWithOwner`. Never hardcode.

---

## Filing a Gap

**Step 1: Gather details.**

If the caller provided a description, use it. If vague, ask for:
- Which component is affected (refer to the Components table in `<repo-root>/README.md` if needed)
- What category of gap it is (functionality, coverage, consistency, integration, observability)
- What the system should do or have (expected)
- What is currently missing, absent, or incomplete (actual)

**Step 2: Check for duplicates.**

Search open gaps before filing:

```bash
gh issue list --label "gap" --state open
```

Compare titles against the gap being filed. If a sufficiently similar issue already exists, report it and stop. If it is a partial match, note the related issue in the new filing.

**Step 3: File the gap.**

Write a concise title (under 70 characters). Before composing the body, read `<repo-root>/.github/ISSUE_TEMPLATE/gap.md` and populate every field defined there. Set **Status** to `pending` for all new gaps. Set **Skill** to the name and version of this skill (see the frontmatter of this file).

Once the body is ready, derive labels from the body fields:

- **Type** — always `gap` (required)
- **Priority** — from `**Priority:**`; must be one of `critical`, `high`, `medium`, `low` (required; default to `medium` if not supplied)
- **Status** — from `**Status:**`; must be one of `pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, `wont-fix` (required; default to `pending` if not supplied)
- **Category** — from `**Category:**`; must be one of `functionality`, `coverage`, `consistency`, `integration`, `observability` (required)
- **Component** — from `**Component:**`; apply only if it is a known component (`agent`, `a2-claude`, `a2-codex`, `a2-gemini`, `ui`); omit if cross-cutting or blank (optional)

```bash
gh issue create --title "<title>" --body "<body>" --label "gap" --label "<priority>" --label "<status>" --label "<category>" [--label "<component>"]
```

**Step 4: Return the issue URL.**

---

## Closing a Gap

**Step 1: Read the current issue body.**

```bash
gh issue view <number> --json body,labels
```

**Step 2: Update the status in the body.**

Replace the `**Status:**` line with `**Status:** implemented` and write the updated body back:

```bash
gh issue edit <number> --body "<updated-body>"
```

**Step 3: Swap the status label.**

Remove any existing status label (`pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, `wont-fix`) and add `implemented`:

```bash
gh issue edit <number> --remove-label "<old-status>" --add-label "implemented"
```

**Step 4: Close the issue.**

```bash
gh issue close <number>
```

---

## Editing a Gap

Use this when updating a gap's fields without closing it — for example, changing priority, updating the implementation approach, recording a dependency, or advancing the status mid-lifecycle.

**Step 1: Read the current issue body and labels.**

```bash
gh issue view <number> --json body,labels
```

**Step 2: Update the body fields.**

The body is structured markdown. Edit only the fields that need to change. Common edits:

- **Status** — replace `**Status:** <old>` with `**Status:** <new>`. Valid values: `pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, `wont-fix`.
- **Priority** — replace `**Priority:** <old>` with `**Priority:** <new>`. Valid values: `critical`, `high`, `medium`, `low`.
- **Category** — replace `**Category:** <old>` with `**Category:** <new>`. Valid values: `functionality`, `coverage`, `consistency`, `integration`, `observability`.
- **Claimed by** — replace `**Claimed by:** none` with `**Claimed by:** <agent-name>` when an agent picks up a gap; set back to `none` when the agent drops it.
- **Depends on** — replace the `- none` entry (or existing entries) under `**Depends on:**` with one bullet per dependency: `- #<number> — <one sentence reason this must be resolved first>`. Use `- none` when there are no dependencies.
- **Implementation** — replace the `**Implementation:**` block with the revised approach.
- **Component** — replace `**Component:** <old>` with `**Component:** <new>`.

Write the updated body back:

```bash
gh issue edit <number> --body "<updated-body>"
```

**Step 3: Sync labels to match the updated body.**

If Status, Priority, or Category changed, swap the old label for the new one:

```bash
gh issue edit <number> --remove-label "<old-label>" --add-label "<new-label>"
```

If the `**Depends on:**` list contains any entry other than `- none`, add the `blocked-by` label:

```bash
gh issue edit <number> --add-label "blocked-by"
```

If the `**Depends on:**` list is `- none`, remove `blocked-by`:

```bash
gh issue edit <number> --remove-label "blocked-by"
```

---

## Commenting on a Gap

Use this when adding a comment to an existing issue without editing its body — for example, noting a re-identification, recording an observation, or leaving a status update.

**Step 1: Compose the comment.**

Write the comment body in plain markdown.

**Step 2: Append the signature.**

Every comment posted by this skill must end with a signature block, separated from the comment body by a blank line:

```
— <agent-name> · <skill-name> v<skill-version>
```

- `<agent-name>` — the value of the `AGENT_NAME` environment variable; use `local-agent` if not set
- `<skill-name>` and `<skill-version>` — from the frontmatter of this file

**Step 3: Post the comment.**

```bash
gh issue comment <number> --body "<comment-body>

— <agent-name> · <skill-name> v<skill-version>"
```

---

## Looking Up a Gap

To find a specific gap by number:

```bash
gh issue view <number> --json number,title,body,labels,state
```

To list gaps by status (e.g. all approved gaps):

```bash
gh issue list --label "gap" --label "<status>" --state open --json number,title,labels
```

Valid status labels: `pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, `wont-fix`.
