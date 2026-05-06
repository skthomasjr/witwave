---
name: docs-validate
description:
  Validate and auto-fix markdown formatting and prose-syntax across the repo's documentation surface. Runs the same
  markdownlint + prettier toolchain that CI does, applies safe auto-fixes, and reports what changed. Trigger when the
  user says "validate docs", "lint docs", "fix doc formatting", or as a step inside the `docs-scan` orchestrator.
version: 0.1.0
---

# docs-validate

Bring documentation files into compliance with the repo's lint configuration — markdownlint rules and Prettier
prose-formatting. This is a purely mechanical pass: only changes that the tools' own auto-fix modes produce are applied.
No prose rewrites, no restructuring, no judgement.

The repo pins both tools via `npx --yes <pkg>@<version>` — there is no committed lockfile, no package.json.
Configuration lives at `.markdownlint.yaml` and `.prettierrc.yaml`; ignore lists at `.markdownlintignore` and
`.prettierignore`.

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path (Primary repository → Local checkout)

Substitute the literal path into the commands below. Run from inside the checkout's working tree.

### 1. Verify the toolchain is reachable

```sh
git -C <checkout> rev-parse --show-toplevel
```

Confirm the checkout exists and is a git repo. If not, **stop and log the absence to your deferred-findings memory** —
kira's contract is to stand down when the source tree isn't ready (see your CLAUDE.md → Responsibilities → 1).

### 2. Read the pinned tool versions from CI

The CI workflow `.github/workflows/ci-docs.yml` pins the markdownlint and prettier versions used in production. Pull
them from there so kira's local runs match what the PR gate expects:

```sh
grep -E "markdownlint-cli@|prettier@" <checkout>/.github/workflows/ci-docs.yml
```

Capture each pinned version. If the grep returns nothing, the workflow has changed shape — log to memory and stop.

### 3. Apply Prettier auto-fixes

Prettier's `--write` mode rewrites files in place to match the configured style (proseWrap, printWidth, etc.). Honor
`.prettierignore`; the tool reads it automatically.

```sh
cd <checkout> \
  && npx --yes prettier@<pinned-version> \
       --write \
       --log-level=warn \
       "**/*.md"
```

Capture the list of files Prettier reported as `(reformatted)`.

### 4. Apply markdownlint auto-fixes

`markdownlint-cli`'s `--fix` mode applies the rule-level fixes that are safe. Rules without auto-fix capability are
reported as diagnostics (no edits) — log those for human review later via `docs-scan`.

```sh
cd <checkout> \
  && npx --yes markdownlint-cli@<pinned-version> \
       --config .markdownlint.yaml \
       --ignore-path .markdownlintignore \
       --fix \
       "**/*.md"
```

(Note: `markdownlint-cli` — NOT `markdownlint-cli2`. The `cli2` variant is a different package with a different CLI
shape; CI pins the original `cli` and we mirror it. If a future CI bump switches to `cli2`, update this skill too.)

Capture: (a) the list of files fixed; (b) the list of remaining diagnostics that need human attention. The latter goes
to deferred-findings memory.

### 5. Project-specific formatting (post-Prettier touchups)

Two project-specific rules that Prettier doesn't enforce on its own and that need to ride alongside the Tier 1
mechanical pass:

#### Rule A — Markdown tables stay column-aligned

Prettier's default behaviour DOES re-pad markdown table columns (`| --- | --- |` separators get aligned to the widest
cell in each column). After step 3 (Prettier), this rule is normally already satisfied — but verify because:

