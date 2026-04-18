# nyx-operator

A Kubernetes operator for the nyx platform. Provides the `NyxAgent` custom
resource, which deploys one named agent — a harness orchestrator plus one
or more backend sidecars (claude, codex, gemini) — as a
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
make docker-build docker-push IMG=<registry>/operator:<tag>
make deploy IMG=<registry>/operator:<tag>
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
| `Deployment`                | always (when `spec.enabled != false`)                    |
| `Service`                   | always; type set by `spec.serviceType` (default ClusterIP) |
| `ConfigMap` (agent)         | when `spec.config` is non-empty                          |
| `ConfigMap` (per backend)   | when a backend's `config` is non-empty (and `enabled != false`) |
| `PersistentVolumeClaim`     | when a backend's `storage.enabled` is true (and `enabled != false`) |
| `HorizontalPodAutoscaler`   | when `spec.autoscaling.enabled` is true                  |
| `PodDisruptionBudget`       | when `spec.podDisruptionBudget.enabled` is true          |
| Dashboard `Deployment`/`Service` | when `spec.dashboard.enabled` is true (#470)        |

When `spec.enabled` is explicitly false, every owned resource above is
torn down (only resources owned via `IsControlledBy` are touched).
Per-backend `backends[].enabled: false` skips that backend's container,
PVC, and ConfigMap while leaving the rest of the agent untouched.

Pod-level Prometheus scrape annotations are emitted onto the Pod template
when `spec.metrics.enabled && spec.metrics.podAnnotations` is true; the
Service-level equivalents are gated on `spec.metrics.serviceAnnotations`
(default true).

All owned resources carry `ownerReferences` pointing at the `NyxAgent`,
so deleting the CR cascades their deletion.

## The `NyxPrompt` resource

One `NyxPrompt` declares a single prompt definition that binds to one or
more `NyxAgent`s. The operator reconciles a `ConfigMap` per
`(NyxPrompt, agent)` pair; the NyxAgent pod mounts each ConfigMap as a
subPath file at `/home/agent/.nyx/<kind>/nyxprompt-<name>.md` (or
`HEARTBEAT.md` for kind=heartbeat) so the harness scheduler picks them up
alongside anything gitSync dropped into the same directory.

See `config/samples/nyx_v1alpha1_nyxprompt.yaml` for a runnable example
and `api/v1alpha1/nyxprompt_types.go` for the full schema.

### Kinds

Each kind maps to one of the harness scheduler directories (or the
singleton heartbeat file):

| `spec.kind`    | Target path                                                  | Required frontmatter |
| -------------- | ------------------------------------------------------------ | -------------------- |
| `job`          | `/home/agent/.nyx/jobs/nyxprompt-<name>.md`                  | `schedule` (cron)    |
| `task`         | `/home/agent/.nyx/tasks/nyxprompt-<name>.md`                 | `schedule` (cron)    |
| `trigger`      | `/home/agent/.nyx/triggers/nyxprompt-<name>.md`              | `endpoint`           |
| `continuation` | `/home/agent/.nyx/continuations/nyxprompt-<name>.md`         | `continues-after` (string or list) |
| `webhook`      | `/home/agent/.nyx/webhooks/nyxprompt-<name>.md`              | `url`                |
| `heartbeat`    | `/home/agent/.nyx/HEARTBEAT.md` (singleton per agent)        | none                 |

### Multi-bind

`spec.agentRefs[]` lists every NyxAgent the prompt binds to. The operator
renders one ConfigMap per agent (name pattern
`nyxprompt-<crname>-<agent>`) with owner-reference cascade and stale-
binding garbage collection. An optional `filenameSuffix` on each ref
disambiguates when the same CR binds to multiple agents that already
have a gitSync-managed prompt sharing the default filename.

### Admission webhook invariants

The `ValidatingWebhookConfiguration` enforces:

- Kind-specific required frontmatter keys (see table above)
- `continues-after` must be a non-empty string or list of strings
- Duplicate `agentRefs` entries rejected
- `kind: heartbeat` is singleton-per-agent — no two NyxPrompts can both
  target the same agent with heartbeat, since the harness reads a single
  `HEARTBEAT.md` and the writes would race

When admission webhooks are disabled (cert-manager not installed + no
BYO-cert set — see "Admission webhook TLS" below) the CRD's structural
schema is the only validation and these invariants are not enforced.

### Status

Each reconcile writes `.status` via the subresource:

- `observedGeneration` — spec generation most recently reconciled
- `readyCount` — number of bindings whose ConfigMap applied cleanly
- `bindings[]` — one entry per `spec.agentRefs`, keyed by `agentName`,
  with `configMapName`, `filename`, `ready`, and a `message` when a
  binding failed (e.g. "target NyxAgent not found")
- `conditions[]` — one `Ready` condition, `True` when every binding is
  ready

The reconciler tracks materialization (did the ConfigMap apply?) — NOT
runtime execution (did the prompt actually fire?). Execution telemetry
lives in Prometheus / conversation.jsonl / trace.jsonl / the dashboard
views. See request
[#642](https://github.com/skthomasjr/autonomous-agent/issues/642) for
the runtime-status proposal.

## Credentials

Both `GitSyncSpec.credentials` and `BackendSpec.credentials` accept the
same three-mode resolver (parity with the Helm chart's
`nyx.resolveCredentials` helper):

```yaml
# Production: reference a pre-created Secret
gitSyncs:
  - name: autonomous-agent
    repo: https://github.com/org/repo
    credentials:
      existingSecret: agent-github-credentials   # must contain GITSYNC_USERNAME + GITSYNC_PASSWORD

# Dev: inline values. Operator reconciles a Secret named
# <agent>-<entry>-gitsync-credentials owned by the NyxAgent (GC'd on
# removal). acknowledgeInsecureInline MUST be true or the admission
# webhook rejects the CR — inline values land in etcd + `kubectl get
# nyxagent -o yaml`, so the ack flag is the explicit opt-in.
gitSyncs:
  - name: autonomous-agent
    repo: https://github.com/org/repo
    credentials:
      username: dev-user
      token: ghp_...
      acknowledgeInsecureInline: true

# Legacy: raw envFrom (unchanged — works when credentials is empty)
gitSyncs:
  - name: autonomous-agent
    envFrom:
      - secretRef:
          name: my-existing-secret
```

The backend equivalent (`BackendSpec.credentials`) uses a `secrets:` map
instead of `username`/`token` so each backend can set its own env-var
shape (`CLAUDE_CODE_OAUTH_TOKEN`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`).

Operator-managed credential Secrets carry `app.kubernetes.io/component:
credentials` and are dual-checked (label + `IsControlledBy`) before any
update or delete — user-created Secrets that collide by name are never
touched.

## Admission webhook TLS

The controller-manager's admission webhook server (port 9443) needs a
TLS cert. The `nyx-operator` Helm chart supports two modes:

- **cert-manager** (default): chart renders a Certificate + Issuer, CA
  bundle auto-injected into the webhook configs
- **BYO Secret**: set `webhooks.existingSecret` + `webhooks.caBundle`
  to reference a pre-created Secret with `tls.crt` + `tls.key`. Use
  this when cert-manager isn't available (air-gapped, service mesh,
  Vault PKI, strict RBAC).

