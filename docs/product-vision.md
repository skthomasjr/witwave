# Product Vision

---

## What This Is

An autonomous agent platform built for infrastructure — not for developers sitting at a terminal. Agents run as
containerized services, operate on their own schedules, collaborate via standard protocols, and do meaningful work
without a human present to start each task.

The unit of deployment is a container. The unit of identity is an agent. The unit of work is a prompt.

---

## AI-Operated Open Source

One of the project's explicit target goals is that **every line of code in this repository is written by AI** — not as a
slogan but as a structural design constraint. Agents write the code, fix the bugs, answer the issues, refine the
requests, open the pull requests, review the pull requests, merge them, cut the releases, write the release notes, and
eventually write the blog posts and handle community outreach at a calibrated level. Humans file issues (questions,
requests, bug reports) and make strategic calls. That is the shape of participation.

This is distinct from "AI-assisted development" (humans write code with AI help) or "agent platform for developers"
(agents help a developer do their job). The target model here is **AI-run**: the agents maintain the platform they are
built on. The repo is both the product and the test bed — every feature shipped, every regression fixed, every incident
postmortem is evidence for whether autonomous agents can actually maintain real software over the long run.

Today this runs with one human contributor guiding the agents through each loop; the target is a model where multiple
external humans file issues and agents carry them end-to-end without human involvement in the day-to-day. The gap
between today and target is tooling (auto-PR close, labeled state transitions, blog pipeline, community-support
dialogue) — not design. See `CONTRIBUTING.md` for the full participation model and current-state-vs-target breakdown.

If the thesis holds, this becomes a reference implementation for how other open-source projects can be maintained the
same way. If it doesn't, the project learns exactly where it breaks and publishes the failure modes.

---

## Target Audience

### Individual Practitioners

Developers, researchers, and hobbyists who want autonomous agents running a project — whether that is a software
development project, a research workflow, a content pipeline, or any other goal-directed work that benefits from
persistent, self-directed automation. The barrier to entry should be low: clone the repo, configure an agent, deploy
with Helm.

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
- Containers should move toward stateless, horizontally scalable operation where the runtime semantics allow it; today's
  named-agent pods intentionally run as singletons until scheduler state and backend session storage are externalized.
- Configuration is injected via environment variables and mounted volumes — never baked into the image
- Agents expose standard HTTP endpoints suitable for Kubernetes `Service` and `Ingress` resources

### Deployment Roadmap

| Artifact            | Status             | Description                                                                                  |
| ------------------- | ------------------ | -------------------------------------------------------------------------------------------- |
| Helm chart          | Shipped            | Parameterized Kubernetes deployment                                                          |
| Kubernetes Operator | Shipped (v1alpha1) | Declarative agent lifecycle management via `WitwaveAgent` / `WitwavePrompt` CRDs (per-agent) |

---

## Support Workloads

The core platform is the agent runtime. The following support workloads surround it:

### Shipped

- **UI** — Vue 3 + PrimeVue web dashboard (`clients/dashboard/`) for monitoring agent activity, reviewing logs,
  triggering ad-hoc prompts, and managing GitHub Issues — aimed at the individual and small-team audience. Enabled via
  the Helm chart's `dashboard:` block.
- **Metrics aggregation** — per-service `/metrics` Prometheus endpoints, PodMonitor/ServiceMonitor templates, bundled
  Grafana dashboards (`charts/witwave/dashboards/`), and opinionated default alerts via `PrometheusRule`
  (`charts/witwave/templates/prometheusrule.yaml`). See F-008.

### On the Roadmap

- **Shared memory service** — a structured key-value store accessible to all agents on the team, replacing the current
  flat markdown memory files for data that requires reliable read/write semantics (see F-003)

---

## Design Principles

**Infrastructure-grade reliability.** Agents should behave like services: predictable startup, clean shutdown,
observable at all times, and recoverable from crashes without operator intervention.

**File-based configuration.** Agent identity, behavior, schedule, and skills are defined in mounted files — not compiled
in. A new agent is a new directory, not a new image.

**Standard protocols.** A2A for inter-agent communication, Prometheus for metrics, OpenTelemetry for tracing, Kubernetes
probes for health — this project should feel native to the infrastructure it runs on.

**Complexity is opt-in.** A single agent deployed with Helm should work out of the box. MCP servers, guardrails,
metrics, and HITL approval gates are all available but never required.

**Professional quality for everyone.** Enterprise features should not require an enterprise deployment. The individual
running a personal project deserves the same quality of tooling as a team deploying to production.

---

## Relationship to Other Docs

| Document                                             | Purpose                                                              |
| ---------------------------------------------------- | -------------------------------------------------------------------- |
| [competitive-landscape.md](competitive-landscape.md) | Reference products and research themes that inform feature decisions |
| [architecture.md](architecture.md)                   | Runtime structure, configuration model, and issue/skill layer        |
| `README.md`                                          | Quickstart and technical reference for running the project           |
