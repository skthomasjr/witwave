# nyx

Helm chart for the nyx platform — nyx harness and backends (claude, codex, gemini).

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

- [claude secrets](../../backends/claude/README.md#secrets) — `ANTHROPIC_API_KEY`
- [codex secrets](../../backends/codex/README.md#secrets) — `OPENAI_API_KEY`
- [gemini secrets](../../backends/gemini/README.md#secrets) — `GEMINI_API_KEY`

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
| `agents` | List of agents to deploy, each as a harness pod | `[{name: adam}]` |
| `agents[].name` | Agent name — used for pod name, service name, and `AGENT_NAME` env var | `adam` |
| `agents[].imagePullSecrets` | Image pull secrets for the agent pod | `[]` |
| `dashboard.enabled` | Deploy the nyx web dashboard | `false` |
| `dashboard.image.repository` | Dashboard image repository | `ghcr.io/skthomasjr/images/dashboard` |
| `dashboard.port` | Dashboard service port | `80` |
| `ingress.enabled` | Deploy a Kubernetes Ingress for the dashboard (#528 — chart fails template render when enabled without an auth mechanism; see below) | `false` |
| `ingress.className` | Ingress class name (e.g. `nginx`, `traefik`) | `""` |
| `ingress.annotations` | Annotations to add to the Ingress resource | `{}` |
| `ingress.hosts` | Hostnames and paths for the Ingress | `[]` |
| `ingress.tls` | TLS configuration for the Ingress (user-supplied list; BYO TLS path). When non-empty, takes precedence over cert-manager-driven TLS | `[]` |
| `ingress.tlsEnabled` | When `true` AND `certManager.enabled=true`, the chart auto-synthesizes an `ingress.tls` entry from `ingress.hosts` and renders a cert-manager `Certificate` targeting the secret below (#639) | `false` |
| `ingress.tlsSecretName` | Secret name for the cert-manager-issued cert. Defaults to `<release>-dashboard-tls` when unset | `""` |
| `certManager.enabled` | Master toggle for cert-manager integration. When `false` (default), the chart renders no cert-manager resources (#639) | `false` |
| `certManager.createIssuer` | Render a chart-owned selfSigned `Issuer`. Ignored when `certManager.issuerRef.name` is set | `true` |
| `certManager.issuerKind` | Kind of Issuer to render when `createIssuer=true`. `Issuer` (namespaced) or `ClusterIssuer` | `Issuer` |
| `certManager.issuerRef.name` | Name of an external (Cluster)Issuer to use instead of a chart-owned one. When set, `createIssuer` is ignored | `""` |
| `certManager.issuerRef.kind` | Kind of the external issuer. Typically `ClusterIssuer` | `""` |
| `ingress.auth.enabled` | Master toggle for the chart-managed auth block (renders the auth Secret and nginx-ingress `auth-*` annotations) | `true` |
| `ingress.auth.type` | Auth mechanism. Currently only `basic` is supported | `basic` |
| `ingress.auth.allowInsecure` | Explicit escape hatch: when `true`, skip the chart's auth block and render the Ingress with no auth. Intended for isolated networks or when a separate auth gateway is fronting the chart | `false` |
| `ingress.auth.basic.existingSecret` | Name of an existing Secret containing an htpasswd `auth` key. When set, the chart does not render its own Secret | `""` |
| `ingress.auth.basic.htpasswd` | Inline htpasswd line(s) rendered into a chart-managed Secret when `existingSecret` is empty. Multi-line strings are supported | `""` |
| `ingress.auth.basic.realm` | Browser auth-prompt realm displayed by nginx-ingress | `nyx dashboard` |
| `autoscaling.enabled` | Deploy a HorizontalPodAutoscaler per agent (Deployment omits `replicas` when enabled). See [Reliability](#reliability) — multi-replica is not yet safe for the harness's singleton schedulers (#559) | `false` |
| `autoscaling.minReplicas` | HPA minimum replicas | `1` |
| `autoscaling.maxReplicas` | HPA maximum replicas | `3` |
| `autoscaling.targetCPUUtilizationPercentage` | HPA CPU utilization target | `80` |
| `autoscaling.targetMemoryUtilizationPercentage` | HPA memory utilization target (optional) | unset |
| `podDisruptionBudget.enabled` | Deploy a PodDisruptionBudget per agent. Strongly recommended for production — safe at replicas=1 (blocks node drains until a replacement pod is Ready instead of dropping immediately). See [Reliability](#reliability) (#559) | `false` |
| `podDisruptionBudget.minAvailable` | Minimum available replicas during voluntary disruption | `1` |
| `podDisruptionBudget.maxUnavailable` | Alternative to `minAvailable` — max unavailable replicas | unset |
| `terminationGracePeriodSeconds` | Pod termination grace period. Must be strictly greater than `preStop.delaySeconds` so SIGTERM fires with enough remaining time for the harness and backends to drain in-flight work before SIGKILL (#547) | `60` |
| `preStop.enabled` | Add a `lifecycle.preStop` sleep on every container so in-flight A2A requests, jobs, and webhook deliveries get a coordinated drain window before SIGTERM (#447) | `false` |
| `preStop.delaySeconds` | preStop sleep duration in seconds. Keep strictly less than `terminationGracePeriodSeconds`; the chart will `fail` render otherwise when `preStop.enabled=true` (#547) | `5` |
| `nodeSelector` | Chart-global `nodeSelector` applied to every agent pod. Per-agent overrides via `agents[].nodeSelector` **replace** (not merge) this value — matches the `autoscaling` / `podDisruptionBudget` semantics. Mirrors the nyx-operator chart and pairs with the NyxAgent CRD (#603, #605) | `{}` |
| `tolerations` | Chart-global pod tolerations applied to every agent pod. Per-agent `agents[].tolerations` **replace** this value. Use with node taints to dedicate node pools to agent workloads (#603) | `[]` |
| `affinity` | Chart-global pod affinity / anti-affinity applied to every agent pod. Per-agent `agents[].affinity` **replace** this value. Rendered with `toYaml` so the full affinity schema is supported (#603) | `{}` |
| `topologySpreadConstraints` | Chart-global topology spread constraints applied to every agent pod. Per-agent `agents[].topologySpreadConstraints` **replace** this value. Interacts with `podDisruptionBudget` on multi-replica agents — spread replicas first, then let the PDB gate drains (#603) | `[]` |
| `priorityClassName` | Chart-global `priorityClassName` applied to every agent pod. Per-agent `agents[].priorityClassName` **replaces** this value. Use to bias scheduling priority for production agent pods (#603) | `""` |
| `agents[].nodeSelector` | Per-agent `nodeSelector`. Replaces chart-global `nodeSelector` when set (#603) | unset |
| `agents[].tolerations` | Per-agent tolerations. Replaces chart-global `tolerations` when set (#603) | unset |
| `agents[].affinity` | Per-agent affinity. Replaces chart-global `affinity` when set (#603) | unset |
| `agents[].topologySpreadConstraints` | Per-agent topology spread constraints. Replaces chart-global `topologySpreadConstraints` when set (#603) | unset |
| `agents[].priorityClassName` | Per-agent `priorityClassName`. Replaces chart-global `priorityClassName` when set (#603) | unset |
| `probes.liveness.initialDelaySeconds` | Liveness probe initial delay | `10` |
| `probes.liveness.periodSeconds` | Liveness probe period | `30` |
| `probes.liveness.timeoutSeconds` | Liveness probe timeout | `5` |
| `probes.liveness.failureThreshold` | Liveness probe failure threshold | `3` |
| `probes.readiness.initialDelaySeconds` | Readiness probe initial delay | `5` |
| `probes.readiness.periodSeconds` | Readiness probe period | `10` |
| `probes.readiness.timeoutSeconds` | Readiness probe timeout | `5` |
| `probes.readiness.failureThreshold` | Readiness probe failure threshold | `3` |
| `metrics.enabled` | Enable Prometheus metrics globally | `false` |
| `serviceMonitor.enabled` | Create one Prometheus Operator `ServiceMonitor` per agent for metrics auto-discovery (requires `metrics.enabled=true` for that agent and the Prometheus Operator CRDs installed in the cluster) | `false` |
| `serviceMonitor.scrapeInterval` | Scrape interval for each ServiceMonitor endpoint | `30s` |
| `serviceMonitor.scrapeTimeout` | Scrape timeout for each ServiceMonitor endpoint | `10s` |
| `serviceMonitor.labels` | Extra labels merged into every ServiceMonitor (e.g. `release: kube-prometheus-stack` for Prometheus Operator's selector) | `{}` |
| `sharedStorage.enabled` | Mount a shared volume into all agent pods and sidecars | `false` |
| `sharedStorage.mountPath` | Mount path inside containers | `/data/shared` |
| `sharedStorage.storageType` | `pvc` or `hostPath` | `pvc` |
| `sharedStorage.size` | PVC storage request | `1Gi` |
| `sharedStorage.storageClassName` | Storage class for PVC (leave blank for cluster default) | `""` |
| `sharedStorage.accessModes` | PVC access modes | `[ReadWriteMany]` |
| `sharedStorage.existingClaim` | Use a pre-existing PVC instead of creating one | `""` |
| `sharedStorage.hostPath` | Host path when `storageType: hostPath` | `""` |
| `cors.allowOrigins` | Explicit list of allowed CORS Origin values for harness HTTP endpoints. Empty list ≡ CORS disabled (the safest default — browser origins are blocked unless explicitly allowed) (#763) | `[]` |
| `cors.allowWildcard` | Explicit acknowledgement required when `cors.allowOrigins` contains `*` (#701). Template fails render when the wildcard is used without this flag AND ingress is enabled, since `Access-Control-Allow-Origin: *` with credentials is a disclosure hole across `/triggers` and `/conversations`. | `false` |
| `storage.retainOnUninstall` | Annotate every chart-owned PVC with `helm.sh/resource-policy=keep` so `helm uninstall` leaves conversation logs / memory / sessions in place. Useful on clusters with delete-reclaim defaults (#767). Operators must clean the PVCs up manually. | `false` |
| `mcpTools.<name>.enabled` | Deploy this MCP tool (`kubernetes`, `helm`, …) as a Deployment + Service in the release namespace | `false` |
| `mcpTools.<name>.image.digest` | Immutable digest pin for the tool image (#855). When set (e.g. `sha256:abc123…`), the chart renders `repository@<digest>` and ignores `tag`. Prefer this in production: MCP pods typically hold a cluster ServiceAccount token. | `""` |
| `mcpTools.<name>.rbac.create` | Render a minimal default `ServiceAccount` + `ClusterRole` + `ClusterRoleBinding` for this tool so enabling it does not 403 out of the box (#762). Set `false` + `serviceAccountName` to manage RBAC out-of-band. When both are unset chart render fails loudly. | `true` |
| `mcpTools.<name>.rbac.rules` | Baseline `ClusterRole` rules when `rbac.create=true`. Least-privilege reads by default; adjust to widen or narrow. | see `values.yaml` |
| `mcpTools.<name>.automountServiceAccountToken` | Three-state override (#856). Omit to default to `true`. Set to `false` for IRSA / workload-identity setups where the projected in-pod SA token should be suppressed (the SA is still attached for annotations). | unset |
| `podSecurity.readOnlyRootFilesystem` | Toggle the container-level `readOnlyRootFilesystem: true` securityContext on harness + backend + dashboard + MCP-tool pods (#948). Default `false` for compatibility with tools that still need to write to `/tmp`; flip to `true` alongside appropriate `emptyDir` scratch mounts to narrow the post-exploit blast radius. | `false` |
| `networkPolicy.ingress.allPortsDashboard` | Explicit opt-out: when `false`, the dashboard peer is restricted to the metrics port only (no app-port ingress). Default `true` preserves pre-#911 behaviour. | `true` |
| `networkPolicy.ingress.allPortsSameNamespace` | Explicit opt-out: when `false` and `allowSameNamespace: true`, same-namespace peers are restricted to the metrics port only. Default `true` preserves pre-#911 behaviour. | `true` |
| `networkPolicy.ingress.dashboardPeer.namespaceSelector` | Explicit `namespaceSelector` on the dashboard peer (#914) so cross-namespace dashboard deployments match correctly instead of relying on the empty-selector fallthrough. | unset (defaults to release namespace) |
| `values.schema.json` coverage | `mcpTools.<name>.rbac.{create,rules}` and the full `networkPolicy` block now have schema coverage so typos fail `helm template` / `helm install --dry-run` loudly rather than surfacing at reconcile time (#973, #909). | — |
| `dashboard.replicas` / `mcpTools.<name>.replicas` | `replicas: 0` is now a first-class value (#912): the templates use `hasKey` rather than truthiness so an explicit zero scales the Deployment down to 0 rather than falling back to `1`. Useful for maintenance windows without uninstalling. | see `values.yaml` |
| `autoscaling.enabled` (target metrics) | HPA rendering now fails loudly when `autoscaling.enabled=true` but no CPU / memory / custom metric target is configured (#913). Previously the chart rendered an empty `metrics:` list, which some Kubernetes versions silently accepted. | — |

### Additional values (completeness pass, #844)

The table above was curated for the most-edited knobs; the entries below fill out the remaining values surface so
`helm show values` is no longer the only discovery path. See `values.yaml` for inline comments on each knob.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `dashboard.replicas` | Number of dashboard pods. Ignored when an HPA targets the dashboard deployment. | `1` |
| `dashboard.clusterDomain` | Cluster DNS domain used by the dashboard's nginx proxy when resolving in-cluster Service names (e.g. `cluster.local`, `cluster.example`). | `cluster.local` |
| `dashboard.image.tag` | Dashboard image tag. Defaults to `.Chart.AppVersion`. `latest`-tag patterns rejected by the schema (#766). | AppVersion |
| `dashboard.image.pullPolicy` | Image pull policy. | unset (K8s default) |
| `dashboard.imagePullSecrets` | Image pull secrets for the dashboard pod. | `[]` |
| `dashboard.resources` | Dashboard container CPU/memory requests + limits. | `{}` |
| `dashboard.securityContext` | Dashboard pod `securityContext`. Merged on top of chart defaults. | `{}` |
| `agents[].image.repository` | Per-agent harness image repository override. | `ghcr.io/skthomasjr/images/harness` |
| `agents[].image.tag` | Per-agent harness image tag. | AppVersion |
| `agents[].image.pullPolicy` | Per-agent harness image pull policy. | unset |
| `agents[].port` | Harness container port (metrics port derives as `port + 1000`). | `8000` |
| `agents[].env` | Additional env vars appended to the harness container. Template fails render when a sensitive key carries a `REPLACE_ME` placeholder (#760). | `[]` |
| `agents[].envFrom` | Additional `envFrom` sources (Secret/ConfigMap refs) for the harness container. | `[]` |
| `agents[].metrics.enabled` | Per-agent metrics override. When unset inherits `.Values.metrics.enabled`. | unset |
| `agents[].resources` | Per-agent harness resources. Replaces `defaults.resources.harness` when set. | unset |
| `agents[].storage.*` | Per-agent shared-storage override block. Mirrors `sharedStorage.*` shape. | unset |
| `agents[].podLabels` / `podAnnotations` | Extra labels / annotations on the harness pod. Reserved `app.kubernetes.io/*` keys are dropped (#477). | `{}` / `{}` |
| `agents[].backends` | List of backend containers co-located in the agent pod. Each has `name`, `image`, `port`, `env`, `envFrom`, `credentials`, `storage`, `resources`, `config`, `gitMappings`. See `values.yaml` for the full schema. | `[]` |
| `agents[].backends[].credentials` | Per-backend credentials: `existingSecret`, or inline `secrets` map with `acknowledgeInsecureInline: true`, or legacy `envFrom`. Admission rejects inline without the ack flag (#832). | unset |
| `agents[].backends[].gitMappings[]` | Per-backend `{gitSync, src, dest}` entries that copy files from a named `agents[].gitSyncs[]` repo into the container. Validated by admission (#832). | unset |
| `agents[].gitSyncs[]` | Per-agent gitSync sidecar definitions: `{name, repo, branch, sshKey, credentials, interval, depth}`. | `[]` |
| `probes.startup.initialDelaySeconds` / `.periodSeconds` / `.timeoutSeconds` / `.failureThreshold` | Startup probe on the harness container. | `5` / `10` / `5` / `6` |
| `probes.startup.path` | Startup-probe HTTP path. | `/health/start` |
| `probes.liveness.path` | Liveness-probe HTTP path. | `/health/live` |
| `probes.readiness.path` | Readiness-probe HTTP path. | `/health/ready` |
| `gitSync.image.repository` / `.tag` / `.pullPolicy` | git-sync sidecar image. | ghcr.io image / AppVersion / unset |
| `metrics.port` | **Deprecated** (#687). Ignored for harness + backends; they now use `app_port + 1000`. Still honoured by MCP tool pods via `mcpTools.metricsPort`. | unset |
| `metrics.podAnnotations` | Render `prometheus.io/*` scrape annotations on every harness + backend pod (legacy scrape style). Off by default (#472). | `false` |
| `metrics.serviceAnnotations` | Same idea on the Service. | `false` |
| `metrics.authToken.existingSecret` | Existing Secret name carrying the metrics bearer token. | `""` |
| `metrics.cacheTTL` | TTL in seconds for the harness's aggregated backend-metrics cache. | `15` |
| `mcpTools.metricsPort` | Dedicated metrics port for MCP tool pods (previously named `metrics.port`; #687). | `9000` |
| `mcpTools.<name>.image.repository` / `.tag` / `.pullPolicy` | MCP tool image fields. | per-tool / AppVersion / unset |
| `mcpTools.<name>.resources` | MCP tool container resources. | `{}` |
| `mcpTools.<name>.serviceAccountName` | Pre-created SA to reuse. When unset + `rbac.create=true` the chart renders one. | `""` |
| `mcpTools.<name>.podSecurityContext` / `securityContext` | MCP tool Pod / container security contexts. | chart defaults |
| `podMonitor.enabled` | Create one Prometheus Operator `PodMonitor` per agent (alternative to ServiceMonitor for headless / pod-level scrape). | `false` |
| `podMonitor.scrapeInterval` / `.scrapeTimeout` / `.labels` / `.path` | PodMonitor knobs; same shape as ServiceMonitor. | `30s` / `10s` / `{}` / `/metrics` |
| `serviceMonitor.path` | ServiceMonitor HTTP path. | `/metrics` |
| `observability.tracing.enabled` | Enable OTel tracing wiring on every harness + backend container (#634). | `false` |
| `observability.tracing.endpoint` | OTLP HTTP endpoint (`http://collector:4318`). | `""` |
| `observability.tracing.sampler` / `.samplerArg` | OTel `OTEL_TRACES_SAMPLER` + `OTEL_TRACES_SAMPLER_ARG`. | `parentbased_traceidratio` / `0.1` |
| `observability.tracing.collector.enabled` | Deploy an in-cluster OpenTelemetry Collector for the release. | `false` |
| `observability.tracing.collector.image.repository` / `.tag` | Collector image. | otel/opentelemetry-collector-contrib / `0.119.0` |
| `observability.tracing.collector.resources` | Collector resource requests/limits. | `{}` |
| `networkPolicy.enabled` | Emit one NetworkPolicy per chart-rendered pod (#759). Default off. | `false` |
| `networkPolicy.ingress.allowDashboard` / `allowSameNamespace` | Shortcut peers. | `true` / `false` |
| `networkPolicy.ingress.metricsFrom` | Raw `NetworkPolicyPeer` list allowed on the metrics port. | `monitoring` namespace |
| `networkPolicy.ingress.additionalFrom` | Raw peers applied to all ports. | `[]` |
| `networkPolicy.egressOpen` / `networkPolicy.egress` | Egress mode: open by default; set `egressOpen: false` to enforce the explicit `egress` allow-list. | `true` / `[]` |
| `defaults.resources.harness` | Chart-wide harness resource requests/limits defaults (#553). Set leaves to `null` to disable. | see `values.yaml` |
| `defaults.resources.backend` | Chart-wide backend resource defaults. | see `values.yaml` |
| `defaults.resources.backends.<name>` | Per-backend-type default keyed by backend name (`claude`, `codex`, `gemini`). | see `values.yaml` |

To deploy multiple agents:

```yaml
agents:
  - name: iris
  - name: nova
  - name: kira
```

## Reliability

### Single-replica default is intentional (#559)

Each agent's Deployment renders `replicas: 1` by default, and both `autoscaling.enabled` and
`podDisruptionBudget.enabled` default to `false`. This is deliberate — not an oversight:

- **harness runs singleton schedulers.** The harness owns the heartbeat scheduler, job scheduler, task
  scheduler, trigger handler, and continuation runner. Running two harness pods for the same agent would
  cause every scheduled item to fire twice.
- **Backends keep local session state.** Conversation logs and per-session memory live on a local volume
  inside each backend pod. Running two backend pods for the same agent can interleave writes.

The trade-off is that with `replicas=1` AND `podDisruptionBudget.enabled=false`, every voluntary disruption
(`kubectl drain`, node upgrade, cluster-autoscaler scale-down) immediately drops the pod. During the ~30s
gap until the replacement is Ready: scheduled jobs and heartbeats miss their fire windows, inbound triggers
return 503, in-flight A2A calls return 5xx, and webhook deliveries are lost.

The chart emits a helm NOTES warning after `helm install`/`helm upgrade` whenever an agent is deployed with
replicas=1 AND both HPA and PDB disabled.

### HA opt-in path

The safest production configuration keeps the singleton topology but adds a PodDisruptionBudget:

```yaml
podDisruptionBudget:
  enabled: true
  minAvailable: 1
```

With `replicas=1` this blocks `kubectl drain` until a replacement pod is Ready on another node — instead of
dropping the pod immediately. The drain takes longer, but there is no service gap.

**Do not simply raise `replicas > 1` or `autoscaling.minReplicas >= 2`** — the harness's singleton schedulers
and the backends' local session state are not yet safe for multi-replica operation. A future change will
externalize scheduler state and session storage so the chart can move to a true HA topology.

## Credentials for gitSync + backends

Every `agents[].gitSyncs[]` and `agents[].backends[]` entry supports a three-way `credentials` block with identical
shape between the two. Pick the path that matches your environment:

```yaml
# Chart-global defaults — inherited when per-entry credentials are unset.
gitSync:
  credentials:
    existingSecret: ""
    username: ""
    token: ""
    acknowledgeInsecureInline: false

backends:
  credentials:
    existingSecret: ""
    secrets: {}                # map of env-var-name → value
    acknowledgeInsecureInline: false

agents:
  - name: bob
    gitSyncs:
      - name: autonomous-agent
        repo: https://github.com/org/repo
        # Per-entry override (omit to inherit chart-global default).
        credentials:
          existingSecret: bob-github-pat    # OR inline below, not both.
    backends:
      - name: claude
        credentials:
          secrets:
            CLAUDE_CODE_OAUTH_TOKEN: "sk-ant-oat-xxxxxxxxxxxx"
          acknowledgeInsecureInline: true
```

**Three modes (mutually exclusive, in precedence order):**

1. **`existingSecret`** — reference a Secret you (or a CI pipeline) pre-created in the release namespace. The
   chart emits `envFrom: - secretRef: name: <existingSecret>`. Recommended for production; tokens never touch
   helm release state or values files.

2. **Inline values** (`username` + `token` for gitSync, `secrets: {}` map for backends) — chart auto-renders
   a Secret named `<release>-<agent>-<entry>-{gitsync,backend}-credentials` and wires envFrom. Dev-friendly:
   a single `--set` flag sourced from `.env` sets everything up. **Must** also set
   `acknowledgeInsecureInline: true` or the chart aborts template render with a pointed warning — inline
   tokens land in etcd release state, `helm get values`, and `kubectl describe`. Our own `values-test.yaml`
   uses this path because smoke tests are ephemeral.

3. **Empty (default)** — no auth envFrom rendered. gitSync runs anonymously (fine for public repos);
   backends start but will fail on first LLM call.

**Legacy `envFrom:` escape hatch** remains supported on every entry for custom auth setups (SSH-key secrets,
multiple secrets merged, ConfigMaps) that the `credentials:` block doesn't cover. When both `credentials:`
and `envFrom:` are set on the same entry, `credentials:` wins.

**Do not embed credentials in the repo URL** (#1077). The chart rejects any `gitSyncs[].repo` that matches
`^https?://[^/]*:[^/]*@` (e.g. `https://user:token@github.com/...`) at render time. Token-bearing URLs get
persisted in the pod spec, the helm release Secret (`helm get values`, `sh.helm.release.v1.*`), apiserver
audit logs, and every `kubectl get pod -oyaml`. The chart also moves `--repo` off the initContainer / sidecar
args onto a `GITSYNC_REPO` environment variable for the same reason — operators add secret-scrubbing to env
dumps far more reliably than to arbitrary positional flags.

**Release-state leak on inline credentials.** Even with `acknowledgeInsecureInline: true`, token values are
captured into the `sh.helm.release.v1.<release>.v<N>` Secret Helm writes to etcd — the rendered Secret object
is part of the release manifest and `helm get values` will echo the inline token back. For anything beyond
ephemeral smoke tests, prefer the `existingSecret` path.

### Installing with credentials from `.env`

There's no Helm-native `.env` reader — easiest path is to shell-source before `helm upgrade`:

```bash
set -a; source .env; set +a
helm upgrade --install nyx-test ./charts/nyx \
  -f ./charts/nyx/values-test.yaml \
  --set-string gitSync.credentials.username="$GITSYNC_USERNAME" \
  --set-string gitSync.credentials.token="$GITSYNC_PASSWORD" \
  --set     gitSync.credentials.acknowledgeInsecureInline=true \
  --set-string backends.credentials.secrets.CLAUDE_CODE_OAUTH_TOKEN="$CLAUDE_CODE_OAUTH_TOKEN" \
  --set     backends.credentials.acknowledgeInsecureInline=true \
  -n nyx-test --create-namespace
```

Use `--set-string` on any value that might parse as a number / boolean to avoid type coercion (`--set x=01234` becomes an int).
Per-agent or per-entry overrides use dot-paths like `--set agents[0].backends[0].credentials.secrets.FOO=bar`.

## Security

### Dashboard Ingress — fail-closed (#528)

When `dashboard.enabled=true` and `ingress.enabled=true`, the nginx pod reverse-proxies every enabled agent's API
(conversations, triggers, jobs). An unauthenticated Ingress is effectively unauthenticated remote code execution
against the cluster, so this chart **fails template render** when `ingress.enabled=true` unless one of the following
is configured:

1. `ingress.auth.enabled=true` (chart-managed basic auth — the default).
2. `ingress.auth.allowInsecure=true` (explicit opt-out for isolated clusters / separate auth gateways).
3. `ingress.annotations` contains one of `nginx.ingress.kubernetes.io/auth-url`,
   `nginx.ingress.kubernetes.io/auth-signin`, or `traefik.ingress.kubernetes.io/router.middlewares` — indicating a
   user-supplied auth proxy / middleware is wired in.

When chart-managed basic auth is used, supply either `ingress.auth.basic.existingSecret` (pointing at a Secret that
already contains an htpasswd `auth` key) or `ingress.auth.basic.htpasswd` (inline). Generate htpasswd lines with
`htpasswd -nbB admin 'choose-a-strong-password'`.

### Pod security (#541)

Agent pods (harness + backends) and the dashboard pod both set `seccompProfile: RuntimeDefault` and run as non-root
(`runAsNonRoot: true`). This keeps the chart compatible with the Pod Security Standards "restricted" profile without
additional values.

### Backend `/mcp` auth parity

All three backends now require a bearer token on the `/mcp`, `/conversations`, `/trace`, and (claude)
`/api/traces[/<id>]` endpoints (#510, #516, #518). Include `CONVERSATIONS_AUTH_TOKEN` in each backend's envFrom
Secret. If it is unset or empty, the backend logs a startup warning (#517) and the shared guard refuses to serve
the protected endpoints unless the operator explicitly sets `CONVERSATIONS_AUTH_DISABLED=true` (#718).

### MCP tool bearer-token auth (#771)

Every MCP tool Deployment rendered by this chart picks up the shared `shared/mcp_auth.py` middleware. Set
`MCP_TOOL_AUTH_TOKEN` on each tool via `mcpTools.<name>.env` or an `envFrom` secret; use `MCP_TOOL_AUTH_DISABLED=true`
to acknowledge running without auth (local dev only).

### MCP command + cwd allow-list

Every backend validates stdio entries in `mcp.json` against per-backend allow-lists before spawning the subprocess
(`MCP_ALLOWED_COMMANDS`, `MCP_ALLOWED_COMMAND_PREFIXES`, `MCP_ALLOWED_CWD_PREFIXES`). Rejections increment
`backend_mcp_command_rejected_total{reason}`. Claude #711, codex #720, gemini #730.

### Optional NetworkPolicy (#759)

`networkPolicy.enabled: true` renders one `NetworkPolicy` per chart-rendered pod (each agent, the dashboard, each
enabled MCP tool). Default off — when enabled with no additional configuration, ingress is restricted to the
monitoring namespace (via `kubernetes.io/metadata.name=monitoring`) for metrics scraping plus the in-release
dashboard for app traffic. Everything else is denied.

Knobs (all under `networkPolicy.ingress`):

- `allowDashboard` — the dashboard pod may reach agent/backend app ports. Default `true`.
- `allowSameNamespace` — any pod in the release namespace may reach any chart-rendered pod. Default `false`.
- `metricsFrom` — raw `NetworkPolicyPeer` list permitted on the metrics port (app port + 1000). Default scopes to
  the `monitoring` namespace.
- `additionalFrom` — raw peers applied to **all** ports.

Egress stays open by default (backends need to reach the Kubernetes API, Helm, harness-configured webhook targets,
DNS, the OTel collector, and peer agents). Flip `networkPolicy.egressOpen: false` to enforce an explicit allow-list
via `networkPolicy.egress`.

### Ingress-scoped A2A cap (#783)

`A2A_MAX_PROMPT_BYTES` (default 1 MiB) caps inbound A2A prompts at the harness before they hit a backend; set to
`0` to disable.

### Per-backend model override

`backends[].model` is now rendered into the backend container as the `BACKEND_MODEL` env var (#489) — previously the
value was dropped. Combined with the backend's own `<NAME>_MODEL` env var (`CLAUDE_MODEL`, `CODEX_MODEL`,
`GEMINI_MODEL`) and per-request routing overrides in harness, this lets the chart set the default model for a
backend without baking it into the image or a per-agent ConfigMap.

## MCP tool Deployments (#644)

The chart renders one `Deployment` + `Service` per entry in `mcpTools` that has `enabled: true`. Each tool container
listens on port **`8000`** using FastMCP's `streamable-http` transport, so backends in other pods can reach it by
Service URL (`http://<release>-mcp-<tool>:8000`) without needing a stdio fork/exec.

```yaml
mcpTools:
  kubernetes:
    enabled: true
    image:
      # Pin immutably in production (#855) — MCP pods hold a cluster SA token.
      digest: sha256:abc123...
    # Three-state (#856); set false for IRSA / workload-identity setups.
    # automountServiceAccountToken: true
    serviceAccountName: mcp-kubernetes   # BYO SA with cluster-read RBAC
  helm:
    enabled: true
    serviceAccountName: mcp-helm         # BYO SA with helm-release RBAC
```

In each agent's `.claude/mcp.json` / `.codex/mcp.json` / `.gemini/mcp.json`, reference the tools by URL:

```json
{
  "mcpServers": {
    "kubernetes": { "url": "http://<release>-mcp-kubernetes:8000" },
    "helm":       { "url": "http://<release>-mcp-helm:8000" }
  }
}
```

The chart renders a minimal default `ServiceAccount` + `ClusterRole` + `ClusterRoleBinding` per MCP tool whenever
`mcpTools.<name>.rbac.create: true` (the default; see `values.yaml` for the baseline `rules`). If you prefer to
manage RBAC out-of-band (e.g. central security team, out-of-cluster IAM, or a reduced verb surface), set
`rbac.create: false`, provide `serviceAccountName: <your-SA>`, and apply the ready-to-use samples in
[`samples/mcp-kubernetes-rbac.yaml`](samples/mcp-kubernetes-rbac.yaml) and
[`samples/mcp-helm-rbac.yaml`](samples/mcp-helm-rbac.yaml) — both are least-privilege starting points that
mirror the chart's in-tree baseline.

Disabled by default. Leave `mcpTools.<name>.enabled: false` (or omit the entry entirely) to skip rendering; backends
configured without the URL just don't call the tool.

## Enabling distributed tracing (#634)

End-to-end OpenTelemetry tracing across harness + backends + operator is opt-in. The pod-side OTel bootstraps
(`shared/otel.py` for Python, `operator/internal/tracing/otel.go` for Go) have shipped since #469/#471 — this chart
owns the env-var wiring.

**This chart does not deploy an OpenTelemetry Collector.** Matching the idiomatic pattern across Strimzi,
cert-manager, Istio, Elastic ECK, Knative, Argo, Crossplane, and grafana-operator, nyx emits OTLP to a user-provided
endpoint and delegates collector deployment to something built for that job.

> **Note:** Earlier chart versions (pre-`0.3.x`) rendered an in-release OTel Collector Deployment + Service +
> ConfigMap gated on `observability.tracing.collector.enabled`. That path was removed — the corresponding values
> keys (`observability.tracing.collector.*`) are gone. Point `observability.tracing.endpoint` at an
> opentelemetry-operator-managed collector or a direct OTLP backend instead.

Options:

- **Recommended:** install the [opentelemetry-operator](https://github.com/open-telemetry/opentelemetry-operator)
  and create an `OpenTelemetryCollector` CR. Point `observability.tracing.endpoint` at the resulting Service.
- **Alternative:** point `observability.tracing.endpoint` at any OTLP-compatible backend directly — Jaeger,
  Tempo, Honeycomb, Grafana Cloud, Datadog, etc.

### Quick start — wire to a collector

```yaml
observability:
  tracing:
    enabled: true
    endpoint: http://otel-collector.observability:4318    # OTLP/HTTP
    # or http://otel-collector.observability:4317 for OTLP/gRPC
    sampler: parentbased_traceidratio
    samplerArg: "0.1"        # 10% sampling
```

With that:

- Every harness and backend pod receives `OTEL_ENABLED=true`, `OTEL_EXPORTER_OTLP_ENDPOINT`, and a
  per-component `OTEL_SERVICE_NAME`.
- `OTEL_TRACES_SAMPLER` / `OTEL_TRACES_SAMPLER_ARG` are forwarded verbatim when set.

### Wiring the operator

The `nyx-operator` chart exposes a matching `observability.tracing` block. Point both at the same endpoint to
trace the reconciler alongside the agents:

```yaml
# values for nyx-operator
observability:
  tracing:
    enabled: true
    endpoint: http://otel-collector.observability:4318
```

See `operator/internal/tracing/otel.go` for the full list of OTel env vars the operator honours — the chart forwards
the standard subset (`OTEL_ENABLED`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG`,
`OTEL_SERVICE_NAME`).
