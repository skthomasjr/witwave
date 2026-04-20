# Changelog

All notable changes to this project are documented here. Format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The
project is pre-1.0 — minor version bumps may introduce user-visible
behaviour changes; they are called out explicitly in the **Changed**
section of each entry.

## [Unreleased]

## [0.5.6] — 2026-04-20

Follow-up to v0.5.5. Three real changes — the LLM-billing defensive
work, a cosign verify-recipe fix surfaced during v0.5.5 validation,
and the chart ↔ operator migration documentation.

### Added

- **A2A retry-policy guard** (#1457): new `A2A_RETRY_POLICY=fast-only|always|never`
  env (default `fast-only`) + `A2A_RETRY_FAST_ONLY_MS` threshold
  (default 5000ms). Under the default, 5xx responses that came back
  AFTER the threshold are refused instead of retried — the theory
  being that a slow 5xx almost always means the backend's LLM call
  ran to completion and only failed on the return path, so retrying
  would bill the prompt a second time.
  `harness_a2a_backend_slow_5xx_no_retry_total{backend,status}`
  counts every refused retry. Policy `always` restores legacy
  behaviour; `never` is the strictest no-double-bill posture.
- **Outer-timeout cancellation observability** (#1457): structured
  WARN on `asyncio.TimeoutError` with `session_id`, `backend`,
  `elapsed_seconds`, `prompt_bytes`, `trace_id` — the fields
  operators need to audit LLM billing for potential double-charges.
  New dedicated counter
  `harness_task_outer_timeout_cancel_total{backend}` distinct from
  the generic `harness_tasks_total{status="timeout"}`.
- **Chart ↔ operator migration documentation** (#1478): full
  seven-step procedure in `operator/README.md`, pointer in
  `charts/witwave/README.md`. Covers PVC preservation, CR
  authoring with `existingClaim`, verification, and the explicit
  "pick one" constraint.
- **Reduced-RBAC footgun callout** (#1461 Option A): explicit ⚠
  callout in the operator chart README naming the
  `rbac.secretsWrite: false` + inline credentials incompatibility
  that today produces a silent reconcile loop, with concrete
  `existingSecret` guidance and a forward-reference to #1461 for
  the apply-time surfacing work.

### Fixed

- **Cosign verify recipe in SECURITY.md**: images strip the leading
  `v` from release tags (docker/metadata-action@v5 default semver
  normalisation), so `v0.5.5` in the image path returned
  MANIFEST_UNKNOWN. Recipe now uses the bare `0.5.5` tag and
  includes an inline comment explaining the convention.
- **ConnectionError propagation** in `harness/backends/a2a.py`:
  added a narrow `except ConnectionError: raise` above the generic
  exception handler so our deliberate error surfaces (slow-5xx
  guard, response-size cap) propagate with their intended metric
  labels instead of being reclassified as `"unexpected error"`.

### Follow-ups tracked

- **#1479** Idempotency-Key header on A2A retries for backend-side
  dedupe (closes the remaining fast-5xx double-bill window; needs
  backend cooperation).
- **#1480** A2A cancellation verb (closes the outer-timeout window;
  blocked on upstream A2A spec work).
- **#1461** Options B (reconciler short-circuit) and C (admission
  webhook) remain deferred as the security-posture UX improvements
  they are, not bugs.

## [0.5.5] — 2026-04-20

Substantial follow-up to v0.5.4 — ten issues closed across security,
observability, operator scale-readiness, and docs. No user-visible
behaviour changes on the happy path; all additions are opt-in or
silent hardening.

### Added

- **Cosign keyless signing on every container image** (#1460). All
  ten images published under `ghcr.io/skthomasjr/images/*` on a tag
  release are now signed via Sigstore's OIDC flow — no long-lived
  signing key in the repo. Verification is opt-in; `docker pull` /
  `helm install` / `ww operator install` continue to work identically.
  See SECURITY.md § Verifying signed release artefacts for the
  `cosign verify` recipe.
- **Gitleaks pre-merge secrets scan** (#1462). New workflow
  `.github/workflows/ci-secrets.yml` runs on every PR + main push
  plus a weekly cron sweep. Allow-list lives at `.gitleaks.toml`;
  policy is zero tolerance for real secrets.
- **Four new Prometheus alerts** (#1465, #1467, #1469, plus #1466):
  `WitwavePVCFillWarning` + `WitwavePVCFillCritical` (70% / 90%
  kubelet_volume_stats thresholds), `WitwaveA2ALatencyHigh` (p99
  harness → backend latency), `WitwaveEventValidationErrors`
  (non-zero schema validation failure rate), and
  `WitwaveWebhookRetryBytesHalfFull` (early warning before
  retry-bytes shedding begins). All eleven alerts (five existing +
  six new across this release) now carry `runbook_url` annotations
  pointing at `docs/runbooks.md` (#1468).
- **Webhook retry-bytes in-flight gauges** (#1466).
  `harness_webhooks_retry_bytes_in_flight{subscription}`,
  `harness_webhooks_retry_bytes_in_flight_total`, and
  `harness_webhooks_retry_bytes_budget_bytes{scope}` — the gauge
  signal that was missing when the shed counter was the only
  observable retry-bytes signal.
- **Operator leader-election flag surface + metric** (#1475). New
  `--leader-election-lease-duration` / `--leader-election-renew-deadline` /
  `--leader-election-retry-period` flags exposed by the operator and
  plumbed through `charts/witwave-operator/values.yaml`. New metric
  `witwaveagent_leader_election_renew_failures_total` declared for
  the alert-key slot; wiring to the renewal-error hook is a
  follow-up.
- **CRD `MaxItems` / `MaxProperties` caps** (#1471) on
  `WitwaveAgent.spec.Backends`, `GitSyncs`, `Config`,
  `PodAnnotations`, `PodLabels`, and `WitwavePrompt.spec.AgentRefs` —
  apiserver-side rejection of pathological CRs before they hit
  etcd's 1MB object-size ceiling.
- **Credential-watch secondary indexer** (#1474) —
  `WitwaveAgentCredentialSecretRefsIndex` field indexer narrows a
  Secret rotation's enqueue set to exactly the agents that reference
  it. At 100+ agents + bulk rotation, eliminates the reconcile
  thundering herd the old full-List mapper produced.
- **`checksum/config` + `checksum/manifest` pod annotations** on the
  harness Deployment (#1476). `kubectl edit cm` / `helm upgrade`
  with ConfigMap-only changes now roll the pod, instead of silently
  no-op'ing from the pod's perspective on config files the
  backends_watcher doesn't hot-reload.
- **CHANGELOG.md** (#1472) at the repo root, format follows
  Keep a Changelog 1.1.0. Backfilled entries for v0.4.0 → v0.5.5.
- **Token + secret rotation procedures** in SECURITY.md (#1463,
  #1464). Covers `HOMEBREW_TAP_GITHUB_TOKEN` (release-to-tap PAT;
  90-day rotation cadence) and `SESSION_ID_SECRET` (MCP session-ID
  HMAC binding; two-secret grace-window rotation).
- **Event schema versioning policy** in docs/events/README.md
  (#1473). Documents additive-vs-breaking bump rules, compat
  windows across major-version transitions, and the subscriber
  contract.
- **`operator/MIGRATION.md`** (#1470) — CRD deprecation policy
  (2 minor-version served-version overlap), conversion-webhook
  architecture, manual `jq`-based fallback, test matrix contract.
  Sets expectations now so the first `v1alpha1` → `v1beta1`
  transition doesn't surprise anyone.
- **`docs/runbooks.md`** (#1468) — on-call playbook per alert.

### Fixed

- Gitleaks config schema (`.gitleaks.toml`) — original commit used
  `[[allowlist]]` (array of tables) syntax from a newer gitleaks
  release; gitleaks 8.24 bundled by `gitleaks-action@v2` wants a
  single `[allowlist]` map. Config refactored to match.

## [0.5.4] — 2026-04-20

### Fixed

- Operator startup log: "Failed to initialize metrics certificate
  watcher" now reads as a proper verb phrase rather than the
  grammar-broken "to initialize …" that made grep patterns and
  structured logging noisier. Duplicate `err` field in the same log
  call removed (closes #58ec91b in part).
- `charts/witwave-operator/Chart.yaml` `home:` URL retargeted from a
  non-existent `witwave-ai/witwave-operator` repo to the canonical
  `skthomasjr/witwave` source. Helm clients and artifacthub now
  render a working link.

### Changed

- `operator/README.md` and `charts/witwave/README.md` status blurbs
  updated to reflect shipped reality — the "first pass" / "work in
  progress" notices were written before the v0.4 chart releases and
  the operator's CRD + ww-CLI plumbing all landed.

## [0.5.3] — 2026-04-20

### Added

- `ww operator logs` — tails pods matching
  `app.kubernetes.io/name=witwave-operator` in the operator
  namespace. `--tail N` (default 100), `--since DUR`, `--no-follow`,
  `--pod NAME`. Scanner buffer bumped to 1 MiB so
  controller-runtime's long structured-log lines don't truncate.
- `ww operator events` — renders three event sources merged into one
  sorted table: events on `WitwaveAgent` CRs (cluster-wide), events
  on `WitwavePrompt` CRs, and events in the operator's own namespace
  (covers pod scheduling failures, image-pull errors, crash loops).
  `--watch` / `-w`, `--warnings`, `--since DUR` (default 1h).

## [0.5.2] — 2026-04-20

### Fixed

- `ww operator status` now reports the real chart version and
  appVersion — embedded chart's `Chart.yaml` is rewritten by a
  goreleaser pre-hook (`scripts/bump-embedded-chart-version.sh`)
  before `go:embed` bakes the binary, so the shipped ww reports the
  release tag instead of the canonical `0.1.0` placeholder on main.

## [0.5.1] — 2026-04-20

### Fixed

- `ww update` on Homebrew installs now runs `brew update` before
  `brew upgrade ww` and verifies via `brew list --versions ww` that
  the installed version actually changed. Previously, `HOMEBREW_NO_AUTO_UPDATE=1`
  or a "recent" brew cache could leave the user on the old version
  while `ww` printed a lying "Upgraded." line.

## [0.5.0] — 2026-04-20

### Added

- `ww operator install / upgrade / status / uninstall` — first-class
  Kubernetes management in the CLI. Operator chart is embedded via
  `go:embed`; no `helm` required on PATH. Singleton detection refuses
  installs when a release exists cluster-wide; `--adopt` takes over
  a cluster whose CRDs were installed manually. RBAC preflight via
  `SelfSubjectAccessReview` fails fast with a readable missing-verbs
  list. `upgrade` server-side-applies CRDs **before** `helm upgrade
  --skip-crds`, working around Helm's "crds/ is install-only"
  semantics so new CRD fields land before the Deployment rolls.
  `uninstall` preserves CRDs + CRs by default; `--delete-crds` +
  `--force` cascade-delete live CRs with a loud banner warning.
- Preflight confirmation banner with a local-vs-production cluster
  heuristic. Skips the prompt on `kind-*` / `minikube` /
  `docker-desktop` / `rancher-desktop` / `orbstack` / `k3d-*` /
  `colima` / localhost servers; always prompts on EKS / GKE / AKS
  ARNs. `--yes` / `WW_ASSUME_YES=true` / `--dry-run` overrides.
- `scripts/sync-embedded-chart.sh [--check]` — CI-guarded drift check
  so the embedded copy can't silently diverge from the canonical
  chart.

### Changed

- **Behaviour change:** when a backend's `auth_env` is configured but
  the referenced env var is empty, harness now raises `ConnectionError`
  at request time with a clear diagnostic. Previously sent
  unauthenticated requests silently, producing "backend instability"
  dashboards when the real cause was a token misconfig. Existing
  deployments with a latent token-env-var typo will start failing fast
  after upgrading; clear `auth_env` in `backend.yaml` if auth isn't
  required, otherwise set the referenced env var to a real token.

## [0.4.4] — 2026-04-20

### Fixed

- A2A response buffering now streams via `client.stream()` +
  `aiter_bytes()` with a configurable cap
  (`A2A_MAX_RESPONSE_BYTES`, default 256 MiB). Prevents harness OOM
  from a pathological backend streaming a multi-GB response.
- Harness proxy fetch endpoints (`/conversations`, `/trace`,
  `/tool-audit`) gained a matching
  `HARNESS_PROXY_MAX_RESPONSE_BYTES` (default 64 MiB) via a
  `_capped_get_json` streaming helper.
- `_extract_text` emits a structured WARN log surfacing the
  response's top-level keys when no A2A fallback shape matched, so
  SDK schema drift is diagnosable without enabling debug logs.
- Operator seeds `Replicas = minReplicas` on first-install when
  autoscaling is enabled — previously Kubernetes defaulted to 1
  during the window between Deployment create and HPA create,
  producing transient under-provisioning.

### Added

- `harness_consensus_backend_errors_total` carries a bounded
  `reason` label (`timeout|connection|backend_error|other`) so
  dashboards can separate deadline hits from network blips.
- Dashboard `Agent` TypeScript interface gained `model?: string` —
  closes a silent schema drift vs `harness/main.py:team_handler`.

## [0.4.3] — 2026-04-19

### Fixed

- `ww update` cache no longer pins users to the previously-cached
  `latest` tag for 24h after upgrading. When
  `cached_latest == current`, the check re-fetches instead of
  returning a stale "you're current" answer.

## [0.4.2] — 2026-04-19

### Fixed

- `dashboard-ci` workflow added — 112 dashboard unit tests now run
  on every PR instead of being silently skipped.
- Dashboard `TeamView` test isolation — module-level state in
  `useTeam` now reset between tests via an `__resetForTesting`
  export.
- `charts-ci` fix: `/tmp/charts/` is created before `helm template`
  redirects into it.

## [0.4.1] — 2026-04-19

### Added

- `ww update` subcommand with `--force` and `--check` flags; in-place
  self-upgrade via Homebrew or `go install`. Configurable via
  `update.mode = off | notify | prompt | auto` in `ww`'s config file.

### Fixed

- `ww` config writer now pre-creates the file with
  `O_CREATE|O_EXCL|0o600` to close a chmod race on first config write.
- `WW_PROFILE=<typo>` now emits a stderr warning listing known
  profiles instead of silently falling back to defaults.

## [0.4.0] — 2026-04-19

### Added

- Homebrew distribution via the
  [witwave-ai/homebrew-ww](https://github.com/witwave-ai/homebrew-ww)
  tap. `brew install witwave-ai/homebrew-ww/ww` is now the primary
  install path.
- Operator chart + controller shipped to GHCR at every tag. `ww` and
  chart version numbers track each release.
- Cosign signing on `ww` CLI binaries via OIDC (GitHub Actions).
  Container images remain unsigned for now (see #1460).

### Changed

- Brand rename from the prior experimental name to **witwave**. All
  Go module paths, Python imports, chart names, directory references
  (`.witwave/`), and environment variables (`WITWAVE_*`) migrated in
  one sweep on 2026-04-19 (commit b966b40).

[Unreleased]: https://github.com/skthomasjr/witwave/compare/v0.5.6...HEAD
[0.5.6]: https://github.com/skthomasjr/witwave/releases/tag/v0.5.6
[0.5.5]: https://github.com/skthomasjr/witwave/releases/tag/v0.5.5
[0.5.4]: https://github.com/skthomasjr/witwave/releases/tag/v0.5.4
[0.5.3]: https://github.com/skthomasjr/witwave/releases/tag/v0.5.3
[0.5.2]: https://github.com/skthomasjr/witwave/releases/tag/v0.5.2
[0.5.1]: https://github.com/skthomasjr/witwave/releases/tag/v0.5.1
[0.5.0]: https://github.com/skthomasjr/witwave/releases/tag/v0.5.0
[0.4.4]: https://github.com/skthomasjr/witwave/releases/tag/v0.4.4
[0.4.3]: https://github.com/skthomasjr/witwave/releases/tag/v0.4.3
[0.4.2]: https://github.com/skthomasjr/witwave/releases/tag/v0.4.2
[0.4.1]: https://github.com/skthomasjr/witwave/releases/tag/v0.4.1
[0.4.0]: https://github.com/skthomasjr/witwave/releases/tag/v0.4.0
