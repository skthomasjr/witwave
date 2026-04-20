# kubernetes MCP tool

MCP server that exposes Kubernetes API operations against the cluster where this tool is deployed.

## Implementation

All operations go through the official `kubernetes` Python client. Kind- agnostic operations (list / get / describe /
apply / delete) use the dynamic client, so any resource the ServiceAccount has RBAC for is reachable — CRDs included.

## Auth

Loads in-cluster config at startup (ServiceAccount token mounted at `/var/run/secrets/kubernetes.io/serviceaccount/`).
Falls back to the local kubeconfig when run outside a cluster for development.

## RBAC

**Do not bind this tool to `cluster-admin` by default.** An LLM-driven tool with `cluster-admin` has the worst-case
blast radius: read every Secret, delete every workload, escalate via aggregated APIs. Start from zero and grant only
what a named caller demonstrably needs; add verbs and kinds as real traffic surfaces them. (See #770.)

The example below is a conservative starting point for a **read-only** deployment scoped to a single namespace. Use it
as a baseline and widen only deliberately (add `create`/`update`/`delete` when you accept the LLM writing; grant
`secrets.get` only when you accept disclosure to any session that reaches the tool; widen to a `ClusterRole` only when
the workload truly needs cross-namespace reach).

```yaml
# Scoped ServiceAccount (same namespace as the mcp-kubernetes Deployment).
apiVersion: v1
kind: ServiceAccount
metadata:
  name: witwave-mcp-kubernetes
  namespace: witwave
---
# Least-privilege Role: read core workload and config kinds, plus the
# pods/log subresource so `logs` works. Does NOT include secrets, SA
# token creation, RBAC kinds, CSRs, or impersonation. Does NOT grant
# apply/delete; add explicit write verbs only if you need them.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: witwave-mcp-kubernetes-read
  namespace: witwave
rules:
  - apiGroups: [""]
    resources:
      - pods
      - services
      - endpoints
      - configmaps
      - namespaces
      - events
      - persistentvolumeclaims
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["pods/log"]
    verbs: ["get"]
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets", "daemonsets", "replicasets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["batch"]
    resources: ["jobs", "cronjobs"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses", "networkpolicies"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: witwave-mcp-kubernetes-read
  namespace: witwave
subjects:
  - kind: ServiceAccount
    name: witwave-mcp-kubernetes
    namespace: witwave
roleRef:
  kind: Role
  name: witwave-mcp-kubernetes-read
  apiGroup: rbac.authorization.k8s.io
```

For multi-namespace read, turn the `Role` into a `ClusterRole` and the `RoleBinding` into a `ClusterRoleBinding` — but
keep the verb list narrow. Never add `secrets` without an explicit decision; Kubernetes Secrets are bearer-equivalent
for whoever holds them.

## Test coverage (#974)

`tools/kubernetes/test_server.py` covers the tool handlers so regressions in the MCP server — shape of tool replies,
error paths when the ServiceAccount lacks a verb, apply/delete propagation — surface in CI rather than first appearing
at reconcile time. Run with `pytest tools/kubernetes/`.

## Tools

| Tool                | Description                                                                                                                                                                                                             |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `list_namespaces`   | List namespaces visible to the ServiceAccount.                                                                                                                                                                          |
| `list_resources`    | List resources of a kind; optional `namespace`, `api_version`, selectors.                                                                                                                                               |
| `get_resource`      | Fetch a single resource by kind / namespace / name.                                                                                                                                                                     |
| `describe`          | Return `{object, events}` for a resource — structured, not kubectl-formatted.                                                                                                                                           |
| `logs`              | Pod logs with `container`, `tail_lines`, `since_seconds`, `previous` options.                                                                                                                                           |
| `apply`             | Server-side apply a YAML/JSON manifest (multi-doc supported).                                                                                                                                                           |
| `delete`            | Delete by kind / namespace / name with configurable propagation policy.                                                                                                                                                 |
| `read_secret_value` | Fetch a Secret's decoded value, gated on `confirm=True` + `MCP_K8S_READ_SECRETS_DISABLED=false`. Every call is audit-logged. Prefer `get_resource` (returns the Secret envelope without decoded data) for normal reads. |

`api_version` is optional but required to disambiguate kinds served by multiple groups (e.g. `Ingress` in
`extensions/v1beta1` vs `networking.k8s.io/v1`).
