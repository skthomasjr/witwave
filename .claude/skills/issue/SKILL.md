---
name: issue
description:
  Manage GitHub Issues for the autonomous-agent repo — create, list, claim, comment, and close tasks and questions
argument-hint:
  "create task|question | list | claim <number> <agent> | comment <number> <message> | close <number> <message>"
---

Manage GitHub Issues in `skthomasjr/autonomous-agent`.

**Arguments:** $ARGUMENTS

The first word is the subcommand. Supported subcommands:

---

## `create task`

Create a new task issue from the task template.

**Arguments:** `create task`

1. Read `.github/ISSUE_TEMPLATE/task.md` to load the template structure.
2. Prompt for the following fields:
   - **Type** — one of: `type/bug`, `type/reliability`, `type/code-quality`, `type/enhancement`, `type/documentation`
   - **Priority** — one of: `priority/p0`, `priority/p1`, `priority/p2`, `priority/p3`
   - **Created by** — the agent or human name creating this issue
   - **Depends on** — issue number(s) or `none`
   - **File** — primary file and line number (e.g. `agent/executor.py:152`)
   - **Description** — clear description of the problem or enhancement
   - **Acceptance criteria** — one or more checklist items describing what done looks like
   - **Notes** — optional freeform context, or `none`
3. Construct the issue body using the template structure with the provided values. Set `Claimed by: none` and
   `Status: status/pending`.
4. Use the Bash tool to create the issue:

```bash
gh issue create \
  --repo skthomasjr/autonomous-agent \
  --title "<concise title derived from description>" \
  --label "<type-label>,<priority-label>,status/pending" \
  --body "<formatted body>"
```

5. Report the issue URL and number.

---

## `create question`

Create a new question issue from the question template.

**Arguments:** `create question`

1. Read `.github/ISSUE_TEMPLATE/question.md` to load the template structure.
2. Prompt for the following fields:
   - **Asked by** — the human or agent name asking the question
   - **Question** — the question text
3. Construct the issue body using the template structure. Set `Claimed by: none`.
4. Use the Bash tool to create the issue:

```bash
gh issue create \
  --repo skthomasjr/autonomous-agent \
  --title "<concise title derived from the question>" \
  --label "type/question" \
  --body "<formatted body>"
```

5. Report the issue URL and number.

---

## `list`

List open issues, optionally filtered.

**Arguments:** `list [type/<label>] [priority/<label>] [status/<label>]`

Use the Bash tool to list issues:

```bash
gh issuelist \
  --repo skthomasjr/autonomous-agent \
  --label "<label-filters>" \
  --json number,title,labels,assignees,createdAt \
  --jq '.[] | "#\(.number) \(.title) [\(.labels | map(.name) | join(", "))]"'
```

If no filters are provided, list all open issues. Display results clearly with issue number, title, and labels.

---

## `claim <number> <agent>`

Claim an issue — mark it as in-progress by the specified agent.

**Arguments:** `claim 42 kira`

1. Use the Bash tool to fetch the current issue body:

```bash
gh issueview <number> \
  --repo skthomasjr/autonomous-agent \
  --json body,labels --jq '{body: .body, labels: [.labels[].name]}'
```

2. Check that the issue is not already claimed — if `Claimed by:` is not `none`, report who has it and stop.
3. Update the body: set `Claimed by: <agent>` and `Status: status/in-progress`.
4. Apply the `status/in-progress` label and remove `status/pending` or `status/approved` if present:

```bash
gh issueedit <number> \
  --repo skthomasjr/autonomous-agent \
  --body "<updated body>" \
  --add-label "status/in-progress" \
  --remove-label "status/pending,status/approved"
```

5. Post a comment:

```bash
gh issuecomment <number> \
  --repo skthomasjr/autonomous-agent \
  --body "[<agent>] Claimed — status/in-progress"
```

6. Confirm the claim.

---

## `comment <number> <message>`

Post a comment on an issue.

**Arguments:** `comment 42 "Investigation complete — root cause is in executor.py:152"`

Use the Bash tool:

```bash
gh issuecomment <number> \
  --repo skthomasjr/autonomous-agent \
  --body "<message>"
```

Report confirmation.

---

## `close <number> <message>`

Close an issue with a final comment.

**Arguments:** `close 42 "Fixed in executor.py — log_entry now called per TextBlock"`

1. Post the closing comment:

```bash
gh issuecomment <number> \
  --repo skthomasjr/autonomous-agent \
  --body "<message>"
```

2. Remove the `status/in-progress` label and close the issue:

```bash
gh issueclose <number> \
  --repo skthomasjr/autonomous-agent \
  --remove-label "status/in-progress"
```

3. Confirm the issue is closed.
