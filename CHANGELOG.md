# Changelog

All notable changes to this project are documented here. Format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The
project is pre-1.0 — minor version bumps may introduce user-visible
behaviour changes; they are called out explicitly in the **Changed**
section of each entry.

## [Unreleased]

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

[Unreleased]: https://github.com/skthomasjr/witwave/compare/v0.5.4...HEAD
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
