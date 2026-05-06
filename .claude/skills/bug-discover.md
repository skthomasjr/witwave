---
name: bug-discover
description:
  Analyze one or all components of the witwave platform for bugs and record findings as tracked bugs. Trigger when the
  user says "find bugs", "discover bugs", "look for bugs", "scan for bugs", "search for bugs", or "run bug discover" —
  with or without a component name.
version: 1.2.0
---

# bug-discover

Analyze a specific component of the witwave platform for bugs.

## Instructions

**Step 1: Identify the component(s).**

Read the Components table in `<repo-root>/README.md` to determine which directory corresponds to what the user said. Map
natural language to the table — "Claude backend", "Claude agent", or just "Claude" all map to `backends/claude/`.
"Orchestrator", "witwave", or "router" map to `harness/`. "UI", "interface", "dashboard", or "frontend" map to
`clients/dashboard/`.

If the user specifies "all" or does not specify a component, run this skill against every component in the Components
table in sequence. Complete all steps for each component before moving to the next.

**Step 2: Understand the component's role in the overall architecture.**

Read the root `README.md` and the component's own `README.md` to understand what this component does, what calls it,
what it calls, and how it fits into the overall system. Then read `AGENTS.md` for any additional architectural context.
This understanding is required before analyzing code — bugs in isolation are often not bugs at all, and bugs that matter
are the ones that break integration points. Pay particular attention to shared code and utilities called by multiple
components, as bugs there have wider blast radius.

**Step 3: Read all source files in that directory.**

Read all source files in the identified directory. Do not skip any file.

**Step 4: Analyze for bugs.**

Always perform a full, independent analysis of the source — do not assume that previously filed bugs represent all known
bugs. Every invocation of this skill is a fresh evaluation.

Focus exclusively on real bugs — not style, not improvements, not missing features. Look for:

- Logic errors (wrong conditionals, off-by-one, incorrect operator)
- Race conditions or concurrency issues (async/await misuse, shared state without locking)
- Error handling gaps (exceptions swallowed silently, missing cleanup on failure paths)
- Resource leaks (file handles, connections, or processes not closed)
- Incorrect assumptions about external data (missing None checks, wrong field names, type mismatches)
- Security issues (injection, unvalidated input, insecure defaults)

**Step 5: Validate each candidate against intentional design before filing.**

The most common failure mode in this skill is filing a candidate that the surrounding code has already addressed. Before
filing each candidate, re-read the full surrounding context — not just the cited line range — and check for any of the
following. If any apply, **drop the candidate and do not file**.

- **Inline comments documenting the choice.** A comment near the cited code that says "this is intentional", references
  a prior issue (`#NNNN`), or explains a design tradeoff usually means the code is correct as written and the candidate
  is a misread. The witwave codebase relies heavily on inline `#NNNN` references — search a 20-line window above and
  below the cited line for them.
- **Adjacent existing handlers.** A flag at line N is often resolved by code at line N±5. Read the 10 lines immediately
  before and after — an `else` branch, a `finally` block running cleanup, an early-return guard, an `except Exception:`
  two lines below the `except TimeoutError:` you flagged — these are the patterns most often missed.
- **Synchronization already in place.** "Race condition" candidates are common false positives. Check whether the
  function is wrapped in a lock (`async with _lock`, `threading.Lock`, etc.), runs on a single-threaded asyncio loop, or
  relies on language-level atomicity (CPython GIL atomicity for reference rebinds and single-list-index assignments). If
  the candidate would require multiple writers to a shared variable but only one path writes, it isn't a race.
- **Defensive checks earlier on the call path.** "Missing nil-check" / "missing validation" candidates are often
  resolved by a check in the caller. Read what calls the function before flagging an internal gap.
- **Documented design tradeoffs.** Some "silent failures" are intentional — a CLI tool quietly falling back to anonymous
  when credentials aren't found, a config parser failing loud at startup rather than degrading silently, a watch handler
  returning empty on transient apiserver errors so controller-runtime's rate limiter handles backoff. If a comment or
  surrounding context explains the choice, the candidate is invalid.
- **Idempotent operations.** "Double cancel" / "double delete" / "double cleanup" are usually safe in well-designed APIs
  (Go's `context.CancelFunc`, Python's `set.discard`, K8s `client.Delete`). Check whether the operation is idempotent
  before flagging duplication as a defect.

A candidate is more likely to be **real** when: the cited code has no nearby `#NNNN` references, no surrounding guards,
no locks, and the failure mode is reachable from a clear external trigger that no other code path validates. A candidate
is more likely to be **false** when: the cited code has an inline comment with a prior issue number within 10 lines, or
an existing handler/check in the same function that the candidate's description didn't reference.

When in doubt, drop the candidate. The bug-implement skill exists as a final filter, but every false positive that
reaches filing wastes effort across refine and approve as well — filtering at this step is the cheapest place to do it.

**Step 6: Report findings.**

For each bug found, report:

- File and line number
- What the bug is
- Why it's a bug (what goes wrong)
- A suggested fix

For each bug found, record it as a tracked issue. Every issue **must** be filed with at minimum the labels `bug` and
`pending`. Include the component, file and line number, what the bug is, what goes wrong at runtime, and the suggested
fix. Do not fix the bug — only report and record it. An issue filed without labels is invalid — verify labels are
present after filing.

If a sufficiently similar bug already exists, do not file a duplicate. Instead, add a comment to the existing bug noting
that it has been re-identified. The comment must include:

- The agent of record that re-identified it
- The name and version of this skill (see the frontmatter of this file)

Report bugs in descending order of severity — data loss, crashes, and security issues first; logic errors and incorrect
behavior next; resource leaks and edge cases last.

If no bugs are found, say so clearly. Do not pad the output with non-bugs.
