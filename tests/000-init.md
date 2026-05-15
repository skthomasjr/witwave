---
description: Deploys the operator-managed test team with ww and verifies Bob/Fred are ready before any tests run.
enabled: true
---

> **Before you start.** Read [`tests/README.md`](./README.md) for the framework conventions, the trigger Bearer-auth
> contract, and the required tabular output format that the run must produce after the suite finishes. Every executed
> test must have a row in that table.

Tear down any existing test agents and workspace before starting fresh:

```bash
ww agent delete bob --namespace witwave-test --delete-git-secret --yes 2>/dev/null || true
ww agent delete fred --namespace witwave-test --delete-git-secret --yes 2>/dev/null || true
ww workspace delete witwave-test --namespace witwave-test --wait --yes 2>/dev/null || true
```

Start a shell with the committed SOPS test-team dotenv loaded:

```bash
mise exec -- scripts/sops-exec-env.py .agents/test/team.sops.env -- bash -l
```

Then verify the required credentials inside that shell:

```bash
: "${CLAUDE_CODE_OAUTH_TOKEN:?set CLAUDE_CODE_OAUTH_TOKEN in .agents/test/team.sops.env}"
: "${GITSYNC_USERNAME:?set GITSYNC_USERNAME in .agents/test/team.sops.env}"
: "${GITSYNC_PASSWORD:?set GITSYNC_PASSWORD in .agents/test/team.sops.env}"
export TRIGGERS_AUTH_TOKEN="${TRIGGERS_AUTH_TOKEN:-$(python3 -c 'import secrets; print(secrets.token_hex(32))')}"
printf 'Using TRIGGERS_AUTH_TOKEN=%s\n' "$TRIGGERS_AUTH_TOKEN"
```

Install or verify the operator:

```bash
ww operator install --yes
ww operator status
```

Create the test workspace. Bob and Fred both bind to this workspace.

```bash
ww workspace create witwave-test \
  --namespace witwave-test \
  --create-namespace \
  --volume memory=1Gi:rwo \
  --yes

ww workspace status witwave-test \
  --namespace witwave-test
```

Deploy Bob:

```bash
ww agent create bob \
  --namespace witwave-test \
  --create-namespace \
  --team test \
  --workspace witwave-test \
  --with-persistence \
  --backend claude \
  --harness-env CONVERSATIONS_AUTH_DISABLED=true \
  --harness-env TRIGGERS_AUTH_TOKEN="$TRIGGERS_AUTH_TOKEN" \
  --harness-env WEBHOOK_TEST_HOST=bob:8000 \
  --harness-env WEBHOOK_TEST_URL_FEATURE_SINK=http://bob:8000/triggers/feature-sink \
  --harness-env WEBHOOK_TEST_URL_WEBHOOK_SINK=http://bob:8000/triggers/webhook-sink \
  --harness-env WEBHOOK_TEST_TOKEN=test-token-abc123 \
  --harness-env WEBHOOK_TEST_BEARER="$TRIGGERS_AUTH_TOKEN" \
  --backend-env claude:CONVERSATIONS_AUTH_DISABLED=true \
  --backend-secret-from-env claude=CLAUDE_CODE_OAUTH_TOKEN \
  --gitsync-bundle https://github.com/witwave-ai/witwave.git@main:.agents/test/bob \
  --gitsync-secret-from-env GITSYNC_USERNAME:GITSYNC_PASSWORD \
  --yes
```

Deploy Fred:

```bash
ww agent create fred \
  --namespace witwave-test \
  --create-namespace \
  --team test \
  --workspace witwave-test \
  --with-persistence \
  --backend claude \
  --harness-env CONVERSATIONS_AUTH_DISABLED=true \
  --backend-env claude:CONVERSATIONS_AUTH_DISABLED=true \
  --backend-secret-from-env claude=CLAUDE_CODE_OAUTH_TOKEN \
  --gitsync-bundle https://github.com/witwave-ai/witwave.git@main:.agents/test/fred \
  --gitsync-secret-from-env GITSYNC_USERNAME:GITSYNC_PASSWORD \
  --yes
```

Poll readiness:

```bash
ww workspace status witwave-test --namespace witwave-test
ww agent status bob --namespace witwave-test
ww agent status fred --namespace witwave-test
```

`ww workspace status` should show Bob and Fred as bound.

Start the local port-forwards that the leaf specs assume:

```bash
if [ -f /tmp/witwave-bob-portforward.pid ]; then kill "$(cat /tmp/witwave-bob-portforward.pid)" 2>/dev/null || true; fi
if [ -f /tmp/witwave-fred-portforward.pid ]; then kill "$(cat /tmp/witwave-fred-portforward.pid)" 2>/dev/null || true; fi

kubectl port-forward -n witwave-test svc/bob 8099:8000 9099:9000 >/tmp/witwave-bob-portforward.log 2>&1 &
echo $! >/tmp/witwave-bob-portforward.pid

kubectl port-forward -n witwave-test svc/fred 8098:8000 >/tmp/witwave-fred-portforward.log 2>&1 &
echo $! >/tmp/witwave-fred-portforward.pid

sleep 2
curl -sf http://localhost:8099/health/ready
curl -sf http://localhost:9099/metrics >/dev/null
curl -sf http://localhost:8098/health/ready
```

Run a minimal A2A round-trip on each agent:

```bash
ww agent send bob --namespace witwave-test "Respond with INIT_BOB_OK."
ww agent send fred --namespace witwave-test "Respond with INIT_FRED_OK."
```

Confirm conversation inspection works:

```bash
ww conversation list --namespace witwave-test --agent bob --expand
ww conversation list --namespace witwave-test --agent fred --expand
```

If any step fails, do your best to diagnose and fix the issue. Fixing infrastructure issues to get the environment
running is expected and encouraged. If a code bug in the system under test is the cause, mark init as failed and report
the issue rather than fixing product code during the smoke run.

Only respond with `INIT_OK` once Bob and Fred are ready, the port-forwards are active, and `ww conversation list` can
read both agents.
