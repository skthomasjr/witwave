# ww — witwave CLI

`ww` is the command-line companion for the Witwave / witwave multi-container agent platform. It talks to a harness over
the shared REST + SSE event surface: tail the live event stream, send A2A prompts, inspect scheduler configuration (jobs
/ tasks / triggers / continuations / heartbeat), and validate scheduler files — all without a browser.

> Output is stable enough to script against, and the wire formats are the same ones the dashboard already uses.
> Versioned releases ship regularly — see `ww version` for the running binary and [CHANGELOG.md](../../CHANGELOG.md) for
> release history.

## Install

```bash
brew install witwave-ai/homebrew-ww/ww
```

The [witwave-ai/homebrew-ww](https://github.com/witwave-ai/homebrew-ww) tap is the primary distribution path. For a
non-Homebrew install from source:

```bash
go install github.com/skthomasjr/witwave/clients/ww@latest
```

## Quick start

```bash
# One-time config.
mkdir -p ~/.config/ww
cat > ~/.config/ww/config.toml <<'EOF'
[profile.default]
base_url  = "http://localhost:8000"
token     = "your-CONVERSATIONS_AUTH_TOKEN"
run_token = "your-ADHOC_RUN_AUTH_TOKEN"   # optional
EOF

# Who's up?
ww status

# Tail the harness event stream.
ww tail --pretty

# Send a prompt to an agent.
ww send iris "what does the team look like right now?"

# Inspect scheduler config.
ww jobs
ww tasks view daily-report
ww triggers
ww heartbeat view
ww continuations

# Validate a trigger file before committing it.
ww validate .agents/active/iris/.witwave/triggers/notify.md
```

## Commands

Every command supports `--help`. Summary:

| Command                    | Purpose                                                                                                                                                             |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ww status`                | Fetch `/agents`, probe each member's `/health`, print a table.                                                                                                      |
| `ww tail`                  | Stream SSE events from `/events/stream`. `--agent`, `--session`, `--types`, `--pretty`.                                                                             |
| `ww send <agent> [text]`   | POST an A2A `message/send` to the harness. `--prompt-file -` reads stdin.                                                                                           |
| `ww jobs [list\|view]`     | Read the `/jobs` snapshot.                                                                                                                                          |
| `ww tasks [list\|view]`    | Read the `/tasks` snapshot.                                                                                                                                         |
| `ww heartbeat [view]`      | Read `/heartbeat`.                                                                                                                                                  |
| `ww triggers [list\|view]` | Read `/triggers`.                                                                                                                                                   |
| `ww continuations […]`     | Read `/continuations`.                                                                                                                                              |
| `ww validate <file>`       | POST a file to `/validate`. Kind inferred from path or passed via `--kind`.                                                                                         |
| `ww version`               | Print the version, commit, and build date. `--short` prints just the semver.                                                                                        |
| `ww operator [cmd]`        | Install / upgrade / inspect / uninstall the witwave-operator Helm release on a Kubernetes cluster; plus `logs` and `events` for diagnostics. See below.             |
| `ww config [cmd]`          | Read, write, and inspect `ww` configuration values — `get`, `set`, `unset`, `list-keys`, `path`. See [Managing config from the CLI](#managing-config-from-the-cli). |
| `ww update`                | Check for and install a newer `ww` release. See [Staying up to date](#staying-up-to-date).                                                                          |

### Streaming

`ww tail` reconnects automatically with exponential backoff (100 ms → 10 s, with ±25 % jitter) and sends `Last-Event-ID`
on reconnect so the harness's ring buffer can fill the gap. SIGINT closes the stream and exits cleanly. JSON-lines
output is the default — pipe to `jq` without worrying about boundaries. `--pretty` flips to a human-friendly line per
event.

`ww tail --agent iris` bypasses the dashboard proxy and hits the harness URL reported by the harness's `/agents`
directory directly. `ww tail --agent iris --session abc` switches to the backend-local per-session drill-down stream at
`/api/sessions/<id>/stream`.

### Sending

`ww send` builds the same A2A envelope the dashboard uses:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "message/send",
  "params": {
    "message": {
      "messageId": "<random>",
      "contextId": "<random or --context>",
      "role": "user",
      "parts": [{ "kind": "text", "text": "<prompt>" }]
    }
  }
}
```

`--backend claude|codex|gemini|echo` adds `metadata.backend_id`; harness executors already honour that field.
`--context` reuses an existing `contextId` for multi-turn sessions.

## Operator management

`ww operator` manages the witwave-operator Helm release — the cluster- scoped CRD controller that reconciles
`WitwaveAgent` and `WitwavePrompt` resources. The operator chart is **embedded** into the `ww` binary via `go:embed`, so
you don't need Helm installed locally or any repo configured; `ww` ships with a known-good chart pinned to its own
release version.

```bash
ww operator install             # embedded chart → witwave-system namespace
ww operator status              # release, pods, CRDs, live CR counts
ww operator upgrade             # CRD server-side apply + helm upgrade
ww operator uninstall           # removes release; CRDs + CRs preserved by default
ww operator uninstall --delete-crds [--force]
ww operator logs                # tail operator pod logs
ww operator events              # Kubernetes events for operator + CRs
```

All six commands honour the ambient kubeconfig and current-context. `--kubeconfig` and `--context` are **global flags on
`ww` itself** (so they work on any subcommand); `--namespace` / `-n` is local to `ww operator` and defaults to
`witwave-system`. The three mutating commands (`install`, `upgrade`, `uninstall`) print a preflight banner showing the
target cluster and either prompt or auto-proceed based on a local-vs-production heuristic:

- **Local clusters skip the prompt** — context name matching `kind-*`, `minikube`, `docker-desktop`, `rancher-desktop`,
  `orbstack`, `k3d-*`, `colima`, or a server URL pointing at `localhost` / `127.0.0.1` / `kubernetes.docker.internal`.
- **Everything else prompts** — EKS/GKE/AKS ARNs, external IPs, unknown context names. Must type `y` to proceed.

Overrides: `--yes` / `-y` or `WW_ASSUME_YES=true` to skip the prompt unconditionally (scripts + CI); `--dry-run` to
print the plan and exit without touching the cluster.

> **`$KUBECONFIG` with multiple files (gotcha).** When `$KUBECONFIG` points at a colon-separated list
> (`KUBECONFIG=a.yaml:b.yaml`), client-go merges the files but `current-context` comes from the **first** file that sets
> one — not the last. If you set up a `dev.yaml` and expect it to win over an earlier `prod.yaml`, it won't. Use
> `--context <name>` explicitly, or put the intended file first in the list. `ww` inherits this behaviour from kubectl
> and helm; don't try to "fix" it by reordering merges.

### Singleton enforcement

The operator is a cluster-scoped singleton. `ww operator install` refuses when a release already exists cluster-wide.
Matrix of outcomes on install:

- **Clean cluster** → proceeds with the install.
- **CRDs present, no Helm release** → refuses unless you pass `--adopt`. Useful for clusters where someone applied the
  CRDs manually via `kubectl apply`; `--adopt` takes over management.
- **Helm release exists** → refuses, points at `ww operator upgrade`.

### Values passthrough

For changes to the operator's chart values (replicas, image overrides, HPA, affinity, etc.), the canonical path is Helm
— but `--set key=val` and `-f values.yaml` on `install`/`upgrade` are planned follow-ups. In the meantime users with
non-default values should pull the chart directly:

```bash
helm pull oci://ghcr.io/skthomasjr/charts/witwave-operator --version <tag>
helm upgrade --install witwave-operator ./witwave-operator \
  -n witwave-system --create-namespace \
  -f my-operator-values.yaml
```

### Upgrade flow

`ww operator upgrade` server-side-applies the embedded chart's CRDs **before** running `helm upgrade --skip-crds`. This
works around Helm's long-standing "crds/ is install-only, never updated" semantics so new CRD fields (new `status`
columns, added `MaxItems` markers, the eventual v1beta1 storage-version switch) land on the apiserver before the
operator pod rolls with code that expects them.

