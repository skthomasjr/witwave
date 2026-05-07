---
name: code-verify
description:
  Tier 2 read-only check that source-file comments still match the code they describe. Validates Python docstrings
  against function signatures (parameter names, types, return claims), Go godoc comments against exported APIs, and Helm
  `values.yaml` comments against the values actually referenced in chart templates. Memory-log only — never auto-fix,
  because every mismatch is a judgment call (update comment vs. fix code). Trigger when the user says "verify code
  comments", "check code docs against reality", or as a step inside `code-cleanup`.
version: 0.1.0
---

# code-verify

Find places where code comments lie about the code they describe. Out of scope: anything in the **Generated / vendored
code** category — the comments there are tool-generated and any drift gets fixed by re-running the generator, not by
hand.

This skill **does not auto-fix** anything. The reason: if a docstring says a function takes `(timeout: int)` but the
signature is `(timeout_seconds: float)`, the right resolution could be either side (rename the parameter back, or update
the docstring) — only a human can decide. Findings go to your deferred-findings memory file.

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path

### 1. Verify the source tree is in place

```sh
git -C <checkout> rev-parse --show-toplevel
```

Stand down + log if missing.

### 2. Enumerate sources by language

Same patterns as `code-format`'s enumeration step. Exclude generated / vendored code.

### 3. Run the language-specific verification passes

Process each language in turn. Findings are append-only memory entries; this skill never edits source files.

#### Check A — Python docstrings vs signatures

For each Python file with public functions / classes (not prefixed with `_`):

- Parse the file (use `ast.parse`).
- For each function / class with a docstring, extract:
  - Parameter names from the signature (`ast.FunctionDef.args.args`)
  - Parameter type annotations
  - Return type annotation
- Parse the docstring (heuristic — Google-style `Args:` / `Returns:` sections, NumPy-style `Parameters` / `Returns`
  sections, or reST `:param:` / `:returns:` directives).
- For each docstring claim about a parameter or return:
  - Parameter name claimed but not in signature → **finding**
  - Parameter type claimed but signature has different annotation → **finding**
  - Return type claimed but signature has different annotation → **finding**

False-positive caution: type annotations may be missing on signatures even when docstrings include them. Don't flag
"docstring says `int`, signature says nothing" as a mismatch — that's a missing annotation, not a contradiction. Skip.

#### Check B — Go godoc vs exported API

For each Go file with exported symbols (capitalised first letter):

- Parse the file (use `go/parser` via a small Go helper, or fall back to regex extraction of `func`, `type`, `var`,
  `const` declarations and the comment block immediately above each).
- For each exported symbol with a godoc comment:
  - The first sentence of the comment should start with the symbol's own name (Go convention; e.g.
    `// ParseFoo parses the foo into…`).
  - Parameter names in the prose should match the signature.
  - Return value descriptions should match the actual return shape (count + types).
- Mismatches → **finding**.

False-positive caution: Go style is looser than Python style. Don't flag stylistic deviations — only flag claims that
contradict the code (wrong parameter name, wrong return type).

#### Check C — Helm `values.yaml` comments vs template usage

For each `charts/*/values.yaml`:

- Parse the YAML to enumerate every value path.
- For each value path with a `#` comment block above it:
  - Search the chart's `templates/**/*.{yaml,tpl,yml}` for references to that value (`.Values.<path>`).
  - **Finding** if no template references the value (the comment documents an orphaned value the chart no longer
    consumes).
  - **Finding** if the comment claims a default that disagrees with the actual default in the YAML.

False-positive caution: some values are intentionally consumed by sub-charts or external operators; if the value name
pattern matches a known-external pattern (e.g. `ingress.annotations.*`), don't flag.

#### Check D — Dockerfile comments vs instruction layers

For each Dockerfile in scope:

- Walk the file line by line, pairing each non-trivial instruction (`RUN`, `COPY`, `ADD`, `ENV`, `EXPOSE`, `USER`, etc.)
  with the comment block immediately above it (if any).
- For each commented instruction, evaluate whether the comment's claim is consistent with the instruction:
  - Comment says "for build cache efficiency" but the layer is at the bottom of a long sequence with no cache benefit →
    **finding**
  - Comment names a package / version that doesn't appear in the `RUN apt-get install` line below it → **finding**
  - Comment claims `ENV FOO=bar` is "required for X" but no later instruction references `$FOO` → **finding**
  - Comment claims a `USER agent` runs as non-root but a later `USER root` undoes it → **finding**

False-positive caution: Dockerfile comments are often general context, not strict claims. Only flag when a comment makes
a SPECIFIC claim contradicted by the surrounding instructions.

