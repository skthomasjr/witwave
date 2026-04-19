---
name: risk-github-issues
description: File, close, edit, comment on, or look up a risk (not a bug, gap, or feature). Trigger when the user says "file a risk", "report a risk", "close the risk", "close risk #N", "update the risk", "edit risk #N", "look up a risk", "find risk #N", "check if a risk exists", "list risks", "show all risks", "comment on risk #N", or "add a comment to the risk".
version: 1.0.2
---

# risk-github-issues

This is a leaf skill. It contains all the commands needed to file, close, edit, and look up risks. Other skills delegate to it using plain English — "file the risk", "close the risk", "update the risk's status" — and this skill carries out the action.

**Repository:** derive at runtime with `gh repo view --json nameWithOwner -q .nameWithOwner`. Never hardcode.

---

## Filing a Risk

**Step 1: Gather details.**

If the caller provided a description, use it. If vague, ask for:
- Which component is affected (refer to the Components table in `<repo-root>/README.md` if needed)
- What category of risk it is (security, reliability, maintainability, performance, observability)
- What condition would cause it to manifest
- What the impact would be

**Step 2: Check for duplicates.**

Search open risks before filing:

```bash
gh issue list --label "risk" --state open
```

Compare titles against the risk being filed. If a sufficiently similar issue already exists, report it and stop. If it is a partial match, note the related issue in the new filing.

**Step 3: File the risk.**

Write a concise title (under 70 characters). Before composing the body, read `<repo-root>/.github/ISSUE_TEMPLATE/risk.md` and populate every field defined there. Set **Status** to `pending` for all new risks. Set **Skill** to the name and version of this skill (see the frontmatter of this file).

Once the body is ready, derive labels from the body fields:

- **Type** — always `risk` (required)
- **Priority** — from `**Priority:**`; must be one of `critical`, `high`, `medium`, `low` (required; default to `medium` if not supplied)
- **Status** — from `**Status:**`; must be one of `pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, `wont-fix` (required; default to `pending` if not supplied)
- **Category** — from `**Category:**`; must be one of `security`, `reliability`, `maintainability`, `performance`, `observability` (required)
- **Component** — from `**Component:**`; apply only if it is a known component (`harness`, `claude`, `codex`, `gemini`, `dashboard`, `operator`, `charts`, `mcp`, `cli`); omit if cross-cutting or blank (optional)

```bash
gh issue create --title "<title>" --body "<body>" --label "risk" --label "<priority>" --label "<status>" --label "<category>" [--label "<component>"]
```

**Step 4: Return the issue URL.**

---

## Closing a Risk

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

Remove all status labels that may be present and add `implemented`. Remove each one that exists — do not assume only one is set:

```bash
gh issue edit <number> --remove-label "pending" --remove-label "approved" --remove-label "in-progress" --remove-label "needs-more-info" --add-label "implemented"
```

**Step 4: Close the issue.**

```bash
gh issue close <number>
```

---

## Editing a Risk

Use this when updating a risk's fields without closing it — for example, changing priority, updating the mitigation, recording a dependency, or advancing the status mid-lifecycle.

**Step 1: Read the current issue body and labels.**

```bash
gh issue view <number> --json body,labels
```

**Step 2: Update the body fields.**

The body is structured markdown. Edit only the fields that need to change. Common edits:

- **Status** — replace `**Status:** <old>` with `**Status:** <new>`. Valid values: `pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, `wont-fix`.
- **Priority** — replace `**Priority:** <old>` with `**Priority:** <new>`. Valid values: `critical`, `high`, `medium`, `low`.
- **Category** — replace `**Category:** <old>` with `**Category:** <new>`. Valid values: `security`, `reliability`, `maintainability`, `performance`, `observability`.
- **Claimed by** — replace `**Claimed by:** none` with `**Claimed by:** <agent-name>` when an agent picks up a risk; set back to `none` when the agent drops it.
- **Depends on** — replace the `- none` entry (or existing entries) under `**Depends on:**` with one bullet per dependency: `- #<number> — <one sentence reason this must be resolved first>`. Use `- none` when there are no dependencies.
- **Mitigation** — replace the `**Mitigation:**` block with the revised suggestion.
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

## Commenting on a Risk

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

## Looking Up a Risk

To find a specific risk by number:

```bash
gh issue view <number> --json number,title,body,labels,state
```

To list risks by status (e.g. all approved risks):

```bash
gh issue list --label "risk" --label "<status>" --state open --json number,title,labels
```

Valid status labels: `pending`, `approved`, `in-progress`, `needs-more-info`, `implemented`, `wont-fix`.
