---
description: Builds all images, deploys the test environment via Helm, and verifies all services are ready before any tests run.
enabled: true
---

> **Before you start.** Read [`tests/README.md`](./README.md) for the framework conventions, the trigger Bearer-auth
> contract, and (most importantly) the **required tabular output format** that the run must produce after the suite
> finishes. Every executed test must have a row in that table.

Tear down any existing test environment before starting fresh:

```
helm uninstall nyx-test -n nyx-test 2>/dev/null || true
```

Clear all test agent logs so tests start with a clean slate:

```
rm -f .agents/test/bob/logs/conversation.jsonl
rm -f .agents/test/bob/logs/tool-activity.jsonl
rm -f .agents/test/bob/logs/agent.log
```

The fastest path — deploy via the project's helper script:

```
./scripts/deploy-test.sh
```

The script sources `.env` at the repo root, validates required vars
(`CLAUDE_CODE_OAUTH_TOKEN`, `GITSYNC_USERNAME`, `GITSYNC_PASSWORD`), creates
the `ghcr-credentials` image-pull secret from the dev's local
`~/.docker/config.json`, and runs `helm upgrade --install nyx-test ./charts/nyx
-f values-test.yaml ...` with credentials passed as `--set` flags.

The chart's inline-credentials pattern (`gitSync.credentials` +
`backends.credentials` with `acknowledgeInsecureInline=true`) renders the
per-agent Secrets for us — no manual `kubectl create secret` chain needed.
See `charts/nyx/README.md#credentials-for-gitsync--backends` for the full
shape, three install modes, and the explicit dev-only tradeoff of landing
tokens in `helm get values`.

Required in `.env`:

```
CLAUDE_CODE_OAUTH_TOKEN=...
GITSYNC_USERNAME=<your-github-username>
GITSYNC_PASSWORD=<github-pat-with-repo-scope>
```

Optional in `.env` (placeholders used when absent — disabled backends
ignore them):

```
OPENAI_API_KEY=...
GEMINI_API_KEY=...
```

If any required var is missing, the script fails fast with a clear message
naming the var. If `~/.docker/config.json` has no `ghcr.io` entry, the
script tells you to run `docker login ghcr.io` first.

### Manual fallback (when the script can't run)

If you need to bypass the script — say you're deploying with a
non-`.env` secret source — the underlying call is just `helm upgrade`:

```
helm upgrade --install nyx-test ./charts/nyx \
  -f ./charts/nyx/values-test.yaml \
  --set-string gitSync.credentials.username="$GITSYNC_USERNAME" \
  --set-string gitSync.credentials.token="$GITSYNC_PASSWORD" \
  --set        gitSync.credentials.acknowledgeInsecureInline=true \
  --set-string "backends.credentials.secrets.CLAUDE_CODE_OAUTH_TOKEN=$CLAUDE_CODE_OAUTH_TOKEN" \
  --set        backends.credentials.acknowledgeInsecureInline=true \
  -n nyx-test --create-namespace
```

### Legacy path (pre-existing-Secrets approach)

If you'd rather pre-create the Secrets yourself and skip the chart-rendered
ones (common in CI), use the `existingSecret` mode — `kubectl create secret
generic bob-claude-secrets --from-literal=CLAUDE_CODE_OAUTH_TOKEN=...` and
point `agents[].backends[].credentials.existingSecret: bob-claude-secrets`.
The chart respects existingSecret references and renders nothing extra.

If any step fails, do your best to diagnose and fix the issue — for example, a missing dependency in a Dockerfile, a stale image, or a broken compose mount. Fixing infrastructure issues to get the environment running is expected and encouraged.

Once the stack is up, poll each service until it reports ready or until 60 seconds have elapsed:

- Bob nyx agent: GET http://localhost:8099/health/ready — expect 200 with `"status": "ready"`
- Bob claude backend: GET http://localhost:8090/health — expect 200 with `"status": "ok"`
- Bob codex backend: GET http://localhost:8091/health — expect 200 with `"status": "ok"`
- Bob gemini backend: GET http://localhost:8092/health — expect 200 with `"status": "ok"`

If any service fails to become ready within 60 seconds, fail immediately with a clear message identifying which service failed. Do not proceed with the remaining tests.

**Trigger auth.** The harness rejects every trigger POST that lacks either a per-trigger HMAC secret or a Bearer token matching `TRIGGERS_AUTH_TOKEN` (security-by-default since 2026-04-12). The test stack ships `TRIGGERS_AUTH_TOKEN=smoke-test-token` in bob's environment via `charts/nyx/values-test.yaml`. Smoke tests use `Authorization: Bearer ${TRIGGERS_AUTH_TOKEN:-smoke-test-token}` in their curl examples — set the env var if you've overridden it, otherwise the default works.

If all services are healthy, continue with the dashboard wire-up below before declaring ready.

## Dashboard wire-up (every smoke-test run)

Whenever the smoke-test stack comes up, finish the initialisation sequence by getting the dashboard reachable from a browser:

1. **Wire up the dashboard.** The chart renders a `nyx-dashboard` Deployment + Service when `dashboard.enabled=true` (the default in `values-test.yaml`). Confirm the Service exists and has an endpoint:

   ```
   kubectl get svc nyx-dashboard -n nyx-test
   kubectl get endpoints nyx-dashboard -n nyx-test
   ```

   Both should return a non-empty entry. If the Service has no endpoints, the dashboard pod is still coming up — wait for it.

2. **Port-forward the dashboard** so it's reachable from `localhost`:

   ```
   kubectl port-forward -n nyx-test svc/nyx-dashboard 8080:80 &
   ```

   Run it in the background so the smoke run can continue. Record the PID so a later spec or cleanup step can `kill` it.

3. **Open the dashboard.** On macOS:

   ```
   open http://localhost:8080
   ```

   On Linux use `xdg-open`; on Windows use `start`. The dashboard should load to the Team view. If it doesn't, record the failure with the browser console output before continuing.

Only respond with INIT_OK once the dashboard loaded end-to-end.

**If you encounter code bugs in the system under test, do not fix them — mark this test as failed and report the issue. Only fix infrastructure and tooling problems that prevent the test environment from starting.**
