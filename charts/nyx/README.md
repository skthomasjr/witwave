# nyx

Helm chart for the nyx platform — nyx harness and backends (a2-claude, a2-codex, a2-gemini).

> **Note:** This chart is a work in progress. Templates and values will be added as the chart is built out.

## Prerequisites

- Kubernetes 1.25+
- Helm 3.10+

## Installation

Create the namespace:

```bash
kubectl create namespace nyx
```

If your images are in a private registry (e.g. GHCR), create an image pull secret:

```bash
kubectl create secret docker-registry ghcr-credentials \
  --docker-server=ghcr.io \
  --docker-username=<github-username> \
  --docker-password=<github-token> \
  --namespace nyx
```

Then reference it in your values:

```yaml
agents:
  - name: adam
    imagePullSecrets:
      - name: ghcr-credentials
```

Create backend secrets for each agent. The required keys depend on which backends you are deploying — see the backend READMEs for details:

- [a2-claude secrets](../../a2-claude/README.md#secrets) — `ANTHROPIC_API_KEY`
- [a2-codex secrets](../../a2-codex/README.md#secrets) — `OPENAI_API_KEY`
- [a2-gemini secrets](../../a2-gemini/README.md#secrets) — `GEMINI_API_KEY`

Example for an agent named `adam` using the Claude backend:

```bash
kubectl create secret generic adam-claude-secrets \
  --from-literal=ANTHROPIC_API_KEY=sk-ant-... \
  --namespace nyx
```

Then reference the secret in your values:

```yaml
agents:
  - name: adam
    backends:
      - name: claude
        envFrom:
          - secretRef:
              name: adam-claude-secrets
```

Install the chart:

```bash
helm install nyx oci://ghcr.io/skthomasjr/charts/nyx --namespace nyx
```

Install a specific version:

```bash
helm install nyx oci://ghcr.io/skthomasjr/charts/nyx --version 0.1.0 --namespace nyx
```

## Uninstall

```bash
helm uninstall nyx --namespace nyx
```

## Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `agents` | List of agents to deploy, each as a nyx-harness pod | `[{name: adam}]` |
| `agents[].name` | Agent name — used for pod name, service name, and `AGENT_NAME` env var | `adam` |
| `agents[].imagePullSecrets` | Image pull secrets for the agent pod | `[]` |
| `dashboard.enabled` | Deploy the nyx web dashboard | `false` |
| `dashboard.image.repository` | Dashboard image repository | `ghcr.io/skthomasjr/images/dashboard` |
| `dashboard.port` | Dashboard service port | `80` |
| `ingress.enabled` | Deploy a Kubernetes Ingress for the dashboard (#528 — chart fails template render when enabled without an auth mechanism; see below) | `false` |
| `ingress.className` | Ingress class name (e.g. `nginx`, `traefik`) | `""` |
| `ingress.annotations` | Annotations to add to the Ingress resource | `{}` |
| `ingress.hosts` | Hostnames and paths for the Ingress | `[]` |
| `ingress.tls` | TLS configuration for the Ingress | `[]` |
| `ingress.auth.enabled` | Master toggle for the chart-managed auth block (renders the auth Secret and nginx-ingress `auth-*` annotations) | `true` |
| `ingress.auth.type` | Auth mechanism. Currently only `basic` is supported | `basic` |
| `ingress.auth.allowInsecure` | Explicit escape hatch: when `true`, skip the chart's auth block and render the Ingress with no auth. Intended for isolated networks or when a separate auth gateway is fronting the chart | `false` |
| `ingress.auth.basic.existingSecret` | Name of an existing Secret containing an htpasswd `auth` key. When set, the chart does not render its own Secret | `""` |
| `ingress.auth.basic.htpasswd` | Inline htpasswd line(s) rendered into a chart-managed Secret when `existingSecret` is empty. Multi-line strings are supported | `""` |
| `ingress.auth.basic.realm` | Browser auth-prompt realm displayed by nginx-ingress | `nyx dashboard` |
| `autoscaling.enabled` | Deploy a HorizontalPodAutoscaler per agent (Deployment omits `replicas` when enabled) | `false` |
| `autoscaling.minReplicas` | HPA minimum replicas | `1` |
| `autoscaling.maxReplicas` | HPA maximum replicas | `3` |
| `autoscaling.targetCPUUtilizationPercentage` | HPA CPU utilization target | `80` |
| `autoscaling.targetMemoryUtilizationPercentage` | HPA memory utilization target (optional) | unset |
| `podDisruptionBudget.enabled` | Deploy a PodDisruptionBudget per agent | `false` |
| `podDisruptionBudget.minAvailable` | Minimum available replicas during voluntary disruption | `1` |
| `podDisruptionBudget.maxUnavailable` | Alternative to `minAvailable` — max unavailable replicas | unset |
| `preStop.enabled` | Add a `lifecycle.preStop` sleep on every container so in-flight A2A requests, jobs, and webhook deliveries get a coordinated drain window before SIGTERM (#447) | `false` |
| `preStop.delaySeconds` | preStop sleep duration in seconds (must be less than `terminationGracePeriodSeconds`) | `30` |
| `probes.liveness.initialDelaySeconds` | Liveness probe initial delay | `10` |
| `probes.liveness.periodSeconds` | Liveness probe period | `30` |
| `probes.liveness.timeoutSeconds` | Liveness probe timeout | `5` |
| `probes.liveness.failureThreshold` | Liveness probe failure threshold | `3` |
| `probes.readiness.initialDelaySeconds` | Readiness probe initial delay | `5` |
| `probes.readiness.periodSeconds` | Readiness probe period | `10` |
| `probes.readiness.timeoutSeconds` | Readiness probe timeout | `5` |
| `probes.readiness.failureThreshold` | Readiness probe failure threshold | `3` |
| `metrics.enabled` | Enable Prometheus metrics globally | `false` |
| `serviceMonitor.enabled` | Create one Prometheus Operator `ServiceMonitor` per agent for metrics auto-discovery (requires `metrics.enabled=true` for that agent and the Prometheus Operator CRDs installed in the cluster) | `false` |
| `serviceMonitor.scrapeInterval` | Scrape interval for each ServiceMonitor endpoint | `30s` |
| `serviceMonitor.scrapeTimeout` | Scrape timeout for each ServiceMonitor endpoint | `10s` |
| `serviceMonitor.labels` | Extra labels merged into every ServiceMonitor (e.g. `release: kube-prometheus-stack` for Prometheus Operator's selector) | `{}` |
| `sharedStorage.enabled` | Mount a shared volume into all agent pods and sidecars | `false` |
| `sharedStorage.mountPath` | Mount path inside containers | `/data/shared` |
| `sharedStorage.storageType` | `pvc` or `hostPath` | `pvc` |
| `sharedStorage.size` | PVC storage request | `1Gi` |
| `sharedStorage.storageClassName` | Storage class for PVC (leave blank for cluster default) | `""` |
| `sharedStorage.accessModes` | PVC access modes | `[ReadWriteMany]` |
| `sharedStorage.existingClaim` | Use a pre-existing PVC instead of creating one | `""` |
| `sharedStorage.hostPath` | Host path when `storageType: hostPath` | `""` |

To deploy multiple agents:

```yaml
agents:
  - name: iris
  - name: nova
  - name: kira
```

## Security

### Dashboard Ingress — fail-closed (#528)

When `dashboard.enabled=true` and `ingress.enabled=true`, the nginx pod reverse-proxies every enabled agent's API
(conversations, triggers, jobs). An unauthenticated Ingress is effectively unauthenticated remote code execution
against the cluster, so this chart **fails template render** when `ingress.enabled=true` unless one of the following
is configured:

1. `ingress.auth.enabled=true` (chart-managed basic auth — the default).
2. `ingress.auth.allowInsecure=true` (explicit opt-out for isolated clusters / separate auth gateways).
3. `ingress.annotations` contains one of `nginx.ingress.kubernetes.io/auth-url`,
   `nginx.ingress.kubernetes.io/auth-signin`, or `traefik.ingress.kubernetes.io/router.middlewares` — indicating a
   user-supplied auth proxy / middleware is wired in.

When chart-managed basic auth is used, supply either `ingress.auth.basic.existingSecret` (pointing at a Secret that
already contains an htpasswd `auth` key) or `ingress.auth.basic.htpasswd` (inline). Generate htpasswd lines with
`htpasswd -nbB admin 'choose-a-strong-password'`.

### Pod security (#541)

Agent pods (harness + backends) and the dashboard pod both set `seccompProfile: RuntimeDefault` and run as non-root
(`runAsNonRoot: true`). This keeps the chart compatible with the Pod Security Standards "restricted" profile without
additional values.

### Backend `/mcp` auth parity

All three backends now require a bearer token on the `/mcp`, `/conversations`, and `/trace` endpoints (#510, #516,
#518). Include `CONVERSATIONS_AUTH_TOKEN` in each backend's envFrom Secret. If it is unset or empty, the backend logs
a startup warning (#517).

### Per-backend model override

`backends[].model` is now rendered into the backend container as the `BACKEND_MODEL` env var (#489) — previously the
value was dropped. Combined with the backend's own `<NAME>_MODEL` env var (`CLAUDE_MODEL`, `CODEX_MODEL`,
`GEMINI_MODEL`) and per-request routing overrides in nyx-harness, this lets the chart set the default model for a
backend without baking it into the image or a per-agent ConfigMap.
