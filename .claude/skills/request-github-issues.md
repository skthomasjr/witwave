---
name: request-github-issues
description: >-
  File, close, edit, comment on, or look up a request. Trigger when the user says "file a request", "submit a request",
  "add a request", "close the request", "close request #N", "update the request", "edit request #N", "look up a
  request", "find request #N", "list requests", "show all requests", "comment on request #N", or "add a comment to the
  request".
version: 1.0.0
---

# request-github-issues

This is a leaf skill. It contains all the commands needed to file, close, edit, and look up requests. Other skills
delegate to it using plain English — "file the request", "close the request", "comment on the request" — and this skill
carries out the action.

**Repository:** derive at runtime with `gh repo view --json nameWithOwner -q .nameWithOwner`. Never hardcode.

---

## Filing a Request

**Step 1: Gather details.**

If the caller provided a description, use it. If not, ask:

- Who is requesting this?
- What do they want — as much or as little detail as they have?

**Step 2: Check for duplicates.**

Search open requests before filing:

```bash
gh issue list --label "request" --state open
```

Compare titles against the request being filed. If a sufficiently similar issue already exists, report it and stop.

**Step 3: File the request.**

Write a concise title (under 70 characters). Before composing the body, read
`<repo-root>/.github/ISSUE_TEMPLATE/request.md` and populate every field defined there.

```bash
gh issue create --title "<title>" --body "<body>" --label "request"
```

**Step 4: Return the issue URL.**

---

## Closing a Request

**Step 1: Close the issue.**

```bash
gh issue close <number>
```

---

## Editing a Request

**Step 1: Read the current issue body.**

```bash
gh issue view <number> --json body,labels
```

**Step 2: Update the body fields.**

Edit only the fields that need to change. Write the updated body back:

```bash
gh issue edit <number> --body "<updated-body>"
```

---

## Commenting on a Request

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

## Looking Up a Request

To find a specific request by number:

```bash
gh issue view <number> --json number,title,body,labels,state
```

To list all open requests:

```bash
gh issue list --label "request" --state open --json number,title,labels
```
