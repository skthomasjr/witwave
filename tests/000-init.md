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
rm -f .agents/test/bob/logs/trace.jsonl
rm -f .agents/test/bob/logs/agent.log
```

Provision the required credentials first. The test stack's `values-test.yaml` references six secrets; every one must exist in the `nyx-test` namespace before the chart can roll out cleanly. Create them with whatever credentials are available:

```
kubectl create namespace nyx-test 2>/dev/null || true

# a2-claude backend — required for any Claude-path smoke test.
kubectl create secret generic bob-claude-secrets \
  --from-literal=CLAUDE_CODE_OAUTH_TOKEN="$CLAUDE_CODE_OAUTH_TOKEN" \
  -n nyx-test
kubectl create secret generic fred-claude-secrets \
  --from-literal=CLAUDE_CODE_OAUTH_TOKEN="$CLAUDE_CODE_OAUTH_TOKEN" \
  -n nyx-test

# a2-codex backend — real OPENAI_API_KEY if available, otherwise
# a placeholder so pod schedules (backend will crash on first LLM call).
kubectl create secret generic bob-codex-secrets \
  --from-literal=OPENAI_API_KEY="${OPENAI_API_KEY:-placeholder-no-openai-key}" \
  -n nyx-test

# a2-gemini backend — same pattern.
kubectl create secret generic bob-gemini-secrets \
  --from-literal=GEMINI_API_KEY="${GEMINI_API_KEY:-placeholder-no-gemini-key}" \
  -n nyx-test

# git-sync sidecar — empty username/password works for public repos.
kubectl create secret generic git-sync-credentials \
  --from-literal=GITSYNC_USERNAME="" \
  --from-literal=GITSYNC_PASSWORD="" \
  -n nyx-test

# ghcr-credentials — image-pull secret. Images are in a private GHCR
# namespace, so this must carry real GHCR auth. Reuse the local docker
# login if the dev is already signed in (the common case):
python3 -c "
import json, sys
with open('$HOME/.docker/config.json') as f:
    cfg = json.load(f)
json.dump({'auths': {'ghcr.io': cfg['auths']['ghcr.io']}}, sys.stdout)
" > /tmp/ghcr-dockerconfig.json
kubectl create secret generic ghcr-credentials \
  --from-file=.dockerconfigjson=/tmp/ghcr-dockerconfig.json \
  --type=kubernetes.io/dockerconfigjson \
  -n nyx-test
rm /tmp/ghcr-dockerconfig.json
```

If you hit a backend whose key you don't have (expired OpenAI trial, no
Gemini access, etc.) the placeholder still lets the chart roll out — the
backend pod just crash-loops on startup once it tries to exercise the
missing credential. The a2-claude path stays fully functional and covers
most harness / observability / trigger smoke tests independent of the
other backends.

Build all images and bring up the test environment:

```
docker build -f harness/Dockerfile -t nyx-harness:latest . \
  && docker build -f backends/a2-claude/Dockerfile -t a2-claude:latest . \
  && docker build -f backends/a2-codex/Dockerfile -t a2-codex:latest . \
  && docker build -f backends/a2-gemini/Dockerfile -t a2-gemini:latest . \
  && helm upgrade --install nyx-test ./charts/nyx -f ./charts/nyx/values-test.yaml -n nyx-test --create-namespace
```

If any step fails, do your best to diagnose and fix the issue — for example, a missing dependency in a Dockerfile, a stale image, or a broken compose mount. Fixing infrastructure issues to get the environment running is expected and encouraged.

Once the stack is up, poll each service until it reports ready or until 60 seconds have elapsed:

- Bob nyx agent: GET http://localhost:8099/health/ready — expect 200 with `"status": "ready"`
- Bob a2-claude backend: GET http://localhost:8090/health — expect 200 with `"status": "ok"`
- Bob a2-codex backend: GET http://localhost:8091/health — expect 200 with `"status": "ok"`
- Bob a2-gemini backend: GET http://localhost:8092/health — expect 200 with `"status": "ok"`

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
