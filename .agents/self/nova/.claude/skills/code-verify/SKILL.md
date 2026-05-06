---
name: code-verify
description:
  Tier 2 read-only check that source-file comments still match the code they describe. Validates Python docstrings
  against function signatures (parameter names, types, return claims), Go godoc comments against exported APIs, and
  Helm `values.yaml` comments against the values actually referenced in chart templates. Memory-log only — never
  auto-fix, because every mismatch is a judgment call (update comment vs. fix code). Trigger when the user says
  "verify code comments", "check code docs against reality", or as a step inside `code-cleanup`.
version: 0.1.0
---

# code-verify

Find places where code comments lie about the code they describe. Out
of scope: anything in the **Generated / vendored code** category — the
comments there are tool-generated and any drift gets fixed by re-running
the generator, not by hand.

This skill **does not auto-fix** anything. The reason: if a docstring
says a function takes `(timeout: int)` but the signature is
`(timeout_seconds: float)`, the right resolution could be either side
(rename the parameter back, or update the docstring) — only a human can
decide. Findings go to your deferred-findings memory file.

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path

### 1. Verify the source tree is in place

```sh
git -C <checkout> rev-parse --show-toplevel
```

Stand down + log if missing.

### 2. Enumerate sources by language

Same patterns as `code-format`'s enumeration step. Exclude generated /
vendored code.

### 3. Run the language-specific verification passes

Process each language in turn. Findings are append-only memory entries;
this skill never edits source files.

#### Check A — Python docstrings vs signatures

For each Python file with public functions / classes (not prefixed
with `_`):

- Parse the file (use `ast.parse`).
- For each function / class with a docstring, extract:
  - Parameter names from the signature (`ast.FunctionDef.args.args`)
  - Parameter type annotations
  - Return type annotation
- Parse the docstring (heuristic — Google-style `Args:` / `Returns:`
  sections, NumPy-style `Parameters` / `Returns` sections, or reST
  `:param:` / `:returns:` directives).
- For each docstring claim about a parameter or return:
  - Parameter name claimed but not in signature → **finding**
  - Parameter type claimed but signature has different annotation →
    **finding**
  - Return type claimed but signature has different annotation →
    **finding**

False-positive caution: type annotations may be missing on signatures
even when docstrings include them. Don't flag "docstring says
`int`, signature says nothing" as a mismatch — that's a missing
annotation, not a contradiction. Skip.

#### Check B — Go godoc vs exported API

For each Go file with exported symbols (capitalised first letter):

- Parse the file (use `go/parser` via a small Go helper, or fall back
  to regex extraction of `func`, `type`, `var`, `const` declarations
  and the comment block immediately above each).
- For each exported symbol with a godoc comment:
  - The first sentence of the comment should start with the symbol's
    own name (Go convention; e.g. `// ParseFoo parses the foo into…`).
  - Parameter names in the prose should match the signature.
  - Return value descriptions should match the actual return shape
    (count + types).
- Mismatches → **finding**.

False-positive caution: Go style is looser than Python style. Don't
flag stylistic deviations — only flag claims that contradict the
code (wrong parameter name, wrong return type).

#### Check C — Helm `values.yaml` comments vs template usage

For each `charts/*/values.yaml`:

- Parse the YAML to enumerate every value path.
- For each value path with a `#` comment block above it:
  - Search the chart's `templates/**/*.{yaml,tpl,yml}` for references
    to that value (`.Values.<path>`).
  - **Finding** if no template references the value (the comment
    documents an orphaned value the chart no longer consumes).
  - **Finding** if the comment claims a default that disagrees with
    the actual default in the YAML.

False-positive caution: some values are intentionally consumed by
sub-charts or external operators; if the value name pattern matches
a known-external pattern (e.g. `ingress.annotations.*`), don't flag.

### 4. Log findings to memory

Append to
`/workspaces/witwave-self/memory/agents/<your-name>/project_code_findings.md`
under a new dated section:

```markdown
## YYYY-MM-DD — code-verify

### Python docstrings

- `<path>:<line>` `<func-or-class>` — <one-line mismatch summary>

### Go godoc

- `<path>:<line>` `<symbol>` — <one-line mismatch summary>

### Helm values

- `<chart>/values.yaml:<line>` `<value-path>` — <one-line summary>
```

Group by language; one finding per line so a human reviewer can scan
quickly.

### 5. Report

Return a structured summary to the caller:

- Total files scanned per language
- Total comments examined per language
- Findings logged per language (count + memory file pointer)
- No commits produced (Tier 2 is memory-log only)

## When to invoke

- As Tier 2 inside `code-cleanup` (the orchestrator).
- On demand: "verify code comments", "check code docs against reality",
  "are the docstrings still accurate?".
- After a refactor that renamed parameters / changed signatures /
  reorganised exported APIs — those are the changes most likely to
  leave comments behind.

## Out of scope for this skill

- **Auto-fixing** — every finding is a judgment call (update comment
  vs. update code). Findings go to memory.
- **Generated / vendored code** — out of scope per CLAUDE.md.
- **Comments that are missing entirely** — that's `code-document`'s
  job (Tier 3). This skill checks comments that *exist* against the
  code; absence isn't a finding here.
- **Stylistic preferences** — only flag contradictions with the code,
  not "this comment could be clearer". Editorial work isn't
  verification.
- **Test code semantic verification** — too fuzzy; the test's intent
  isn't always derivable from its body. Tier 2 stays on application
  code only.
