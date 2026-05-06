---
name: code-format
description:
  Tier 1 mechanical code-formatting pass. Runs language-specific formatters and linters on every active source file —
  ruff format + ruff check --fix for Python, gofmt + goimports for Go, prettier for JSON/YAML/TS/Vue/TOML, yamllint for
  YAML semantic warnings, helm lint for chart sanity. Excludes generated / vendored code and markdown (kira's domain).
  Auto-fixes commit per language. Trigger when the user says "format code", "lint code", "code format pass", or as a
  step inside `code-cleanup`.
version: 0.1.0
---

# code-format

Bring source files into compliance with each language's project-pinned
formatter / linter. Pure mechanical pass — only changes the tools' own
auto-fix modes produce. No prose changes, no comment authoring, no
behavioural changes.

The tools come from the project itself wherever possible:

- Python — `ruff` configured via `pyproject.toml` (or root `ruff.toml`
  if present)
- Go — `gofmt` and `goimports` (no project-side config; they encode
  community style directly)
- JSON / YAML / TS / Vue / TOML — `prettier` configured via
  `.prettierrc.yaml`, with `.prettierignore` honoured
- YAML semantic warnings — `yamllint` configured via `.yamllint` if
  present, otherwise default config
- Helm charts — `helm lint <chart-dir>` for each chart; sanity-only

Markdown is **never** touched by this skill — kira owns that surface
via `docs-validate`.

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path (Primary repository → Local
  checkout)

### 1. Verify the source tree is in place

```sh
git -C <checkout> rev-parse --show-toplevel
```

If the checkout is missing or empty, log to deferred-findings memory and
**stop** (per CLAUDE.md → Responsibilities → 1).

### 2. Confirm the toolchain is reachable

Each formatter must be runnable. The container's `claude` image needs
each tool present. If any are missing, log a `tooling-missing:<tool>`
entry to memory and skip THAT language's pass — but continue with the
other languages so a partial pass still produces value.

```sh
command -v ruff       || echo "MISSING: ruff"
command -v gofmt      || echo "MISSING: gofmt"
command -v goimports  || echo "MISSING: goimports"
command -v prettier   || command -v npx || echo "MISSING: prettier (npx fallback also missing)"
command -v yamllint   || echo "MISSING: yamllint"
command -v helm       || echo "MISSING: helm"
```

### 3. Enumerate target files per language

Use `git ls-files` to walk only tracked source. Exclude generated /
vendored content per CLAUDE.md → Code categories.

```sh
# Python
git -C <checkout> ls-files \
  'harness/**/*.py' 'backends/**/*.py' 'tools/**/*.py' \
  'shared/**/*.py' 'tests/**/*.py'

# Go (exclude generated + embedded)
git -C <checkout> ls-files '*.go' \
  | grep -v '/zz_generated' \
  | grep -v '^vendor/' \
  | grep -v '^clients/ww/dist/' \
  | grep -v '^clients/ww/internal/operator/embedded/'

# Prettier targets — JSON, YAML, TOML, TS, Vue (NO markdown)
git -C <checkout> ls-files \
  '*.json' '*.yaml' '*.yml' '*.toml' '*.ts' '*.tsx' '*.vue' \
  | grep -v -F -f <checkout>/.prettierignore 2>/dev/null \
  || true

# YAML for yamllint (same patterns, separate run)
git -C <checkout> ls-files '*.yaml' '*.yml'

# Helm charts — directories with Chart.yaml
git -C <checkout> ls-files 'charts/*/Chart.yaml' \
  | xargs -n1 dirname
```

### 4. Run each language's formatter

Run each tool against its file list. Capture: (a) files modified, (b)
remaining diagnostics that weren't auto-fixable.

#### Python — ruff

```sh
cd <checkout>
ruff format <file-list>          # mechanical reformat
ruff check --fix <file-list>     # safe lint auto-fixes only
ruff check <file-list>           # remaining diagnostics, no fix
```

#### Go — gofmt + goimports

```sh
cd <checkout>
gofmt -w <file-list>
goimports -w <file-list>
```

These are byte-deterministic — no remaining diagnostics to log.

#### Prettier (JSON / YAML / TS / Vue / TOML)

```sh
cd <checkout>
npx --yes prettier@<pinned-version> --write <file-list>
```

Use the version pinned in `.github/workflows/ci-docs.yml` if discoverable;
otherwise the latest 3.x. Capture files reformatted.

#### yamllint

```sh
cd <checkout>
yamllint --strict <file-list>
```

yamllint has no auto-fix — capture all warnings as deferred-findings
memory entries (rule + line + message).

#### helm lint

```sh
for chart in <chart-dirs>; do
  cd <checkout>
  helm lint "$chart"
done
```

Captures any chart-template or values issues. No auto-fix; failures go
to memory.

### 5. Commit per language

Group fixes by language so the commit log stays readable:

```sh
git -C <checkout> add <python-files-modified>
git -C <checkout> commit -m "code: ruff format + auto-fix Python"
```

Then for Go:

```sh
git -C <checkout> add <go-files-modified>
git -C <checkout> commit -m "code: gofmt + goimports Go"
```

Then for prettier-handled files:

```sh
git -C <checkout> add <prettier-files-modified>
git -C <checkout> commit -m "code: prettier format JSON/YAML/TS"
```

Skip any commit whose file list is empty.

### 6. Log non-auto-fixable diagnostics

For each language's leftover diagnostics, append a section to
`/workspaces/witwave-self/memory/agents/<your-name>/project_code_findings.md`:

```markdown
## YYYY-MM-DD — code-format diagnostics

### Python (ruff)

- `<path>:<line>` — `<rule-id>` <message>

### YAML (yamllint)

- `<path>:<line>` — `<rule-id>` <message>

### Helm (helm lint)

- `<chart>/<file>` — <message>
```

Append, don't replace — preserves the trail across runs.

### 7. Report

Return a structured summary to the caller:

- Per-language: files modified count + commit SHA (or "no changes")
- Per-language: remaining diagnostic count
- Total commits produced this run
- Pointer to deferred-findings memory if new entries landed

Do NOT delegate the push from this skill — `code-cleanup` (the
orchestrator) owns push delegation. When this skill is run standalone,
the caller can ask iris to push the batch directly.

## When to invoke

- As Tier 1 inside `code-cleanup` (the orchestrator).
- On demand: "format code", "lint code", "code format pass".
- Before a release, as a final cleanliness check.

## Out of scope for this skill

- **Markdown formatting** — kira's domain via `docs-validate`.
- **Generated / vendored code** — explicitly off-limits per CLAUDE.md →
  Code categories. Don't reformat what's regenerated.
- **Comment authoring or rewriting** — Tier 3 (`code-document`).
- **Comment-vs-code semantic verification** — Tier 2 (`code-verify`).
- **Pushing the batch** — orchestrator's job; or caller delegates to
  iris if running this skill standalone.
- **Behaviour-changing fixes** — even if a linter flags a real bug,
  this skill doesn't fix it. Log + move on.
