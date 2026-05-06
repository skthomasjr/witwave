# Smoke Tests

Two distinct smoke surfaces live in this repo:

1. **Deployed-agent conversation smoke** (this document, below). Tests that the three LLM backends produce the expected
   canonical responses when the test stack (`bob`, `fred`) has been deployed. Runs automatically on deploy via
   scheduler-fired jobs; operators inspect the conversation log at `GET /conversations/bob` / `GET /conversations/fred`
   after the fact.

2. **CLI hello-world smoke** (`scripts/smoke-ww-agent.sh`). Tests that the `ww agent` subtree works end-to-end against a
   real cluster: `create`, `list`, `status`, `send`, `logs`, `events`, `delete`, plus the validation paths (invalid
   names, duplicate create). Creates one throwaway agent backed by the echo image (no API keys required), asserts
   expected output, and deletes at the end. Run after cutting a new `ww` release against a cluster with the operator
   installed:

   ```bash
   ww operator install                # one-time per cluster
   ./scripts/smoke-ww-agent.sh        # ~60s against a local cluster
   ```

   Knobs via env: `WW_BIN`, `WW_SMOKE_NS`, `WW_SMOKE_AGENT`, `WW_SMOKE_KEEP`. See the script header for details.

This document describes the **deployed-agent conversation smoke** — what to look for in the conversation log after
deploying the test stack. All tests run automatically on deploy (run-once jobs) or on a recurring schedule. Check the
conversation at `GET /conversations/bob` and `GET /conversations/fred`.

The authoritative model for each call is in the conversation log, not the response text — models often misreport their
own version.

---

## Bob

### Jobs — run-once on deploy

These fire immediately when the stack comes up. Look for them near the top of the conversation log.

| Job                                   | Agent      | What to check                                                                                                                                                                |
| ------------------------------------- | ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `backend-check-claude`                | bob-claude | Response identifies itself as Claude; logged under `bob-claude`                                                                                                              |
| `backend-check-codex`                 | bob-codex  | Response identifies itself as Codex; logged under `bob-codex`                                                                                                                |
| `model-check-claude-default`          | bob-claude | `model` field in log matches the default Claude model in `backend.yaml`                                                                                                      |
| `model-check-claude-opus`             | bob-claude | `model` field = `claude-opus-4-7`                                                                                                                                            |
| `model-check-claude-sonnet`           | bob-claude | `model` field = `claude-sonnet-4-6`                                                                                                                                          |
| `model-check-claude-haiku`            | bob-claude | `model` field = `claude-haiku-4-5-20251001`                                                                                                                                  |
| `model-check-codex-default`           | bob-codex  | `model` field matches the default Codex model in `backend.yaml`                                                                                                              |
| `model-check-codex-gpt-5-1-codex-max` | bob-codex  | `model` field = `gpt-5.1-codex-max`                                                                                                                                          |
| `model-check-codex-gpt-5-1-codex`     | bob-codex  | `model` field = `gpt-5.1-codex`                                                                                                                                              |
| `animal-memory-claude`                | bob-claude | Response acknowledges hamsters; seeds session memory                                                                                                                         |
| `animal-memory-codex`                 | bob-codex  | Response acknowledges hamsters; seeds session memory                                                                                                                         |
| `budget-exceeded-claude`              | bob-claude | `system` entry in log: `Budget exceeded: N tokens used of 10 limit.` — verifies `max-tokens` enforcement surfaces a `BudgetExceededError` as a system conversation-log entry |
| `fanin-a`                             | default    | `FANIN_A_OK` response; first leg of fan-in continuation test                                                                                                                 |
| `fanin-b`                             | default    | `FANIN_B_OK` response; second leg of fan-in continuation test                                                                                                                |

### Jobs — run-once, continuation chains

These fire once and trigger continuation chains. Check that all steps appear in order in the log.

| Job                    | Continuation chain                                               | What to check                                                                                               |
| ---------------------- | ---------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `animal-memory-claude` | → `animal-memory-claude-turtles` → `animal-memory-claude-recall` | Final recall response names both hamsters and turtles; all three logged under `bob-claude` in session order |
| `animal-memory-codex`  | → `animal-memory-codex-turtles` → `animal-memory-codex-recall`   | Same as above but under `bob-codex`                                                                         |

