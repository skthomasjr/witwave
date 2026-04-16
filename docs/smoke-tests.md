# Smoke Tests

This document describes what to look for in the conversation log after deploying the test stack. All tests run automatically on deploy (run-once jobs) or on a recurring schedule. Check the conversation at `GET /conversations/bob` and `GET /conversations/fred`.

The authoritative model for each call is in the conversation log, not the response text — models often misreport their own version.

---

## Bob

### Jobs — run-once on deploy

These fire immediately when the stack comes up. Look for them near the top of the conversation log.

| Job | Agent | What to check |
|---|---|---|
| `backend-check-claude` | bob-claude | Response identifies itself as Claude; logged under `bob-claude` |
| `backend-check-codex` | bob-codex | Response identifies itself as Codex; logged under `bob-codex` |
| `model-check-claude-default` | bob-claude | `model` field in log matches the default Claude model in `backend.yaml` |
| `model-check-claude-opus` | bob-claude | `model` field = `claude-opus-4-6` |
| `model-check-claude-sonnet` | bob-claude | `model` field = `claude-sonnet-4-6` |
| `model-check-claude-haiku` | bob-claude | `model` field = `claude-haiku-4-5-20251001` |
| `model-check-codex-default` | bob-codex | `model` field matches the default Codex model in `backend.yaml` |
| `model-check-codex-gpt-5-1-codex-max` | bob-codex | `model` field = `gpt-5.1-codex-max` |
| `model-check-codex-gpt-5-1-codex` | bob-codex | `model` field = `gpt-5.1-codex` |
| `animal-memory-claude` | bob-claude | Response acknowledges hamsters; seeds session memory |
| `animal-memory-codex` | bob-codex | Response acknowledges hamsters; seeds session memory |
| `budget-exceeded-claude` | bob-claude | `system` entry in log: `Budget exceeded: N tokens used of 10 limit.` — verifies `max-tokens` enforcement surfaces a `BudgetExceededError` as a system conversation-log entry |
| `fanin-a` | default | `FANIN_A_OK` response; first leg of fan-in continuation test |
| `fanin-b` | default | `FANIN_B_OK` response; second leg of fan-in continuation test |

### Jobs — run-once, continuation chains

These fire once and trigger continuation chains. Check that all steps appear in order in the log.

| Job | Continuation chain | What to check |
|---|---|---|
| `animal-memory-claude` | → `animal-memory-claude-turtles` → `animal-memory-claude-recall` | Final recall response names both hamsters and turtles; all three logged under `bob-claude` in session order |
| `animal-memory-codex` | → `animal-memory-codex-turtles` → `animal-memory-codex-recall` | Same as above but under `bob-codex` |

### Jobs — recurring

| Job | Schedule | What to check |
|---|---|---|
| `ping` | every 5 min | `JOB_OK` response; `continuation-ping` fires immediately after in same session |
| `ping-claude` | every 30 min | `JOB_OK` under `bob-claude` |
| `ping-codex` | every 30 min | `JOB_OK` under `bob-codex` |
| `ping-default` | every 10 min | `JOB_OK` under default backend (`bob-codex`) |

### Heartbeat

The heartbeat is configured in `HEARTBEAT.md` with its own cron schedule and dispatches a prompt through the heartbeat scheduler (distinct from the job scheduler). Verifying it catches whole-heartbeat-subsystem outages that a job smoke test cannot.

| Dispatcher | Schedule | What to check |
|---|---|---|
| heartbeat | every hour (top of the hour) | `HEARTBEAT_OK` appears in the conversation log within ~1 minute of each hour boundary |

### Consensus — run-once on deploy

Two jobs fan out to three backends (Codex Max, Claude Sonnet, Claude Haiku) independently, then synthesize. Check that:

1. All three prompts fire within milliseconds of each other (same timestamp ±100ms)
2. All three backends respond independently
3. A synthesis prompt appears **after** all three responses, containing all three `[backend]: ...` responses
4. The synthesis response is logged under the correct synthesizer

| Job | Synthesizer | What to check |
|---|---|---|
| `consensus-test-codex` | bob-codex / `gpt-5.1-codex-max` | Synthesis prompt logged under `bob-codex`; synthesis response present |
| `consensus-test-claude` | bob-claude / `claude-opus-4-6` | Synthesis prompt logged under `bob-claude`; synthesis response present |

### Tasks

Tasks fire based on time windows. Check the log after the window opens.

| Task | Window | What to check |
|---|---|---|
| `task-ping` | daily at 00:00 UTC | `TASK_OK`; `continuation-ping-delayed` fires ~10s later in same session |
| `task-smoke` | any time (24h window) | `TASK_SMOKE_OK` |
| `task-ping-loop` | Mon-Fri 00:00 UTC, loops every 10m for 1h | `LOOP_OK` first 2 fires, `LOOP_DONE` on 3rd; task stops looping after `LOOP_DONE` |
| `task-ping-window` | daily 08:00 America/Chicago, 2h window | `WINDOW_OK`; check `ts` in log is within the Chicago window |
| `task-ping-full` | Mon-Fri 08:00 America/Chicago, 4h window, loops every 30m | `FULL_DONE` on first fire; task stops; logged under `bob-claude` with `claude-haiku-4-5-20251001` |

### Triggers

