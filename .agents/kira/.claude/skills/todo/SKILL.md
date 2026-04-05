---
name: todo
description: Manage the TODO.md cooperative lock and cleanup
argument-hint: "lock <status> <agent> | unlock <agent> | cleanup"
---

Manage `~/workspace/TODO.md`.

**Arguments:** $ARGUMENTS

The first word is the subcommand. Supported subcommands:

---

## `lock <status> <agent>`

Acquire the cooperative lock before doing any work on TODO.md.

1. Read `~/workspace/TODO.md` and check the status block at the top.
2. If `status` is not `idle`, check `locked_at`:
   - If the timestamp is older than 30 minutes (UTC), treat the lock as expired — log a warning that the previous lock
     was stale and proceed as if `status` were `idle`.
   - Otherwise abort — report that TODO.md is locked and by whom, and do not proceed.
3. Run `date -u +%Y-%m-%dT%H:%M:%SZ` in bash to get the current UTC timestamp.
4. Write the status block with `status: <status>`, `locked_by: <agent>`, `locked_at: <timestamp>` using the exact value
   from step 3 — do not use a placeholder.
5. Confirm the lock was acquired.

---

## `unlock <agent>`

Release the lock after work is complete.

1. Read `~/workspace/TODO.md` and check `locked_by`.
2. If `locked_by` does not match `<agent>`, abort — never clear a lock held by another agent. Report the error.
3. Write the status block with `status: idle`, `locked_by: null`, `locked_at: null`.
4. Confirm the lock was released.

---

## `cleanup`

Remove completed items and stamp fully-cleared sections with the all-clear marker.

1. Read `~/workspace/TODO.md`.
2. For each of the four sections (`## Bugs`, `## Reliability`, `## Code Quality`, `## Enhancements`):
   - Remove all lines that are completed items (`- [x] ...`) including their continuation lines (indented lines
     immediately following a `- [x]` line that are part of the same item).
   - If the section now has no remaining `- [ ]` items, replace its entire body with `✨ *All clear*`.
   - If the section still has unchecked `- [ ]` items, leave them intact — do not add the all-clear line.
3. Write the updated content back to `TODO.md`. Preserve the frontmatter status block exactly as-is — do not modify the
   lock state.
4. Run `/lint-markdown` on `TODO.md` after writing.
5. Confirm how many items were removed and which sections were stamped all-clear.
