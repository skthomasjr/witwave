---
name: code-document
description:
  Tier 3 authoring pass — adds NEW comments / docstrings / godoc / helm-docs-style annotations to undocumented exported
  symbols and Helm `values.yaml` entries. Discipline is hard: every new comment must be GROUNDED in the code's actual
  behaviour. No claims that aren't demonstrably true from reading the body. Symbols that resist truthful description
  stay undocumented and get logged for human review. Trigger when the user says "document code", "add missing
  docstrings", "document helm values", "fill in code comments", or via scheduled job (weekly-ish).
version: 0.1.0
---

# code-document

Add missing inline documentation where it would meaningfully help future
contributors (humans and AI agents). The audience for these comments is
explicitly **anyone reading or modifying the code next** — including
future Claude / Codex sessions running on this repo, sibling agents
writing new code, and humans tracing through a behaviour they don't
yet understand.

The discipline is the same discipline `docs-research` uses for external
sources, applied to a different ground truth: **the code itself**.
Every comment authored must be derivable from reading the function /
template body. No claims from training data, no pattern-matched filler,
no plausible-sounding speculation. If you can't write a true sentence
about a symbol from reading its body, leave it undocumented and log it
for human attention.

## What gets documented

Three target categories, each with its own discipline:

### A — Go exported symbols (godoc)

Targets: any exported symbol (capitalised name) in active Go code that
lacks a godoc comment block.

Convention:

- The comment immediately precedes the declaration.
- The first sentence starts with the symbol name itself
  (`// ParseFoo parses…`).
- Subsequent sentences explain non-obvious behaviour, edge cases, and
  invariants the caller needs to know.
- Document parameters / return values only when their roles aren't
  obvious from the names + types.

What to write:

- Read the function body in full.
- State what it does in one sentence (the godoc summary).
- Name any concrete invariants the body enforces — error conditions,
  side effects, concurrency assumptions.
- Don't pad. A two-line godoc is fine if the function is two lines.

What to skip:

- Trivial getters / setters / constructors with self-evident bodies —
  godoc on those is noise, not signal.
- Symbols whose body uses external state you can't trace from the
  current file (cross-package context, generated stubs, etc.). Log
  for human review.

### B — Python public docstrings

Targets: any public function (no leading `_`) or class in active
Python code that lacks a docstring.

Convention follows the surrounding file's style. If the package mostly
uses Google-style (`Args:` / `Returns:` sections), match it. If reST or
NumPy style, match that. Don't introduce a new style.

What to write:

- One-line summary on the first line.
- Blank line, then expanded description if needed.
- `Args:` / `Returns:` / `Raises:` sections only when their content is
  non-obvious from the type hints + signature.

What to skip:

- Trivial functions whose name + signature say it all (e.g. an alias,
  a one-line setter).
- Functions with branching logic you can't fully trace — log for
  review.

### C — Helm `values.yaml` (helm-docs convention)

Targets: top-level keys in `charts/*/values.yaml` that lack a `#`
comment block above them.



Convention (project-specific, follow what the well-commented values
in the same chart do):

- Each documented value has 2-4 lines of `#` comments above it
  describing:
  - What the value controls
  - The default (if not obvious from the literal in the YAML)
  - Known caveats / side effects
- Use the `# -- ` prefix only if existing comments in the file use
  it; otherwise plain `# ` is fine.

What to write:

- Read the chart's templates to find every reference to this value
  (`.Values.<path>`).
- Describe what the templates do with the value.
- Note any non-obvious dependencies (`enabled` flags that gate other
  values, etc.).

What to skip:

- Values whose templates reference them in many places with varying
  semantics — if you can't summarise the role in 2-4 lines, log for
  human review rather than over-simplifying.
- Values that look like they're consumed by an external chart /
  operator rather than this chart's templates — log + skip.

### D — Dockerfile non-obvious instructions

Targets: `RUN`, `COPY`, `ADD`, `ENV`, `USER`, `EXPOSE`, `HEALTHCHECK`,
`ARG`, `WORKDIR` instructions in active Dockerfiles whose intent
isn't obvious from the line itself AND that lack a comment above
them.

