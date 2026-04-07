---
name: deploy
description: Deploy, redeploy, or tear down a nyx-agent environment using Docker Compose.
argument-hint: "[up|down|redeploy|status] [environment]"
---

Manage nyx-agent environments using Docker Compose.

## Actions

- **up** — build the image and bring up the environment
- **redeploy** — build the image and force-recreate all running containers
- **down** — tear down the environment (stop and remove containers)
- **status** — show which environments are running and which agents in each are reachable

If no action is given, infer from context: "deploy" or "bring up" → `up`; "redeploy" or "recreate" → `redeploy`; "teardown", "tear down", or "bring down" → `down`; "status", "what's running", or "which environments" → `status`.

## Determining the environment

The environment name corresponds to the suffix of a `docker-compose.<env>.yml` file in the repo root.

If no environment is given, discover available environments and running containers:

```bash
ls docker-compose.*.yml
```

Then check which have running containers:

```bash
docker compose -f docker-compose.<env>.yml ps --services --filter status=running 2>/dev/null
```

- For `up`: if only one environment is **not** running, use that one.
- For `redeploy` or `down`: if only one environment **is** running, use that one.
- If the environment still can't be determined, ask the user.

## Running the action

**up / redeploy:**
```bash
docker build -f agent/Dockerfile -t nyx-agent:latest . && docker compose -f docker-compose.<env>.yml up -d --force-recreate
```

**down:**
```bash
docker compose -f docker-compose.<env>.yml down
```

**status:**

For each `docker-compose.*.yml` found in the repo root (or just the specified environment), get the container list and their state:

```bash
docker compose -f docker-compose.<env>.yml ps
```

For each running container that maps port 8000 internally, extract the host port from the compose file and probe the agent's A2A discovery endpoint:

```bash
curl -sf http://localhost:<host-port>/.well-known/agent.json
```

Report a table per environment showing: container name, status, and — for agent containers — whether the A2A endpoint responded and the agent's name from the response. Non-agent containers (e.g. `ui`) should show status only.

Report the outcome clearly — confirm which containers were affected or report any errors.