### Uninstall safety

Default uninstall preserves CRDs + CRs, so a mis-click cannot cascade-delete user data. Pass `--delete-crds` to remove
the CRDs too; when any live `WitwaveAgent` or `WitwavePrompt` CRs exist, `ww` refuses unless you also pass `--force`.
The preflight banner prints a loud `WARNING: N CRs will be deleted` line in that case.

### Status output

```text
$ ww operator status
Target cluster: docker-desktop  (context: docker-desktop)

Witwave Operator
  Namespace:      witwave-system
  Release:        witwave-operator (Helm, rev 2, deployed)
  Chart version:  <X.Y.Z>
  App version:    <X.Y.Z>
  ww version:     <X.Y.Z>  (match)

Pods
  witwave-operator-abc123  Running

CRDs
  witwaveagents.witwave.ai           v1alpha1
  witwaveprompts.witwave.ai           v1alpha1

Reconciles managed
  WitwaveAgent:   3
  WitwavePrompt:  1
```

The `ww version` line renders `(match)` / `(patch skew)` / `(minor skew)` / `(major skew — upgrade blocked)` so operator
and binary version mismatches are visible at a glance. Local `ww` builds (built outside the release path) render
`(local build — skew unknown)` instead of a phantom "skew" warning.

### Operator diagnostics — logs + events