- Files in `.prettierignore` are skipped by Prettier; if any contain markdown tables, those won't have been re-padded.
- "Tables" inside fenced code blocks (` ```markdown ... ` ) aren't real markdown tables and Prettier won't touch them —
  but if a doc claims to show a table example, the example should still look aligned. That's an authorial choice; flag
  in memory rather than auto-fix.

For each ignored-but-tabled file, log a one-line entry to deferred-findings memory. Don't try to format ignored files —
they're excluded for a reason.

#### Rule B — YAML frontmatter line lengths

Markdown files with YAML frontmatter (the `---`-bracketed block at the top of every `SKILL.md`, `HEARTBEAT.md`,
deferred-findings memory entry, etc.) frequently have **single-line string values that blow past the prose `printWidth`
limit**. Prettier formats the markdown body but treats the frontmatter as opaque YAML and leaves long lines alone. The
end state: prose stays at proseWrap=120 but the frontmatter `description:` runs 300+ characters on one line, which reads
badly in any narrow editor.

Apply this rule:

- For each `*.md` file with YAML frontmatter, parse the frontmatter block (lines between the opening and closing `---`).
- For each string-valued key whose serialised line exceeds the configured `printWidth` from `.prettierrc.yaml`
  (currently 120):
  - Rewrite the value as a YAML **folded scalar** using `>-` (folded, strip-trailing-newlines) so the YAML semantics are
    preserved (folded scalars collapse to a single string with spaces between wrapped lines).
  - Wrap the content to `printWidth - 2` columns of prose (the `-2` accounts for the YAML indent), preserving any inline
    backticks, links, and punctuation.
  - Indent each wrapped line two spaces relative to the key, per YAML folded-scalar convention.

Example transformation:

```yaml
# before — one long line, hard to scan in a narrow editor
description:
  Validate and auto-fix markdown formatting and prose-syntax across the repo's documentation surface. Runs the same
  markdownlint + prettier toolchain that CI does, applies safe auto-fixes, and reports what changed. Trigger when the
  user says "validate docs", "lint docs", "fix doc formatting".
```

```yaml
# after — folded scalar, wraps at proseWrap=120
description: >-
  Validate and auto-fix markdown formatting and prose-syntax across the repo's documentation surface. Runs the same
  markdownlint + prettier toolchain that CI does, applies safe auto-fixes, and reports what changed. Trigger when the
  user says "validate docs", "lint docs", "fix doc formatting".
```

Implementation: a small Python script using `yaml.safe_load` to parse and
`yaml.safe_dump(..., default_style='>', width=N)` to re-emit (or hand-rolled wrapping if PyYAML's defaults don't match
exactly). Run inside the workspace checkout, write changes in-place, capture the list of files modified.

After this rule fires, re-run Prettier once more — the frontmatter rewrite shouldn't change the markdown body, but
running Prettier again is a cheap idempotent verification that the file still satisfies the formatter.

#### Reporting these touchups

Both rules' modifications count toward the "files modified" count in step 6's report, broken out separately so the
orchestrator (`docs-scan` / `docs-cleanup`) can group them into their own commit:

> `docs: format YAML frontmatter to respect printWidth`

(Or include them in the main `docs: apply markdownlint + prettier auto-fixes` commit if the orchestrator prefers a
single batch — that's the orchestrator's call, not this skill's.)

### 6. Report

Return a structured summary to the caller:

- Files modified by Prettier (count + paths)
- Files modified by markdownlint (count + paths)
- Diagnostics that couldn't be auto-fixed (count + paths + rule IDs)

Do NOT commit here. The orchestrator (`docs-scan`) batches commits across all docs skills before invoking `git-push`.

## When to invoke

- As the **validation phase** of `docs-scan` (the heartbeat- driven orchestrator).
- As an **on-demand check** — the user says "lint docs" or "fix doc formatting" and you run this skill alone.
- **After a large doc edit** — bring everything back to spec before committing.

## Out of scope for this skill

- Prose rewrites for clarity, tone, or voice (judgement work, not validation).
- Adding or removing docs (deciding whether a doc should exist).
- Fixing broken links or stale path references — that's `docs-links`.
- Verifying claims against the code — that's `docs-verify` (Tier 2).
- Committing or pushing — the orchestrator owns that.
- Editing the lint config itself (`.markdownlint.yaml`, `.prettierrc.yaml`) — those are repo policy, not kira's call.
