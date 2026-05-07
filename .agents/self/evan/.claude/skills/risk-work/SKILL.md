---
name: risk-work
description:
  Find and fix security risks (CVEs in dependencies, secrets in source, insecure code patterns) across one or more
  sections at a caller-specified depth (1-10). Sibling skill to `bug-work` — same single-pass shape (scan → persist
  → validate → reason as set → fix-or-flag → commit per finding → delegate push + CI watch to iris with fix-forward
  semantics), different toolchain (`govulncheck`, `pip-audit`, `gitleaks`, `trivy`, `bandit`, `gosec`). State lives
  in commits and `project_evan_findings.md` memory only — no GitHub issues, no labels, no multi-session funnel.
  Trigger when the user says "work risks", "fix risks", "find risks", "scan for risks", "do risk work", or specifies
  depth/sections (e.g. "fix risks in operator depth 5").
version: 0.2.0
---

# risk-work

Single-pass **find AND fix** for security risks in the witwave-ai/witwave repo.

Sibling to `bug-work`. Same skeleton — atomic per-finding commits, iris-delegated push + CI watch, fix-forward then
revert as fallback, deferred-findings memory with `[pending]/[fixed: <SHA>]/[flagged: <reason>]` markers. Different
lens (security risks instead of logic defects), different toolchain (security analyzers instead of bug analyzers).

The lens: **"What's exposed?"** A correctness bug is the code doing the wrong thing; a risk is the code creating
exploitable surface even when it works correctly. Examples: a CVE'd dependency that's loaded but reachable from
public input, a secret committed to the repo, a path-traversal pattern in an HTTP handler, a `subprocess.call(...,
shell=True)` with user-controlled arguments.

## Inputs

Same shape as bug-work:

- **`depth`** — integer 1-10. Default `3`. Refuse cleanly if outside 1-10.
- **`sections`** — comma-separated section names or aliases. Default `all-deps` for risk-work (most
  dependency-CVE coverage; mirrors what `pip-audit` and `govulncheck` care about). Other aliases:
  `all-python`, `all-go`, `all-backends`, `all-tools`, `all-day-one`. Refuse cleanly if a section name
  doesn't match the bug-work section list.

Sections are inherited from bug-work (see `bug-work/SKILL.md` → "Sections"). The same 14 day-one sections plus the
3 v2-deferred ones apply. New alias for risk-work:

- **`all-deps`** → `harness`, `shared`, all four backends, all three tools (the Python sections that have
  `requirements.txt` files, where `pip-audit` finds direct CVEs).

## Depth scale

Depth maps the same way as bug-work: **how hard you hunt for risks.** The fix-bar is depth-independent.

| Depth   | What you read per candidate                                    | Concerns checked from the gauntlet                                | Candidate pool                                                    |
| ------- | -------------------------------------------------------------- | ------------------------------------------------------------------ | ----------------------------------------------------------------- |
| **1-2** | Just the analyzer hit                                          | None — trust the analyzer's CVE database / pattern match          | Critical/High CVEs only; obvious secret hits                      |
| **3-4** | ±20-line context window for code findings; `requirements.txt` for dep findings | #1 (intentional `# noqa`/`# nosec`), #2 (already-mitigated upstream) | Adds Medium CVEs; obvious-FP secret patterns dropped              |
| **5-6** | Full function body; full lockfile chain for transitive deps    | #1, #2, #3 (reachability from public input), #4 (sandbox/wrap)    | Adds reachability-driven prioritisation; transitive CVEs surface  |
| **7-8** | Full source file + caller chain                                | All 8                                                              | Pattern-matched insecure code (path traversal, command injection) |
| **9-10**| Full subsystem + RBAC manifests + CSP/auth-flow review         | All 8 + adversarial threat-model pass                              | Architectural risks (RBAC overreach, missing auth, weak crypto)   |

## The validation gauntlet (8 concerns, risk-flavoured)

Mirrors bug-work's gauntlet but with risk-specific framing. Drop the candidate when any concern catches:

1. **Intentional suppression nearby.** `# noqa: S###` (bandit) or `# nosec` (gosec) or a comment explaining why
   the pattern is safe in context — drop.
