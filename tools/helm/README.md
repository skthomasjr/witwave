# helm MCP tool

MCP server that exposes Helm release operations against the cluster where
this tool is deployed.

## Implementation

Shells out to the `helm` CLI (installed in the image). Helm has no REST API
and no Python SDK — the only first-class programmatic surface is the Go SDK,
so every Python wrapper in the ecosystem ultimately execs `helm`. We do the
same directly, using `--output json` where Helm supports it so results come
back structured.

The bundled `helm` CLI is pinned to **v3.20.2** (#769) to carry the CERT-Bund
WID-SEC-2026-1048 fixes (file manipulation / security-control bypass / potential
RCE, CVSS 8.6). Refresh the pin in `tools/helm/Dockerfile` at least quarterly
and whenever a new Helm security advisory lands — every caller inherits the
bundled CLI.

## Auth

Helm picks up the in-cluster API server and ServiceAccount token from the
standard env vars (`KUBERNETES_SERVICE_HOST`, the mounted SA token at
`/var/run/secrets/kubernetes.io/serviceaccount/`). No kubeconfig is handled
inside this server.

## RBAC

**Do not bind this tool to `cluster-admin` by default.** Helm can install
any chart, and with `cluster-admin` that means installing any workload
anywhere — including ones that themselves hold `cluster-admin`. An
LLM-driven caller should operate with the narrowest RBAC that lets the
intended charts render, and only be widened deliberately. (See #770.)

The example below is a **read-only** baseline scoped to a single
namespace: it lets the tool enumerate releases and read the chart
state Helm persists in Secrets, but cannot `install`, `upgrade`,
`rollback`, or `uninstall`. Add the write verbs (and the kind-specific
verbs each chart needs) only when you accept the LLM writing to the
cluster.

```yaml
# Scoped ServiceAccount (same namespace as the mcp-helm Deployment).
apiVersion: v1
kind: ServiceAccount
metadata:
  name: nyx-mcp-helm
  namespace: nyx
---
# Minimum Role for read-only Helm: list_releases, get_release, get_values,
# get_manifest, history. Helm stores release state as Secrets with
# label owner=helm (v3); we restrict access to that kind via the Role
# scope. For full install/upgrade/rollback/uninstall you MUST widen this
# Role with the verbs the target chart needs — one Role per
# namespace-of-concern is the right default, not cluster-admin.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: nyx-mcp-helm-read
  namespace: nyx
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list"]
    # Limit further in production by resourceNames or a downstream
    # admission policy (e.g. Kyverno) that matches owner=helm.
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: nyx-mcp-helm-read
  namespace: nyx
subjects:
  - kind: ServiceAccount
    name: nyx-mcp-helm
    namespace: nyx
roleRef:
  kind: Role
  name: nyx-mcp-helm-read
  apiGroup: rbac.authorization.k8s.io
```

For write operations (`install`, `upgrade`, `rollback`, `uninstall`),
extend this Role with the exact resources/verbs the target chart
requires, or (when the chart spans namespaces) promote to a
`ClusterRole` + `ClusterRoleBinding` and review carefully. Never
grant `cluster-admin` as a shortcut — the blast radius of an LLM-driven
misstep is then every workload in every namespace.

## Tools

| Tool            | Description                                                                  |
| --------------- | ---------------------------------------------------------------------------- |
| `list_releases` | List releases; optional `namespace` or `all_namespaces`.                     |
| `get_release`   | Assembled view: current revision + values + rendered manifest.               |
| `get_values`    | User-supplied values (or all computed values with `all_values=True`).        |
| `get_manifest`  | Rendered manifest for a release.                                             |
| `history`       | Revision history (bounded by `max_revisions`).                               |
| `install`       | Install a chart; supports `version`, `repo`, `create_namespace`, `wait`.     |
| `upgrade`       | Upgrade a release; `install_if_missing`, `reset_values`, `reuse_values`.     |
| `rollback`      | Roll back to a prior revision (raw CLI output — Helm has no JSON mode here). |
| `uninstall`     | Uninstall a release; optional `keep_history`.                                |
| `repo_add`      | Add a chart repository.                                                      |
| `repo_update`   | Refresh local chart repo indexes.                                            |

`install`/`upgrade` accept a `values` dict that is serialized to a temp YAML
file and passed with `-f`; the file is removed after the call.
