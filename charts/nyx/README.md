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

No values are currently defined. See [values.yaml](values.yaml).
