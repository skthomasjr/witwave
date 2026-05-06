# Smoke tests

End-to-end smoke tests for the witwave autonomous agent platform, exercised against a running test deployment of the
`witwave` Helm chart with `values-test.yaml` (agents `bob` and `fred` in the `witwave` namespace).

## Running

The tests are markdown specs designed to be executed by an agent (Claude Code, Codex, or a human) that reads each file
in order and follows the instructions. There is no central test runner — each spec is self-contained.

```text
000-init.md      — build images, install chart, poll readiness
001…024          — individual smoke checks
900-cleanup.md   — uninstall the test release
```

Stop the run early if `000-init.md` fails (no point exercising agents that aren't ready).

### Check the spec's `enabled` flag _before_ running

**Before executing any spec, read its frontmatter and verify `enabled: true`.** A spec with `enabled: false` must be
skipped entirely — do not dispatch the test prompt, do not curl the endpoint, do not look in the conversation log.
Report it as `⊘ skipped` in the output table with the reason pulled from the spec's description or a nearby comment.

Why this matters: `enabled: false` usually signals that the underlying fixture (a disabled job, a removed backend, an
absent env var) is not present in the running deployment. Running the spec anyway produces a false-negative failure that
masks real issues. The check is cheap:

```bash
awk '/^---$/{c++; if (c==2) exit} c==1 && /^enabled:/{print}' tests/014-continuation-fires.md
# enabled: true   ← run it
# enabled: false  ← skip it, record as ⊘
```

Agents exercising the suite should default to **skip** when `enabled:` is missing or unparseable, and should log the
decision so the reviewer can tell skip-from-spec apart from skip-from-absent-fixture.

## Framework conventions

- **Numbering**: `000` is init; `9xx` is teardown; everything else is a leaf test. Sub-tests share a base number with
  letter suffixes (`003.a`, `003.b`).
- **Each spec must declare `description` and `enabled` in YAML frontmatter.**
- **Tests should be idempotent** — re-running after a partial failure must not require manual cleanup.
- **Code bugs are findings, not fixes.** Each spec ends with a "do not fix code bugs" note. Tooling/infra fixes are
  expected and welcome (and should be committed at cleanup time).

## Trigger auth contract

The harness rejects every trigger POST that lacks either a per-trigger HMAC secret or a Bearer token matching
`TRIGGERS_AUTH_TOKEN` (security-by-default since 2026-04-12). The test stack ships
`TRIGGERS_AUTH_TOKEN=smoke-test-token` in bob's environment via `charts/witwave/values-test.yaml`, plus webhook env vars
(`WEBHOOK_TEST_HOST`, `WEBHOOK_TEST_URL_FEATURE_SINK`, `WEBHOOK_TEST_URL_WEBHOOK_SINK`, `WEBHOOK_TEST_TOKEN`,
`WEBHOOK_TEST_BEARER`). Smoke tests use:

```
-H "Authorization: Bearer ${TRIGGERS_AUTH_TOKEN:-smoke-test-token}"
```

If you've overridden `TRIGGERS_AUTH_TOKEN`, the env var resolves; otherwise the literal default works.

## Required tabular output

After running the suite, **produce a markdown table summarising every test**. This is the canonical artefact the run
delivers — without it, results are scattered across individual spec outputs and hard to triage. Use exactly these
columns, in this order:

| Column     | Required | Notes                                                                                                                      |
| ---------- | -------- | -------------------------------------------------------------------------------------------------------------------------- |
| `Test`     | yes      | Numeric ID + suffix (e.g. `003.a`)                                                                                         |
| `Name`     | yes      | Short name from frontmatter or filename (e.g. `session-init`)                                                              |
| `Status`   | yes      | One of `pass`, `fail`, `skipped`, `deferred` (use the emoji equivalents `✅` / `❌` / `⊘` / `⏸` if rendering for a human) |
| `Evidence` | yes      | Concrete proof — log line excerpt, HTTP code, returned token. One sentence, ≤ 120 chars                                    |
| `Notes`    | optional | Freeform — why deferred, what blocked, follow-up issue numbers                                                             |

Example:

```markdown
| Test  | Name                          | Status | Evidence                                              | Notes    |
| ----- | ----------------------------- | ------ | ----------------------------------------------------- | -------- |
| 001   | heartbeat-fires               | ✅     | A2A round-trip returned `HEARTBEAT_OK`                |          |
| 002   | job-executes                  | ✅     | conv log: `Job: ping` → `JOB_OK`                      |          |
| 003.a | session-init                  | ✅     | A2A response: `SESSION_INIT_OK`                       |          |
| 008   | heartbeat-session-persistence | ⏸     | not run — needs `* * * * *` schedule swap + git push  | deferred |
| 020   | metrics-aggregation           | ✅     | 1724 `agent_*` series + 1087 `a2_*{backend=…}` series |          |
```

After the table, include:

- **Bugs surfaced** — anything that looked like a code bug, with reproduction steps. Do not fix; file an issue.
- **Tooling/infra fixes applied** — what was patched to keep the suite running, with commit SHA(s).
- **Deferred** — a one-line reason per skip/defer, ideally with the missing fixture path.

## Test inventory (current)

| Test  | Surface                            |
| ----- | ---------------------------------- |
| 000   | init: build, deploy, poll ready    |
| 001   | heartbeat fires                    |
| 002   | job executes                       |
| 003.a | session init                       |
| 003.b | session continuity                 |
| 004   | trigger fires                      |
| 005   | health endpoints                   |
| 006   | trigger discovery                  |
| 007.a | backend routing                    |
| 007.b | backend model                      |
| 008   | heartbeat session persistence      |
| 009   | trigger dedup                      |
| 010   | trigger payload                    |
| 011   | job session persistence            |
| 012   | task fires                         |
| 013   | task continuation                  |
| 014   | continuation fires                 |
| 015   | continuation delayed               |
| 016   | continuation chaining              |
| 017   | continuation on-error              |
| 018   | continuation trigger-when          |
| 019   | enabled-false (per-backend toggle) |
| 020   | metrics aggregation                |
| 021   | webhook delivery                   |
| 022   | webhook env-url interpolation      |
| 023   | webhook headers                    |
| 024   | webhook extract                    |
| 900   | cleanup                            |
