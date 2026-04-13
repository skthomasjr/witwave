# nyx

Helm chart for the nyx autonomous agent platform — nyx orchestrator and backends (a2-claude, a2-codex, a2-gemini).

> **Note:** This chart is a work in progress. Templates and values will be added as the chart is built out.

## Prerequisites

- Kubernetes 1.25+
- Helm 3.10+

## Installation

Create the namespace:

```bash
kubectl create namespace nyx
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

To deploy multiple agents:

```yaml
agents:
  - name: iris
  - name: nova
  - name: kira
```
