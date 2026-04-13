# nyx

Helm chart for the nyx platform — nyx orchestrator and backends (a2-claude, a2-codex, a2-gemini).

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
| `agents` | List of agents to deploy, each as a nyx-agent pod | `[{name: adam}]` |
| `agents[].name` | Agent name — used for pod name, service name, and `AGENT_NAME` env var | `adam` |
| `agents[].imagePullSecrets` | Image pull secrets for the agent pod | `[]` |

To deploy multiple agents:

```yaml
agents:
  - name: iris
  - name: nova
  - name: kira
```
