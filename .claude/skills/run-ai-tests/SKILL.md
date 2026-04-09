---
name: run-ai-tests
description: Run AI-driven tests from the tests/ directory in order and report results
argument-hint: "[--continue-on-failure]"
---

Run all AI-driven tests in the `tests/` directory in filename order.

**Arguments:** $ARGUMENTS

## Argument

The optional argument `--continue-on-failure` causes the runner to execute all tests regardless of failures. Without it, the runner stops at the first failed test (except `900-cleanup.md`, which always runs).

## Steps

1. **Parse arguments.** Check whether `--continue-on-failure` was passed.

2. **Discover tests.** List all `*.md` files in the `tests/` directory, sorted by filename. The filename stem (without the number prefix and without `.md`) is the test name — e.g. `001-heartbeat-fires.md` → `Heartbeat Fires` (strip leading digits and hyphens, title-case the remainder).

3. **Run each test in order.** For each test file:
   - Read the file. The `description` frontmatter field describes what the test does. The body is the test instruction.
   - If the `enabled` frontmatter field is `false`, skip the test and record it as `SKIP — disabled`.
   - Execute the test by following the instructions in the body exactly.
   - Determine pass or fail based on the criteria stated in the body.
   - Record the result: test name, status (PASS / FAIL), and a one-line summary of what happened.
   - If the test fails and `--continue-on-failure` was **not** passed: stop immediately, skip remaining tests (except run `900-cleanup.md` if it has not yet run), and proceed to the report.
   - If the test fails and `--continue-on-failure` **was** passed: record the failure and continue to the next test.

4. **Always run cleanup.** `900-cleanup.md` runs regardless of failures or the `--continue-on-failure` flag — even if an earlier test caused an early stop.

5. **Report results.** After all tests have run, print a summary table:

```
Test Results
────────────────────────────────────────
 PASS  000 Init
 PASS  001 Heartbeat Fires
 FAIL  002 Job Executes        — backend returned empty response
 SKIP  003 Session Continuity  — skipped due to prior failure
 PASS  900 Cleanup
────────────────────────────────────────
Passed: 3  Failed: 1  Skipped: 1  Total: 5
```

Use `PASS`, `FAIL`, or `SKIP` as the status. Skipped tests are ones that were not run due to an earlier failure without `--continue-on-failure`, or because `enabled: false` is set in their frontmatter.

If all tests passed, end with: **All tests passed.**
If any tests failed, end with: **X test(s) failed.**
