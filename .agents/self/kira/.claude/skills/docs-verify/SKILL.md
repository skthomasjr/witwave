---
name: docs-verify
description:
  Cross-check Category C documentation (README files, CONTRIBUTING, LICENSE, CHANGELOG, docs/, per-subproject READMEs)
  against the current state of the code — verify that code references, file paths, command examples, env-var names,
  version numbers, and configuration claims still match reality. Memory-log discrepancies; never auto-fix (semantic
  intent is fragile and either side could be wrong). Trigger when the user says "verify docs", "check docs against
  code", "are the docs accurate?", or as a step inside `docs-cleanup`.
version: 0.1.0
---

# docs-verify

Find places where Category C documentation says something the code contradicts. Out of scope: Category A (agent identity
under `.agents/**`) and Category B (repo-root `CLAUDE.md`, `AGENTS.md`, `.claude/skills/**`) — those are agent prompts,
not human-facing docs. Per the doc-categories policy in your CLAUDE.md, semantic work on those categories needs a
different posture and is explicitly off-limits to autonomous skills.

This skill **does not auto-fix** anything. The reason: if a doc claims `ww agent create` exists and the code shows the
command is now `ww agent new`, the right fix could be **either** updating the doc OR restoring the command name (rename
was wrong) — only a human can decide. Findings go to your deferred-findings memory file for human review.

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path

### 1. Verify the source tree is in place

```sh
git -C <checkout> rev-parse --show-toplevel
```

If the checkout is missing or empty, log to memory and stand down (per your CLAUDE.md → Responsibilities → 1).

### 2. Enumerate Category C docs

Build the file list from path patterns. Anchor at `<checkout>`:

| Include pattern                            | Notes                                                                                        |
| ------------------------------------------ | -------------------------------------------------------------------------------------------- |
| `README.md` (root)                         | Top-level project README                                                                     |
| `CONTRIBUTING.md`                          | Contribution guide                                                                           |
| `LICENSE` (or `LICENSE.md`)                | Legal — usually nothing to verify                                                            |
| `CHANGELOG.md`                             | Release notes — version numbers + dates                                                      |
| `SECURITY.md`                              | Security policy                                                                              |
| `docs/**/*.md`                             | Project docs tree                                                                            |
| `**/README.md` (excluding `.agents/**`)    | Per-subproject READMEs (clients/, charts/, tools/, helm/, operator/, harness/, backends/, …) |
| `**/CHANGELOG.md` (excluding `.agents/**`) | Per-subproject changelogs if they exist                                                      |

Excludes (Cat A + Cat B):

- `.agents/**` — anything under here is Cat A.
- Root `CLAUDE.md` and root `AGENTS.md` — Cat B.
- `.claude/skills/**` and `.codex/**` at repo root — Cat B.

Use `git -C <checkout> ls-files` filtered with the patterns above so you only consider tracked files.

### 3. Extract verifiable references from each doc

For each Category C file, scan for these reference shapes:

| Reference shape            | Example                                                        | What to check                                                    |
| -------------------------- | -------------------------------------------------------------- | ---------------------------------------------------------------- |
| **File / directory paths** | `` `clients/ww/cmd/agent.go` `` or `` `docs/bootstrap.md` ``   | Does the path exist in the repo?                                 |
| **Command examples**       | `` `ww agent create iris ...` ``                               | Does that subcommand exist in the CLI?                           |
| **Env var names**          | `TASK_TIMEOUT_SECONDS`, `HARNESS_EVENTS_AUTH_TOKEN`            | Does the env var still appear in the codebase / docs/configmaps? |
| **Code identifiers**       | function/class names like `ParseBackendEnvs` or `WitwaveAgent` | Does the symbol exist?                                           |
| **Version numbers**        | `v0.13.0`, `markdownlint-cli@0.43.0`                           | Match the actual current/pinned version?                         |
| **URLs to repo content**   | `https://github.com/witwave-ai/witwave/blob/main/...`          | Resolves to a real path?                                         |
| **Configuration claims**   | "the operator stamps `OTEL_*` vars on harness and backends"    | Does the operator code actually do that?                         |

Use heuristic regex extraction — don't try to parse markdown formally; just find tokens that look like code refs
(backtick- quoted snippets, fenced-code blocks, URLs).

### 4. Verify each reference

For each extracted reference, run a low-cost check:

- **Paths:** `[ -e <checkout>/<path> ]`
- **Commands:** `grep -r <subcommand>` in `clients/ww/cmd/`
- **Env vars:** `grep -r <NAME>` in `harness/`, `backends/`, `operator/`, `clients/ww/`
- **Identifiers:** `grep -rn <symbol>` in the relevant language's source dirs
- **Versions:** for release tags, `git tag --list` and pick the latest; for tool pins, `grep <tool>@` in
  `.github/workflows/`
- **URLs:** parse the path component; `[ -e <checkout>/<path> ]`

A reference passes if the check finds it; fails otherwise.

False-positive caution: a code identifier mentioned in the doc might exist under a different file or with a renamed
symbol. Before flagging as broken, do a broader text search; if it matches even loosely, treat as inconclusive (skip —
not a finding).

### 5. Log findings to memory

For each broken reference, append to the deferred-findings memory file at
`/workspaces/witwave-self/memory/agents/<your-name>/project_doc_findings.md` under a new section:

```markdown
## YYYY-MM-DD — docs-verify

### <doc path>:<line>

- **Claim:** "<excerpt of the prose>"
- **Reference:** `<extracted token>`
- **Reality:** <one-line summary of what actually exists / is named / is at that path>
- **Recommended action:** <update doc | rename code | flag for human> with one-line reasoning
```

Group findings by source file so a human reviewing knows where to look. Don't dedupe across scans — each new run appends
a new section with today's date so the trail is honest.

### 6. Report

Return a structured summary to the caller:

- Total Category C files scanned
- Total verifiable references extracted
- Number of references that verified clean
- Number of broken references logged (per-file count)
- Pointer to the deferred-findings memory file

## When to invoke

- Inside `docs-cleanup` (the orchestrator).
- On demand: "verify docs", "check docs against code", "are the docs telling the truth?", "find stale doc claims".
- After a large refactor that renames functions, moves files, or bumps versions — those are the changes most likely to
  leave docs behind.

## Out of scope for this skill

- **Cat A / Cat B docs** — explicitly off-limits per your CLAUDE.md → Doc categories policy. Agent prompts and local
  dev-tooling instructions are not human-facing documentation; changes there affect agent or assistant behaviour and
  need a different posture.
- **Auto-fixing** — every finding is a judgment call (update doc vs. fix code vs. accept as intentional). Findings land
  in memory; humans decide.
- **Deep semantic checking** — this skill checks identifiable references (paths, names, versions). It doesn't try to
  validate that prose claims are true at a deeper level ("this paragraph accurately describes the architecture"). That
  needs human review.
- **External URL liveness** — `docs-links` already covers internal cross-refs; external URL validation is a separate
  concern (network-dependent, flaky, not in Tier 2 scope).
