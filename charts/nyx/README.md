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
| `ui.enabled` | Deploy the nyx web UI | `false` |
| `ui.image.repository` | UI image repository | `ghcr.io/skthomasjr/images/ui` |
| `ui.port` | UI service port | `80` |
| `ui.corsAllowOrigin` | `Access-Control-Allow-Origin` value on UI static responses | `"*"` |
| `ui.connectSrc` | CSP `connect-src` directive — restrict which origins UI scripts may contact | `"*"` |
| `ui.securityContext` | Pod-level securityContext for the UI Deployment (opt-in; unset preserves stock nginx:alpine compatibility) | unset |
| `ingress.enabled` | Deploy a Kubernetes Ingress for the UI | `false` |
| `ingress.className` | Ingress class name (e.g. `nginx`, `traefik`) | `""` |
| `ingress.annotations` | Annotations to add to the Ingress resource | `{}` |
| `ingress.hosts` | Hostnames and paths for the Ingress | `[]` |
| `ingress.tls` | TLS configuration for the Ingress | `[]` |
| `autoscaling.enabled` | Deploy a HorizontalPodAutoscaler per agent (Deployment omits `replicas` when enabled) | `false` |
| `autoscaling.minReplicas` | HPA minimum replicas | `1` |
| `autoscaling.maxReplicas` | HPA maximum replicas | `3` |
| `autoscaling.targetCPUUtilizationPercentage` | HPA CPU utilization target | `80` |
| `autoscaling.targetMemoryUtilizationPercentage` | HPA memory utilization target (optional) | unset |
| `podDisruptionBudget.enabled` | Deploy a PodDisruptionBudget per agent | `false` |
| `podDisruptionBudget.minAvailable` | Minimum available replicas during voluntary disruption | `1` |
| `podDisruptionBudget.maxUnavailable` | Alternative to `minAvailable` — max unavailable replicas | unset |
| `probes.liveness.initialDelaySeconds` | Liveness probe initial delay | `10` |
| `probes.liveness.periodSeconds` | Liveness probe period | `30` |
| `probes.liveness.timeoutSeconds` | Liveness probe timeout | `5` |
| `probes.liveness.failureThreshold` | Liveness probe failure threshold | `3` |
| `probes.readiness.initialDelaySeconds` | Readiness probe initial delay | `5` |
| `probes.readiness.periodSeconds` | Readiness probe period | `10` |
| `probes.readiness.timeoutSeconds` | Readiness probe timeout | `5` |
| `probes.readiness.failureThreshold` | Readiness probe failure threshold | `3` |
| `metrics.enabled` | Enable Prometheus metrics globally | `false` |
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