```bash
ww operator logs                   # tail 100 lines + follow
ww operator logs --tail 500
ww operator logs --since 1h
ww operator logs --no-follow       # snapshot
ww operator logs --pod <name>      # specific pod

ww operator events                 # last 1h of operator + CR events
ww operator events --watch         # or -w; stream new events
ww operator events --warnings      # Warning type only
ww operator events --since 15m
```

Both commands auto-resolve the target cluster + namespace from the parent flags (`--kubeconfig`, `--context`,
`--namespace`) so you don't re-type.

**`ww operator logs`** tails every pod matching `app.kubernetes.io/name=witwave-operator` in the operator namespace.
Multi-pod output gets a `[pod-name]` prefix; single-pod output doesn't. Scanner buffer is bumped to 1 MiB so
controller-runtime's long structured-log lines (stack traces, big reconcile payloads) don't truncate.

**`ww operator events`** shows three sources merged:

1. Events on `WitwaveAgent` CRs (any namespace by default — CRs live wherever users deploy them).
2. Events on `WitwavePrompt` CRs (same scope).
3. Events in the operator's own namespace — Pod scheduling failures, image-pull errors, crash loops on the operator
   itself.

Defaults to the last 1 hour so first paint is bounded. `--warnings` filters to `type=Warning` which is the "what's going
wrong?" signal. `--watch` opens watch streams against each source and fans new events into the same table format as the
initial listing.

Scope note: `-n <ns>` on the parent narrows CR-event listing to one namespace. Without it, CR events are listed
cluster-wide (the sensible default for CRs). The operator-namespace listing always stays on `witwave-system` regardless.

## Agent management

`ww agent` creates, lists, inspects, and deletes `WitwaveAgent` custom resources. The witwave-operator (installed via
`ww operator install`) reconciles each CR into a running agent pod with harness + backend sidecars.

```bash
ww agent create hello          # deploy an agent running the echo backend (no API keys)
ww agent send hello "ping"     # round-trip A2A call via the apiserver Service proxy
ww agent logs hello            # tail the harness container (-c <backend> for a sidecar)
ww agent events hello          # CR + pod events scoped to this agent
ww agent list                  # list agents in the context's namespace
ww agent list -A               # list across every namespace you can read
ww agent status hello          # phase, backends, last reconcile history
ww agent delete hello          # operator cascades pod cleanup via owner refs
```

