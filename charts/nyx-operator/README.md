# nyx-operator

Helm chart for the nyx operator — deploys the nyx-operator controller manager
and the `NyxAgent` CRD.

## Prerequisites

- Kubernetes 1.25+
- Helm 3.10+

## Installation

Create the namespace:

```bash
kubectl create namespace nyx
```

If your images are in a private registry (e.g. GHCR), create an image pull secret
and reference it in values:

```bash
kubectl create secret docker-registry ghcr-credentials \
  --docker-server=ghcr.io \
  --docker-username=<github-username> \
  --docker-password=<github-token> \
  --namespace nyx
```

```yaml
imagePullSecrets:
  - name: ghcr-credentials
```

Install:

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

The `NyxAgent` CRD is annotated with `helm.sh/resource-policy: keep`, so it
is **not** deleted on uninstall. Existing `NyxAgent` resources remain in the
cluster and can be reconciled by a re-installed operator. Delete the CRD
manually if you want to fully tear down:

```bash
kubectl delete crd nyxagents.nyx.ai
```

## Values

| Parameter                    | Description                                                                                  | Default                                     |
| ---------------------------- | -------------------------------------------------------------------------------------------- | ------------------------------------------- |
| `image.repository`           | Controller manager image repository                                                          | `ghcr.io/skthomasjr/images/nyx-operator`    |
| `image.tag`                  | Image tag (defaults to `.Chart.AppVersion`)                                                  | `""`                                        |
| `image.pullPolicy`           | Image pull policy                                                                            | `IfNotPresent`                              |
| `imagePullSecrets`           | Image pull secrets for the controller pod                                                    | `[]`                                        |
| `replicaCount`               | Number of controller manager replicas                                                        | `1`                                         |
| `installCRDs`                | Install the `nyxagents.nyx.ai` CRD with the chart                                            | `true`                                      |
| `rbac.create`                | Create the ClusterRole/ClusterRoleBinding and leader-election Role/RoleBinding               | `true`                                      |
| `leaderElection.enabled`     | Pass `--leader-elect` to the manager and create a leader-election RoleBinding                | `true`                                      |
| `metrics.enabled`            | Expose controller-runtime metrics and create a ClusterIP Service for them                    | `false`                                     |
| `metrics.port`               | Metrics port                                                                                 | `8443`                                      |
| `metrics.secure`             | Serve metrics over HTTPS (self-signed unless cert-manager is wired in)                       | `true`                                      |
| `probes.port`                | Health/liveness probe bind address port                                                      | `8081`                                      |
| `serviceAccount.create`      | Create a ServiceAccount for the controller                                                   | `true`                                      |
| `serviceAccount.automount`   | Automount the SA token                                                                       | `true`                                      |
| `serviceAccount.annotations` | Annotations to add to the ServiceAccount                                                     | `{}`                                        |
| `serviceAccount.name`        | Use a pre-existing ServiceAccount name (auto-derived when empty)                             | `""`                                        |
| `podAnnotations`             | Annotations to add to the manager pod                                                        | `{}`                                        |
| `podLabels`                  | Labels to add to the manager pod                                                             | `{}`                                        |
| `podSecurityContext`         | Pod-level securityContext (defaults satisfy PSS "restricted")                                | see `values.yaml`                           |
| `securityContext`            | Container-level securityContext                                                              | `allowPrivilegeEscalation: false` + drop ALL|
| `resources`                  | CPU/memory requests and limits for the manager container                                     | 10m/64Mi requests, 500m/128Mi limits        |
| `extraArgs`                  | Additional command-line flags passed to `/manager`                                           | `[]`                                        |
| `nodeSelector`               | Node selector for the controller pod                                                         | `{}`                                        |
| `tolerations`                | Tolerations for the controller pod                                                           | `[]`                                        |
| `affinity`                   | Affinity rules for the controller pod                                                        | `{}`                                        |