#### Check E — Shell-script comments vs function bodies

For each shell script in scope:

- Identify functions (POSIX `name()` or `function name` syntax).
- For each function with a header comment, evaluate whether the comment's description matches the body:
  - Comment claims function takes an argument named `$path` but the body references `$1` only → minor finding (style,
    not contradiction)
  - Comment claims function returns 0 on success / 1 on failure but the body never explicitly `return`s → **finding** if
    the claim is specific
  - Comment claims a side effect (writes a file, sets a global) that the body doesn't perform → **finding**

False-positive caution: shell scripts are often glue code where comments describe intent rather than mechanics. Don't
flag every loose description; focus on contradictions.

#### Check F — GitHub Actions workflow comments vs step references

For each workflow file in scope:

- Walk steps with `# comment` blocks above them.
- For each commented step, evaluate the comment's claim against the step's `uses:` / `run:` / `with:`:
  - Comment names an action or version that doesn't match the step's `uses:` reference → **finding**
  - Comment claims a step is conditional ("only on tags") but the step lacks a corresponding `if:` guard → **finding**
  - Comment claims the step's output feeds a later step but no later step references this step's `outputs.*` →
    **finding**

False-positive caution: workflow comments often describe the overall job intent, not strict per-step claims. Only flag
when a comment makes a specific claim the YAML structure contradicts.

### 4. Log findings to memory

Append to `/workspaces/witwave-self/memory/agents/<your-name>/project_code_findings.md` under a new dated section.
Each finding ends with a **status marker** matching the team-wide schema (parallel to evan's `bug-work` format)
so zora's backlog counter reads every peer's findings file uniformly:

- **`[pending]`** — default for newly-detected findings. The mismatch is real and worth a human's eyes; no
  judgment yet on what to do about it.
- **`[flagged: <reason>]`** — used when the finding has a *specific* judgment-call obstacle worth recording
  inline (e.g., `[flagged: code-symbol-renamed-but-doc-may-be-architectural-aspiration]`). Most code-verify
  findings stay `[pending]` since the skill is memory-log-only — human decides; reasons accrue as humans
  triage.
- **`[fixed: <SHA>]`** — when the underlying mismatch gets resolved later (by a sibling agent or human),
  mutate the marker to record the resolving commit. zora's counter treats `[fixed:]` as closed; `[pending]`
  and `[flagged:]` as open backlog.

Format:

```markdown
## YYYY-MM-DD — code-verify

### Python docstrings

- `<path>:<line>` `<func-or-class>` — <one-line mismatch summary> [pending]

### Go godoc

- `<path>:<line>` `<symbol>` — <one-line mismatch summary> [pending]

### Helm values

- `<chart>/values.yaml:<line>` `<value-path>` — <one-line summary> [pending]

### Dockerfile comments

- `<path>:<line>` — <one-line claim-vs-instruction mismatch> [pending]

### Shell-script comments

- `<path>:<line>` `<func-name>` — <one-line claim-vs-body mismatch> [pending]

### Workflow comments

- `<path>:<line>` — <one-line claim-vs-step mismatch> [pending]
```

Group by language; one finding per line so a human reviewer can scan quickly.

**Existing narrative-format entries** (from runs before 2026-05-07) stay as-is — don't re-mark retroactively.
Only new sections written from this skill onward use the marker schema. Over time the new format dominates;
zora's interim per-peer adapter handles the mixed state.

### 5. Report

Return a structured summary to the caller:

- Total files scanned per language
- Total comments examined per language
- Findings logged per language (count + memory file pointer)
- No commits produced (Tier 2 is memory-log only)

## When to invoke

- As Tier 2 inside `code-cleanup` (the orchestrator).
- On demand: "verify code comments", "check code docs against reality", "are the docstrings still accurate?".
- After a refactor that renamed parameters / changed signatures / reorganised exported APIs — those are the changes most
  likely to leave comments behind.

## Out of scope for this skill

- **Auto-fixing** — every finding is a judgment call (update comment vs. update code). Findings go to memory.
- **Generated / vendored code** — out of scope per CLAUDE.md.
- **Comments that are missing entirely** — that's `code-document`'s job (Tier 3). This skill checks comments that
  _exist_ against the code; absence isn't a finding here.
- **Stylistic preferences** — only flag contradictions with the code, not "this comment could be clearer". Editorial
  work isn't verification.
- **Test code semantic verification** — too fuzzy; the test's intent isn't always derivable from its body. Tier 2 stays
  on application code only.
