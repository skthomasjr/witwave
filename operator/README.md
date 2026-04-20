# witwave-operator

A Kubernetes operator for the witwave platform. Provides the `WitwaveAgent` custom
resource, which deploys one named agent — a harness orchestrator plus one
or more backend sidecars (claude, codex, gemini) — as a
`Deployment` + `Service` + optional `ConfigMap`, `HPA`, `PDB`, and `PVC`.

Built with Operator SDK v1.42 (Go). Mirrors the deployment shape of the
[witwave Helm chart](../charts/witwave/) and is intended as an alternative install
path once the CRD is stable. The Helm chart remains the supported install
method while the operator is in `v1alpha1`.

> **Status:** v1alpha1, published to GHCR, installable via the `ww`
> CLI or raw Helm. `WitwaveAgent` and `WitwavePrompt` resources are
> both in-tree. Git-sync sidecars render as part of the agent
> Deployment. Dashboard and Ingress still live in the agent Helm
> chart (`charts/witwave`) — a future `WitwavePlatform` CRD would
> subsume those, but today the two-chart split is the supported
> shape.

## Requirements

- `kubectl` against a cluster (kind, minikube, EKS, etc.) — the runtime target.
- For developing the controller itself: Go 1.24+ and Operator SDK v1.42+.

## Getting Started — the `ww` CLI path (recommended for users)

The fastest install path is via the [`ww`](../clients/ww/) CLI, which
ships with the operator chart embedded. No Helm repo, no `helm`
binary, no clone of this repo required:

```bash
brew install witwave-ai/homebrew-ww/ww
ww operator install              # installs into witwave-system
ww operator status               # verify
```

`ww operator install` runs singleton detection (refuses when another
release exists), RBAC preflight via `SelfSubjectAccessReview`, and
Helm install of the embedded chart. `ww operator upgrade` runs a CRD
server-side-apply pre-step so new CRD fields land before the pod
rolls. Paired diagnostics:

```bash
ww operator logs      # tail operator pod logs
ww operator events    # Kubernetes events for operator + CRs
```