### GitOps scaffolding (repo-first workflow)

`ww agent scaffold` materialises a ww-conformant agent directory structure on a remote git
repo so a later `ww agent git add` can wire a deployed agent to pull from it. The scaffolder uses your
machine's git credentials — whatever `git push` against that remote already works, `ww agent scaffold`
works too.

```bash
# Scaffold into an empty repo (gets bootstrapped with an initial commit)
ww agent scaffold hello --repo skthomasjr/witwave-test

# With an optional group segment — lands in .agents/prod/hello/ instead of .agents/hello/
ww agent scaffold iris --repo github.com/org/agents --group prod

# Dry-run prints the plan and file list without touching disk or remote
ww agent scaffold hello --repo owner/repo --dry-run

# Retain the clone locally for inspection / iteration
ww agent scaffold hello --repo owner/repo --clone-to ./local-agents

# Re-scaffolding an existing agent is refused unless --force
ww agent scaffold hello --repo owner/repo --force
```

**Layout produced** (flat default, no group):

```
.agents/hello/
├── README.md              # short human-readable description + next-step hints
├── .witwave/
│   └── backend.yaml       # routing — single backend, ports 8001+ per PORT-1..4
└── .<backend>/
    ├── agent-card.md      # A2A identity card skeleton
    └── <CLAUDE|AGENTS|GEMINI>.md   # behavioural instructions (LLM backends only)
```

Dormant subsystems (`HEARTBEAT.md`, `jobs/`, `tasks/`, `triggers/`, `continuations/`, `webhooks/`) are
**not** pre-created — per DESIGN.md SUB-1..4 their absence is how an agent expresses "I don't use this
yet." Future `ww agent add-job`, `add-task`, etc. verbs will materialise them on demand.

**Auth** — three paths, tried in order:

1. `GITHUB_TOKEN` / `GH_TOKEN` / `GIT_TOKEN` env vars (for CI + scripting)
2. `gh auth token` (for gh-authenticated users — default on dev laptops)
3. `git credential fill` (for non-GitHub remotes or users without gh)

SSH URLs (`git@host:owner/repo`) use your ssh-agent. Credentials are never stored by ww — same posture
as `git push`, just through a ww-friendly CLI.

With no flags, `ww agent create <name>` deploys the **echo backend** — a zero-dependency stub that returns a canned
response quoting the caller's prompt (see [`backends/echo/`](../../backends/echo/README.md)). Pick a real LLM backend
with `--backend claude|codex|gemini`; the chosen backend's image is published at the same version as the `ww` binary.

Namespace handling follows DESIGN.md NS-1..4:

- No `-n` → the kubeconfig context's namespace (falls back to `default`). The command always prints the resolved
  namespace at the top of its output.
- `-A` is only valid on `list` — never on `create`, `status`, or `delete`.

Create waits up to `--timeout` (default `2m`) for the operator to report the agent Ready. Pass `--no-wait` to return as
soon as the CR is accepted (scripts + CI). All mutating commands (`create`, `delete`) honour `--yes` /
`WW_ASSUME_YES=true` and `--dry-run` the same way `ww operator install` does.

`ww agent send` uses the Kubernetes apiserver's built-in Service proxy so any `ClusterIP` Service is reachable without
local port-forwarding or an external LoadBalancer. This makes round-trip A2A calls from a laptop against a cluster-only
agent Just Work. Caveats: the apiserver proxy has payload size caps and isn't suited for streaming — use `ww agent
logs -f` for live observation, or the dedicated `ww send --base-url ...` path for long-running streams against an
externally-reachable harness URL.

`ww agent events` is a one-shot scoped variant of `ww operator events`: events on the WitwaveAgent CR plus events on
pods matching `app.kubernetes.io/name=<agent-name>`. No `--watch` mode — when you need live signal, `ww agent logs -f`
usually tells you more.

## Config

Config lives at `$XDG_CONFIG_HOME/ww/config.toml`, falling back to `~/.config/ww/config.toml`. TOML shape:

```toml
[profile.default]
base_url  = "http://localhost:8000"
token     = "..."
run_token = "..."
timeout   = "30s"