2. **Already mitigated upstream.** The harness or operator validates / sandboxes the input before the vulnerable
   path is reached — drop. (Example: `subprocess.run(..., shell=True)` is fine if `args` is hardcoded; risky if
   user-controlled.)
3. **Reachability from public input.** The vulnerable code path isn't reachable from any user-controlled data
   surface (HTTP handler, A2A message, MCP tool input). `govulncheck` reports this natively for Go; for Python you
   reason from the call graph manually at depth ≥5. Unreached vulnerabilities are lower-priority — at depth 1-4 they
   still flag; at depth ≥5 reachability gates the fix-bar's severity decision.
4. **Defensive wrapping at the boundary.** A `validate_url(host)` / `bleach.clean(html)` / `shlex.quote(arg)` call
   between the user input and the vulnerable function — drop the candidate (the boundary is doing its job).
5. **Documented design tradeoff.** A comment near the cited code explains why the pattern is safe in this codebase
   (e.g., "we permit shell=True here because args is constructed from a static allowlist"). Drop.
6. **Idempotent / no exposure.** A `gosec` finding that flags `os.Chmod(file, 0666)` is real if the file is
   externally accessible; not a risk if the file lives only in a per-pod tmp dir. Read the surrounding code.
7. **CVE no longer present in the pinned version.** `pip-audit` and `govulncheck` operate on lockfiles; if a
   patched version is already pinned and the analyzer is operating on a stale cache, drop. Re-run the analyzer
   with `--no-cache` to confirm.
8. **False-positive secret pattern.** `gitleaks` sometimes flags template strings, test fixtures, or example
   payloads in docs as secrets. Validate that the "secret" is actually a live credential (length, entropy, plausible
   key prefix) before flagging.

How rigorously you walk the gauntlet is depth-driven (see depth-scale table). When in doubt, drop.

## The fix-bar (4 rules, depth-independent)

ALL must hold to fix; otherwise the candidate goes to flag bin.

1. **Dep-bump or function-body contained.** Risk fixes that touch lockfiles (`requirements.txt`, `go.mod`,
   `package.json`) are auto-fixable IF the patched version is documented AND the bump is to a non-major release
   (semver-compatible). Code-change fixes follow bug-work's "function-body contained" rule — no public API change,
   no shared-state writes other callers depend on.
2. **Blast radius.**
   - Dep-bump fixes: read the immediate callers of the dep'd module. If any caller uses an API the bump's release
     notes flag as changed/removed, flag instead.
   - Code-change fixes: read callers + callees once. If the fix changes return-value semantics callers rely on,
     flag.
3. **Test coverage.** Tests exist for the affected file/path OR (for dep bumps) the using code is exercised by
   tests. **No tests covering the path → flag-only.** Same as bug-work.
4. **Severity meets depth threshold.** Risks have CVSS bands (Critical / High / Medium / Low) or analyzer-reported
   severity (gosec, bandit, semgrep grade their rules). At depth 1-2: only **Critical + High** auto-fix. At depth
   ≥5: **Medium** also. **Low** never auto-fixes — always flag for human triage.

A candidate that fails any rule → flag bin. All four pass → fix bin.

## Toolchain invocations (bug-class filters for security)

| Tool          | Invocation                                                                                                      |
| ------------- | --------------------------------------------------------------------------------------------------------------- |
| `govulncheck` | `cd <section> && govulncheck -mode=source -show=verbose ./...` — reports CVEs reachable from imports            |
| `gosec`       | `cd <section> && gosec -severity high -confidence medium ./...` — high-severity, medium-confidence Go security  |
| `pip-audit`   | `cd <checkout> && pip-audit --requirement <section>/requirements.txt --strict --vulnerability-service osv`      |
| `bandit`      | `bandit -r <section>/ --severity-level high --confidence-level medium --skip B101` (skip assert-used, test idiom) |
| `gitleaks`    | `gitleaks detect --source <section> --no-banner --no-color --report-format json --report-path /tmp/gitleaks.json` |
| `trivy`       | `trivy fs --scanners vuln,secret --severity CRITICAL,HIGH,MEDIUM <section>` — file-system CVE + secret scan     |

For a wide pass (`sections=all-deps`), prefer per-section invocations over a single repo-wide one — keeps each
section's findings attributable in the candidate list.

