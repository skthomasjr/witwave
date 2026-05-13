---
description: Tears down the operator-managed test agents after all tests have run.
enabled: true
---

Stop local port-forwards started by `000-init.md`:

```bash
if [ -f /tmp/witwave-bob-portforward.pid ]; then kill "$(cat /tmp/witwave-bob-portforward.pid)" 2>/dev/null || true; rm -f /tmp/witwave-bob-portforward.pid; fi
if [ -f /tmp/witwave-fred-portforward.pid ]; then kill "$(cat /tmp/witwave-fred-portforward.pid)" 2>/dev/null || true; rm -f /tmp/witwave-fred-portforward.pid; fi
```

Delete the test agents and workspace:

```bash
ww agent delete bob --namespace witwave-test --delete-git-secret --yes 2>/dev/null || true
ww agent delete fred --namespace witwave-test --delete-git-secret --yes 2>/dev/null || true
ww workspace delete witwave-test --namespace witwave-test --wait --yes 2>/dev/null || true
```

If the commands fail, diagnose and fix tooling/infra issues until the environment is clean. The operator owns the Deployments, Services, PVCs, and backend credential Secrets; `ww agent delete` should cascade those resources. Delete agents before deleting the workspace because `WitwaveWorkspace` refuses deletion while any agent is still bound.

If all services are down, respond with `CLEANUP_OK`. If cleanup could not be completed, report the error and respond with `CLEANUP_FAILED`.

Any fixes made during initialization or cleanup should be committed and pushed only when explicitly requested by the user.

**If you encounter code bugs in the system under test, do not fix them during cleanup; mark cleanup as passed if teardown worked and report the bug separately. Only fix infrastructure and tooling problems.**
