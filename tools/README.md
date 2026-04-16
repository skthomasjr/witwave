# tools/

MCP-compatible tool servers that extend agents with cluster-side capabilities.

Each subdirectory is a standalone MCP server. Servers are designed to run inside
the Kubernetes cluster where nyx is deployed and operate against that cluster
via in-cluster credentials (ServiceAccount token + the cluster's kubeconfig
surface). They are consumed by agents through the MCP protocol configured in
each backend's `mcp.json` / `config.toml`.

## Current tools

| Tool                       | Purpose                                                    |
| -------------------------- | ---------------------------------------------------------- |
| [kubernetes/](kubernetes/) | MCP server exposing Kubernetes API operations             |
| [helm/](helm/)             | MCP server exposing Helm release operations               |

## Conventions

- One directory per tool.
- Each tool ships a `Dockerfile`, a `server.py` MCP entrypoint, and a
  `requirements.txt`.
- Servers target the **deployed** cluster only — no support for arbitrary
  remote kubeconfigs. Auth is in-cluster by default.
- Tool names are stable identifiers; agents reference them by name in MCP
  configuration.
