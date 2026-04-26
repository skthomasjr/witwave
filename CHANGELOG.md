# Changelog

All notable changes to this project are documented here. Format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The
project is pre-1.0 — minor version bumps may introduce user-visible
behaviour changes; they are called out explicitly in the **Changed**
section of each entry.

## [Unreleased]

## [0.8.0] — 2026-04-26

First release under the `witwave-ai/witwave` org (transferred from
`skthomasjr/witwave` on 2026-04-26). New container images and helm
charts are published to `ghcr.io/witwave-ai/...`; the old GHCR
namespace becomes a frozen archive going forward. The `ww` CLI's
update-check now points at the new Releases endpoint. Existing
clones can follow GitHub's HTTP redirect or run
`git remote set-url origin git@github.com:witwave-ai/witwave.git`.

Autonomous bug + risk cycle output (74 commits, 73 closed issues
across 10 cycles). The work was driven by the develop skill's
discover/refine/approve/implement loop applied to bugs and risks
only — gaps, features, and docs phases were skipped per the
session scope. Issues are grouped by component below; full
provenance lives in the GitHub issue history (#1599–#1672) and
linked commits.

### Security

- **mcp-prometheus: refuse to start on cloud-metadata bearer**
  (#1652). When `PROMETHEUS_BEARER_TOKEN` is set and
  `PROMETHEUS_URL` host resolves to a cloud-provider instance-
  metadata endpoint (169.254.169.254, fd00:ec2::254,
  metadata.google.internal, metadata.azure.com), startup raises —
  regardless of `PROMETHEUS_ALLOW_PLAINTEXT_BEARER`. The metadata
  IP is privileged regardless of transport.
- **mcp-prometheus: response body redacted from non-200 logs**
  (#1639). The WARN log emitted on upstream errors no longer
  includes the body snippet (kept the status code + byte count).
- **mcp-helm: validate `--repo` URL scheme on install / upgrade /
  diff** (#1638, #1664). Rejects file://, javascript://, and other
  non-http(s) schemes that previously slipped past
  `_reject_flag_like()`.
- **mcp-helm: port-aware allowlist matching for `repo_add`**
  (#1601). `MCP_HELM_REPO_URL_ALLOWLIST` entries now match
  hostname AND port; bare-host entries match URLs with no explicit
  port or the scheme's default; `host:port` entries require exact
  match. Backwards compatible for default-port URLs.
- **mcp tool Dockerfiles: HEALTHCHECK switched to exec-form**
  (#1651). Removes shell-form interpolation of `MCP_PORT` so a
  malformed env value can't escape into a shell context.
- **mcp shared: `mcp-prometheus` added to
  `DEFAULT_MCP_ALLOWED_COMMANDS`** (#1640). All three shipped MCP
  tool binaries are now accepted by default.
- **claude / shared: bearer-token decode hardened to
  `errors='strict'`** (#1617). Invalid UTF-8 returns a JSON-RPC
  400 instead of silently coalescing distinct token byte
  sequences onto the same caller-identity hash.
- **codex: Chromium `--no-sandbox` is now opt-in** via
  `CHROMIUM_SANDBOX_DISABLED` (#1619). Default-off so the host
  kernel sandbox runs where supported.
- **codex: prompt-size cap (`MAX_PROMPT_BYTES`, default 10 MiB)**
  rejecting oversized requests at the executor entry (#1620).
  Mirrored in echo at 1 MiB (#1650).
- **claude: `/mcp` body cap streams instead of trusting
  `Content-Length`** (#1609). New `MCP_MAX_BODY_BYTES` env
  (default 4 MiB); HTTP 413 on overflow with a clean JSON-RPC
  error body.
- **gemini: `MCP_CONFIG_PATH` realpath-prefix validation** (#1610).
  Refuses to load files outside `MCP_CONFIG_PATH_ALLOWED_PREFIX`
  (default `/home/agent/`).
- **shared: `prompt_env.substitutions_total` cardinality bounded**
  (#1668). Dropped the attacker-influenced `var` label; per-var
  detail still surfaces in WARN logs at miss/deny time.
- **ww: `ww config get` redacts secret-key values** (#1646).
  Mirrors the existing `ww config set` posture; secret keys print
  `<redacted>` to stdout.
- **ww: config Save uses atomic CreateTemp + Chmod + Rename**
  (#1607, #1654). Bearer tokens never observable at any mode but
  0o600.
- **ww: brew/git/gh shell-outs wrapped in
  `context.WithTimeout`** (#1616) and credential env vars stripped
  before exec via `sanitizeShellEnv`.
- **operator: ClusterRole RBAC split for Secrets read/write**
  resolved at the canonical source (#1613). Scripts/ added a
  drift-check helper.
- **operator: validating webhook emits warning on inline-credential
  use under restrictive RBAC posture** (#1623).
- **operator chart: monitoring CRDs RBAC gated on
  `metrics.enabled`** (#1659). No verbs requested when metrics are
  off.
- **operator chart: CRDs carry `helm.sh/resource-policy: keep`
  at the canonical source** (#1614, #1647). `helm uninstall` no
  longer cascades into deletion of WitwaveAgent / WitwavePrompt
  CRs.
- **agent chart: image digest pinning supported across harness,
  backends, and dashboard** (#1612, #1665). Mirrors the existing
  mcpTools digest support.
- **agent chart: ingress basic-auth empty-htpasswd validation**
  (#1626). Render fails fast when auth is enabled but no htpasswd
  / existingSecret is provided.

### Reliability

- **claude / codex / gemini: split `/health` (liveness, always 200
  once up) from `/health/ready` (readiness, 503 while
  initialising or boot-degraded)** (#1608 claude, #1672 codex +
  gemini). Operators using K8s readinessProbe should point at
  `/health/ready`; `/health` remains for livenessProbe. The
  agent chart's backend probes were updated to match (cycle-10
  follow-on); the harness's own readiness gate now probes backend
  `/health/ready` (cycle-9 follow-on).
- **claude: bounded sub-app lifespan shutdown wait** via
  `SUB_APP_SHUTDOWN_TIMEOUT_SEC` (default 10s) (#1618). Faulty
  sub-apps no longer stall pod termination.
- **claude: `SqliteTaskStore.close()` race fix** (#1649). New
  `_closing` sentinel prevents `_get_conn()` from spawning a fresh
  connection during teardown.
- **codex: `MAX_SESSIONS=0` no longer crashes the LRU eviction
  path** (#1629). Clamped at parse to `max(1, ...)`.
- **codex: MCP watcher normal-exit drops readiness** (#1630).
  Mirrors the claude readiness-gate pattern.
- **codex: `backend_task_last_success_timestamp_seconds` gated on
  `_budget_exceeded`** (#1662). Budget-exceeded responses no
  longer mask as success in the gauge.
- **codex: token-budget check uses `total_tokens` only** (#1600).
  Dropped the `output_tokens` fallback that under-counted prompt
  + cached-input tokens against the cap.
- **gemini: `max_tokens=0` no longer raises ZeroDivisionError**
  (#1602). Tightened guard to also require `> 0`.
- **gemini: API-key rotation atomicity** (#1621). Build-then-swap
  the new client; on failure preserve the previously-cached one
  so transient credential blips don't take down the backend.
- **gemini: session-history file unlink races eviction** (#1611).
  Backpressure branch waits up to
  `_EVICT_BACKPRESSURE_SAVE_WAIT_SEC` (default 30s) on the per-
  session done-event before attempting removal.
- **gemini: history-save force-split fallback** (#1622). When no
  safe AFC boundary exists in the trim window, cut at the
  earliest user-role entry so long mid-AFC tails can't keep the
  on-disk file oversized indefinitely.
- **harness: `A2A_SESSION_CONTEXT_CACHE_MAX` validated at module
  import** (#1648). Non-int or `< 1` fails fast with a CRITICAL
  log.
- **harness: rate-limited WARN on background-task shed path**
  (#1644). New `BACKGROUND_SHED_LOG_WINDOW_SEC` (default 10s).
  Sustained drops surface to operator logs without spam.
- **harness: shed-path `coro.close()` exception logged** (#1670).
  Replaces the silent `except Exception: pass` with a WARN that
  carries the source label and exception repr.
- **operator: `teardownDisabledAgent` cleans up NetworkPolicy
  and MCP tool resources** (#1635). Disabling a CR no longer
  leaves stale per-agent NetworkPolicy + mcp-`<tool>` Deployments
  to await OwnerRef GC.
- **operator: dashboard reconcile no longer panics when dashboard
  is disabled** (#1660). Nil-guard mirrors the ConfigMap and
  Service paths.
- **operator: WitwavePrompt status retry refreshes
  `reconciledGeneration` after re-Get** (#1636). Fixes a one-cycle
  observedGeneration lag under concurrent spec writes + 409
  conflicts.
- **operator: List operations on cleanup paths now paginate** via
  a shared `paginatedList` helper (`Limit=500` + `Continue`)
  (#1656). Bounds memory + apiserver load on namespaces with many
  agents.
- **operator: leader-election timing flags validated at startup**
  (#1657). `leaseDuration > renewDeadline > retryPeriod` is now
  enforced; misconfigs fail fast instead of silently deadlocking
  election.
- **operator: validating webhook enforces port upper bounds**
  (#1669). Rejects configs whose metrics-port reservation
  (port + 1000) would overflow 1..65535.
- **operator: webhook validating `failurePolicy=Ignore` by
  default** (#1624). Mutating webhook stays on Fail (sets
  reconciler-critical defaults). New
  `webhooks.validatingFailurePolicy` knob.
- **operator: WitwaveAgent CR teardown adds IsControlledBy guard
  on shared manifest CM** (#1599). Foreign-owned CMs are no
  longer deleted on the empty-membership branch.
- **agent chart: `podDisruptionBudget.enabled=true` by default**
  (#1625). Safe at `replicaCount=1` (PDB delays drain until
  reschedule); behaviour change for new installs only.
- **operator chart: same default flip on
  `podDisruptionBudget.enabled`** (#1628). Operator is a control-
  plane component; PDBs ship on by default.
- **operator chart: `probes.startup.failureThreshold` raised
  30 → 60** (#1627, #1642). 600s grace covers cold-start under
  leader-election + multi-replica.
- **agent chart: MCP tool pods get default resource
  requests/limits** (#1658). Removes the QoS=BestEffort eviction
  vulnerability.
- **dashboard: SSE reconnect floor raised 50ms → 500ms** (#1615);
  added `MIN_TRUSTED_SERVER_RETRY_MS=100` for clamping suspect
  server `retry:` hints; added `MAX_CONSECUTIVE_FAILURES=30`
  terminal-failed state with reset on `open()` (#1653).
- **dashboard: SSE per-stream rate cap (200 evts/sec)** with
  reactive `droppedEventCount` for observability (#1606); new
  `parseFailureCount` for malformed payload visibility (#1634).
- **dashboard: `useChat.loadHistory` clears `loadingHistory` on
  every error path** (#1633). No more stuck spinner.
- **dashboard: `ConversationsView` watchers stopped in
  `teardownStream`** (#1661). Fixes stale callbacks firing on
  closed streams during filter switches.
- **dashboard: `seenIds` Set bounded at 5000 entries with LRU-ish
  eviction** (#1605). Long-lived tabs no longer accumulate
  unbounded dedup state.
- **dashboard: DOMPurify link-rel hook regression test** (#1604).
  Pins `target=_blank rel="noopener noreferrer"` injection so a
  future `removeAllHooks()` call breaks CI rather than silently
  regressing the phishing mitigation.
- **ww: TUI modal lifecycle context plumbed end-to-end** (#1631).
  Quitting the app cancels in-flight create/delete/scaffold/send
  operations instead of letting them dangle.
- **ww: TUI log-tail goroutines drained on cycle/close** (#1663).
  No more apiserver-connection leak on rapid 'c' presses in
  aggregate mode.
- **ww: TUI send-modal stale-frame draw fix** (#1603). Active
  flag gates the QueueUpdateDraw.
- **ww: TUI preflight banner rendered before agent list per
  DESIGN.md KC-4** (#1632).
- **ww: helm-uninstall ctx-cancel observability** (#1655). 60s
  background waiter logs WARN if the in-flight goroutine doesn't
  settle.

### Changed (operator-visible behaviour)

- **K8s probes: agent + operator chart defaults updated** (see
  Reliability above). Operators with custom values that override
  `readinessProbe.path` to `/health` should review — the chart now
  ships `/health/ready` for backends post-#1672.
- **`acknowledgeInsecureInline=true` triggers an admission warning
  in the operator validating webhook** (#1623). Guides operators
  toward `existingSecret` references when the operator's
  `secretsWrite` permissions are restricted.
- **`MCP_HELM_REPO_URL_ALLOWLIST` entry format extended**
  (#1601). `host:port` syntax now supported; bare-host entries
  unchanged but only match default-port URLs.

### Operator chart RBAC drift checker

`scripts/check-rbac-drift.sh` added (#1613). Renders the chart
ClusterRole and diffs against `operator/config/rbac/role.yaml` to
catch future regressions of the `secretsWrite` split. Manual + CI
ready (`chmod +x` once after pulling).

### Removed

(none)

## [0.7.19] — 2026-04-25

### Added

- **`ww tui` create modal — Secret KEY fields are now combo boxes**
  with autocomplete suggesting the conventional env-var names for
  the selected backend type. Type "AUTH" → AWS_*, AZURE_*,
  GITHUB_AUTH_TOKEN-style entries surface; pick one with
  ↑↓+Enter or keep typing for a custom name (the suggestions are
  hints, never constraints). Powered by tview's
  `InputField.SetAutocompleteFunc`; substring match (case-
  insensitive) so credential names sharing common stems are easy
  to discover.

  Built-in catalog ships per backend type:

  - claude: `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`,
    `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
    `AWS_SESSION_TOKEN`, `AWS_REGION`
  - codex: `OPENAI_API_KEY`, `OPENAI_ORG`, `OPENAI_PROJECT`,
    `AZURE_OPENAI_API_KEY`, `AZURE_OPENAI_ENDPOINT`,
    `AZURE_OPENAI_API_VERSION`
  - gemini: `GEMINI_API_KEY`, `GOOGLE_API_KEY`,
    `GOOGLE_APPLICATION_CREDENTIALS`, `GOOGLE_CLOUD_PROJECT`
  - echo: nothing — popup stays hidden for the no-credentials
    hello-world case

  User-extensible via a new `[tui.expected_env_vars]` block in
  `~/.witwave/config.toml`:

      [tui.expected_env_vars]
      claude = ["MY_CUSTOM_VAR"]
      codex  = ["MY_OPENAI_PROXY_KEY"]

  Custom entries MERGE with the built-ins (dedup + sort) — adding
  your own can never accidentally drop the canonical suggestions.
  Removing built-ins isn't supported yet (block-list semantics
  would land if anyone asks).

  The autocomplete closure reads `cf.state.backend` on every
  keystroke, so changing the Backend dropdown updates suggestions
  live for any unfocused KEY field — no rebuild required.

## [0.7.18] — 2026-04-25

### Changed

- **`ww tui` create modal — secrets section is now dynamic per-pair**
  (Phase 2). Replaces the multi-line "Backend secrets" TextArea with
  a list of editable `KEY` / `VALUE` InputField pairs that grows
  and shrinks at runtime. Two new buttons in the form's button row:

  - `[+ Secret]` appends a fresh empty pair and lands focus on the
    new pair's KEY field so the user can type immediately.
  - `[− Secret]` pops the trailing pair (no-op when empty) and
    lands focus on the previous pair's VALUE — or on the
    Existing-Secret field when no pairs remain.

  Per-pair removal beyond the tail: clear the row's KEY field;
  empty-KEY pairs are silently skipped at submit so the row
  effectively disappears from the resulting Secret without needing
  a tear-down.

  Values prefixed with `$` still mean "lift from shell env" — same
  convention Phase 1 introduced. Empty value on a non-empty KEY is
  refused with a hint pointing at "clear the KEY to drop the pair."

  On-disk shape changes:
  `[tui.create_defaults]` now stores secrets as a TOML list of
  `"KEY=VALUE"` strings (`secrets = ["KEY1=value1", "KEY2=$VAR"]`)
  instead of the previous `secrets_block` multi-line string.
  Hand-editable, round-trips cleanly. Existing config files with
  the old `secrets_block` key are silently ignored on read; users
  get fresh state on next successful create. Pre-1.0; the migration
  surface is small.

  resolveTUISecrets walks the typed pairs slice instead of parsing
  a string block. Submit-time validation: empty KEY = drop, empty
  value on non-empty KEY = error, duplicate KEY across pairs =
  error, `$VAR` = env-lift with actionable error on unset.

## [0.7.17] — 2026-04-25

### Changed

- **`ww tui` create modal — secrets redesign (Phase 1)**. Dropped
  the Auth mode dropdown entirely. The four old modes (none /
  profile / from-env / existing-secret / set-inline) collapse into
  two more focused fields:

  - **Existing Secret name (optional)** — single-line. When set,
    references a pre-built K8s Secret as-is (verified, never
    modified). Wins over the secrets block.
  - **Backend secrets** — multi-line. One `KEY=VALUE` per line.
    Values prefixed with `$` are lifted from the shell environment
    at submit time; everything else is literal. Empty in both
    fields = no Secret minted (legitimate for echo).

  Examples in the placeholder cover both shapes:

      ANTHROPIC_API_KEY=sk-ant-literal-value
      GITHUB_TOKEN=$GITHUB_PAT     (leading $ → read from shell env)
      CUSTOM_HEADER=hello-world

  Old `[tui.create_defaults]` schema keys (`auth_mode`,
  `auth_value`) are no longer read. Users with a saved file from
  earlier versions get fresh fallback defaults on next launch and
  new state on next successful create. Pre-1.0; the migration
  surface is small. The `WW_TUI_DEFAULT_AUTH_MODE` and
  `WW_TUI_DEFAULT_AUTH_VALUE` env vars are removed; new
  `WW_TUI_DEFAULT_EXISTING_SECRET` env var pins the new field.
  `WW_TUI_DEFAULT_SECRETS_BLOCK` deliberately not added — multi-
  line values don't pair well with shell env vars; users wanting
  a pinned set of secrets edit `~/.witwave/config.toml` directly.

  Phase 2 — per-row UI with checkbox + env-var dropdown — remains
  on the roadmap as its own follow-up.

## [0.7.16] — 2026-04-25

### Changed

- **`ww tui` create modal — Auth value field is now a multi-line
  TextArea** (4 rows tall) instead of a single-line InputField.
  Set-inline mode naturally takes one `KEY=VALUE` per line — no
  more cramming five pairs into a comma-separated single line that
  scrolls horizontally. Parser accepts BOTH newlines (the natural
  shape with the multi-line field) and commas (back-compat with
  the earlier single-line shape, and convenient when pasting a
  dotenv-style snippet from another doc); blank lines and trailing
  separators are trimmed. Placeholder text refreshed to a multi-
  line example so the expected shape is visible on first open.
  Modal height bumped 30 → 34 to fit the taller field without
  form-internal scroll.

## [0.7.15] — 2026-04-25

### Added

- **`ww agent send --backend <name>`** — bypasses the harness's
  default routing and dispatches the prompt directly to the named
  backend sidecar via the A2A `metadata.backend_id` hint. Empty
  flag preserves the existing no-metadata-no-routing-hint behaviour
  so calls without it are bit-for-bit identical to before.
  `agent.SendOptions.BackendID` carries the field for programmatic
  callers.

- **`ww tui` send modal (`s` on the list)** — keybinding opens a
  scoped send-message modal for the selected agent. Form has a
  Target dropdown (`(agent — harness routes)` first, then each
  declared backend), a prompt input with placeholder hint, and a
  scrollable response view that fills the lower half of the modal.
  Long replies open at the top so the lede is visible without
  scrolling. In-flight Send is guarded by a mutex + sending flag —
  impatient Enter-mashing can't stack parallel goroutines against a
  hung apiserver proxy. Errors stay inline with an ERROR: marker
  so the user can adjust + retry without re-typing the prompt.
  Same arrow-key translation the create/delete modals use; ESC and
  Cancel both close cleanly.

  Footer on the list updated to include the new key:
  `↑/↓ · a add · d delete · s send · l logs · r refresh · ↵ details (soon) · q/esc quit`.

## [0.7.14] — 2026-04-25

### Added

- **`--auth-set` — fourth backend-credential mode** alongside the
  existing `--auth` (named profile), `--auth-from-env` (lift from
  shell), and `--auth-secret` (reference existing Secret). Stamps
  literal `KEY=VALUE` pairs onto the backend's credential Secret
  at command time. Wired on both `ww agent create` (form
  `<backend>:<KEY>=<VALUE>` since multi-backend per command) and
  `ww agent backend add` (form `<KEY>=<VALUE>` since the backend
  is already positional). Repeatable per `(backend, KEY)`;
  duplicate-KEY-within-same-backend is a hard error rather than
  silent last-write-wins. Mutually exclusive with the other three
  auth modes per backend. SECURITY: command-line values land in
  shell history + ps output; for production tokens prefer
  `--auth-secret` (pre-create with `kubectl create secret
  --from-env-file`) or `--auth-from-env` (lift from a sourced env
  file). The minted Secret's `created-by` annotation records key
  NAMES only — values never leak into metadata that
  `kubectl get secret -o yaml` would surface.
- **`ww tui` create-modal `set-inline` mode** — TUI parity for
  `--auth-set`. The Auth-mode dropdown grows a fifth option
  (`set-inline`); the Auth-value field accepts a comma-separated
  list of `KEY=VALUE` pairs, equivalent to one `--auth-set
  <backend>:KEY=VALUE` per pair on the CLI side. Same dup-key
  rejection + empty-value rejection as the CLI parser.

### Documentation

- README backend-credentials section grows from three paths to
  four; cheatsheet line gains a `--auth-set` example.
- WALKTHROUGH § 3 (Multi-model consensus for real) credentials
  table updated to four modes; § 5a (Backend Add) gains a
  `--auth-set` snippet showing the no-prefix form. § 9 (What's
  next) lists the `ww tui` surface for the first time and adds
  the planned `ww agent backend auth set/unset/list/show` subtree
  to the roadmap.
- README adds a new **Interactive TUI** section covering keymap +
  the layered defaults (env > saved > fallback) the create modal
  uses.

## [0.7.13] — 2026-04-24

### Added

- **`ww tui` · delete modal (`d` on the list)** — orange-bordered
  confirmation dialog naming the target agent + namespace, with
  three checkboxes mapping directly to the CLI flags:
  `Remove repo folder`, `Delete ww-managed credential Secret(s)`,
  and `Purge` (the convenience superset). Ticking `Purge`
  auto-ticks the two granular flags so the form reflects the
  actual blast radius. Submit invokes `agent.Delete`
  asynchronously; the list's poll loop renders the row
  disappearing within milliseconds (refreshNow ping) rather than
  on the next 2s tick.

### Changed

- **`ww tui` · long-form create modal** — the 5-field skeleton
  grows three more (Auth mode dropdown, Auth value input, GitOps
  repo input). When `--repo` is set, submit runs three sequential
  phases — `agent.Create` → `agent.Scaffold` → `agent.GitAdd` —
  each with its own banner state ("creating CR…", "scaffolding
  repo…", "attaching gitSync…") so the user sees progress.
  Failures short-circuit and the error strip names the failing
  phase plus a CLI command to retry the rest from. Modal height
  bumped 18 → 22 so the new fields fit without form-internal
  scroll.

- **`ww tui` · `l` opens logs, Enter reserved for details** —
  re-bound the list keymap so Enter is free for the upcoming
  per-agent details view (status / events / conversation log /
  send-prompt). `l` takes over the one-shot "tail logs" action;
  matches the k9s convention of lowercase verbs for one-shot
  actions and Enter for "drill in." Until the details view
  lands, Enter flashes a 3-second hint in the footer naming the
  agent + pointing at `l` and `ww agent status` from the CLI.

### Fixed

- **`ww tui` · ESC in the logs view returns to the list** instead
  of quitting the app. The app-level `SetInputCapture` was
  catching `KeyEscape` and calling `app.Stop()` before per-page
  handlers could run; ESC is now page-local (logs view → back to
  list, create / delete modal → cancel). Ctrl-C remains the
  app-level emergency bail; `q` still quits from anywhere.

## [0.7.12] — 2026-04-24

### Changed

- **`ww tui` · logs default to aggregate across all containers** —
  Enter on a row now opens in "all containers" mode rather than
  dropping straight into harness. Fans out one tail goroutine per
  real container (harness + each declared backend); each writes
  through an `io.Writer` decorator that prepends `[<container>] `
  so the interleaved body reads as
  `[harness] routing → echo` / `[echo] received prompt "ping"`.
  `c` still cycles, but the rotation now starts with the aggregate
  view and then proceeds through each individual container
  (`all → harness → echo → … → all`). One shared cancel context
  tears every tail down atomically on ESC / cycle — no goroutine
  leaks on rapid key-mashing. Error reporting stamps the failing
  container name in aggregate mode so you can tell which tail died
  when the others are healthy.

## [0.7.11] — 2026-04-24

### Added

- **`ww tui` · per-agent logs drill-down (Enter on a row)** —
  replaces the "drill down (soon)" stub with a live log-tailing
  view. Header shows the agent identity + current container +
  stream status; body autoscrolls new log lines from the
  apiserver's `/logs?follow=true` stream; footer lists the two
  navigation keys. `c` cycles through the agent's containers
  (harness + each declared backend); ESC cancels the stream and
  returns to the list with the previously-selected row still
  highlighted. Reuses `agent.Logs` under the hood so buffer size,
  SinceTime/TailLines, and multi-pod fan-in semantics match the
  CLI exactly. Writes from the log goroutine are copied out of
  bufio's reusable scanner buffer and queued onto tview's UI
  thread via QueueUpdateDraw — no interleaved rendering.
  MaxLines capped at 5000 for multi-hour tails; above that,
  `kubectl logs --since` is the right tool.

- **`ww agent backend add <agent> <name>[:<type>]`** — completes the
  backend-lifecycle trio (add / remove / rename) that was missing
  its third leg. Appends a backend to a running agent without the
  delete+recreate dance that used to lose gitSync wiring, team
  membership, and credentials.

  Reuses the `BackendAuthResolver` + profile catalog from
  `ww agent create --auth`. The three auth flags on `backend add`
  drop the `<backend>=<value>` prefix because the backend is already
  named positionally: `--auth oauth`, `--auth-from-env VAR[,VAR2]`,
  `--auth-secret <name>`. Missing credentials on an LLM backend
  surfaces a warning in the preflight banner rather than silently
  allowing a broken pod.

  Port picking: auto-assigns the first free slot in [8001, 8050];
  fills gaps in sparse layouts rather than appending at end. CRD
  `MaxItems: 50` cap caught with a nicer diagnostic than the
  apiserver's schema-validation blob.

  When the agent has exactly one gitSync wired, also scaffolds
  `.agents/<…>/.<name>/agent-card.md` (+ behavioural stub for LLM
  backends) to the repo and regenerates `.witwave/backend.yaml` to
  list the new backend. Routing stays put — new backend is present
  but idle until the user redistributes. Pass `--no-repo-folder`
  for a CR-only change. 11 fake-client tests cover the full matrix
  (happy path, duplicate-name, unknown type, invalid name, missing
  agent, dry-run non-mutation, auth profile mint, no-auth LLM
  warning, sparse-port gap fill, inline backend.yaml regeneration,
  50-backend cap).

### Changed

- Drop duplicate `Cloning <repo> …` log line in four repo-touching
  verbs (`backend add`, `backend remove`, `backend rename`,
  `agent delete`). `cloneOrInit()` already prints it; every caller
  was re-printing the same message, producing doubled log output.
  No information lost — `scope.repoDisplay` and `ref.Display`
  resolve to the same string.

### Fixed

- **claude backend: SyntaxError on import at `executor.py:2546`**
  — #1491's rebind fix placed `global ALLOWED_TOOLS` mid-function
  (inside `settings_watcher`), AFTER earlier reads of the name.
  Python requires any `global` to come before every reference in
  the same scope, so the module failed to parse at all and every
  claude sidecar built from the #1491 merge crash-looped at
  container start. Hoisted the declaration to the top of
  `settings_watcher` (just after the docstring) and dropped the
  duplicate at the rebind site. `py_compile` clean; new
  `claude:latest` image built from this release picks up the fix.

## [0.7.10] — 2026-04-24

### Added

- **`ww tui` · `a` to add an agent** — keybinding opens a centered
  modal form (Name / Namespace / Backend / Team / Create namespace if
  missing). Submit invokes `agent.Create()` asynchronously (Wait=false)
  so the TUI doesn't freeze; the list's poll loop shows the new row
  appearing and its Pending → Ready transition live. DNS-1123
  validation on name + team surfaces inline; apiserver errors
  (AlreadyExists, RBAC denied, etc.) populate an error strip above
  the form so the user can fix and resubmit without retyping.
  ESC/Cancel closes cleanly; form state is reset on every open so a
  cancelled submission doesn't leak values into the next one.
  Footer updated: `↑/↓ move · a add · r refresh · ↵ drill down (soon) · q/esc quit`.

  Design notes: `AssumeYes=true` because `k8s.Confirm` can't prompt
  over a tview canvas; banner chatter discarded via a local writer
  so `agent.Create` stdout doesn't leak under the surface. Auth
  (`--auth / --auth-from-env / --auth-secret`) deliberately NOT on
  the form — typing tokens into a TUI form is the wrong UX; users
  who need credentials stay on the CLI until a richer credential
  picker lands alongside the drill-down view.

## [0.7.9] — 2026-04-24

### Added

- **`ww tui` — live agent list** (replaces the welcome stub). Polls
  the apiserver every 2 seconds and renders WitwaveAgents across
  every namespace the caller can read, in a `k9s`-style table with
  `NAMESPACE`, `TEAM`, `NAME`, `PHASE`, `READY`, `BACKENDS`, `AGE`
  columns. Agents created / deleted / transitioning out-of-band (via
  another CLI session, kubectl, Helm) update in place without a
  keystroke. Header strip shows cluster + context + a rollup
  (`Ready N · Degraded N · Pending N`); footer shows keybindings.
  `r` forces an immediate refresh; selection survives each snapshot
  swap by `(namespace, name)` identity so the highlighted row
  doesn't jump when rows shift above it. Empty / no-cluster /
  fetch-error states all render inline — never a black screen.
  Drill-down (Enter on a row) is still a stub pointing at #1450 —
  per-agent logs/events/send panels land in a follow-up.
  `agent.ListAgents()` shipped alongside as the render-ready
  data path shared by CLI and TUI.

- **`ww agent create --auth / --auth-from-env / --auth-secret`** —
  three repeatable, per-backend credential flags that close the last
  "CLI-only" gap. Previously users had to `kubectl create secret` and
  `kubectl patch` the CR after `ww agent create`; now a Claude agent
  with an OAuth token or API key is a single invocation:
  `ww agent create iris --backend claude --auth claude=oauth` reads
  `$CLAUDE_CODE_OAUTH_TOKEN` from the shell, mints a ww-labelled
  Secret in the namespace, and stamps `spec.backends[].credentials.
  existingSecret` on the CR so the operator wires it into the backend
  container's envFrom at reconcile time. Profiles ship for claude
  (`api-key`, `oauth`); more per-backend profiles and Vertex/Bedrock
  shapes land as follow-ups. Pre-existing Secrets are referenced
  verbatim via `--auth-secret` (verified, never modified). Arbitrary
  env vars are liftable via `--auth-from-env` for custom setups not
  covered by a named profile.

- **`ww agent team {join, leave, list, show}`** — first-class CLI
  surface for the operator's existing team-membership plumbing. Team
  membership is the label `witwave.ai/team` on the WitwaveAgent CR;
  the operator reconciles one `witwave-manifest-<team>` ConfigMap per
  distinct value and mounts it at `/home/agent/manifest.json`. Verbs
  are a pure label patch — no CRD schema change, no pod restart.
  `join` is idempotent for same-team joins and explicit about
  cross-team moves (was → now); `leave` drops the label so the agent
  falls back to the namespace-wide manifest; `list` renders a tree of
  teams → members (with an `(ungrouped)` bucket); `show` prints an
  agent's team + sorted teammates.
- **`ww agent create --team <team>`** — stamp `witwave.ai/team=<team>`
  at creation time. Avoids the race where a follow-up `team join`
  briefly drops the agent into the namespace-wide manifest before
  landing the label. Low-key flag: no default, not promoted in
  onboarding docs.

### Changed

- **`ww agent list` now defaults to cluster-wide scope** (was: context
  namespace only). The `kubectl get pods -A` idiom most operators
  reach for anyway is now the default; narrow to a single namespace
  with `--namespace`. The NAMESPACE column is always shown regardless
  of scope so grep/sort pipelines work uniformly. `-A` is preserved
  for kubectl parity but is now functionally redundant. DESIGN.md
  NS-3 updated to codify the read-verb carve-out from NS-1's
  context-first resolution.
- DESIGN.md gains a **TEAM-1..5 rules block** codifying the
  team-membership contract: label-based (not a CRD field), no default
  team, per-namespace scope, operator-owned cleanup, `--team`
  deliberately not a prominent flag.
- README.md agent-cheatsheet backfilled to cover the full verb surface
  (git, backend, team, delete with `--purge`), plus the
  `default` → `witwave` namespace fallback correction (was stale
  since 0.7.8) and a `--create-namespace` mention that was missing
  from the prose.

## [0.7.8] — 2026-04-24

### Added

- **`ww agent create --create-namespace`** — mirrors
  `helm install --create-namespace`. Provisions the target namespace
  before the CR apply when it doesn't exist (labelled
  `app.kubernetes.io/managed-by: ww` so teardown tooling can tell
  ww-created namespaces from hand-authored ones); no-op otherwise.
  Lets a virgin cluster go zero-to-agent in a single invocation.
- **`ww agent delete --remove-repo-folder`** — clones the (single)
  wired gitSync repo, `git rm -r`s the agent's `.agents/<…>/`
  subtree, commits, pushes. Runs BEFORE the CR delete so a repo-side
  failure leaves cluster state intact and the user can retry. Hard-
  fails on multi-gitSync ambiguity; soft-skips when no gitSync is
  wired.
- **`ww agent delete --delete-git-secret`** — after the CR is gone,
  reaps every ww-managed credential Secret referenced by the CR's
  gitSyncs[]. User-created Secrets preserved via the managed-by
  label gate.
- **`ww agent delete --purge`** — convenience flag:
  `--remove-repo-folder --delete-git-secret`. For decommissioning an
  agent permanently in one command.
- **End-to-end walkthrough** (`clients/ww/WALKTHROUGH.md`) — zero-to-
  gitOps-wired-multi-backend-agent narrative with every verb
  exercised. Long-form flags, multi-line snippets, copy-pasteable
  throughout.
- **Smoke Phase 5 (gitOps round-trip)** in `scripts/smoke-ww-agent.sh`
  — gated on `WW_SMOKE_GITHUB_REPO`. Exercises scaffold → create
  multi-backend → git add → rename → remove `--remove-repo-folder` →
  delete `--purge` end-to-end against a real repo.
- Fake-client unit tests for every CR-mutation verb (`GitAdd`,
  `GitList`, `GitRemove`, `BackendRemove`, `BackendRename`, `Delete`
  including all its new cleanup modes).

### Changed

- **Default namespace is now `witwave`** (was `default`). When
  neither `--namespace` nor the kubeconfig context pins one, every
  `ww agent *` verb falls back to `witwave` via the new
  `agent.DefaultAgentNamespace` constant. Rationale: ww-managed
  resources benefit from a dedicated blast radius, and landing in
  `default` by accident invites cross-tenancy incidents on shared
  clusters. Breaks kubectl parity by design — see DESIGN.md NS-1.
- **Namespace-source log line** — the
  `Using namespace: <ns> (<source>)` banner now distinguishes
  `(from kubeconfig context)` from `(ww default)` so operators can
  tell an inherited namespace from a quiet fallback (DESIGN.md NS-2).
- DESIGN.md NS-1/NS-2 rewritten; new NS-5 codifies the
  `--create-namespace` contract.

### Unlocks

Virgin cluster + virgin repo to a fully-wired agent, then full
teardown, in flat flags:

```bash
ww agent create consensus \
    --namespace witwave \
    --create-namespace \
    --backend echo-1:echo \
    --backend echo-2:echo

ww agent scaffold consensus --repo <you>/my-witwave-config \
    --backend echo-1:echo \
    --backend echo-2:echo

ww agent git add consensus \
    --namespace witwave \
    --repo <you>/my-witwave-config \
    --auth-from-gh

# Later, when you're done:
ww agent delete consensus \
    --namespace witwave \
    --purge \
    --yes
```

## [0.7.7] — 2026-04-23

### Added

- **`ww agent backend remove <agent> <backend>`** — drops a backend
  from `spec.backends[]`, regenerates the inline `spec.config`
  backend.yaml when ww owns it (agents: list + routing no longer
  reference the removed entry), and refuses to remove the last
  backend (CRD minItems: 1). Pass `--remove-repo-folder` to also
  delete the corresponding `.agents/<…>/.<backend>/` folder from the
  gitSync repo and rewrite the repo's backend.yaml to drop the
  removed entry — one atomic commit, one push, same auth story as
  `ww agent scaffold`.
- **`ww agent backend rename <agent> <old> <new>`** — renames a
  backend atomically across the CR, harness + per-backend gitMappings,
  inline backend.yaml, AND the gitSync repo. The repo-side move uses
  git's native rename detection (`git mv`), regenerates the repo's
  `.witwave/backend.yaml` with the new name, and pushes in a single
  commit. `--no-repo-rename` skips the repo phase. Refuses on DNS-1123
  violations, same-name no-ops, and collisions with an existing
  backend of the target name.
- Repo-side cleanup + rename for both verbs is best-effort from the
  user's perspective: the CR update lands first, so a push failure
  prints a manual-recovery recipe instead of reverting cluster state.

### Unlocks

Lifecycle management on multi-backend agents without hand-editing
either the CR or the repo:

```bash
ww agent create consensus --backend claude --backend codex --backend echo
ww agent backend rename consensus echo smoke                   # echo → smoke
ww agent backend remove consensus smoke --remove-repo-folder   # drop it cleanly
```

## [0.7.6] — 2026-04-23

### Added

- **Multi-backend agents** — `ww agent create` and `ww agent scaffold`
  both gain a repeatable `--backend` flag. Two shapes accepted per
  entry:
  - `<type>` — name = type (e.g. `--backend claude`), the single-
    backend shortcut
  - `<name>:<type>` — explicit name + type pair (e.g.
    `--backend echo-1:echo --backend echo-2:echo`), required when two
    backends of the same type must coexist on one agent
  Each declared backend gets a distinct container name, distinct port
  (8001, 8002, …), and a distinct folder under `.agents/<agent>/.<name>/`
  in the gitOps repo. The generated `backend.yaml` enumerates every
  backend under `agents:` and routes every concern (a2a, heartbeat,
  jobs, tasks, triggers, continuations) to the **first** backend by
  default — operators redistribute routing by editing the file
  post-scaffold. This unlocks the multi-model consensus pattern the
  framework has always supported at the CRD level but that the CLI
  couldn't express until now:
  ```
  ww agent create consensus --backend claude --backend codex
  ```

### Backward compat

- `ww agent create hello` (no flags) and `ww agent scaffold hello
  --repo owner/repo` continue to produce a single default-echo
  backend — identical CR + repo output to 0.7.5.
- `--backend echo` (single bare type) still works for users who don't
  need multi-backend naming.
- `ww agent git add` needed no changes — its mapping generator already
  walked `spec.backends[]` by name, so multi-backend agents auto-derive
  per-backend gitMappings on attach.

## [0.7.5] — 2026-04-23

### Changed

- **`ww agent git add` gitSync default name now derives from the repo.**
  Previously the default `gitSyncs[].name` was the hardcoded label
  `witwave`, producing `/git/witwave/` on the pod regardless of which
  repo was wired. The new default sanitises the repo's basename to
  DNS-1123 (lowercase, `.`/`_`/`+` → `-`, trim hyphens), so
  `--repo skthomasjr/witwave-test` produces a gitSync named
  `witwave-test` and a filesystem at `/git/witwave-test/…` — matching
  what the user typed. Pass `--sync-name <name>` explicitly when
  wiring two repos with the same basename, or two branches of the
  same repo. The literal `witwave` label remains as a terminal
  fallback (exposed as `FallbackGitSyncName`) when sanitisation
  produces an empty string.
- **`ww agent git remove` auto-selects the sole gitSync.** When the
  agent has exactly one sync configured and `--sync-name` isn't
  passed, remove picks that one automatically. Zero → "nothing to
  remove" error. Multiple → refuse with the list of names so the
  caller can disambiguate. Eliminates the "what was that sync-name
  I used?" round-trip via `git list`.

## [0.7.4] — 2026-04-23

### Fixed

- **Operator git-sync wiring was broken for CR-based gitOps.** The
  mapping helper anchored rsync sources at `/git/<gs.Name>/<src>` but
  the init + sidecar containers never told git-sync to symlink at that
  path. git-sync v4 defaults `--link` to `HEAD`, so the actual
  symlink lived at `/git/HEAD/` and every mapping hit ENOENT →
  `git-map-init` crash-looped indefinitely. Fix: pass
  `--link=<gs.Name>` in both args builders so the init's and sidecar's
  symlink names match the path the helper constructs.

## [0.7.3] — 2026-04-23

### Added

- **`ww agent scaffold <name> --repo <…>`** — seeds a ww-conformant
  agent directory structure on a remote git repo using the user's
  existing system git credentials. No ww-managed credential store; auth
  resolution walks env tokens → `gh auth token` → `git credential
  fill` → ssh-agent in that order. Empty-repo bootstrap is handled (go-
  git's `PlainInit` path). Phase 1 of the gitOps wiring plan.
- **Scaffold seeds hourly `HEARTBEAT.md`** by default — gives every
  scaffolded agent a self-exercising proof-of-life signal from the
  moment it's wired up. Pass `--no-heartbeat` to opt out. Documented
  exception to DESIGN.md SUB-4; every other dormant subsystem remains
  absent.
- **Scaffold branch auto-detection** — `--branch` defaults to empty;
  scaffold queries the remote's HEAD symref (`git ls-remote --symref`)
  and uses the repo's real default branch. Covers `master`/`develop`/
  `trunk` without requiring the flag. Falls back to `main` on empty
  repos.
- **Scaffold merges on existing agents** — re-running `ww agent
  scaffold <existing>` no longer refuses. Missing files land; identical
  files are silent; drifted files are **preserved** (kubectl-apply-style
  merge). `--force` overwrites drifted files only — never touches
  user-added content outside the skeleton list.
- **`ww agent git {add,list,remove}`** — Phase 2 gitOps verbs.
  `git add` attaches a gitSync sidecar + harness/per-backend
  gitMappings to an existing WitwaveAgent CR. Three mutually-exclusive
  auth paths:
  `--auth-secret <name>` (reference pre-created K8s Secret, production),
  `--auth-from-gh` (mint from `gh auth token`, dev laptops),
  `--auth-from-env <VAR>` (mint from a named env var, CI/CD / .env).
  ww-minted Secrets carry `app.kubernetes.io/managed-by: ww` so
  `remove --delete-secret` can distinguish them from user-managed
  Secrets and refuse to clobber the latter.

### Fixed

- **Release pipeline built the operator with `DefaultImageTag=v<ver>`**
  (the raw `github.ref_name`) while `docker-metadata-action` published
  images with the `v` stripped. The operator then rendered pods that
  requested e.g. `git-sync:v0.7.2` and got GHCR 404 → ImagePullBackOff.
  `.github/workflows/release.yaml` now derives a stripped version
  (`${GITHUB_REF_NAME#v}`) and passes that as the `VERSION` build-arg.
  Non-tag runs (branch pushes) have no `v` prefix so the strip is a
  no-op.

## [0.7.2] — 2026-04-23

### Changed

- **Harness watchers go quiet when their directories are absent.**
  The five optional subsystems (jobs, tasks, triggers, continuations,
  webhooks) used to INFO-log `"<name> directory not found — retrying in
  10s"` every 10 seconds forever when content was missing. That's 30
  lines/minute of noise on a hello-world agent that legitimately uses
  none of those subsystems. Missing-directory logs now fire at DEBUG
  (visible under `-v`, silent by default). The *missing → present*
  transition — when content actually materialises, e.g. via a gitSync
  pull or a later ConfigMap mount — is preserved as a single INFO line
  so operators see the moment a subsystem comes online.

  The readiness gate (`/health/ready`) is unchanged: it continues to
  depend on backend routing config, not on optional-subsystem content.
  A dormant agent is now correctly both quiet AND schema-Ready.

### Added

- **DESIGN.md — SUB-1..4** codify the "file-presence-as-enablement"
  architectural property. An agent's enabled subsystems are expressed
  through content on disk under `.witwave/`, not through CRD fields.
  The absence of content is a normal, expected state that means "this
  agent intentionally does not use this subsystem." Future CLI verbs
  that enable a subsystem (e.g. `ww agent add-job <file>`) will do so
  by materialising content — no CRD bit-flipping, one source of truth.
- **`harness/test_run_awatch_loop_logging.py`** — 3 tests covering the
  dormant-directory contract: DEBUG-only on every miss, INFO exactly
  once on transition missing → present, and no transition-INFO when
  the directory exists on the first iteration (boot).

## [0.7.1] — 2026-04-23

### Fixed

- **`ww agent create` produced unhealthy pods.** The CR builder put both
  the harness and backend sidecar on port 8000. Pods share one network
  namespace, so one container's readiness probe hit the other's HTTP
  server and failed. Fixed by offsetting backends to 8001-8050 (one port
  per CRD-allowed backend slot). Codified as DESIGN.md PORT-1..4.
- **Harness never flipped Ready without an inline routing config.** The
  minimal CR from `ww agent create` didn't include `.witwave/backend.yaml`,
  so `/health/ready` stayed 503 with reason `no-backends-configured`
  (harness/main.py:524-534). The builder now stamps an inline config
  entry rendering a single-backend routing YAML that points the harness
  at the sidecar.

### Added

- **`ww operator install --if-missing`** — new flag that makes install
  idempotent. When the operator is already installed, logs a one-line
  no-op instead of refusing with `ErrPreflightRefused`. Useful for
  "ensure the operator is here" flows in scripts and CI.
- **`scripts/smoke-ww-agent.sh`** self-heals via `--if-missing` if the
  operator isn't installed when the smoke begins. Smoke is now truly
  turn-key: just `./scripts/smoke-ww-agent.sh` against any cluster.

### Design rules

DESIGN.md gains **PORT-1..4** codifying the agent-pod port contract:
harness on 8000 (hard-coded), backends on 8001-8050 (CRD cap fits the
range exactly), metrics on 9000 (dedicated listener), callers may
override via explicit CR fields but ww's builder enforces PORT-1..3
on generated CRs.

## [0.7.0] — 2026-04-23

### Added

- **`ww agent` subtree** — new command family for managing WitwaveAgent
  custom resources from the CLI. Closes the hello-world loop: a new user
  can go from zero to a working agent round-trip in two commands
  (`ww operator install && ww agent create hello && ww agent send hello "ping"`)
  with no API keys required.
  - `ww agent create <name>` — apply a minimum-viable WitwaveAgent CR.
    Defaults to the echo backend (no credentials required). Waits up to
    `--timeout` (default 2m) for the operator to report Ready; `--no-wait`
    opts out. `--backend echo|claude|codex|gemini` selects the backend;
    `--dry-run` renders the preflight banner and exits.
  - `ww agent list [-A]` — kubectl-style table with phase, ready count,
    backends, age. `-A` lists cluster-wide.
  - `ww agent status <name>` — curated describe: phase, ready replicas,
    backend summary, last-5 reconcile history.
  - `ww agent delete <name>` — deletes the CR; the operator cascades
    pod/Service cleanup via owner refs.
  - `ww agent send <name> "<prompt>"` — A2A `message/send` round-trip via
    the Kubernetes apiserver's built-in Service proxy. Works with any
    ClusterIP Service (no port-forward lifecycle, no external LoadBalancer
    required). `--raw` prints the full JSON-RPC envelope.
  - `ww agent logs <name>` — multi-pod container log streaming. Default
    container `harness`; `-c <name>` for backend/sidecar containers.
  - `ww agent events <name>` — scoped event snapshot: CR events + events
    on pods matching `app.kubernetes.io/name=<agent-name>`. `--warnings`,
    `--since`.
- **DESIGN.md — namespace rules NS-1..4** codify tenant-subtree namespace
  handling: default to the context's namespace with fallback to `default`
  (NS-1), always print the resolved namespace (NS-2), `-A` only on read
  verbs (NS-3), `create` exempt from the "explicit `-n` required for
  mutations" discipline for hello-world ergonomics (NS-4).

### Implementation notes

- Package layout mirrors `internal/operator/`: one file per concern
  (create, list, status, delete, send, logs, events) plus pure helpers
  (types, defaults, validate, build). Uses dynamic client +
  `unstructured.Unstructured` rather than a typed generated client —
  same pattern as `internal/operator/install.go`, avoids cross-module
  dependency on `operator/api/v1alpha1`.
- 30+ test assertions cover pure-function surface: DNS-1123 name
  validation + 50-char length cap, image-ref resolution (release / dev /
  empty versions, port-in-registry edge case), namespace precedence,
  CR builder invariants.

## [0.6.0] — 2026-04-23

### Added

- **`echo` backend** — a fourth backend image (`backends/echo/`) that ships
  as a zero-dependency stub A2A server. Returns a canned response quoting
  the caller's prompt; requires no API keys or external services. Serves
  two purposes: (1) the hello-world default for `ww agent create` so a new
  user can deploy a live agent with "access to a Kubernetes cluster and
  the CLI" as the only prerequisites, and (2) a reference implementation
  of the common A2A backend contract — demonstrates the dedicated-port
  metrics listener, the common `backend_*` metric baseline, and the
  contract-conformance pytest template for future backend types. See
  `backends/echo/README.md` for the in-scope vs intentional-non-scope list.
- **Release matrix** (`.github/workflows/release.yaml`) now publishes
  `ghcr.io/witwave-ai/images/echo` on every tag.
- **Chart integration** — `charts/witwave/values.yaml` defines
  proportionally small resource defaults for echo (~1/10th the envelope
  of an LLM-backed sidecar) and includes a commented `backends[]`
  example. `operator/config/samples/witwave_v1alpha1_witwaveagent.yaml`
  and the operator chart README now reference echo as a valid backend.
- **Events schema** (`docs/events/events.schema.json`) extended the
  `HookDecision.backend` and `AgentLifecycle.backend` enums to accept
  `echo` — prevents runtime validation rejection of echo-sourced events.
- **Dashboard** — `BackendType` now accepts `echo`; `BackendBubble.vue`
  and `tokens.css` carry a neutral slate palette entry for echo
  (`--witwave-brand-echo`), visually distinguishing it from the
  vendor-branded LLM backends.

## [0.5.8] — 2026-04-20

### Added

- **`ww tui` subcommand** (#1450 stub). Launches an interactive
  terminal UI: welcome banner, "what's coming" bullets,
  tracking-issue pointer, and live confirmation of the target
  kubeconfig context (cluster, context, namespace). Kubeconfig
  resolution is best-effort — if it fails the TUI still launches
  and shows a "No cluster configured" diagnostic in place of the
  context block. Exit with `q`, `esc`, or `ctrl-c`. No feature
  panels yet; the point of this release is to establish the
  framework. `--kubeconfig` / `--context` / `--namespace` flags
  mirror the `ww operator *` surface.

### Changed

- **TUI framework locked in as `rivo/tview`.** Shipped the stub
  on `charmbracelet/bubbletea` in one intermediate commit, then
  switched to tview before release on reflection that the
  long-term UX target is k9s-style (agent list → drill in →
  watch logs/events/sessions), and k9s runs on tview. Matching
  the framework means users who know k9s carry muscle memory
  across for free. The stub is small enough that the swap was
  nearly free; the same swap after real feature panels shipped
  would have been expensive.
- **Competitive landscape doc updated** to reflect fresh research
  on OpenClaw (20+ chat-platform integrations, macOS menu-bar
  companion with voice wake, TypeScript/Node.js implementation,
  MIT license, calendar-versioned release cadence) and to
  capture the "witwave is OpenClaw for teams with Kubernetes
  clusters" positioning frame in the Reference Products entry.
  Marked OpenClaw explicitly as "primary open-source
  competitor" in the section heading and restructured
  Relative-standing into explicit differentiator lists
  (5 in witwave's favor, 4 in OpenClaw's).

### Deps

- Added: `github.com/rivo/tview`, `github.com/gdamore/tcell/v2`
  (tview + the low-level terminal library it builds on).
- Net go.sum reduction — tview's transitive graph is lighter
  than the Charm ecosystem chain we temporarily added and
  then removed.

## [0.5.7] — 2026-04-20

Docs-only release. No code changes, no behaviour changes. Closes
out the doc audit + prettier/markdownlint conformance work that
surfaced during session wrap-up.

### Fixed

- Documentation audit across the 8 most-read markdown files (README,
  SECURITY, CHANGELOG, operator + chart READMEs, ww + dashboard
  READMEs, runbooks): 12 stale-version / broken-link / wrong-
  endpoint / docs-vs-code-drift issues corrected. Notably: README
  Helm-install chart version 0.5.2 → 0.5.6; `ww operator status`
  sample output switched to `<X.Y.Z>` placeholders so it no longer
  goes stale every release; `docs/runbooks.md` `/tool-audit`
  → `/trace?decision=deny` (the former endpoint doesn't exist);
  `harness/README.md` AGENT_NAME default corrected to `witwave`
  (code default, not the documented `local-agent`).
- Table column realignment on the `tools/kubernetes/README.md`
  Tools table after the `read_secret_value` row was added in
  commit 423ae13.

### Changed

- Applied `prettier --write` + `markdownlint` across all 8
  audit-pass files. Respects the repo's `.prettierrc.yaml`
  (proseWrap: always, printWidth: 120) and `.markdownlint.yaml`
  (MD013 line length, MD034 bare URLs, MD040 fenced-code language
  tags, MD051 link fragments). Largest diffs come from reflowing
  paragraphs that had been manually wrapped at ~80 chars; no
  content change beyond the six markdownlint fixes listed in the
  commit.
- New request filed: #1481 (enforce markdown linting in CI). The
  tools sat as standards-documents rather than gates, which is
  how the drift accumulated. Tracking issue captures the design
  trade-offs (pre-merge vs main-only scans, repo-wide cleanup vs
  incremental enforcement).

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
  ten images published under `ghcr.io/witwave-ai/images/*` on a tag
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

[Unreleased]: https://github.com/witwave-ai/witwave/compare/v0.5.8...HEAD
[0.5.8]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.8
[0.5.7]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.7
[0.5.6]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.6
[0.5.5]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.5
[0.5.4]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.4
[0.5.3]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.3
[0.5.2]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.2
[0.5.1]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.1
[0.5.0]: https://github.com/witwave-ai/witwave/releases/tag/v0.5.0
[0.4.4]: https://github.com/witwave-ai/witwave/releases/tag/v0.4.4
[0.4.3]: https://github.com/witwave-ai/witwave/releases/tag/v0.4.3
[0.4.2]: https://github.com/witwave-ai/witwave/releases/tag/v0.4.2
[0.4.1]: https://github.com/witwave-ai/witwave/releases/tag/v0.4.1
[0.4.0]: https://github.com/witwave-ai/witwave/releases/tag/v0.4.0