### Jobs — recurring

| Job            | Schedule     | What to check                                                                  |
| -------------- | ------------ | ------------------------------------------------------------------------------ |
| `ping`         | every 5 min  | `JOB_OK` response; `continuation-ping` fires immediately after in same session |
| `ping-claude`  | every 30 min | `JOB_OK` under `bob-claude`                                                    |
| `ping-codex`   | every 30 min | `JOB_OK` under `bob-codex`                                                     |
| `ping-default` | every 10 min | `JOB_OK` under default backend (`bob-codex`)                                   |

### Heartbeat

The heartbeat is configured in `HEARTBEAT.md` with its own cron schedule and dispatches a prompt through the heartbeat
scheduler (distinct from the job scheduler). Verifying it catches whole-heartbeat-subsystem outages that a job smoke
test cannot.

| Dispatcher | Schedule                     | What to check                                                                         |
| ---------- | ---------------------------- | ------------------------------------------------------------------------------------- |
| heartbeat  | every hour (top of the hour) | `HEARTBEAT_OK` appears in the conversation log within ~1 minute of each hour boundary |

### Consensus — run-once on deploy

Two jobs fan out to three backends (Codex Max, Claude Sonnet, Claude Haiku) independently, then synthesize. Check that:

1. All three prompts fire within milliseconds of each other (same timestamp ±100ms)
2. All three backends respond independently
3. A synthesis prompt appears **after** all three responses, containing all three `[backend]: ...` responses
4. The synthesis response is logged under the correct synthesizer

| Job                     | Synthesizer                     | What to check                                                          |
| ----------------------- | ------------------------------- | ---------------------------------------------------------------------- |
| `consensus-test-codex`  | bob-codex / `gpt-5.1-codex-max` | Synthesis prompt logged under `bob-codex`; synthesis response present  |
| `consensus-test-claude` | bob-claude / `claude-opus-4-7`  | Synthesis prompt logged under `bob-claude`; synthesis response present |

### Tasks

Tasks fire based on time windows. Check the log after the window opens.

| Task               | Window                                                    | What to check                                                                                     |
| ------------------ | --------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `task-ping`        | daily at 00:00 UTC                                        | `TASK_OK`; `continuation-ping-delayed` fires ~10s later in same session                           |
| `task-smoke`       | any time (24h window)                                     | `TASK_SMOKE_OK`                                                                                   |
| `task-ping-loop`   | Mon-Fri 00:00 UTC, loops every 10m for 1h                 | `LOOP_OK` first 2 fires, `LOOP_DONE` on 3rd; task stops looping after `LOOP_DONE`                 |
| `task-ping-window` | daily 08:00 America/Chicago, 2h window                    | `WINDOW_OK`; check `ts` in log is within the Chicago window                                       |
| `task-ping-full`   | Mon-Fri 08:00 America/Chicago, 4h window, loops every 30m | `FULL_DONE` on first fire; task stops; logged under `bob-claude` with `claude-haiku-4-5-20251001` |

### Triggers

Triggers require a manual POST. Fire them with:

```bash
curl -X POST http://localhost:8099/triggers/ping \
  -H "Authorization: Bearer $TRIGGERS_AUTH_TOKEN"
```

| Trigger   | Endpoint                                                  | What to check                                                                    |
| --------- | --------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `ping`    | `POST /triggers/ping`                                     | `TRIGGER_OK` in log; `continuation-trigger-ping` fires ~5s later in same session |
| `echo`    | `POST /triggers/echo` body `{"token":"abc123"}`           | Response is `ECHO:abc123`                                                        |
| `webhook` | `POST /triggers/webhook` with valid `X-Hub-Signature-256` | Response summarizes the payload                                                  |

### Continuations

Continuations are triggered by the jobs and tasks above. They should not need to be fired manually — verify they appear
in the correct session, in order, after their upstream.