See [`clients/ww/README.md`](../clients/ww/README.md#operator-management)
for the full command surface (`--kubeconfig`, `--context`, `--namespace`,
`--yes`, `--dry-run`, `--adopt`, `--delete-crds`, `--force`, `--watch`,
`--warnings`, `--tail`).

## Getting Started — Helm directly

For users who prefer Helm (GitOps pipelines, custom values, chart
forks), the chart is published to GHCR:

```bash
helm install witwave-operator oci://ghcr.io/skthomasjr/charts/witwave-operator \
  --version <tag> --namespace witwave-system --create-namespace
```

See [charts/witwave-operator/README.md](../charts/witwave-operator/README.md)
for the full values reference.

## Getting Started — developing the controller

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

Apply the sample `WitwaveAgent`:

```bash
kubectl apply -k config/samples/
kubectl get witwaveagent
```

Uninstall (development target):

```bash
kubectl delete -k config/samples/
make undeploy
make uninstall
```

For a ww-installed operator, use `ww operator uninstall` instead.

## Migrating from the agent chart (`charts/witwave`)

The operator and the agent Helm chart (`charts/witwave`) are two
independent deployment paths that both produce agent Deployments.
Running BOTH against overlapping agent names in the same namespace
produces duplicate resources — one named `{release}-{agent}` (from
the chart) and one named `{agent}` (from the operator) — each with
its own pods, HPA, PDB, configs. No silent corruption happens, but
you'll have doubled resources split-brained across two controllers.
Pick one.

Tracked caveat: this is a known footgun on deliberate migration,
not a defect in either path. Tracked for polish as
[#1478](https://github.com/skthomasjr/witwave/issues/1478). The
procedure below is the supported migration path today.

### Chart → operator

Premise: you have agents running via `helm install <release>
./charts/witwave -f values.yaml` and want to adopt the operator.

1. **Inventory what you're migrating.** List Deployments, Services,
   ConfigMaps, PVCs, and Secrets produced by the chart for each
   agent you're moving. `kubectl get all,cm,pvc,secret -l
   app.kubernetes.io/part-of=witwave -n <ns>` covers most of it.

2. **Preserve PVC data if any.** Backend conversation logs and
   session memory live on per-agent PVCs. The chart's PVCs survive
   `helm uninstall` by default — verify your PVC reclaim policy is
   `Retain` (not `Delete`) before step 3. `kubectl get pvc -n <ns>`
   → check each PVC's StorageClass reclaim policy.

3. **Uninstall the chart.** `helm uninstall <release> -n <ns>`.
   This removes Deployments + Services + ConfigMaps but leaves
   PVCs intact (per step 2). Agent pods stop serving traffic at
   this moment.

4. **Install the operator** if you haven't already:
   `ww operator install` (see the ww CLI path above).

5. **Write a `WitwaveAgent` CR per agent**, naming the CR with
   exactly the bare agent name — NOT the prefixed `{release}-{name}`.
   Point `spec.storage.existingClaim` at each preserved PVC from
   step 2 so the new operator-managed pod adopts the existing
   conversation + memory data.

   ```yaml
   apiVersion: witwave.ai/v1alpha1
   kind: WitwaveAgent
   metadata:
     name: iris          # bare name; NO `prod-` prefix
     namespace: <ns>
   spec:
     backends:
       - name: claude
         storage:
           existingClaim: prod-iris-claude-memory   # your surviving PVC
         credentials:
           existingSecret: ...                      # pre-provisioned
   ```

6. **Apply the CRs.** `kubectl apply -f witwaveagent-*.yaml`.
   Operator reconciles each, creates new Deployments (named bare
   `iris`, not `prod-iris`), mounts the preserved PVCs.

7. **Verify with `ww operator status`** — pods Running, CRs Ready,
   no leftover chart-rendered resources in `kubectl get
   deployments`.

### What's preserved vs lost on migration

| Preserved | Lost |
|---|---|
| Backend conversation logs on PVC | In-memory session state (pods restart) |
| `.witwave/` prompts materialised via git-sync if the new pod mounts the same source | A2A request queues mid-flight during step 3 |
| Referenced Secrets (if `existingSecret`) | Nothing that wasn't already lost on any pod restart |

No data copying required — the storage class does the work. The
downtime is however long it takes to apply the CRs and let the
operator reconcile: typically 30 seconds to a few minutes.

### Operator → chart (reverse migration)

Rare, but the shape is the same with sides swapped. Delete
`WitwaveAgent` CRs (the operator GC's owned resources on CR delete),
then `helm install <release> ./charts/witwave` with matching agent
names + `existingClaim` pointing at the surviving PVCs. The new
resources will be named `{release}-{agent}` per the chart
convention.

### Why both paths produce different names

The agent chart prefixes Deployments with the Helm release name
because multiple chart installs coexist on one cluster (e.g.
`prod` + `test` namespaces with different release names). The
operator skips the prefix because it's a singleton-per-cluster
controller whose CR `metadata.name` already uniquely identifies
the agent. The two conventions don't interoperate; migration is
always a rename.

## The `WitwaveAgent` resource

One `WitwaveAgent` corresponds to one named agent (e.g. `iris`, `nova`, `kira`).
Its spec mirrors the per-agent shape used by the Helm chart's `agents[]`
list. See `config/samples/witwave_v1alpha1_witwaveagent.yaml` for a minimal example
and `api/v1alpha1/witwaveagent_types.go` for the full schema.

Owned resources per `WitwaveAgent`:

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

All owned resources carry `ownerReferences` pointing at the `WitwaveAgent`,
so deleting the CR cascades their deletion.

## The `WitwavePrompt` resource

One `WitwavePrompt` declares a single prompt definition that binds to one or
more `WitwaveAgent`s. The operator reconciles a `ConfigMap` per
`(WitwavePrompt, agent)` pair; the WitwaveAgent pod mounts each ConfigMap as a
subPath file at `/home/agent/.witwave/<kind>/witwaveprompt-<name>.md` (or
`HEARTBEAT.md` for kind=heartbeat) so the harness scheduler picks them up
alongside anything gitSync dropped into the same directory.

See `config/samples/witwave_v1alpha1_witwaveprompt.yaml` for a runnable example
and `api/v1alpha1/witwaveprompt_types.go` for the full schema.

### Kinds

Each kind maps to one of the harness scheduler directories (or the
singleton heartbeat file):

| `spec.kind`    | Target path                                                  | Required frontmatter |
| -------------- | ------------------------------------------------------------ | -------------------- |
| `job`          | `/home/agent/.witwave/jobs/witwaveprompt-<name>.md`                  | `schedule` (cron)    |
| `task`         | `/home/agent/.witwave/tasks/witwaveprompt-<name>.md`                 | `schedule` (cron)    |
| `trigger`      | `/home/agent/.witwave/triggers/witwaveprompt-<name>.md`              | `endpoint`           |
| `continuation` | `/home/agent/.witwave/continuations/witwaveprompt-<name>.md`         | `continues-after` (string or list) |
| `webhook`      | `/home/agent/.witwave/webhooks/witwaveprompt-<name>.md`              | `url`                |
| `heartbeat`    | `/home/agent/.witwave/HEARTBEAT.md` (singleton per agent)        | none                 |

### Multi-bind

`spec.agentRefs[]` lists every WitwaveAgent the prompt binds to. The operator
renders one ConfigMap per agent (name pattern
`witwaveprompt-<crname>-<agent>`) with owner-reference cascade and stale-
binding garbage collection. An optional `filenameSuffix` on each ref
disambiguates when the same CR binds to multiple agents that already
have a gitSync-managed prompt sharing the default filename.

### Admission webhook invariants

The `ValidatingWebhookConfiguration` enforces:

- Kind-specific required frontmatter keys (see table above)
- `continues-after` must be a non-empty string or list of strings
- Duplicate `agentRefs` entries rejected
- `kind: heartbeat` is singleton-per-agent — no two WitwavePrompts can both
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
  binding failed (e.g. "target WitwaveAgent not found")
- `conditions[]` — one `Ready` condition, `True` when every binding is
  ready

The reconciler tracks materialization (did the ConfigMap apply?) — NOT
runtime execution (did the prompt actually fire?). Execution telemetry
lives in Prometheus / conversation.jsonl / tool-activity.jsonl / the dashboard
views. See request
[#642](https://github.com/skthomasjr/witwave/issues/642) for
the runtime-status proposal.

## Credentials

Both `GitSyncSpec.credentials` and `BackendSpec.credentials` accept the
same three-mode resolver (parity with the Helm chart's
`witwave.resolveCredentials` helper):

```yaml
# Production: reference a pre-created Secret
gitSyncs:
  - name: witwave
    repo: https://github.com/org/repo
    credentials:
      existingSecret: agent-github-credentials   # must contain GITSYNC_USERNAME + GITSYNC_PASSWORD

# Dev: inline values. Operator reconciles a Secret named
# <agent>-<entry>-gitsync-credentials owned by the WitwaveAgent (GC'd on
# removal). acknowledgeInsecureInline MUST be true or the admission
# webhook rejects the CR — inline values land in etcd + `kubectl get
# witwaveagent -o yaml`, so the ack flag is the explicit opt-in.
gitSyncs:
  - name: witwave
    repo: https://github.com/org/repo
    credentials:
      username: dev-user
      token: ghp_...
      acknowledgeInsecureInline: true

# Legacy: raw envFrom (unchanged — works when credentials is empty)
gitSyncs:
  - name: witwave
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
TLS cert. The `witwave-operator` Helm chart supports two modes:

- **cert-manager** (default): chart renders a Certificate + Issuer, CA
  bundle auto-injected into the webhook configs
- **BYO Secret**: set `webhooks.existingSecret` + `webhooks.caBundle`
  to reference a pre-created Secret with `tls.crt` + `tls.key`. Use
  this when cert-manager isn't available (air-gapped, service mesh,
  Vault PKI, strict RBAC).

Full setup in `charts/witwave-operator/README.md` — both modes are covered
with copy-paste snippets.

## `WitwaveAgent` status

The controller writes the following status fields:

- `phase` — one of `Pending`, `Ready`, `Degraded`, `Error` (shown as a
  printer column).
- `readyReplicas` — mirrored from the Deployment's `status.readyReplicas`.
- `observedGeneration` — the spec generation most recently reconciled.
- `conditions` — `Available`, `Progressing`, and `ReconcileSuccess`
  following the standard Kubernetes condition convention.

## Field indexers and controller-runtime wiring

Two indexers feed the reconciler's reverse-lookup paths without triggering full-list fan-outs:

- `WitwavePromptAgentRefIndex` — indexes each `WitwavePrompt.spec.agentRefs[].name`, so a `WitwaveAgent` reconcile can
  enumerate every WitwavePrompt bound to it in O(1) for rebind / teardown.
- `WitwaveAgentTeamIndex` — indexes `WitwaveAgent` by team label so cross-agent views can resolve team membership
  without listing every WitwaveAgent in the namespace.

Leader election is on by default (`--leader-elect=true`). Multi-replica operator rollouts are safe without
additional flags.

## Per-container metrics port

Every managed container (harness, backends, MCP tools) exposes `/metrics` on `app_port + 1000` by default,
matching the chart (#687) and removing the need for per-container `MetricsPort` overrides on the WitwaveAgent
CR. The CRD's `MetricsPort` field is deprecated — set only if you need to override the convention.

## WitwavePrompt CRD installation

The WitwavePrompt CRD is wired into `config/crd/kustomization.yaml` alongside WitwaveAgent, so `make install` now
applies both CRDs in one pass.

## Chart / operator feature fidelity

The Helm chart (`charts/witwave`) and this operator render equivalent
workloads for the same inputs. Remaining by-design asymmetries:

| Concept                          | Chart | Operator | Notes |
| -------------------------------- | ----- | -------- | ----- |
| `WitwavePrompt` CRD                  | —     | ✓        | Operator-only; chart path uses gitSync mappings to materialise prompts. |
| Dashboard `Ingress` + basic auth | ✓     | —        | Operator delegates: BYO `Ingress` / `HTTPRoute` / `Route` pointing at the `<agent>-dashboard` Service. Matches Strimzi / cert-manager / Argo / ECK convention. |
| Trigger Ingress (external webhooks reaching `/triggers/*`) | — | — | Neither path emits it. Users hand-roll or use service mesh routing. Design discussion is [request #trigger-ingress](https://github.com/skthomasjr/witwave/issues) (pending filing). |

Tracked open requests (not gaps):

| Topic                                            | Issue | State |
| ------------------------------------------------ | ----- | ----- |
| WitwavePrompt runtime execution status on CR         | [#642](https://github.com/skthomasjr/witwave/issues/642) | request, Ready: false |

## Metrics

The manager exposes `/metrics` (controller-runtime default) on the port
configured by `--metrics-bind-address`. Standard reconcile / workqueue /
client-go counters come for free.

WitwaveAgent-specific domain metrics added on top (#471):

| Metric                                | Type    | Labels             | Meaning                                                                          |
| ------------------------------------- | ------- | ------------------ | -------------------------------------------------------------------------------- |
| `witwaveagent_phase_transitions_total`    | counter | `from`, `to`       | Status.phase transitions (Pending → Ready, Ready → Degraded, etc.)               |
| `witwaveagent_pvc_build_errors_total`     | counter | `backend`          | Backend PVC entries skipped due to invalid spec (e.g. `storage.size` parse fail) |
| `witwaveagent_dashboard_enabled`          | gauge   | `namespace`, `name`| 1 when `spec.dashboard.enabled=true`, 0 otherwise. `sum()` for cluster total.    |
| `witwaveagent_teardown_step_errors_total` | counter | `kind`, `reason`   | Per-kind teardown failures when `spec.enabled=false` or the CR is deleted; useful for alerting when cascade cleanup is partial |
| `witwaveprompt_status_patch_conflicts_total` | counter | `namespace`, `name` | `WitwavePrompt` status subresource patch 409 conflicts retried with fresh `resourceVersion` (#950). Sustained non-zero rate points at a noisy reconciler (too many concurrent writers) or cache lag under load. |

WitwavePrompt binding-outcome metrics (label schema `namespace`, `name`) track per-binding ConfigMap apply
results so operators can alert on chronically unready bindings.

The gauges (`witwaveagent_dashboard_enabled`, WitwavePrompt binding gauges) are dropped on resource deletion; the
counters persist (Prometheus convention — counters are monotonic).

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
`Reconcile()` call (`witwaveagent.reconcile`) attributed with `witwave.namespace`,
`witwave.name`, and the resulting `witwave.phase`. Errors are recorded on the span
so collectors flag them red. W3C trace-context propagator matches the
Python side (#468/#469) so inbound `traceparent` headers propagate across
the agent boundary.

Standard OTel env vars apply: `OTEL_EXPORTER_OTLP_ENDPOINT`,
`OTEL_SERVICE_NAME` (defaults to `witwave-operator`), `OTEL_TRACES_SAMPLER`.
`POD_NAMESPACE` and `POD_NAME` (set via downward API in the chart's pod
spec) feed `k8s.namespace` and `k8s.pod.name` resource attributes.

When `OTEL_ENABLED` is unset/false (default), the no-op tracer takes over
and the per-reconcile overhead is a single branch + interface dispatch.

**The operator does not deploy an OpenTelemetry Collector.** Matching the
idiomatic pattern across Strimzi, cert-manager, Istio, Elastic ECK,
Knative, Argo, Crossplane, and grafana-operator, witwave-operator emits OTLP
to a user-provided endpoint and delegates collector deployment to
something purpose-built for that job:

- **Recommended:** install the
  [opentelemetry-operator](https://github.com/open-telemetry/opentelemetry-operator)
  and create an `OpenTelemetryCollector` CR, then point
  `OTEL_EXPORTER_OTLP_ENDPOINT` at the resulting Service.
- **Alternative:** point at any OTLP-compatible target directly —
  Jaeger, Tempo, Honeycomb, Grafana Cloud, Datadog.

Wiring per WitwaveAgent:

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