[profile.prod]
base_url  = "https://witwave.example.com"
token     = "..."
```

Precedence, high to low:

1. Command-line flag (`--base-url`, `--token`, `--run-token`, `--timeout`, `--profile`).
2. Environment variable (`WW_BASE_URL`, `WW_TOKEN`, `WW_RUN_TOKEN`, `WW_TIMEOUT`, `WW_PROFILE`).
3. Config file profile (selected by `--profile` / `WW_PROFILE`, default `default`).
4. Compiled-in default (`http://localhost:8000`, 30 s timeout).

### Config file discovery

The config file is resolved in this order (first match wins):

1. `--config <path>` CLI flag
2. `WW_CONFIG` env var
3. `$HOME/.witwave/config.toml` _(preferred default — brand-aligned dotfile dir)_
4. `$XDG_CONFIG_HOME/ww/config.toml`
5. Platform user-config dir (`~/.config/ww/config.toml` on Linux, `~/Library/Application Support/ww/config.toml` on
   macOS, `%AppData%\ww\config.toml` on Windows)

The CLI creates `$HOME/.witwave/config.toml` on the first `ww config set` when no existing file is found.

### Managing config from the CLI

Use `ww config` to read, write, and inspect config values without opening the file by hand:

```bash
ww config path                                         # where the file lives
ww config list-keys                                    # valid keys + value shapes
ww config set update.mode notify                       # persist a value
ww config set profile.default.base_url https://.../    # persist a URL
ww config get update.channel                           # read current value
ww config unset update.mode                            # remove a key
```

Every value is validated against the key's schema before being written (mode must be one of `off/notify/prompt/auto`,
channel must be `stable` or `beta`, duration strings must parse, base URLs must have a scheme). The file is created with
mode `0600` on first write because bearer tokens live plaintext inside.

Ad-hoc run endpoints use `run_token` when set; otherwise `ww` falls back to `token` and logs a warning to stderr. Set
both when you have a harness that distinguishes them.

## Staying up to date

`ww` checks once per day (cached on disk) whether a newer release is available and prints a one-line banner after the
command runs:

```text
↑ ww v0.5.0 is available (you're on v0.4.0). https://github.com/skthomasjr/witwave/releases/tag/v0.5.0
  To upgrade: brew upgrade ww
```

The upgrade instruction is tailored to how `ww` was installed — Homebrew taps get `brew upgrade ww`, `go install` users
get the matching `go install` command, standalone binaries get a download URL.

### Configuration

```toml
[update]
mode     = "notify"   # off | notify | prompt | auto
interval = "24h"      # cache TTL between GitHub API calls
channel  = "stable"   # stable | beta
```

| Mode     | Behavior                                                                             |
| -------- | ------------------------------------------------------------------------------------ |
| `off`    | No check, no network, no banner                                                      |
| `notify` | _(default)_ Print the banner after the command                                       |
| `prompt` | Print the banner, ask `Upgrade now? [Y/n]`, and run the matching installer on `Y`    |
| `auto`   | Print the banner and run the matching installer without asking — unattended upgrades |

`prompt` auto-downgrades to `notify` when stdin is not a TTY (scripts, pipelines, CI). `auto` is only recommended if
you've explicitly opted in to unattended upgrades.

Channels:

- `stable` — only surfaces non-prerelease tags. Users on a beta still get notified when a stable release ships, because
  per SemVer `v0.4.0 > v0.4.0-beta.N`.
