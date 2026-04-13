---
description: Builds all images, deploys the test environment via Helm, and verifies all services are ready before any tests run.
enabled: true
---

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

Build all images and bring up the test environment:

```
docker build -f agent/Dockerfile -t nyx-agent:latest . \
  && docker build -f a2-claude/Dockerfile -t a2-claude:latest . \
  && docker build -f a2-codex/Dockerfile -t a2-codex:latest . \
  && docker build -f a2-gemini/Dockerfile -t a2-gemini:latest . \
  && helm upgrade --install nyx-test ./charts/nyx -f ./charts/nyx/values-test.yaml -n nyx-test --create-namespace
```

If any step fails, do your best to diagnose and fix the issue — for example, a missing dependency in a Dockerfile, a stale image, or a broken compose mount. Fixing infrastructure issues to get the environment running is expected and encouraged.

Once the stack is up, poll each service until it reports ready or until 60 seconds have elapsed:

- Bob nyx agent: GET http://localhost:8099/health/ready — expect 200 with `"status": "ready"`
- Bob a2-claude backend: GET http://localhost:8090/health — expect 200 with `"status": "ok"`
- Bob a2-codex backend: GET http://localhost:8091/health — expect 200 with `"status": "ok"`
- Bob a2-gemini backend: GET http://localhost:8092/health — expect 200 with `"status": "ok"`

If any service fails to become ready within 60 seconds, fail immediately with a clear message identifying which service failed. Do not proceed with the remaining tests.

If all services are healthy, respond with INIT_OK.

**If you encounter code bugs in the system under test, do not fix them — mark this test as failed and report the issue. Only fix infrastructure and tooling problems that prevent the test environment from starting.**