What to write:

- A 1-3 line `# ...` block immediately above the instruction.
- State the *why*, not the *what* — the line itself shows the
  what. Examples:
  - `# Pinned to 0.43.0 because newer versions changed the
    `--config` flag behaviour we depend on.`
  - `# Layer ordering: package install runs before COPY .
    so that source-only changes don't bust the build cache.`
  - `# Dropped CAP_NET_ADMIN because the runtime can't modify
    iptables under PSS-restricted (#1260).`
- Match the comment style of well-commented instructions
  elsewhere in the same Dockerfile.

What to skip:

- Trivial / canonical instructions (`WORKDIR /app`, `EXPOSE 8000`
  on a Python web service) — comments there are noise, not
  signal.
- Instructions whose intent depends on cross-file context you
  can't trace from the Dockerfile alone — log for human review.

### E — Shell-script functions / non-obvious blocks

Targets: function definitions in shell scripts that lack a header
comment, plus non-obvious code blocks (complex `awk` invocations,
non-obvious `set` flag combinations, subtle `IFS` usage, etc.).

What to write:

- For functions: 2-4 line header comment summarising the
  function's purpose, key arguments (positional or named), exit-
  code semantics if the function uses `return`.
- For non-obvious blocks: 1-2 line inline comment explaining
  why the construction is the way it is (POSIX portability,
  avoiding a specific shellcheck warning, working around a
  specific tool's quirks).
- Match the comment style already used by well-commented
  functions in the same script.

What to skip:

- Trivial one-line wrappers / aliases.
- Functions whose body is genuinely opaque — log for review.

### F — GitHub Actions workflow steps

Targets: workflow steps whose role isn't obvious from the
`uses:` / `run:` reference alone AND that lack a comment.

What to write:

- A `# ...` block above the step explaining the *why* of:
  - `if:` guards (why this step is conditional)
  - non-obvious `permissions:` / `env:` blocks
  - choice of action version pin
  - dependency on prior-step outputs
- Match the comment style used elsewhere in the same workflow
  file.

What to skip:

- Trivial steps (checkout, setup-go) where the action name
  speaks for itself.
- Steps whose logic depends on workflow-wide context you can't
  trace from the file alone — log for review.

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path

### 1. Verify the source tree is in place

```sh
git -C <checkout> rev-parse --show-toplevel
git -C <checkout> status --porcelain
```

Stand down + log if missing or dirty.

### 2. Pin git identity

Invoke the `git-identity` skill (idempotent).

### 3. Capture the pre-author ref

```sh
PRE_AUTHOR_SHA=$(git -C <checkout> rev-parse HEAD)
```

### 4. Find candidates per category

Walk the source per the patterns above. For each category, build a
list of `(file, symbol, line)` candidates that lack documentation.

Bound the work per run: **at most 25 candidates total across all
categories** in a single invocation. Larger queues create reviewer
fatigue; better to refresh in small batches that get reviewed promptly
than land 200 new comments at once.

If the candidate pool is larger than 25, pick by priority:

1. **Cat A — Go exported symbols in operator/ and clients/ww/cmd/**
   (highest visibility — these are the public API surfaces of two
   subprojects).
2. **Cat C — Helm values.yaml entries** (high user impact — these
   are the chart's user-facing surface).
3. **Cat B — Python public functions in harness/ and backends/**
   (high internal visibility).
4. **Cat D — Dockerfile non-obvious instructions** (build / image
   posture — drift here breaks images silently).
5. **Cat F — Workflow steps** (CI surface — drift here breaks
   release / publish flows silently).
6. **Cat E — Shell-script functions** (often glue, lower per-item
   visibility but still matters for release tooling).
7. Everything else.

Log the not-yet-documented remainder to memory so callers know what
the queue looks like.

### 5. For each candidate, derive a grounded description

This is the load-bearing step. For each candidate:

- Read the symbol's body / value's template usage in full.
- Write the description from what the code demonstrably does.
- Read it back: every sentence should be defensible from a code-reading
  alone.
- If a sentence can't be defended that way, drop it. Better short and
  true than long and partially invented.

If you can't write a single defensible sentence about the symbol, mark
it as **resisted** and skip — it'll go to memory at the end.

### 6. Apply edits

Use Read + Edit tool calls to insert the new documentation in the
correct position (immediately above the declaration for Go / Python;
immediately above the value for Helm). Preserve indentation, blank
lines, and surrounding style.

### 7. Commit per language / target category

Group by what was authored:

```sh
# Go godoc
git -C <checkout> add <go-files-modified>
git -C <checkout> commit -m "code: add godoc comments to undocumented exports"

# Python docstrings
git -C <checkout> add <py-files-modified>
git -C <checkout> commit -m "code: add docstrings to public Python symbols"

# Helm values
git -C <checkout> add <values-yaml-files-modified>
git -C <checkout> commit -m "code: add helm-docs-style comments to chart values"

# Dockerfile comments
git -C <checkout> add <dockerfile-files-modified>
git -C <checkout> commit -m "code: comment non-obvious Dockerfile instructions"

# Shell-script comments
git -C <checkout> add <shell-files-modified>
git -C <checkout> commit -m "code: add header comments to shell-script functions"

# Workflow comments
git -C <checkout> add <workflow-files-modified>
git -C <checkout> commit -m "code: comment non-obvious workflow steps"
```

One commit per category so a human reviewer can revert any single
category's batch independently. The commit body should list each
symbol documented:

```
docs added:
- <package>.<symbol>  (file:line)
- ...
```

This gives a reviewer a one-glance audit of what was authored.

### 8. Log resisted candidates to memory

For symbols where you couldn't write a truthful description, append
to deferred-findings memory:

```markdown
## YYYY-MM-DD — code-document — resisted candidates

### Go exports

- `<package>.<Symbol>` (`<file>:<line>`) — <one-line reason>

### Python publics

- `<module>.<symbol>` (`<file>:<line>`) — <one-line reason>

### Helm values

- `<chart>:<value-path>` — <one-line reason>

### Dockerfile instructions

- `<path>:<line>` `<INSTRUCTION>` — <one-line reason>

### Shell-script functions

- `<path>:<func-name>` — <one-line reason>

### Workflow steps

- `<path>:<line>` `<step-name>` — <one-line reason>
```

The reasons feed back into manual review — common reasons include
"behaviour depends on cross-package state", "value consumed by an
external chart", "templating logic too complex to summarise in
helm-docs format".

### 9. Decide whether to push

```sh
git -C <checkout> log --oneline ${PRE_AUTHOR_SHA}..HEAD
```

- **No commits** → nothing to push. Report and exit.
- **One or more** → proceed to step 10.

### 10. Delegate the push to iris via `call-peer`

Same pattern as the other nova orchestrators. Send iris a self-
contained prompt listing the commit subjects and ask her to run
`git-push`.

### 11. Report

Return a structured summary:

- Pre / post SHAs
- Per-category: candidates considered, candidates documented,
  candidates resisted
- Total commits produced
- Iris's push outcome
- Pointer to deferred-findings memory for resisted candidates

## When to invoke

- **Scheduled** — a job in `.witwave/jobs/` fires this skill on a
  weekly-ish cadence (cron entry is a follow-up to this skill landing).
- **On demand** — the user or a sibling sends "document code", "add
  missing docstrings", "document helm values", "fill in code
  comments".
- **After a refactor that introduced new exported symbols** — they're
  the most likely to lack documentation.

## Out of scope for this skill

- **Cat A / Cat B documentation** in the docs-categories sense (agent
  identity, repo-root CLAUDE.md / AGENTS.md) — those are kira's
  domain.
- **Pattern-matched / training-data documentation** — explicit hard
  rule. If you can't ground the claim in the code, the symbol stays
  undocumented and goes to memory.
- **Removing or rewriting existing documentation** — that's
  `code-verify`'s territory (and even there, it's memory-log only).
  This skill only ADDS where docs are missing.
- **Bundling many categories into one commit** — separate commits per
  category preserve the per-category revert path.
- **Inferring beyond what the code supports** — restate what the body
  does; don't extrapolate intent.