## Memory format

Reuses bug-work's exact format. The deferred-findings file is shared:
`/workspaces/witwave-self/memory/agents/evan/project_evan_findings.md`. Run sections distinguish by skill:

```markdown
## YYYY-MM-DD HH:MM UTC — risk-work run (depth=N, sections=...)

**Status: in-progress.** Pre-sweep SHA: `<sha>`.

### <section name>

- **<file>:<line>** `<analyzer rule>` <severity> — <one-line analyzer message>  [pending]
- ...
```

`<severity>` is one of `[CRITICAL]` / `[HIGH]` / `[MEDIUM]` / `[LOW]`. Marker reasons in `[flagged: <reason>]`
extend bug-work's vocabulary with risk-specific entries:

- `severity-below-depth-threshold` — Low or Medium (at depth <5) findings auto-flag.
- `dep-bump-breaks-caller` — fix-bar #2 caught a caller-incompat in the bump.
- `dep-bump-major-version` — non-semver-compatible bump, needs human approval.
- `unreached-from-public-input` — at depth ≥5, the gauntlet dropped this for reachability.
- `false-positive-secret` — gitleaks pattern matched a template/fixture/docs example.
- `no-patched-version-yet` — CVE has no patched release; watch upstream.

Plus all bug-work reasons (`function-body-not-contained`, `blast-radius-unclear`, `no-test-coverage`,
`ambiguous-analyzer-rule`, `fix-broke-local-tests`, `fix-needs-unfamiliar-api-confirmation`, `gauntlet-dropped`,
`fix-forward-failed`).

## Process (8 steps)

Read these from CLAUDE.md before starting:

- **`<checkout>`** — local working-tree path
- **`<branch>`** — default branch (`main`)

### 0. Verify the source tree

Same as bug-work Step 0. Pin git identity via `git-identity` skill.

### 0.5. Recover stuck commits

Same as bug-work Step 0.5. Delegate to iris if `git rev-list --count origin/main..HEAD` is non-zero. Capture
`PRE_SWEEP_SHA` after recovery.

### 1. Scan

For each section in the resolved input, run the risk-toolchain analyzers from the table above on the file types
that section contains. The pattern is:

- **Python sections** (`harness`, `shared`, `backends/*`, `tools/*`):
  - `pip-audit` if a `requirements.txt` exists in the section root
  - `bandit` over the section
  - `gitleaks` over the section
- **Go sections** (`operator`, `clients/ww`):
  - `govulncheck` over the module
  - `gosec` over the module
  - `gitleaks` over the section
- **Dockerfile-bearing sections**: `trivy fs` over the section (catches base-image CVEs and config issues)
- **Cross-cutting** (when `sections` includes the whole repo or `all-deps`): one `gitleaks detect` over the whole
  checkout to catch repo-wide secret commits

Concatenate hits into the **candidate list**. Each candidate carries: section, file, line, rule, message, severity,
raw analyzer output. Severity is mandatory for risk findings — it gates the fix-bar.

### 1.5. Persist the candidate list to memory IMMEDIATELY

Same as bug-work Step 1.5. `mkdir -p` the memory dir; write the run-section template (with severity tags); each
candidate gets a `[pending]` marker. Mandatory regardless of candidate count.

### 2. Validate per candidate (depth-gated)

Walk the 8-concern risk gauntlet at the depth's intensity (see depth-scale table). Drop candidates that any
concern catches.

### 3. Reason about candidates as a set

Look at the surviving set:

- **Common dep upgrade.** Multiple CVEs that all resolve via the same dep bump → one commit, not many.
- **Conflicts.** Two patches to the same lockfile in incompatible ways → pick one.
- **Cascading severity.** A High-severity dep CVE that's only reachable through code that ALSO has a Medium
  bandit finding — fix the Medium first, the High becomes unreachable, drop it.
- **Order by safety.** Smallest blast radius first; lockfile-only bumps before code changes.

### 4. Decide fix vs. flag (per candidate)

Apply the 4-rule risk fix-bar above. Bin to **fix** or **flag**.

### 5. Fix each fixable candidate

For each candidate in the fix bin, processed in step-3 order:

