---
name: risk-work
description:
  Find and fix security risks (CVEs in dependencies, secrets in source, insecure code patterns, RBAC overreach,
  injection risks). Sibling skill to `bug-work` — same single-pass shape (scan → persist → validate → fix-or-flag →
  commit → log → iris-delegated push + CI watch with fix-forward), different toolchain (`govulncheck`, `pip-audit`,
  `gitleaks`, `trivy fs`, `bandit`, `gosec`, `semgrep` with security rulesets). **STATUS: stub. Toolchain not yet
  installed in the agent image.** Invoke only after the v2 toolchain commit lands. Trigger when the user says "work
  risks", "fix risks", "find risks", "scan for risks", "do risk work", or specifies depth/sections.
version: 0.1.0
---

# risk-work

**STATUS: stub. The toolchain this skill depends on is not yet installed in the backend image. Invoking this skill
today will refuse cleanly with a "toolchain not yet available" message and log the request to deferred-findings
memory.** This file captures the design intent so v2 implementation has a clean starting point.

Sibling to `bug-work`. Same shape — single-pass find AND fix, atomic per-finding commits, iris-delegated push + CI
watch, fix-forward then revert as fallback, deferred-findings memory with `[pending]/[fixed: <SHA>]/[flagged:
<reason>]` markers. Different lens (security risks instead of logic defects) and different toolchain.

The team contract that applies to bug-work applies here verbatim:

- **evan-commits / iris-pushes.** Iris owns all git and GitHub authority.
- **Autonomous by default.** No manual-approval mode. Five automated gates between an analyzer hit and a permanent
  commit on `main` (gauntlet, fix-bar, local tests, CI watch, fix-forward → revert fallback).
- **State lives in commits + memory.** No GitHub issues, no labels.
- **Atomic per-finding commits.** One risk fix per commit. Bisectable history.

What's different from bug-work:

- **Toolchain.** Replaces `go vet`/`staticcheck`/`errcheck`/`ineffassign`/`ruff B` with security-focused analyzers
  (see Toolchain section below).
- **Lens.** "What's exposed?" not "What's wrong?". A correctness bug is the code doing the wrong thing; a risk is
  the code creating exploitable surface even if it works correctly.
- **Fix-bar nuances.** Many risk fixes are **lockfile changes** (dependency bumps), not code changes inside a
  function body. The bug-work fix-bar's "function-body contained" rule is replaced with **"dep-bump or
  function-body contained."** Same blast-radius and test-coverage reasoning.
- **Reachability matters more.** A CVE in a dependency that's loaded but never reached from public input is a
  lower-priority finding. The validation gauntlet adds a reachability concern.
- **Severity is CVSS, not editorial.** Critical/High/Medium/Low maps to CVSS bands, not gut feel.

## Inputs (planned)

Same shape as bug-work:

- **`depth`** — integer 1-10. Default `3`. Same depth-scale semantics.
- **`sections`** — section names or aliases. Default `all-day-one`. **Plus a new alias:** `all-deps` →
  `harness`, `shared`, all backends, all tools (the Python sections that have requirements.txt).

## Sections + Toolchain (planned, pending image install)

The v2 toolchain that needs to land in `backends/{claude,codex,gemini}/Dockerfile` before this skill is invocable:

| Tool          | Purpose                                            | Install                                                                                |
| ------------- | -------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `govulncheck` | Go CVE-vs-import-graph reachability                | `go install golang.org/x/vuln/cmd/govulncheck@v1.X.Y`                                  |
| `pip-audit`   | Python dependency CVE scan                         | `pip install pip-audit==X.Y.Z`                                                         |
| `gitleaks`    | Secret-in-source detection                         | release binary download (alpine-musl)                                                  |
| `trivy`       | Filesystem CVE scan + image scan                   | release binary download                                                                |
| `bandit`      | Python security-class lints (B-prefix is bandit)   | `pip install bandit==X.Y.Z`                                                            |
| `gosec`       | Go security-class lints                            | `go install github.com/securego/gosec/v2/cmd/gosec@vX.Y.Z`                             |
| `semgrep`     | Pattern-matched security rules (curated rulesets)  | `pip install semgrep==X.Y.Z`                                                           |