| Continuation                   | Upstream                                    | What to check                                                                                      |
| ------------------------------ | ------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `continuation-ping`            | `job:ping`                                  | Fires immediately after `ping` job; same session; `CONTINUATION_OK`                                |
| `continuation-ping-delayed`    | `task:task-ping`                            | Fires ~10s after `task-ping`; same session; `CONTINUATION_DELAYED_OK`                              |
| `continuation-trigger-ping`    | `trigger:ping`                              | Fires ~5s after trigger ping; same session; `CONTINUATION_TRIGGER_OK`                              |
| `animal-memory-claude-turtles` | `job:animal-memory-claude`                  | Fires after deploy; adds turtles to Claude session                                                 |
| `animal-memory-claude-recall`  | `continuation:animal-memory-claude-turtles` | Fires after turtles; response names both hamsters and turtles                                      |
| `animal-memory-codex-turtles`  | `job:animal-memory-codex`                   | Same chain on Codex                                                                                |
| `animal-memory-codex-recall`   | `continuation:animal-memory-codex-turtles`  | Response names both hamsters and turtles on Codex                                                  |
| `continuation-fanin-test`      | `job:fanin-a` + `job:fanin-b`               | Fires only after **both** fanin jobs complete; `FANIN_OK` response; verifies fan-in state tracking |

### Webhooks

