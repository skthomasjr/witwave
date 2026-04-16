# tools/ — MCP components

Every subdirectory here is an **MCP component**, treated equally regardless of
what it wraps. "MCP" is the component name in this repo — each server speaks the
Model Context Protocol and is consumed by backends via their MCP configuration
(`mcp.json` for a2-claude, `config.toml` for a2-codex, equivalent for
a2-gemini).

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