1. **Read the code in full** (function body + immediate callers + callees), or **read the dep change** (the
   patched version's CHANGELOG / release notes for breaking-change confirmation).
2. **Web-search the patched API if unfamiliar.** If the dep bump or the security pattern involves an API or
   framework behaviour you can't characterise, web-search before applying. Drop to flag with
   `fix-needs-unfamiliar-api-confirmation` if the search reveals more complexity than expected.
3. **Apply the fix.** Either:
   - **Dep bump:** edit the lockfile (`requirements.txt`, `go.mod`). For Python, also run `pip install -r
     requirements.txt --upgrade` to lock; for Go, run `go mod tidy`.
   - **Code change:** apply the minimal patch (escape input, add validation, replace `shell=True` with arg-list
     form, etc.).
4. **Run scoped tests locally:**
   - Go: `cd <checkout>/<section> && go test ./...`
   - Python: `cd <checkout> && pytest <section>/`

   **If tests pass** → continue.

   **If tests fail** → fix-forward, ONCE. Same shape as bug-work Step 5.4. Adjust the fix in-place, re-run tests.
   Pass on retry → commit. Still fails → revert + flag with `fix-forward-failed`.

5. **Re-run the analyzer that originally flagged.** The patched code or bumped dep should no longer fire the rule.
   If it still fires, the fix is incomplete — adjust before committing.
6. **Verify no adjacent regressions.** Re-read 20 lines around the change.
7. **Commit (one finding per commit):**

   ```sh
   git -C <checkout> add <files>
   git -C <checkout> commit -m "fix(<section>): <one-line risk description> [<SEVERITY>]

   <2-4 lines: what the risk was, why it's exploitable, what the fix
   does. Reference the analyzer rule (e.g. \"bandit B602 flagged at
   <file>:<line>: subprocess with shell=True and unvalidated input\").
   For dep bumps, reference the CVE id and the patched version.>
   "
   ```

8. **Mutate the marker in memory IMMEDIATELY.** `[pending]` → `[fixed: <commit-SHA>]`. Per-candidate.

### 6. Finalise the run section

Same as bug-work Step 6. Walk leftover `[pending]` markers, mutate to `[flagged: <reason>]`, mutate run-section
header `**Status: in-progress.**` → `**Status: complete.**`, append summary line:
`"M total candidates: F fixed, G flagged, D dropped at gauntlet."`

Order flagged candidates within each section by severity: Critical → High → Medium → Low. (Same shape as
bug-work's data-loss-first ordering, just with formal CVSS bands.)

### 7. Push + watch CI (via iris)

Identical to bug-work Step 7. Delegate push + CI watch to iris, including the failing-job log fetch on red. On any
red CI:

- **Fix-forward, ONCE.** Read iris's failure log excerpt. If in scope (small targeted change, clearly remediable
  from the log), write a fix-forward commit, run scoped local tests, ask iris to push + re-watch.
- **If fix-forward fails or out of scope** → batch-revert.

Same bound: exactly one fix-forward attempt per CI failure event.

### 8. Report

Return a structured summary:

- Pre / post SHAs
- Sections scanned, depth used
- Per-section: candidates considered, dropped at gauntlet, fixed (with SHAs + severities), flagged
- Severity totals (Critical / High / Medium / Low across the run)
- Iris's push outcome
- CI watch outcome (per workflow)
- Pointer to `project_evan_findings.md` for flag-only details
- If batch-reverted: failing workflow URL + revert commit SHA
- If fix-forwarded: fix-forward commit SHA + post-fix-forward CI conclusion

## Out of scope for this skill

Same as bug-work, plus:

- **Compliance audits.** SOC 2 / HIPAA / PCI checks are policy work, not bug-class fixing.
- **Penetration testing.** Active probing requires explicit authorization; out of autonomous scope.
- **Cryptographic primitive selection.** AES-GCM vs ChaCha20-Poly1305 is an architecture decision, not a fix evan
  should make autonomously. Flag for human review.
- **Major-version dep bumps.** Always flag. Breaking-change risk requires human review even if a patched version
  exists.
- **Semgrep.** Deferred — not yet in the agent image. Add in a future toolchain expansion if the bandit + gosec +
  govulncheck combo turns out to leave gaps.
