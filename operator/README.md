# nyx-operator

A Kubernetes operator for the nyx platform. Provides the `NyxAgent` custom
resource, which deploys one named agent — a nyx-harness orchestrator plus one
or more backend sidecars (a2-claude, a2-codex, a2-gemini) — as a
`Deployment` + `Service` + optional `ConfigMap`, `HPA`, `PDB`, and `PVC`.

Built with Operator SDK v1.42 (Go). Mirrors the deployment shape of the
[nyx Helm chart](../charts/nyx/) and is intended as an alternative install
path once the CRD is stable. The Helm chart remains the supported install
method while the operator is in `v1alpha1`.

> **Status:** first pass. The `NyxAgent` type and reconciler are in place.
> Git-sync sidecars, cross-agent manifest, UI, and Ingress are deferred to
> a future `NyxPlatform` CRD — run the Helm chart alongside for those for now.

## Requirements

- Go 1.24+
- Operator SDK v1.42+
- `kubectl` against a cluster (kind, minikube, EKS, etc.) for `make install`
  and `make deploy`

## Getting Started

Build and install CRDs against the current kubeconfig context:

```bash
make generate           # regenerate DeepCopy
make manifests          # regenerate CRD YAML + RBAC
make install            # apply CRDs to the cluster
```

Run the controller locally (outside the cluster) against the current context:

```bash
make run
```

Build and push the manager image, then deploy it into the cluster:

```bash
make docker-build docker-push IMG=<registry>/nyx-operator:<tag>
make deploy IMG=<registry>/nyx-operator:<tag>
```

Apply the sample `NyxAgent`:

```bash
kubectl apply -k config/samples/
kubectl get nyxagent
```

Uninstall:

```bash
kubectl delete -k config/samples/
make undeploy
make uninstall
```

## The `NyxAgent` resource

One `NyxAgent` corresponds to one named agent (e.g. `iris`, `nova`, `kira`).
Its spec mirrors the per-agent shape used by the Helm chart's `agents[]`
list. See `config/samples/nyx_v1alpha1_nyxagent.yaml` for a minimal example
and `api/v1alpha1/nyxagent_types.go` for the full schema.

Owned resources per `NyxAgent`:

| Resource                    | When                                                     |
| --------------------------- | -------------------------------------------------------- |
| `Deployment`                | always                                                   |
| `Service` (ClusterIP)       | always                                                   |
| `ConfigMap` (agent)         | when `spec.config` is non-empty                          |
| `ConfigMap` (per backend)   | when a backend's `config` is non-empty                   |
| `PersistentVolumeClaim`     | when a backend's `storage.enabled` is true               |
| `HorizontalPodAutoscaler`   | when `spec.autoscaling.enabled` is true                  |
| `PodDisruptionBudget`       | when `spec.podDisruptionBudget.enabled` is true          |
| Dashboard `Deployment`/`Service` | when `spec.dashboard.enabled` is true (#470)        |

All owned resources carry `ownerReferences` pointing at the `NyxAgent`,
so deleting the CR cascades their deletion.

## Status

The controller writes the following status fields:

- `phase` — one of `Pending`, `Ready`, `Degraded`, `Error` (shown as a
  printer column).
- `readyReplicas` — mirrored from the Deployment's `status.readyReplicas`.
- `observedGeneration` — the spec generation most recently reconciled.
- `conditions` — `Available`, `Progressing`, and `ReconcileSuccess`
  following the standard Kubernetes condition convention.

## Deferred

The operator does **not** yet handle:

- Git-sync sidecars / git mappings (Helm chart's `gitSyncs` and `gitMappings`)
- Cross-agent `manifest.json` for peer discovery
- UI `Deployment`, `Service`, and `Ingress`
- Shared-storage `PVC` creation — only references to a pre-existing PVC
  are supported via `spec.sharedStorage.claimName`
- Admission webhooks for validation and defaulting

Track gaps as separate issues as they come up; the Helm chart covers
these in the interim.

The `nyx-operator` Helm chart provides an optional `ServiceMonitor` for
Prometheus Operator integration — see `charts/nyx-operator/README.md`
for the `serviceMonitor.*` values.

## Metrics

The manager exposes `/metrics` (controller-runtime default) on the port
configured by `--metrics-bind-address`. Standard reconcile / workqueue /
client-go counters come for free.

NyxAgent-specific domain metrics added on top (#471):

| Metric                                | Type    | Labels             | Meaning                                                                          |
| ------------------------------------- | ------- | ------------------ | -------------------------------------------------------------------------------- |
| `nyxagent_phase_transitions_total`    | counter | `from`, `to`       | Status.phase transitions (Pending → Ready, Ready → Degraded, etc.)               |
| `nyxagent_pvc_build_errors_total`     | counter | `backend`          | Backend PVC entries skipped due to invalid spec (e.g. `storage.size` parse fail) |
| `nyxagent_dashboard_enabled`          | gauge   | `namespace`, `name`| 1 when `spec.dashboard.enabled=true`, 0 otherwise. `sum()` for cluster total.    |

The dashboard gauge series is dropped on agent deletion; the two counters
persist (Prometheus convention — counters are monotonic).

OpenTelemetry tracing on the operator's reconcile loop is tracked
separately under #471 (the (A) half of that issue is implemented; the
OTel half is pending).

## License

Apache 2.0 — see [LICENSE](../LICENSE) (once present) for the full text.