Triggers require a manual POST. Fire them with:

```bash
curl -X POST http://localhost:8099/triggers/ping \
  -H "Authorization: Bearer $TRIGGERS_AUTH_TOKEN"
```

| Trigger | Endpoint | What to check |
|---|---|---|
| `ping` | `POST /triggers/ping` | `TRIGGER_OK` in log; `continuation-trigger-ping` fires ~5s later in same session |
| `echo` | `POST /triggers/echo` body `{"token":"abc123"}` | Response is `ECHO:abc123` |
| `webhook` | `POST /triggers/webhook` with valid `X-Hub-Signature-256` | Response summarizes the payload |

### Continuations

Continuations are triggered by the jobs and tasks above. They should not need to be fired manually — verify they appear in the correct session, in order, after their upstream.

| Continuation | Upstream | What to check |
|---|---|---|
| `continuation-ping` | `job:ping` | Fires immediately after `ping` job; same session; `CONTINUATION_OK` |
| `continuation-ping-delayed` | `task:task-ping` | Fires ~10s after `task-ping`; same session; `CONTINUATION_DELAYED_OK` |
| `continuation-trigger-ping` | `trigger:ping` | Fires ~5s after trigger ping; same session; `CONTINUATION_TRIGGER_OK` |
| `animal-memory-claude-turtles` | `job:animal-memory-claude` | Fires after deploy; adds turtles to Claude session |
| `animal-memory-claude-recall` | `continuation:animal-memory-claude-turtles` | Fires after turtles; response names both hamsters and turtles |
| `animal-memory-codex-turtles` | `job:animal-memory-codex` | Same chain on Codex |
| `animal-memory-codex-recall` | `continuation:animal-memory-codex-turtles` | Response names both hamsters and turtles on Codex |
| `continuation-fanin-test` | `job:fanin-a` + `job:fanin-b` | Fires only after **both** fanin jobs complete; `FANIN_OK` response; verifies fan-in state tracking |

### Webhooks

Webhooks fire outbound HTTP POSTs after matching upstream completions. They require `WEBHOOK_TEST_HOST` and `WEBHOOK_TEST_TOKEN` env vars to be set for the env-url and headers tests.

| Webhook | Fires when | What to check |
|---|---|---|
| `chain-test` | A2A response contains `WEBHOOK_FIRE` | POST delivered to `webhook-sink` trigger; `WEBHOOK_CHAIN_OK` appears in log |
| `test-env-url` | A2A response contains `WEBHOOK_ENV_URL_FIRE` | POST delivered to `{{env.WEBHOOK_TEST_HOST}}/triggers/feature-sink`; `FEATURE_SINK_OK` in log |
| `test-extract` | A2A response contains `WEBHOOK_EXTRACT_FIRE` | LLM extraction runs; extracted word appears in POST body; `FEATURE_SINK_OK` in log |
| `test-headers` | A2A response contains `WEBHOOK_HEADERS_FIRE` | POST includes `X-Test-Token` and `X-Static-Header` headers; `FEATURE_SINK_OK` in log |

---

## Fred

Fred runs a single Claude backend. Simpler test surface — used to verify multi-agent deployment.

| Test | What to check |
|---|---|
| `ping` job (every 5 min) | `JOB_OK` in fred's conversation log |
| `continuation-ping` | Fires after `ping`; same session; `CONTINUATION_OK` |

---

## Fresh deploy (wipe history)

To get a clean conversation log — no leftover data from a previous deploy — delete the PVCs before upgrading. The chart recreates them automatically on next install.

```bash
# Scale down first so PVCs are released
helm uninstall nyx-test -n nyx

# Delete all PVCs for the test release
kubectl delete pvc -n nyx \
  nyx-test-bob-claude-data \
  nyx-test-bob-codex-data \
  nyx-test-bob-gemini-data \
  nyx-test-fred-claude-data \
  nyx-test-shared

# Redeploy — PVCs are recreated and run-once jobs fire fresh
helm upgrade --install nyx-test ./charts/nyx \
  -f ./charts/nyx/values-test.yaml \
  -n nyx --create-namespace
```

---

## Viewing the conversation (UI)

The test stack deploys a web UI at `nyx-test-ui`. Port-forward it locally:

```bash
kubectl port-forward svc/nyx-test-ui 8080:80 -n nyx
```

Then open http://localhost:8080 in your browser. Select **bob** or **fred** from the agent dropdown to see the conversation log. Agent messages show `agent · model · N tok · timestamp · session_id` in the subdued label line.

To bring up both the agent API and the UI at the same time:

```bash
# UI defaults to port 8000 for the agent when accessed on port 8080
kubectl port-forward svc/nyx-test-bob 8000:8099 -n nyx &
kubectl port-forward svc/nyx-test-ui 8080:80 -n nyx &
```

Then open http://localhost:8080. The UI resolves the agent at `http://localhost:8000` automatically.

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
10. Fan-in: `fanin-a` and `fanin-b` both appear; `continuation-fanin-test` fires exactly once after both, with `FANIN_OK`
11. Budget: `budget-exceeded-claude` produces a `system` log entry matching `Budget exceeded: N tokens used of 10 limit.`
12. Heartbeat: `HEARTBEAT_OK` appears in the conversation log within ~1 minute of each hour boundary
