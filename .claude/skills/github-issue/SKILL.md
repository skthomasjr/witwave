---
name: github-issue
description:
  Manage GitHub Issues for the autonomous-agent repo — create, list, search, view, claim, comment, relabel, and close
  tasks and questions
argument-hint: >-
  create task [status/<status>] | create question | list | search <query> | view <number> | claim <number> <agent> |
  comment <number> <message> | relabel <number> add <label> [remove <label>] | close <number> <message>
---

Manage GitHub Issues in `skthomasjr/autonomous-agent`.

**Arguments:** $ARGUMENTS

The first word is the subcommand. Supported subcommands:

---

## `create task`

Create a new task issue from the task template.

**Arguments:** `create task [status/approved]`

The optional status argument overrides the default status. If omitted, status defaults to `status/pending`.

1. Read `<repo-root>/.github/ISSUE_TEMPLATE/task.md` to load the template structure.
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
   `Status: <status>` using the resolved status value.
4. Use the Bash tool to create the issue:

```bash
gh issue create \
  --title "<concise title derived from description>" \
  --label "<type-label>,<priority-label>,<status>" \
  --body "<formatted body>"
```

5. Report the issue URL and number.

---

## `create question`

Create a new question issue from the question template.

**Arguments:** `create question`

1. Read `<repo-root>/.github/ISSUE_TEMPLATE/question.md` to load the template structure.
2. Prompt for the following fields:
   - **Asked by** — the human or agent name asking the question
   - **Question** — the question text
3. Construct the issue body using the template structure. Set `Claimed by: none`.
4. Use the Bash tool to create the issue:

```bash
gh issue create \
  --title "<concise title derived from the question>" \
  --label "type/question" \
  --body "<formatted body>"
```

5. Report the issue URL and number.

---

## `list`

List issues, optionally filtered.

**Arguments:** `list [type/<label>] [priority/<label>] [status/<label>] [state:open|closed|all]`

Use the Bash tool to list issues. Default state is `open`. Pass `state:all` to include closed issues:

```bash
gh issue list \
  --state <open|closed|all> \
  --label "<label-filters>" \
  --json number,title,labels,assignees,createdAt \
  --jq '.[] | "#\(.number) \(.title) [\(.labels | map(.name) | join(", "))]"'
```

If no filters are provided, list all open issues. Display results clearly with issue number, title, and labels.

---

## `search <query>`

Search open issues by keyword.

**Arguments:** `search "executor session_id"`

Use the Bash tool to search issues:

```bash
gh search issues "$QUERY" --state open --repo ${GH_REPO:-skthomasjr/autonomous-agent} \
  --json number,title,labels \
  --jq '.[] | "#\(.number) \(.title) [\(.labels | map(.name) | join(", "))]"'
```

Display results clearly with issue number, title, and labels. If no results are found, report that clearly.

---

## `view <number>`

Fetch an issue's full body and comment thread.

**Arguments:** `view 42`

Use the Bash tool:

```bash
gh issue view <number> --comments \
  --json number,title,labels,body,comments \
  --jq '{
    number: .number,
    title: .title,
    labels: [.labels[].name],
    body: .body,
    comments: [.comments[] | {author: .author.login, body: .body, createdAt: .createdAt}]
  }'
```

Display the issue body followed by each comment in chronological order with its author and timestamp.

---

## `claim <number> <agent>`

Claim an issue — mark it as in-progress by the specified agent.

**Arguments:** `claim 42 kira`

1. Use the Bash tool to fetch the current issue body:

```bash
gh issue view <number> \
  --json body,labels --jq '{body: .body, labels: [.labels[].name]}'
```

2. Check that the issue is not already claimed — if `Claimed by:` is not `none`, report who has it and stop.
3. Update the body: set `Claimed by: <agent>` and `Status: status/in-progress`.
4. Apply the `status/in-progress` label and remove `status/pending` or `status/approved` if present:

```bash
gh issue edit <number> \
  --body "<updated body>" \
  --add-label "status/in-progress" \
  --remove-label "status/pending,status/approved"
```

5. Post a comment:

```bash
gh issue comment <number> \
  --body "[<agent>] Claimed — status/in-progress"
```

6. Confirm the claim.

---

## `relabel <number> add <label> [remove <label>]`

Add and/or remove labels on an issue without touching the body or posting a comment.

**Arguments:** `relabel 42 add status/wont-fix remove status/approved`

Use the Bash tool:

```bash
gh issue edit <number> \
  --add-label "<label-to-add>" \
  --remove-label "<label-to-remove>"
```

Omit `--remove-label` if no label needs to be removed. Multiple labels can be comma-separated in either argument.
Report the updated label set.

---

## `comment <number> <message>`

Post a comment on an issue.

**Arguments:** `comment 42 "Investigation complete — root cause is in executor.py:152"`

Use the Bash tool:

```bash
gh issue comment <number> \
  --body "<message>"
```

Report confirmation.

---

## `close <number> <message>`

Close an issue with a final comment.

**Arguments:** `close 42 "Fixed in executor.py — log_entry now called per TextBlock"`

1. Post the closing comment:

```bash
gh issue comment <number> \
  --body "<message>"
```

2. Remove the `status/in-progress` label and close the issue:

```bash
gh issue edit <number> --remove-label "status/in-progress"
gh issue close <number>
```

3. Confirm the issue is closed.
