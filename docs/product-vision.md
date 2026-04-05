# Product Vision

---

## What This Is

An autonomous agent platform built for infrastructure — not for developers sitting at a terminal. Agents run as
containerized services, operate on their own schedules, collaborate via standard protocols, and do meaningful work
without a human present to start each task.

The unit of deployment is a container. The unit of identity is an agent. The unit of work is an agenda item.

---

## Target Audience

### Individual Practitioners

Developers, researchers, and hobbyists who want autonomous agents running a project — whether that is a software
development project, a research workflow, a content pipeline, or any other goal-directed work that benefits from
persistent, self-directed automation. The barrier to entry should be low: clone the repo, configure an agent, run
`docker compose up`.

### Enterprise Teams

Organizations that need autonomous agents deployed on managed infrastructure with the reliability, observability, and
operational characteristics expected of production services. Health endpoints, metrics, structured logging, role-based
agent identities, and standard Kubernetes deployment patterns should all be first-class — not afterthoughts.

The same codebase should serve both audiences. Complexity is opt-in.

---

## Deployment Target

**Kubernetes is the primary deployment target.** All infrastructure decisions should be evaluated against Kubernetes
compatibility:

- Health endpoints follow the Kubernetes three-probe model (`/health/start`, `/health/live`, `/health/ready`)
- Containers are stateless and horizontally scalable
- Configuration is injected via environment variables and mounted volumes — never baked into the image
- Agents expose standard HTTP endpoints suitable for Kubernetes `Service` and `Ingress` resources

### Deployment Roadmap

| Artifact             | Status      | Description                                     |
| -------------------- | ----------- | ----------------------------------------------- |
| `docker-compose.yml` | Shipped     | Local development and single-host deployment    |
| Helm chart           | Planned     | Parameterized Kubernetes deployment for teams   |
| Kubernetes Operator  | Considering | Declarative agent lifecycle management via CRDs |

---

## Support Workloads

The core platform is the agent runtime. The following support workloads are on the roadmap as the platform matures:

- **UI** — a lightweight web interface for monitoring agent activity, reviewing logs, triggering agenda items, and
  managing TODO.md — aimed at the individual and small-team audience
- **Shared memory service** — a structured key-value store accessible to all agents on the team, replacing the current
  flat markdown memory files for data that requires reliable read/write semantics (see F-003)
- **Metrics aggregation** — a Prometheus-compatible scrape target and optional Grafana dashboard for teams running
  agents at scale (see F-008)

---

## Design Principles

**Infrastructure-grade reliability.** Agents should behave like services: predictable startup, clean shutdown,
observable at all times, and recoverable from crashes without operator intervention.

**File-based configuration.** Agent identity, behavior, schedule, and skills are defined in mounted files — not compiled
in. A new agent is a new directory, not a new image.

**Standard protocols.** A2A for inter-agent communication, Prometheus for metrics, OpenTelemetry for tracing, Kubernetes
probes for health — this project should feel native to the infrastructure it runs on.

**Complexity is opt-in.** A single agent running `docker compose up` should work out of the box. MCP servers,
guardrails, metrics, and HITL approval gates are all available but never required.

**Professional quality for everyone.** Enterprise features should not require an enterprise deployment. The individual
running a personal project deserves the same quality of tooling as a team deploying to production.

---

## Relationship to Other Docs

| Document                                             | Purpose                                                              |
| ---------------------------------------------------- | -------------------------------------------------------------------- |
| [competitive-landscape.md](competitive-landscape.md) | Reference products and research themes that inform feature decisions |
| [features-proposed.md](features-proposed.md)         | Active feature pipeline — proposed, approved, and promoted features  |
| [features-completed.md](features-completed.md)       | Implemented features, for historical reference                       |
| `README.md`                                          | Quickstart and technical reference for running the project           |
| `CLAUDE.md`                                          | Behavioral guidance for Claude Code working in this repo             |
