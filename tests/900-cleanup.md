---
description: Tears down the test environment after all tests have run.
enabled: true
---

Bring down the test stack and remove containers:

```
helm uninstall witwave-test -n witwave-test
```

If the command fails, do your best to diagnose and fix the issue — for example, manually stopping containers or removing
networks — until the environment is clean.

If all services are down, respond with CLEANUP_OK. If cleanup could not be completed, report the error and respond with
CLEANUP_FAILED.

Any fixes made during initialization or cleanup (to Dockerfiles, requirements, compose files, or other infrastructure)
should be committed and pushed:

```
git add <changed files>
git commit -m "Fix test infrastructure: <short description>"
git push origin main
```

**If you encounter code bugs in the system under test, do not fix them — mark cleanup as passed regardless and report
the bug separately. Only fix infrastructure and tooling problems.**
