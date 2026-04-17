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
| `rbac.scope`                 | `cluster` (install a ClusterRole + ClusterRoleBinding, operator watches all namespaces) or `namespace` (install a Role + RoleBinding per `rbac.watchNamespaces` entry; ClusterRole is skipped) (#532) | `cluster`                       |
| `rbac.watchNamespaces`       | Namespaces the operator watches when `rbac.scope=namespace`. Each entry receives a per-namespace Role + RoleBinding. Empty falls back to the release namespace. Ignored when `rbac.scope=cluster` | `[]`                               |
| `leaderElection.enabled`     | Pass `--leader-elect` to the manager and create a leader-election RoleBinding                | `true`                                      |
| `metrics.enabled`            | Expose controller-runtime metrics and create a ClusterIP Service for them                    | `false`                                     |
| `metrics.port`               | Metrics port                                                                                 | `8443`                                      |
| `metrics.secure`             | Serve metrics over HTTPS (self-signed unless cert-manager is wired in)                       | `true`                                      |
| `serviceMonitor.enabled`     | Create a Prometheus Operator `ServiceMonitor` for metrics auto-discovery (requires `metrics.enabled=true` and the Prometheus Operator CRDs installed in the cluster) | `false` |
| `serviceMonitor.scrapeInterval` | Scrape interval for the ServiceMonitor endpoint                                            | `30s`                                      |
| `serviceMonitor.scrapeTimeout`  | Scrape timeout for the ServiceMonitor endpoint                                             | `10s`                                      |
| `serviceMonitor.labels`         | Extra labels merged into the ServiceMonitor (e.g. `release: kube-prometheus-stack` for Prometheus Operator's selector)                                          | `{}`                                       |
| `serviceMonitor.tlsConfig`      | TLS config for the scrape (used only when `metrics.secure=true`); defaults to `insecureSkipVerify: true` for the manager's self-signed cert                    | see `values.yaml`                          |
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
| `extraArgs`                  | Additional command-line flags passed to `/manager`. Pair with `rbac.scope=namespace` by adding `--watch-namespaces=<csv>` so controller-runtime's cache matches the RBAC blast radius (#532) | `[]`                |
| `terminationGracePeriodSeconds` | Pod termination grace period. Must be > `preStop.delaySeconds` so SIGTERM fires with enough remaining time for controller-runtime's graceful shutdown (leader-lease release, in-flight reconcile drain) before SIGKILL (#512) | `30` |
| `preStop.enabled`            | Add a `lifecycle.preStop` sleep on the manager container so in-flight reconciliations drain before SIGTERM (#465)                                                   | `false`                                     |
| `preStop.delaySeconds`       | preStop sleep duration in seconds. Keep strictly less than `terminationGracePeriodSeconds`                                                                          | `5`                                         |
| `nodeSelector`               | Node selector for the controller pod                                                         | `{}`                                        |
| `tolerations`                | Tolerations for the controller pod                                                           | `[]`                                        |
| `affinity`                   | Affinity rules for the controller pod                                                        | `{}`                                        |
| `webhooks.enabled`           | Render the admission webhook Service, Mutating/Validating webhook configs, and cert-manager resources. Requires cert-manager installed in the cluster (#624) | `false` |
| `webhooks.port`              | Container port for the webhook server (matches controller-runtime default)                   | `9443`                                      |
| `webhooks.certDir`           | Directory the cert-manager-issued TLS pair is mounted at inside the container                | `/tmp/k8s-webhook-server/serving-certs`     |
| `webhooks.failurePolicy`     | Webhook failure policy. `Fail` rejects admission on error; `Ignore` allows it. Safer default for invariant checks | `Fail` |
| `webhooks.certManager.enabled` | cert-manager integration toggle. Required when `webhooks.enabled=true` in this first pass — BYO-cert installs aren't supported until a later gap | `true` |
| `webhooks.certManager.createIssuer` | Render a chart-owned selfSigned Issuer. Ignored when `webhooks.certManager.issuerRef.name` is set | `true`                                   |
| `webhooks.certManager.issuerKind` | Kind of Issuer to render when `createIssuer=true`. `Issuer` or `ClusterIssuer`           | `Issuer`                                    |
| `webhooks.certManager.issuerRef.name` | Name of an external (Cluster)Issuer to use instead of a chart-owned one              | `""`                                        |
| `webhooks.certManager.issuerRef.kind` | Kind of the external issuer                                                          | `""`                                        |

## Admission webhook (#624)

When `webhooks.enabled=true`, the chart renders a webhook `Service`, `MutatingWebhookConfiguration`, and
`ValidatingWebhookConfiguration` resources. The controller-manager needs a TLS serving cert; the chart supports two
modes.

### TLS mode 1: cert-manager (default)

```yaml
webhooks:
  enabled: true
  certManager:
    enabled: true
    createIssuer: true
```

The chart renders a cert-manager `Certificate` + selfSigned `Issuer` (or references an external `Issuer` /
`ClusterIssuer` via `certManager.issuerRef`). The CA bundle is auto-injected into the webhook configs via the
`cert-manager.io/inject-ca-from` annotation — no base64 blobs in values.

### TLS mode 2: BYO Secret

For air-gapped clusters, service-mesh-managed TLS, existing Vault PKI, or any environment where cert-manager isn't
appropriate:

```yaml
webhooks:
  enabled: true
  existingSecret: my-webhook-serving-cert   # Secret pre-created with tls.crt + tls.key
  caBundle: LS0tLS1CRUdJTi...               # base64 of the PEM CA that signed tls.crt
  certManager:
    enabled: false
```

Pre-create the Secret:

```bash
# assuming you have ca.crt, tls.crt, tls.key on disk signed by your CA
kubectl create secret generic my-webhook-serving-cert \
  -n nyx-operator-system \
  --from-file=tls.crt=./tls.crt \
  --from-file=tls.key=./tls.key

# then encode the CA for the caBundle value
base64 -w0 < ca.crt
```

When `existingSecret` is set, the chart skips all cert-manager integration (no `Certificate`, no `Issuer`, no
CA-injection annotation) and stamps the user-supplied `caBundle` literally onto the webhook configs' `caBundle`
field. The DNS SANs on `tls.crt` must include `<release>-webhook.<namespace>.svc` and
`<release>-webhook.<namespace>.svc.cluster.local` or the apiserver will refuse to call the webhook.

### Invariants enforced

Initial scaffold ships **one defaulting rule** (populate `spec.port=8080` when unset) and **one validating rule**
(reject duplicate backend names in `spec.backends`). Further invariants land as follow-up gaps on top of this
skeleton.

### Values shape

Mirrors the primary `nyx` chart's cert-manager block (#639) so operators running both charts side-by-side see
identical knobs for `certManager.enabled`, `createIssuer`, `issuerKind`, and `issuerRef.{name,kind}`.

### Disabled (default)

`webhooks.enabled=false` skips every webhook resource entirely; the controller runs without admission webhooks and
`cmd/main.go` logs a note at startup. CR validation falls back to CRD structural-schema checks only.

## Namespace-scoped RBAC (#532)

By default the operator installs a ClusterRole + ClusterRoleBinding and watches `NyxAgent` resources cluster-wide.
For multi-tenant clusters or least-privilege rollouts, switch to namespace-scoped RBAC:

```yaml
rbac:
  scope: namespace
  watchNamespaces:
    - tenant-a
    - tenant-b

extraArgs:
  - --watch-namespaces=tenant-a,tenant-b
```

In namespace mode the chart renders a `Role` + `RoleBinding` pair per entry in `watchNamespaces` (falling back to the
release namespace when the list is empty) and **does not** create a `ClusterRole`/`ClusterRoleBinding`. Always pair
it with `--watch-namespaces` in `extraArgs` so controller-runtime's informer cache matches — otherwise the operator's
watches will hit RBAC errors the moment it tries to list outside the permitted namespaces.

## Graceful shutdown (#465, #512)

`terminationGracePeriodSeconds` (default `30`) and the optional `preStop.delaySeconds` sleep are parameterised so the
manager has enough time to release its leader lease and drain in-flight reconciles before SIGKILL. Keep
`preStop.delaySeconds` strictly less than `terminationGracePeriodSeconds`.