- `beta` — includes prereleases (`v*-beta.N`, `v*-rc.N`). Use this to track the bleeding edge.

### Upgrade on demand

The config-driven banner is passive. To trigger an upgrade explicitly (without waiting for the next check cycle or
flipping `mode`), run:

```bash
ww update             # check + upgrade if newer
ww update --check     # check only, don't upgrade
ww update --force     # skip the check; always run the upgrade
```

`ww update` delegates to the installer matching the current binary's provenance (`brew upgrade ww` for Homebrew,
`go install ...@latest` for `go install` users, a download hint for standalone binaries). Works even when `mode = off` —
the subcommand is an explicit request, not a passive check.

### Environment overrides

All of these win over `config.toml`:

| Variable               | Effect                                           |
| ---------------------- | ------------------------------------------------ |
| `WW_UPDATE_MODE`       | Override `mode` (`off`/`notify`/`prompt`/`auto`) |
| `WW_UPDATE_CHANNEL`    | Override `channel` (`stable`/`beta`)             |
| `WW_UPDATE_INTERVAL`   | Override `interval` (duration string)            |
| `WW_NO_UPDATE_CHECK=1` | Force mode = `off` for this run                  |

The check is also force-disabled when any of `CI`, `GITHUB_ACTIONS`, `BUILDKITE`, `CIRCLECI`, or `GITLAB_CI` is truthy —
automated runners never get banners or prompts. A version-check failure (network down, API outage, JSON parse error) is
always silent and never interferes with the actual command.

## Output modes

- Default: colored, tabular human output when stdout is a TTY. Colors disabled automatically when stdout is piped or
  `NO_COLOR` is set.
- `--json`: pretty-printed JSON for snapshot commands, one JSON object per line for streams. Add `--compact` to collapse
  snapshot JSON to a single line.
- Errors always go to stderr. Exit codes:

  | code | meaning                                                     |
  | ---- | ----------------------------------------------------------- |
  | 0    | success                                                     |
  | 1    | logical error — 4xx, validation failed, target not found    |
  | 2    | transport error — network, timeout, auth, 5xx after retries |

## Verbose tracing

`-v` logs each request line + status to stderr. `-vv` additionally dumps request and response bodies (truncated at 4 KiB
per direction).

## Design rules

Design invariants for the CLI (kubeconfig handling, command taxonomy, flag
conventions, exit codes) live in [DESIGN.md](DESIGN.md). Read it before adding
a new command; cite rule numbers (`KC-3`, `TAX-1`, …) in PRs when a change
touches one.

## Building from source

```bash
go build -ldflags "\
  -X 'github.com/skthomasjr/witwave/clients/ww/cmd.Version=0.1.0' \
  -X 'github.com/skthomasjr/witwave/clients/ww/cmd.Commit=$(git rev-parse --short HEAD)' \
  -X 'github.com/skthomasjr/witwave/clients/ww/cmd.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)' \
" -o bin/ww .
```

## Implementation notes

- `ww status` hits the harness `/agents` endpoint. A dashboard-proxied `/api/team` endpoint also exists in Witwave
  deployments and returns the same information in a slightly different shape — switching to it is a follow-up once `ww`
  grows a dashboard-proxy mode.
- The SSE parser is intentionally minimal — it implements the subset the harness emits plus the `:` keepalive comment
  used to keep HTTP/2 proxies awake. Field-name-only lines per the broader SSE spec are tolerated but not exercised.
- Goreleaser config ships darwin/linux amd64+arm64 builds and a Homebrew formula targeting `witwave-ai/homebrew-ww`. The
  tap repo exists; `goreleaser release` additionally requires the `HOMEBREW_TAP_GITHUB_TOKEN` repo secret (a
  fine-grained PAT scoped to the tap with Contents: Read-and-Write) — without it, the formula push step fails but the
  binaries + GitHub Release still ship.
