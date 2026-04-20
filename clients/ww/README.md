# ww — witwave CLI

`ww` is the command-line companion for the Witwave / witwave multi-container
agent platform. It talks to a harness over the shared REST + SSE event
surface: tail the live event stream, send A2A prompts, inspect scheduler
configuration (jobs / tasks / triggers / continuations / heartbeat), and
validate scheduler files — all without a browser.

> Early days. This is v0.1: the primary commands exist, output is stable
> enough to script against, and the wire formats are the same ones the
> dashboard already uses.

## Install

For now, from a checkout of `skthomasjr/autonomous-agent`:

```bash
go install github.com/skthomasjr/autonomous-agent/clients/ww@latest
```

When the Homebrew tap lands:

```bash
brew install skthomasjr/homebrew-ww/ww
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

| Command                   | Purpose                                                                                  |
| ------------------------- | ---------------------------------------------------------------------------------------- |
| `ww status`               | Fetch `/agents`, probe each member's `/health`, print a table.                           |
| `ww tail`                 | Stream SSE events from `/events/stream`. `--agent`, `--session`, `--types`, `--pretty`.  |
| `ww send <agent> [text]`  | POST an A2A `message/send` to the harness. `--prompt-file -` reads stdin.                |
| `ww jobs [list\|view]`    | Read the `/jobs` snapshot.                                                               |
| `ww tasks [list\|view]`   | Read the `/tasks` snapshot.                                                              |
| `ww heartbeat [view]`     | Read `/heartbeat`.                                                                       |
| `ww triggers [list\|view]`| Read `/triggers`.                                                                        |
| `ww continuations […]`    | Read `/continuations`.                                                                   |
| `ww validate <file>`      | POST a file to `/validate`. Kind inferred from path or passed via `--kind`.              |
| `ww version`              | Print the version, commit, and build date. `--short` prints just the semver.             |

### Streaming

`ww tail` reconnects automatically with exponential backoff (100 ms →
10 s, with ±25 % jitter) and sends `Last-Event-ID` on reconnect so the
harness's ring buffer can fill the gap. SIGINT closes the stream and
exits cleanly. JSON-lines output is the default — pipe to `jq` without
worrying about boundaries. `--pretty` flips to a human-friendly line
per event.

`ww tail --agent iris` bypasses the dashboard proxy and hits the
harness URL reported by the harness's `/agents` directory directly.
`ww tail --agent iris --session abc` switches to the backend-local
per-session drill-down stream at `/api/sessions/<id>/stream`.

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
      "parts": [{"kind": "text", "text": "<prompt>"}]
    }
  }
}
```

`--backend claude|codex|gemini` adds `metadata.backend_id`; harness
executors already honour that field. `--context` reuses an existing
`contextId` for multi-turn sessions.

## Config

Config lives at `$XDG_CONFIG_HOME/ww/config.toml`, falling back to
`~/.config/ww/config.toml`. TOML shape:

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

Ad-hoc run endpoints use `run_token` when set; otherwise `ww` falls
back to `token` and logs a warning to stderr. Set both when you have a
harness that distinguishes them.

## Output modes

- Default: colored, tabular human output when stdout is a TTY. Colors
  disabled automatically when stdout is piped or `NO_COLOR` is set.
- `--json`: pretty-printed JSON for snapshot commands, one JSON object
  per line for streams. Add `--compact` to collapse snapshot JSON to a
  single line.
- Errors always go to stderr. Exit codes:

  | code | meaning                                                    |
  | ---- | ---------------------------------------------------------- |
  | 0    | success                                                    |
  | 1    | logical error — 4xx, validation failed, target not found    |
  | 2    | transport error — network, timeout, auth, 5xx after retries |

## Verbose tracing

`-v` logs each request line + status to stderr. `-vv` additionally
dumps request and response bodies (truncated at 4 KiB per direction).

## Building from source

```bash
go build -ldflags "\
  -X 'github.com/skthomasjr/autonomous-agent/clients/ww/cmd.Version=0.1.0' \
  -X 'github.com/skthomasjr/autonomous-agent/clients/ww/cmd.Commit=$(git rev-parse --short HEAD)' \
  -X 'github.com/skthomasjr/autonomous-agent/clients/ww/cmd.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)' \
" -o bin/ww .
```

## Scope notes (v0.1)

- `ww status` hits the harness `/agents` endpoint. A dashboard-proxied
  `/api/team` endpoint also exists in Witwave deployments and returns the
  same information in a slightly different shape — switching to it is a
  follow-up once `ww` grows a dashboard-proxy mode.
- The SSE parser is intentionally minimal — it implements the subset
  the harness emits plus the `:` keepalive comment used to keep HTTP/2
  proxies awake. Field-name-only lines per the broader SSE spec are
  tolerated but not exercised.
- Goreleaser config ships darwin/linux amd64+arm64 builds and a
  Homebrew formula targeting `skthomasjr/homebrew-ww` — the tap repo
  does not exist yet, so `goreleaser release` will fail cleanly at the
  Homebrew publish step until it does.
