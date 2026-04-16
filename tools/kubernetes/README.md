# kubernetes MCP tool

MCP server that exposes Kubernetes API operations against the cluster where
this tool is deployed.

## Implementation

All operations go through the official `kubernetes` Python client. Kind-
agnostic operations (list / get / describe / apply / delete) use the
dynamic client, so any resource the ServiceAccount has RBAC for is
reachable — CRDs included.

## Auth

Loads in-cluster config at startup (ServiceAccount token mounted at
`/var/run/secrets/kubernetes.io/serviceaccount/`). Falls back to the local
kubeconfig when run outside a cluster for development.

## RBAC

The tool's access is whatever its ServiceAccount can do. For a full-cluster
tool, bind it to `cluster-admin` (or a narrower ClusterRole if you want to
constrain what the LLM can reach):

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: nyx-mcp-kubernetes
  namespace: nyx
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: nyx-mcp-kubernetes
subjects:
  - kind: ServiceAccount
    name: nyx-mcp-kubernetes
    namespace: nyx
roleRef:
  kind: ClusterRole
  name: cluster-admin
  apiGroup: rbac.authorization.k8s.io
```

## Tools

| Tool              | Description                                                                     |
| ----------------- | ------------------------------------------------------------------------------- |
| `list_namespaces` | List namespaces visible to the ServiceAccount.                                  |
| `list_resources`  | List resources of a kind; optional `namespace`, `api_version`, selectors.       |
| `get_resource`    | Fetch a single resource by kind / namespace / name.                             |
| `describe`        | Return `{object, events}` for a resource — structured, not kubectl-formatted.  |
| `logs`            | Pod logs with `container`, `tail_lines`, `since_seconds`, `previous` options.   |
| `apply`           | Server-side apply a YAML/JSON manifest (multi-doc supported).                   |
| `delete`          | Delete by kind / namespace / name with configurable propagation policy.         |

`api_version` is optional but required to disambiguate kinds served by
multiple groups (e.g. `Ingress` in `extensions/v1beta1` vs
`networking.k8s.io/v1`).
