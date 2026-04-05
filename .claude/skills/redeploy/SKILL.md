---
name: redeploy
description: Rebuild the claude-agent Docker image and recreate all running containers.
argument-hint: ""
---

Rebuild the claude-agent image and redeploy all containers using Docker Compose.

Use the Bash tool to run the following from the repo root:

```bash
docker build -t claude-agent:latest . && docker compose up -d --force-recreate
```

Report the outcome clearly — confirm which containers were recreated or report any build/deploy errors.
