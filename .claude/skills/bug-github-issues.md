---
name: bug-github-issues
description: File a bug, close a bug, edit a bug, or look up a bug. Trigger when the user says "file a bug", "report a bug", "close the bug", "close bug #N", "update the bug", "edit bug #N", "look up a bug", "find bug #N", or "check if a bug exists".
version: 1.0.8
---

# bug-github-issues

This is a leaf skill. It contains all the commands needed to file, close, edit, and look up bugs. Other skills delegate to it using plain English — "file the bug", "close the bug", "update the bug's status" — and this skill carries out the action.

**Repository:** derive at runtime with `gh repo view --json nameWithOwner -q .nameWithOwner`. Never hardcode.

---

## Filing a Bug

**Step 1: Gather details.**

If the caller provided a description, use it. If vague, ask for:
- Which component is affected (refer to the Components table in `<repo-root>/README.md` if needed)
- What the expected behavior is
- What the actual behavior is

**Step 2: Check for duplicates.**

Search open bugs before filing:

```bash
gh issue list --label "bug" --state open
```

Compare titles against the bug being filed. If a sufficiently similar issue already exists, report it and stop. If it is a partial match, note the related issue in the new filing.

**Step 3: File the bug.**

Write a concise title (under 70 characters). Before composing the body, read `<repo-root>/.github/ISSUE_TEMPLATE/bug.md` and populate every field defined there. Set **Status** to `pending` for all new bugs. Set **Skill** to the name and version of this skill (see the frontmatter of this file).

Once the body is ready, derive labels from the body fields:

- **Type** — always `bug` (required)
- **Priority** — from `**Priority:**`; must be one of `critical`, `high`, `medium`, `low` (required; default to `medium` if not supplied)
- **Status** — from `**Status:**`; must be one of `pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, `wont-fix` (required; default to `pending` if not supplied)
- **Component** — from `**Component:**`; apply only if it is a known component (`agent`, `a2-claude`, `a2-codex`, `a2-gemini`, `ui`); omit if cross-cutting or blank (optional)

```bash
gh issue create --title "<title>" --body "<body>" --label "bug" --label "<priority>" --label "<status>" [--label "<component>"]
```

**Step 4: Return the issue URL.**

---

## Closing a Bug

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

## Editing a Bug

Use this when updating a bug's fields without closing it — for example, changing priority, updating the fix description, recording a dependency, or advancing the status mid-lifecycle.

**Step 1: Read the current issue body and labels.**

```bash
gh issue view <number> --json body,labels
```

**Step 2: Update the body fields.**

The body is structured markdown. Edit only the fields that need to change. Common edits:

- **Status** — replace `**Status:** <old>` with `**Status:** <new>`. Valid values: `pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, `wont-fix`.
- **Priority** — replace `**Priority:** <old>` with `**Priority:** <new>`. Valid values: `high`, `medium`, `low`.
- **Depends on** — replace `**Depends on:**` with `**Depends on:** #<number>` (comma-separate multiple).
- **Fix** — replace the `**Fix:**` block with the revised suggestion.
- **Component** — replace `**Component:** <old>` with `**Component:** <new>`.

Write the updated body back:

```bash
gh issue edit <number> --body "<updated-body>"
```

**Step 3: Sync labels to match the updated body.**

If Status or Priority changed, swap the old label for the new one:

```bash
gh issue edit <number> --remove-label "<old-label>" --add-label "<new-label>"
```

If a `**Depends on:**` field was populated, add the `blocked-by` label:

```bash
gh issue edit <number> --add-label "blocked-by"
```

If the `**Depends on:**` field was cleared, remove `blocked-by`:

```bash
gh issue edit <number> --remove-label "blocked-by"
```

---

## Looking Up a Bug

To find a specific bug by number:

```bash
gh issue view <number> --json number,title,body,labels,state
```

To list bugs by status (e.g. all approved bugs):

```bash
gh issue list --label "bug" --label "<status>" --state open --json number,title,labels
```

Valid status labels: `pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, `wont-fix`.