Webhooks fire outbound HTTP POSTs after matching upstream completions. They require `WEBHOOK_TEST_URL_FEATURE_SINK`,
`WEBHOOK_TEST_URL_WEBHOOK_SINK`, and `WEBHOOK_TEST_TOKEN` env vars to be set for the env-url and headers tests.
Env-derived webhook URLs resolve only through `url-env-var` (see #524); inline `{{env.VAR}}` in the `url:` field is no
longer supported.

| Webhook        | Fires when                                   | What to check                                                                          |
| -------------- | -------------------------------------------- | -------------------------------------------------------------------------------------- |
| `chain-test`   | A2A response contains `WEBHOOK_FIRE`         | POST delivered to `webhook-sink` trigger; `WEBHOOK_CHAIN_OK` appears in log            |
| `test-env-url` | A2A response contains `WEBHOOK_ENV_URL_FIRE` | POST delivered to the URL in `WEBHOOK_TEST_URL_FEATURE_SINK`; `FEATURE_SINK_OK` in log |
| `test-extract` | A2A response contains `WEBHOOK_EXTRACT_FIRE` | LLM extraction runs; extracted word appears in POST body; `FEATURE_SINK_OK` in log     |
| `test-headers` | A2A response contains `WEBHOOK_HEADERS_FIRE` | POST includes `X-Test-Token` and `X-Static-Header` headers; `FEATURE_SINK_OK` in log   |

---

## Fred

Fred runs a single Claude backend. Simpler test surface — used to verify multi-agent deployment.

| Test                     | What to check                                       |
| ------------------------ | --------------------------------------------------- |
| `ping` job (every 5 min) | `JOB_OK` in fred's conversation log                 |
| `continuation-ping`      | Fires after `ping`; same session; `CONTINUATION_OK` |

---

## Fresh deploy (wipe history)

To get a clean conversation log — no leftover data from a previous deploy — delete the PVCs before upgrading. The chart
recreates them automatically on next install.

```bash
# Scale down first so PVCs are released
helm uninstall witwave-test -n witwave

# Delete all PVCs for the test release
kubectl delete pvc -n witwave \
  witwave-test-bob-claude-data \
  witwave-test-bob-codex-data \
  witwave-test-bob-gemini-data \
  witwave-test-fred-claude-data \
  witwave-test-shared

# Redeploy — PVCs are recreated and run-once jobs fire fresh
helm upgrade --install witwave-test ./charts/witwave \
  -f ./charts/witwave/values-test.yaml \
  -n witwave --create-namespace
```

---

## Viewing the conversation (dashboard)

The test stack deploys the web dashboard at `witwave-test-dashboard`. Port-forward it locally:

```bash
kubectl port-forward svc/witwave-test-dashboard 5173:80 -n witwave
```

Then open <http://localhost:5173>. The Team view lists every agent with per-backend health bubbles; click one to open
its chat panel. The Conversations view shows an aggregated log across all agents with agent/role/search filters; the
Calendar view plots the same log on a day/week grid. Everything is served by the dashboard pod — no extra port-forwards
per agent are needed.

---

## Quick checklist

After deploying, confirm in order:

1. All pods `5/5 Running` (bob) and `3/3 Running` (fred)
2. `GET /health/ready` returns `{"status":"ready"}` on both agents
3. Run-once jobs appear near the top of the bob conversation log
4. Model check logs show correct `model` fields (authoritative — ignore self-reported text)
5. Agent messages show a token count in the conversation log (`tokens` field non-null for Claude and Codex)
6. Animal memory chain: hamsters → turtles → recall with both animals, on both Claude and Codex
7. Consensus fan-out: three simultaneous prompts, three responses, one synthesis — correct synthesizer per job
8. `ping` job fires every 5 min with `continuation-ping` immediately after
9. Fred's `ping` job and `continuation-ping` appear in fred's conversation log
10. Fan-in: `fanin-a` and `fanin-b` both appear; `continuation-fanin-test` fires exactly once after both, with
    `FANIN_OK`
11. Budget: `budget-exceeded-claude` produces a `system` log entry matching
    `Budget exceeded: N tokens used of 10 limit.`
12. Heartbeat: `HEARTBEAT_OK` appears in the conversation log within ~1 minute of each hour boundary

---

## Future smoke tests

Planned additions. Kept here so the intent stays visible without cluttering the active tables above. Promoted to a real
test (and a row in the tables) when the required plumbing exists and the test can stay inside the prompt-in /
conversation-log-out verification pattern.

### Medium complexity — fits the pattern but needs coordinated fixtures

| Test                                              | What it would verify                                                                                                               | Why deferred                                                                                                                                                                                                                                                   |
| ------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Prompt-kind filter on webhooks**                | `notify-on-kind` glob filter — a webhook subscribed to `job:*` fires on job responses but not on trigger responses (or vice versa) | Needs two webhook configs plus matching chain-sink triggers; moderate new fixture surface                                                                                                                                                                      |
| **Concurrent load ordering**                      | Multiple jobs firing at the same cron tick all complete without scheduler interleaving bugs                                        | Needs 3+ jobs with identical `schedule:` plus log-ordering assertions the doc can explain clearly                                                                                                                                                              |
| **Session persistence across pod restart**        | Session ID and memory survive a pod restart (PVC persistence works end-to-end)                                                     | Requires manual deploy-time choreography — seed session, restart pod, verify memory — not a single-file change                                                                                                                                                 |
| **Per-message `model` override via A2A metadata** | `metadata.model` on an inbound A2A request overrides the routing default and lands in the log                                      | Triggers don't yet propagate `metadata.model` from the HTTP payload to the dispatch; needs trigger-side plumbing before a smoke test fits                                                                                                                      |
| **Gemini backend parity**                         | `backend-check-gemini`, `model-check-gemini-*`, `animal-memory-gemini`, `ping-gemini`                                              | Gemini backend is supported; the gap is a deployed test agent — `.agents/test/luke/` is scaffolded but not yet wired into `charts/witwave/values-test.yaml`. Once `luke` (or `bob-gemini`) lands in the test chart, promote these rows into the active tables. |

### Not a fit for this document

These are real things worth testing but don't fit the prompt-in / conversation-log-out pattern. They should live in a
separate integration / e2e test suite (for example `operator/test/e2e/` or a dedicated shell-based suite), not here.

| Test                                                                                            | Why it doesn't fit                                                                                                                  |
| ----------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| Metrics endpoint smoke (`/metrics` scrape asserts specific counters move)                       | Verification is regex over Prometheus text output, not conversation log                                                             |
| Trigger trace / header propagation (e.g. `X-Trace-Id` round-trips)                              | Verification is on HTTP headers, not conversation log                                                                               |
| Read-only endpoints (`/conversations`, `/triggers`, etc.) return well-formed responses          | curl-and-assert-on-JSON, not conversation log                                                                                       |
| Agent-card hot-reload (`/.well-known/agent.json` updates after editing mounted `agent-card.md`) | Verification is on the agent-card JSON response, not the conversation log; also requires mutating a ConfigMap-mounted file mid-test |
| Strict token-count assertion (`tokens` field always non-null for Claude and Codex)              | Not a separate dispatched test — an invariant check on fields of existing log entries; lives better as a log-shape linter           |
