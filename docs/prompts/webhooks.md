# Webhooks

Webhooks fire outbound HTTP POST requests after a prompt run completes. Each webhook is defined as a `*.md` file in
the agent's `.nyx/webhooks/` directory. The webhook runner evaluates all filters after every prompt — from any
source (A2A, heartbeat, job, task, trigger, continuation) — and POSTs to the subscription's URL only if all pass.

Webhooks complement inbound triggers: triggers are internet → agent; webhooks are agent → internet.

## Example

```markdown
---
name: slack-job-summary
url: https://hooks.slack.com/services/{{env.SLACK_TEAM}}/{{env.SLACK_CHANNEL}}/{{env.SLACK_TOKEN}}
notify-when: on_success
notify-on-kind:
  - job:daily-report
content-type: application/json
timeout: 10s
retries: 3
headers:
  Authorization: Bearer {{env.MY_API_TOKEN}}
extract:
  error_count: Look at the output and return only the number of errors found. Return a single integer.
  summary: Summarize the output in one plain-text sentence, no markdown.
body: |
  {
    "text": "{{summary}}",
    "blocks": [
      {
        "type": "section",
        "text": {"type": "mrkdwn", "text": "{{summary}}"}
      },
      {
        "type": "context",
        "elements": [
          {"type": "mrkdwn", "text": "{{kind}} · {{duration_seconds}}s · {{error_count}} errors · {{agent}}"}
        ]
      }
    ]
  }
---

{{response_preview}}
```

## How It Works

1. A prompt completes (job, task, heartbeat, trigger, continuation, or A2A).
2. All webhook subscriptions are evaluated against the filters (`notify-when`, `notify-on-kind`, `notify-on-response`).
3. For each matching subscription:
   a. Built-in variables and `{{env.VAR}}` references are substituted into the markdown body.
   b. If `extract:` is defined, each extraction prompt is sent to the backend LLM with the rendered markdown body as context. The LLM's response becomes a named variable.
   c. All variables (built-in + extracted) are substituted into the `body:` template to produce the POST payload.
   d. The POST fires asynchronously with the configured headers, timeout, and retry policy.

## Built-in Variables

Available in the markdown body, `body:` template, header values, and `url:`.

| Variable               | Value                                                      |
| ---------------------- | ---------------------------------------------------------- |
| `{{agent}}`            | Agent name (e.g. `iris`)                                   |
| `{{kind}}`             | Prompt kind (e.g. `heartbeat`, `job:daily-report`, `a2a`) |
| `{{session_id}}`       | Session ID used for the prompt run                         |
| `{{source}}`           | Source name (job name, trigger endpoint, etc.)             |
| `{{model}}`            | Model used for the prompt run                              |
| `{{success}}`          | `True` or `False`                                          |
| `{{error}}`            | Error message, or empty string on success                  |
| `{{duration_seconds}}` | Prompt execution time in seconds                           |
| `{{response_preview}}` | First 2048 characters of the prompt response               |
| `{{timestamp}}`        | ISO 8601 UTC timestamp of delivery                         |
| `{{delivery_id}}`      | UUID unique to this delivery attempt                       |

`{{env.VAR}}` resolves the environment variable `VAR` at delivery time. Available anywhere variables are supported.

Extracted variables (defined under `extract:`) are also available in the `body:` template and header values.

## Frontmatter Fields

| Field                    | Required   | Description                                                                                          |
| ------------------------ | ---------- | ---------------------------------------------------------------------------------------------------- |
| `url`                    | One of two | Literal webhook destination URL. Supports `{{env.VAR}}` interpolation.                              |
| `url-env-var`            | One of two | Name of an env var holding the destination URL.                                                      |
| `name`                   | No         | Display name used in logs and metrics. Defaults to filename stem.                                    |
| `description`            | No         | Human-readable summary.                                                                              |
| `enabled`                | No         | `false` disables without deleting. Default: `true`.                                                  |
| `notify-when`            | No         | `always`, `on_success` (default), or `on_error`.                                                     |
| `notify-on-kind`         | No         | Glob pattern list matched against prompt kind. Omit to match all.                                    |
| `notify-on-response`     | No         | Glob pattern list matched against response text. Omit to match all.                                  |
| `content-type`           | No         | `Content-Type` header. Default: `application/json`.                                                  |
| `headers`                | No         | YAML map of additional HTTP headers. Values support `{{env.VAR}}` interpolation.                     |
| `timeout`                | No         | Request timeout. Format: `10s`, `30s`, `2m`. Default: `10s`.                                         |
| `retries`                | No         | Number of retry attempts on failure. Default: `0` (no retry).                                        |
| `extract`                | No         | YAML map of `variable_name: prompt`. Each prompt is sent to the LLM with the markdown body as context; the response becomes a variable available in `body:`. |
| `body`                   | No         | YAML literal block scalar (`body: \|`) used as the POST body template. Supports all variables including extracted ones. If omitted, the default JSON envelope is sent. |
| `signing-secret-env-var` | No         | Name of an env var holding an HMAC-SHA256 secret → `X-Hub-Signature-256` header (GitHub-compatible). |
| `model`                  | No         | Model override for LLM extraction calls.                                                             |
| `agent`                  | No         | Backend ID override for LLM extraction calls (e.g. `codex`); defaults to routing config.             |

## Default Envelope

When no `body:` is provided and no `extract:` is defined, this JSON envelope is sent:

```json
{
  "event": "agent.prompt.completed",
  "agent": "iris",
  "timestamp": "2026-04-10T04:00:00Z",
  "delivery_id": "<uuid>",
  "payload": {
    "kind": "job:daily-report",
    "session_id": "...",
    "success": true,
    "error": null,
    "duration_seconds": 12.3,
    "response_preview": "...",
    "model": "claude-opus-4-6"
  }
}
```

## HTTP Behavior

- POST only.
- Async fire-and-forget — does not block the caller.
- Timeout: configurable via `timeout:`, default `10s`.
- Retries: configurable via `retries:`, default `0`. Retries use exponential backoff.
- No redirect following.
- Body capped at 256 KiB.

## Kind Pattern Matching

`notify-on-kind` uses Python `fnmatch` — supports exact match (`job:daily-report`), prefix wildcard (`job:*`),
substring glob (`*report*`), and catch-all (`*` or omit the field).
