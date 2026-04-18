# tools/ — MCP components

Every subdirectory here is an **MCP component**, treated equally regardless of
what it wraps. "MCP" is the component name in this repo — each server speaks the
Model Context Protocol and is consumed by backends via their MCP configuration
(`mcp.json` under `.claude/`, `.codex/`, or `.gemini/` — all three backends use
the same wire format).

MCP servers are designed to run inside the Kubernetes cluster where nyx is
deployed and operate against that cluster via in-cluster credentials
(ServiceAccount token + RBAC). They are shared infrastructure — one deployment
typically serves every agent in the cluster rather than being replicated
per-agent.

## Current MCP components

| Component        | Directory                  | Image                     |
| ---------------- | -------------------------- | ------------------------- |
| `mcp-kubernetes` | [kubernetes/](kubernetes/) | `mcp-kubernetes:latest`   |
| `mcp-helm`       | [helm/](helm/)             | `mcp-helm:latest`         |

## Conventions

- One directory per component.
- Each ships a `Dockerfile`, a `server.py` MCP entrypoint, and a
  `requirements.txt`.
- Image tag is always `mcp-<dirname>:latest`.
- Servers target the **deployed** cluster only — no support for arbitrary
  remote kubeconfigs. Auth is in-cluster by default; access is whatever the
  ServiceAccount's RBAC allows.
- Stable component names are referenced by agents in their MCP configuration.
- Register new components in the `Building Images` section of `AGENTS.md` and
  tag related issues/PRs with the `mcp` GitHub label.

## Digest pinning (#855)

The `nyx` Helm chart accepts a `digest` field alongside `repository`/`tag`
on every MCP tool (`mcpTools.<name>.image.digest`). When set the template
renders `repository@<digest>` and `tag` is ignored. Prefer an immutable
digest over a rolling tag in production: MCP pods typically hold a
cluster ServiceAccount token, and a re-tagged upstream image could be
pulled silently with vulnerable code.

```yaml
mcpTools:
  kubernetes:
    enabled: true
    image:
      repository: ghcr.io/skthomasjr/images/mcp-kubernetes
      digest: sha256:abc123...
```
