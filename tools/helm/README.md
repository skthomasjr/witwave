# helm MCP tool

MCP server that exposes Helm release operations against the cluster where
this tool is deployed.

## Implementation

Shells out to the `helm` CLI (installed in the image). Helm has no REST API
and no Python SDK — the only first-class programmatic surface is the Go SDK,
so every Python wrapper in the ecosystem ultimately execs `helm`. We do the
same directly, using `--output json` where Helm supports it so results come
back structured.

## Auth

Helm picks up the in-cluster API server and ServiceAccount token from the
standard env vars (`KUBERNETES_SERVICE_HOST`, the mounted SA token at
`/var/run/secrets/kubernetes.io/serviceaccount/`). No kubeconfig is handled
inside this server.

## RBAC

Helm stores release state as Secrets in the release namespace and must be
able to create every resource in the charts it installs. For a tool that
should manage any chart anywhere, bind its ServiceAccount to
`cluster-admin`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: nyx-mcp-helm
  namespace: nyx
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: nyx-mcp-helm
subjects:
  - kind: ServiceAccount
    name: nyx-mcp-helm
    namespace: nyx
roleRef:
  kind: ClusterRole
  name: cluster-admin
  apiGroup: rbac.authorization.k8s.io
```

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
