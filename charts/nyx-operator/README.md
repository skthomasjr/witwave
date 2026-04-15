# nyx-operator

Helm chart for the nyx operator — deploys the nyx-operator controller manager and CRDs.

> **Note:** This chart is a work in progress. CRD types and controller logic will be added as the operator is built out.

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
imagePullSecrets:
  - name: ghcr-credentials
```

Install the chart:

```bash
helm install nyx-operator oci://ghcr.io/skthomasjr/charts/nyx-operator --namespace nyx
```

Install a specific version:

```bash
helm install nyx-operator oci://ghcr.io/skthomasjr/charts/nyx-operator --version 0.1.0 --namespace nyx
```

## Uninstall

```bash
helm uninstall nyx-operator --namespace nyx
```

## Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Controller manager image repository | `ghcr.io/skthomasjr/images/nyx-operator` |
| `image.tag` | Image tag (defaults to chart appVersion) | `""` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `imagePullSecrets` | Image pull secrets for the controller pod | `[]` |
| `replicaCount` | Number of controller manager replicas | `1` |
| `serviceAccount.create` | Create a service account for the controller | `true` |
| `serviceAccount.annotations` | Annotations to add to the service account | `{}` |
| `resources` | CPU/memory resource requests and limits | `{}` |
| `nodeSelector` | Node selector for the controller pod | `{}` |
| `tolerations` | Tolerations for the controller pod | `[]` |
| `affinity` | Affinity rules for the controller pod | `{}` |
