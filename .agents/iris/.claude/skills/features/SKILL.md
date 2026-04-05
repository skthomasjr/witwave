---
name: features
description: Manage the feature pipeline in docs/features-proposed.md and docs/features-completed.md
argument-hint: "graduate <feature-id> | approve <feature-id> | reject <feature-id> | promote <feature-id>"
---

Manage features in the project's feature pipeline.

**Arguments:** $ARGUMENTS

The first word is the subcommand. Supported subcommands:

---

## `graduate <feature-id>`

Move a fully implemented feature from `features-proposed.md` to `features-completed.md`.

**Arguments:** `graduate F-XXX`

1. Read `<repo-root>/docs/features-proposed.md` and locate the `### F-XXX` section matching the given feature ID. If it
   is not found, report the error and stop.
2. Read `<repo-root>/docs/features-completed.md`.
3. Check that the feature is not already present in `features-completed.md`. If it is, report it and stop.
4. Construct the graduated entry:
   - Use the feature's existing `### F-XXX — <title>` heading
   - Set `**Status:** implemented`
   - Write a `**Summary:**` of 1–2 sentences describing what was built, based on the feature's Implementation field and
     any context from the GitHub Issues that were completed for it
   - Include a `---` separator after the entry
5. Append the graduated entry to `features-completed.md`, after the last existing `---` separator.
6. Remove the feature's entire `### F-XXX` section (from its heading line to just before the next `###` heading or end
   of file) from `features-proposed.md`.
7. Run `/lint-markdown` on both files.
8. Confirm the feature was graduated successfully.

---

## `approve <feature-id>`

Mark a feature as approved in `features-proposed.md`, making it eligible for promotion to GitHub Issues.

**Arguments:** `approve F-XXX`

1. Read `<repo-root>/docs/features-proposed.md` and locate the `### F-XXX` section matching the given feature ID. If it
   is not found, report the error and stop.
2. Check the feature's current **Status:**. If it is already `approved`, `promoted`, or `rejected`, report the current
   status and stop — do not modify it.
3. Check the feature's **Questions:** field. If it contains any unresolved questions (i.e. the value is not `none`),
   report them and stop — a feature with open questions must not be approved.
4. Change `**Status:** proposed` to `**Status:** approved`.
5. Run `/lint-markdown` on the file.
6. Confirm the feature was approved.

---

## `reject <feature-id>`

Mark a feature as rejected in `features-proposed.md`.

**Arguments:** `reject <feature-id> <reason>`

The first word after `reject` is the feature ID. The remainder is the reason for rejection.

1. Read `<repo-root>/docs/features-proposed.md` and locate the `### F-XXX` section matching the given feature ID. If it
   is not found, report the error and stop.
2. Check the feature's current **Status:**. If it is already `rejected`, report it and stop.
3. Change the **Status:** to `rejected`.
4. Append a `**Rejection reason:**` field after **Status:** with the provided reason.
5. Run `/lint-markdown` on the file.
6. Confirm the feature was rejected.

---

## `promote <feature-id>`

Break down an approved feature into tasks and create GitHub Issues for each.

**Arguments:** `promote F-XXX`

1. Read `<repo-root>/docs/features-proposed.md` and locate the `### F-XXX` section. If not found, report the error and
   stop.
2. Check the feature's **Status:**. If it is not `approved`, report the current status and stop — only approved features
   may be promoted.
3. Check the feature's **Questions:** field. If it contains unresolved questions (value is not `none`), report them and
   stop — do not promote a feature with open questions.
4. Check for any existing open GitHub Issues tagged with this feature ID using
   `gh issue list --search "[F-XXX]" --state open` to avoid creating duplicates.
5. Break the feature down into tasks. Use the **Implementation** field as the authoritative description of what to
   build. Aim for the fewest tasks that still make sense as independent units of work — group changes to the same file
   or logical concern into a single task.
6. If during breakdown you encounter ambiguities that cannot be resolved from the codebase or docs, do not create
   incomplete issues. Instead:
   - Change the feature's **Status:** back to `proposed` in `features-proposed.md`
   - Add or update the **Questions:** field with each unresolved question listed specifically
   - Report what was unclear and stop — the feature will need to be re-approved before it can be promoted
7. For each task, create a GitHub issue using `/github-issue create task status/approved`. Use the feature's **Value**
   and **Implementation** fields as source material. The issue should be self-contained:
   - **Type** — `type/enhancement`
   - **Priority** — `priority/p2` unless the feature description indicates otherwise
   - **Created by** — the agent running this skill
   - **File** — the file(s) involved in the task
   - **Description** — a full paragraph explaining what the task implements, why it matters (from the Value field), and
     how it fits into the broader feature (from the Implementation field). Include the feature ID for traceability.
   - **Acceptance criteria** — specific, verifiable conditions derived from the task description
   - **Notes** — the feature ID and title for traceability
8. Change the feature's **Status:** in `features-proposed.md` from `approved` to `promoted`.
9. Run `/lint-markdown` on `features-proposed.md`.
10. Confirm how many issues were created and report them.