Full setup in `charts/nyx-operator/README.md` — both modes are covered
with copy-paste snippets.

## `NyxAgent` status

The controller writes the following status fields:

- `phase` — one of `Pending`, `Ready`, `Degraded`, `Error` (shown as a
  printer column).
- `readyReplicas` — mirrored from the Deployment's `status.readyReplicas`.
- `observedGeneration` — the spec generation most recently reconciled.
- `conditions` — `Available`, `Progressing`, and `ReconcileSuccess`
  following the standard Kubernetes condition convention.

## Chart / operator feature fidelity

The Helm chart (`charts/nyx`) and this operator render equivalent
workloads for the same inputs. Remaining by-design asymmetries:

| Concept                          | Chart | Operator | Notes |
| -------------------------------- | ----- | -------- | ----- |
| `NyxPrompt` CRD                  | —     | ✓        | Operator-only; chart path uses gitSync mappings to materialise prompts. |
| Dashboard `Ingress` + basic auth | ✓     | —        | Operator delegates: BYO `Ingress` / `HTTPRoute` / `Route` pointing at the `<agent>-dashboard` Service. Matches Strimzi / cert-manager / Argo / ECK convention. |
| Trigger Ingress (external webhooks reaching `/triggers/*`) | — | — | Neither path emits it. Users hand-roll or use service mesh routing. Design discussion is [request #trigger-ingress](https://github.com/skthomasjr/autonomous-agent/issues) (pending filing). |

Tracked open requests (not gaps):

| Topic                                            | Issue | State |
| ------------------------------------------------ | ----- | ----- |
| NyxPrompt runtime execution status on CR         | [#642](https://github.com/skthomasjr/autonomous-agent/issues/642) | request, Ready: false |

Recently closed — shipped in the operator:

| Topic                                            | Issue | Landed |
| ------------------------------------------------ | ----- | ------ |
| MCP tools streamable-http + chart-deployed       | [#644](https://github.com/skthomasjr/autonomous-agent/issues/644) | `564ae83` |
| Metrics on dedicated :9000 listener (9-gap series) | [#643](https://github.com/skthomasjr/autonomous-agent/issues/643) (→ [#645-#653](https://github.com/skthomasjr/autonomous-agent/issues/645)) | `9b935e7` + `2c571c4` |

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

### Prometheus Operator integration

The operator reconciles two optional CRs when the
`monitoring.coreos.com/v1` API group is installed on the cluster:

- `ServiceMonitor` via `spec.serviceMonitor.enabled` (#476) — scrapes the
  harness's `/metrics` endpoint through the per-agent Service
- `PodMonitor` via `spec.podMonitor.enabled` (#582) — scrapes each
  backend container's `/metrics` directly on the pod via the named
  `<backend>-metrics` port (backend-level telemetry like tokens, tool
  use, context usage)

Both use an unstructured client so the operator build has no hard
dependency on prometheus-operator Go types. When the CRD is absent the
reconciler logs once per reconcile and no-ops; reconciliation resumes
automatically once the CRDs appear.

## Tracing (OpenTelemetry)

When `OTEL_ENABLED=true`, the manager emits one server span per
`Reconcile()` call (`nyxagent.reconcile`) attributed with `nyx.namespace`,
`nyx.name`, and the resulting `nyx.phase`. Errors are recorded on the span
so collectors flag them red. W3C trace-context propagator matches the
Python side (#468/#469) so inbound `traceparent` headers propagate across
the agent boundary.

Standard OTel env vars apply: `OTEL_EXPORTER_OTLP_ENDPOINT`,
`OTEL_SERVICE_NAME` (defaults to `nyx-operator`), `OTEL_TRACES_SAMPLER`.
`POD_NAMESPACE` and `POD_NAME` (set via downward API in the chart's pod
spec) feed `k8s.namespace` and `k8s.pod.name` resource attributes.

When `OTEL_ENABLED` is unset/false (default), the no-op tracer takes over
and the per-reconcile overhead is a single branch + interface dispatch.

**The operator does not deploy an OpenTelemetry Collector.** Matching the
idiomatic pattern across Strimzi, cert-manager, Istio, Elastic ECK,
Knative, Argo, Crossplane, and grafana-operator, nyx-operator emits OTLP
to a user-provided endpoint and delegates collector deployment to
something purpose-built for that job:

- **Recommended:** install the
  [opentelemetry-operator](https://github.com/open-telemetry/opentelemetry-operator)
  and create an `OpenTelemetryCollector` CR, then point
  `OTEL_EXPORTER_OTLP_ENDPOINT` at the resulting Service.
- **Alternative:** point at any OTLP-compatible target directly —
  Jaeger, Tempo, Honeycomb, Grafana Cloud, Datadog.

Wiring per NyxAgent:

```yaml
spec:
  env:
    - name: OTEL_ENABLED
      value: "true"
    - name: OTEL_EXPORTER_OTLP_ENDPOINT
      value: http://otel-collector.observability:4318
```

Client-go HTTP transport instrumentation (so Kubernetes API calls emit
child spans) is a separate enhancement — not yet implemented; tracked
follow-on if needed.

## License

Apache 2.0 — see [LICENSE](../LICENSE) (once present) for the full text.
