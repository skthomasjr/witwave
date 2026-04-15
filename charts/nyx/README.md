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
