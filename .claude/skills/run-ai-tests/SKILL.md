---
name: run-ai-tests
description: Run AI-driven tests from the tests/ directory in order and report results
argument-hint: "[--continue-on-failure] [<test-numbers>]"
---

Run AI-driven tests in the `tests/` directory.

**Arguments:** $ARGUMENTS

## Arguments

- `--continue-on-failure` — execute all tests regardless of failures. Without it, the runner stops at the first failed test (except `900-cleanup.md`, which always runs).
- `<test-numbers>` — one or more space-separated test numbers (e.g. `4 5` or `004 005`). When provided, only those tests are run in numeric order. `000-init.md` and `900-cleanup.md` are always added automatically — do not include them in the list. All other tests are skipped with `SKIP — not selected`.

Examples:
- `/run-ai-tests` — run all tests
- `/run-ai-tests 4 5` — run init, tests 004 and 005, then cleanup
- `/run-ai-tests --continue-on-failure 1 2 3` — run init, tests 001–003 continuing past failures, then cleanup

## Steps

1. **Parse arguments.**
   - Check whether `--continue-on-failure` was passed.
   - Collect any numeric tokens as the target set. Normalize each to a zero-padded 3-digit prefix (e.g. `4` → `004`, `005` stays `005`).
   - If no numeric tokens are given, the target set is empty (run all tests).

2. **Discover tests.** List all `*.md` files in the `tests/` directory, sorted by filename. The filename stem (without the number prefix and without `.md`) is the test name — e.g. `001-heartbeat-fires.md` → `Heartbeat Fires` (strip leading digits and hyphens, title-case the remainder).

3. **Build the run list.**
   - If the target set is **empty**: the run list is all discovered tests in order.
   - If the target set is **non-empty**: the run list is `000-init.md`, then each test whose 3-digit prefix matches a value in the target set (in filename order), then `900-cleanup.md`. Tests not in the target set are recorded as `SKIP — not selected` and included in the report but not executed.

4. **Run each test in the run list in order.** For each test file:
   - Read the file. The `description` frontmatter field describes what the test does. The body is the test instruction.
   - If the `enabled` frontmatter field is `false`, skip the test and record it as `SKIP — disabled`.
   - Execute the test by following the instructions in the body exactly.
   - Determine pass or fail based on the criteria stated in the body.
   - Record the result: test name, status (PASS / FAIL), and a one-line summary of what happened.
   - If the test fails and `--continue-on-failure` was **not** passed: stop immediately, skip remaining tests (except run `900-cleanup.md` if it has not yet run), and proceed to the report.
   - If the test fails and `--continue-on-failure` **was** passed: record the failure and continue to the next test.

5. **Always run cleanup.** `900-cleanup.md` runs regardless of failures or the `--continue-on-failure` flag — even if an earlier test caused an early stop.

6. **Report results.** After all tests have run, print a summary table:

```
Test Results
────────────────────────────────────────
 PASS  000 Init
 PASS  004 Trigger Fires
 FAIL  005 Trigger Payload     — response did not contain PAYLOAD_TEST_7x9q
 SKIP  001 Heartbeat Fires     — not selected
 PASS  900 Cleanup
────────────────────────────────────────
Passed: 3  Failed: 1  Skipped: 1  Total: 5
```

Use `PASS`, `FAIL`, or `SKIP` as the status. In the summary, show executed tests first (in run order), then skipped tests. Skipped tests are ones that were not run due to an earlier failure without `--continue-on-failure`, because `enabled: false` is set in their frontmatter, or because they were not in the target set.

If all tests passed, end with: **All tests passed.**
If any tests failed, end with: **X test(s) failed.**