Per-section toolchain mapping (mirrors bug-work's section table):

| Section                  | Files in tree                          | Risk-work toolchain                                                                        |
| ------------------------ | -------------------------------------- | ------------------------------------------------------------------------------------------ |
| `harness`                | Python + Dockerfile                    | `pip-audit` (requirements.txt) + `bandit` + `semgrep --config=p/python` + `trivy fs`      |
| `shared`                 | Python                                 | `bandit` + `semgrep --config=p/python`                                                    |
| `backends/<name>` (×4)   | Python + Dockerfile                    | `pip-audit` + `bandit` + `semgrep` + `trivy fs`                                           |
| `tools/<name>` (×3)      | Python + Dockerfile                    | `pip-audit` + `bandit` + `semgrep` + `trivy fs`                                           |
| `operator`               | Go + Dockerfile + RBAC                 | `govulncheck` + `gosec` + `semgrep --config=p/golang` + `trivy fs` + RBAC scope check     |
| `clients/ww`             | Go (+ Dockerfile if present)           | `govulncheck` + `gosec` + `semgrep --config=p/golang`                                      |
| `helpers/git-sync`       | Dockerfile                             | `trivy fs` (Dockerfile + base-image scan)                                                 |
| `scripts`                | Shell                                  | `gitleaks` + `semgrep --config=p/bash` (limited; mostly shellcheck-style)                 |
| `workflows`              | GitHub Actions YAML                    | `semgrep --config=p/github-actions` + `gitleaks` (action inputs)                          |
| (cross-cutting)          | the whole repo                         | `gitleaks detect` (all paths) + `trivy fs --scanners secret,vuln` (all paths)             |

## Validation gauntlet (planned)

Mirrors the bug-work gauntlet but with risk-specific concerns. Each candidate, depth-gated:

1. **Reachability from public input.** Is the vulnerable code path reached by user-controlled data? An unreached CVE
   is a lower-priority finding. (`govulncheck` does this natively for Go.)
2. **Already mitigated upstream.** Does the harness or operator already validate / sandbox the input before reaching
   the vulnerable path?
3. **Dependency in use vs. transitive-only.** Direct deps are fixable by bumping the immediate version; transitive
   deps may require waiting on the direct dep to update.
4. **CVSS severity threshold.** At depth 1-2, only Critical + High auto-fix. At depth ≥5, Medium also.
5. **Patched version exists.** No fix is possible if no patched version is published yet — flag with a "watch
   upstream" reason.
6. **False-positive secret patterns.** `gitleaks` sometimes flags template strings or test fixtures. Validate that
   the "secret" is actually live credentials before flagging.
7. **Documented design tradeoff** (same as bug-work concern #5): a comment near the cited code explains the choice.
8. **Stale finding** — same as bug-work concern #7-#8.

## Fix-bar (planned)

ALL must hold to fix; otherwise flag.

1. **Dep-bump or function-body contained.** Risk fixes that touch lockfiles (`requirements.txt`, `go.mod`,
   `package.json`) are auto-fixable IF the bump is to a documented patched version AND no breaking-change in the
   release notes. Code-change fixes follow bug-work's function-body rule.
2. **Blast radius.** A dep bump's blast radius = "every consumer of this dep." Read the immediate callers; if any
   call uses an API the bump's release notes say is changed, flag.
3. **Test coverage.** Same as bug-work — tests must exist on the affected path. Dep-bump fixes still need tests
   that exercise the using code.
4. **Patched-version availability.** No patched version → flag with `no-patched-version-yet`.
5. **Severity threshold met.** Below the depth-gated CVSS threshold → flag.

## Process (planned)

Mirrors bug-work's 8-step process verbatim — same step numbering, same persist-immediately durability guard, same
per-candidate marker mutation, same iris-delegated push + fix-forward semantics. The differences are entirely in
**Step 1 (Scan)** — different toolchain — and **Step 4 (Fix-bar)** — dep-bump rule and CVSS threshold. All other
steps are identical to `bug-work/SKILL.md`.

For the v2 implementation: copy `bug-work/SKILL.md`'s process section verbatim, swap the Step 1 toolchain table for
the risk-work toolchain table above, swap the fix-bar for the risk-work fix-bar above, and swap the gauntlet for
the risk-work gauntlet above. Everything else (memory format, push delegation, fix-forward, batch-revert) stays
identical.

## Out of scope for this skill

Same exclusions as bug-work, plus:

- **Compliance audits.** SOC 2 / HIPAA / PCI checks are policy work, not bug-class fixing.
- **Penetration testing.** Active probing is a different domain entirely (and ethically requires explicit
  authorization).
- **Cryptographic primitive selection.** When and whether to use AES-GCM vs ChaCha20-Poly1305 is an architecture
  decision, not a fix evan should make autonomously.

## Status

**v1 design captured here; v2 implementation pending.** Blocking work:

1. Add the toolchain to `backends/{claude,codex,gemini}/Dockerfile`. Pin every version. Estimate: ~50 lines of
   Dockerfile, ~150 MB of image bloat from gosec + trivy + semgrep + bandit + the rest.
2. Flesh out this SKILL.md with the full step-by-step procedure (currently the process section says "see
   bug-work").
3. First-run validation: `risk-work depth=2 sections=all-deps` to surface CVE drift, then iterate.

Memo the v2 work to the user's auto-memory under `project_evan_risk_work_v2.md` (already written there) so the
context survives across sessions.

Until that's done, this skill refuses cleanly when invoked, with a message pointing at the v2-pending memo.
