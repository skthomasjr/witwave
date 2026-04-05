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

1. Read `the repo root/docs/features-proposed.md` and locate the `### F-XXX` section matching the
   given feature ID. If it is not found, report the error and stop.
2. Read `the repo root/docs/features-completed.md`.
3. Check that the feature is not already present in `features-completed.md`. If it is, report it and stop.
4. Construct the graduated entry:
   - Use the feature's existing `### F-XXX — <title>` heading
   - Set `**Status:** implemented`
   - Write a `**Summary:**` of 1–2 sentences describing what was built, based on the feature's Implementation field and
     any context from the TODO items that were completed for it
   - Include a `---` separator after the entry
5. Append the graduated entry to `features-completed.md`, after the last existing `---` separator.
6. Remove the feature's entire `### F-XXX` section (from its heading line to just before the next `###` heading or end
   of file) from `features-proposed.md`.
7. Run `/lint-markdown` on both files.
8. Confirm the feature was graduated successfully.

---

## `approve <feature-id>`

Mark a feature as approved in `features-proposed.md`, making it eligible for promotion to `TODO.md`.

**Arguments:** `approve F-XXX`

1. Read `the repo root/docs/features-proposed.md` and locate the `### F-XXX` section matching the
   given feature ID. If it is not found, report the error and stop.
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

1. Read `the repo root/docs/features-proposed.md` and locate the `### F-XXX` section matching the
   given feature ID. If it is not found, report the error and stop.
2. Check the feature's current **Status:**. If it is already `rejected`, report it and stop.
3. Change the **Status:** to `rejected`.
4. Append a `**Rejection reason:**` field after **Status:** with the provided reason.
5. Run `/lint-markdown` on the file.
6. Confirm the feature was rejected.

---

## `promote <feature-id>`

Break down an approved feature into TODO tasks and add them to `TODO.md` under `## Enhancements`.

**Arguments:** `promote F-XXX`

1. Read `the repo root/docs/features-proposed.md` and locate the `### F-XXX` section. If not
   found, report the error and stop.
2. Check the feature's **Status:**. If it is not `approved`, report the current status and stop — only approved features
   may be promoted.
3. Check the feature's **Questions:** field. If it contains unresolved questions (value is not `none`), report them and
   stop — do not promote a feature with open questions.
4. Read `the repo root/TODO.md` and check the existing `## Enhancements` section for any items
   already prefixed with this feature ID — collect them to avoid duplicates.
5. Break the feature down into tasks. Use the **Implementation** field as the authoritative description of what to
   build. Aim for the fewest tasks that still make sense as independent units of work — group changes to the same file
   or logical concern into a single task. Each task must:
   - Be prefixed with the feature ID — e.g. `[F-XXX]`
   - Reference the file(s) involved
   - Describe the full change clearly in one line
   - Be formatted as `- [ ] [F-XXX] <file> — <description>`
6. If during breakdown you encounter ambiguities that cannot be resolved from the codebase or docs, do not add
   incomplete tasks. Instead:
   - Change the feature's **Status:** back to `proposed` in `features-proposed.md`
   - Add or update the **Questions:** field with each unresolved question listed specifically
   - Report what was unclear and stop — the feature will need to be re-approved before it can be promoted
7. Append all tasks to the `## Enhancements` section in `TODO.md`. If the section does not exist, create it. Do not
   modify any other section.
8. Change the feature's **Status:** in `features-proposed.md` from `approved` to `promoted`.
9. Run `/lint-markdown` on both files.
10. Confirm how many tasks were added and report them.
