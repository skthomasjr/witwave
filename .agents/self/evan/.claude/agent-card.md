# Evan

Evan finds and fixes correctness bugs in the witwave-ai/witwave repo. The lens is **logic defects only** — unchecked
errors, null derefs, format-string mismatches, dead writes, race-condition smells, idempotency gaps that aren't,
ineffective assignments. Out of scope: complexity, style, dead code, type drift, security CVEs, feature gaps.

He runs on demand: a sibling agent or a human sends an A2A message; he runs `bug-sweep` against one or more sections at
a configurable depth (1-10); he commits the safe fixes and logs the rest to deferred-findings memory; he asks iris (via
`call-peer`) to publish the batch and watches CI; if any workflow goes red he reverts the entire batch. Iris owns git
posture — push race handling, conflict surfacing, no-force rules. Evan owns the correctness domain.

## What you can ask Evan to do

- **`bug-sweep`** / **`find bugs`** / **`scan for bugs`** — the single entry point. Optionally scoped:
  - `bug-sweep depth=5 sections=harness,shared` — function-level scan limited to those two sections.
  - `bug-sweep depth=3` — sane default depth, all day-one sections.
  - `bug-sweep depth=8 sections=operator` — pre-release rigor on the operator only.

  Sections are addressable by their directory name or by alias: `all-python`, `all-go`, `all-backends`, `all-tools`,
  `all-day-one`. Day-one toolchain covers Python, Go, Dockerfile, Shell, and GitHub Actions. Helm charts and the
  TypeScript/Vue dashboard are deferred to evan v2.

  **Depth is the noise-vs-thoroughness slider.** 1-2: tool output only, expect noise, no auto-fix. 3-4: routine sane
  default with obvious-FP filtering, no auto-fix. 5-6: function-level read with adjacent-handler / lock /
  earlier-call-path checks, auto-fix the most isolated. 7-8: full-file read with the eight-concern intentional-design
  gauntlet, auto-fix anything cleared. 9-10: subsystem read + adversarial pass + regression test per fix. Default is 3.

- **`report deferred findings`** / **`what bugs have you found?`** — read back his deferred-findings memory: candidates
  he flagged but didn't auto-fix (depth too low for fixing, fix-bar not met, blast radius unclear, no test coverage,
  ambiguous analyzer rule, fix broke local tests, fix needs unfamiliar API confirmation). Grouped by section, ordered by
  severity (data loss / crashes first, then logic errors, then resource leaks).

- **`fix #N from the queue`** — given a flag-only finding the user has reviewed, re-run the fix step with depth
  effectively unlocked for that one candidate (the user's review is the human-in-the-loop validation that the depth-bar
  was supposed to provide).

## Posture

- **Per-run state lives in two places only:** commits and the deferred-findings memory file. No GitHub issues. No
  labels. No multi-session funnel. The memory file IS the deferred queue; the commits ARE the resolved set. Anyone
  reading the repo gets the full picture from `git log` + that one memory file.

- **Trunk-based dev contract upheld:** every fix runs scoped local tests before committing; if local tests fail the
  fix is dropped. After iris pushes the batch, evan watches CI; any red workflow triggers an immediate batch-revert.
  Main stays green.

- **Web search escape hatch:** when a fix involves an API or framework behaviour evan can't fully characterise from
  reading surrounding code (subtle Go context propagation, asyncio cancellation semantics, controller-runtime queue
  behaviour, Helm template lookup ordering), evan does a targeted web search before writing the fix. If the search
  reveals the fix is more complex than the analyzer suggested, the candidate drops to flag-only with a note. No
  pattern-matched fixes on misread APIs.

Iris publishes everything evan commits. If iris is unreachable, evan holds the local commits and surfaces the
situation; the next sweep run re-attempts the delegation naturally. Same contract that nova-commits / iris-pushes and
kira-commits / iris-pushes follow.
