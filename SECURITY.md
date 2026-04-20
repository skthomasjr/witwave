# Security Policy

This project runs LLM-generated code inside Kubernetes clusters with access
to the apiserver, Secrets, the pod network, and webhooks reaching external
systems. Security reports are taken seriously. This document describes how
to get one to us and what we consider in scope.

## Reporting a vulnerability

Email: **security@witwave.ai**

Please do **not** open a public GitHub issue for a suspected vulnerability.
If email is inconvenient, GitHub's private vulnerability reporting (on the
repository's **Security** tab → "Report a vulnerability") works too.

A useful report includes:

- Affected version — commit SHA, chart version, or image tag
- A minimal reproducer or enough detail that a maintainer can recreate it
- Your assessment of impact (auth bypass, privilege escalation, information
  disclosure, etc.)
- A suggested fix, if you have one in mind

Reports in English preferred; other languages will slow us down but won't
stop us.

## What to expect after you report

This is a small project. We'll aim to acknowledge your report quickly and
keep you updated as we investigate. Complex issues may take longer than
simple ones — we'd rather give you an honest timeline than miss one we
already promised.

If your report turns out to be out of scope (see below), we'll tell you
why; it's not a brush-off.

## Supported versions

The project is pre-1.0. Security fixes land on `main` and flow into the
next tagged release. We don't backport to prior tags.

| Version     | Gets fixes?                              |
| ----------- | ---------------------------------------- |
| `main`      | Yes                                      |
| Latest tag  | Typically yes, when a fix release is cut |
| Older tags  | No — upgrade to the latest               |

## In scope

Issues we consider in scope (non-exhaustive):

- **Auth bypass** on protected endpoints — `/conversations`, `/trace`,
  `/mcp`, `/api/traces[/...]`, `/events/stream`, `/api/sessions/*/stream`,
  harness trigger endpoints, ad-hoc-run endpoints listed under
  `/.well-known/agent-runs.json`.
- **Bearer-token exposure** in logs, metrics, events, persisted JSONL, or
  anywhere else a scrape/collector might pick it up.
- **Session-ID hijacking** across caller identities. When
  `SESSION_ID_SECRET` is set, `shared/session_binding.derive_session_id`
  HMACs session IDs to the caller's bearer fingerprint; a bypass or
  downgrade is in scope.
- **MCP command allow-list bypass** — invoking shell commands outside
  `MCP_ALLOWED_COMMANDS` / `MCP_ALLOWED_COMMAND_PREFIXES` /
  `MCP_ALLOWED_CWD_PREFIXES`, or coaxing the allow-list into accepting
  something it shouldn't.
- **Operator privilege escalation** — a CR-driven write reaching a kind,
  namespace, or Secret outside the documented RBAC scope (see
  `charts/witwave-operator/values.yaml` `rbac.*` keys).
- **SSRF in webhook delivery** — reaching in-cluster or localhost
  services via URL shapes the allow-list (`WEBHOOK_ALLOW_LOOPBACK_HOSTS`,
  scheme/host/port checks) didn't anticipate.
- **Redaction bypass** — sensitive values surviving the `shared/redact.py`
  pipeline into logs, events, or trace attributes. The redaction guarantee
  is idempotent merge-spans; a counterexample is a bug.
- **Traceparent / session-ID injection** letting a caller set or forge
  another agent's trace context or session identity.
- **Server-Side Apply regressions** where a reconciler write overwrites a
  human-owned field on a Secret, ConfigMap, or other kind labelled
  `app.kubernetes.io/component: credentials`.

## Out of scope

- **LLM jailbreaks** that make the backend produce certain text. The
  model's content output is not a security boundary. Hook policies and
  redaction are defenses in depth, not perimeter controls.
- **Denial-of-service by an already-authorized caller.** Authorized
  callers can always degrade their own agent's availability. If the DoS
  vector reaches unauthorized callers, that's in scope.
- **Issues that require cluster-admin to begin with.** If the attacker
  already has cluster-admin on the cluster, they don't need this platform.
- **Third-party dependency CVEs** with existing upstream advisories —
  track those upstream; we'll pick up fixes through normal dependency
  updates.
- **Missing security headers on non-auth-gated paths** that don't carry
  sensitive data (the bare `/health` and `/metrics` listeners, assuming
  `METRICS_ENABLED` is off on public networks — which it should be).

## Known-hazardous areas (designed-in risks, not bugs)

A few risks are inherent to how the platform works. Reporting these as
bugs is out of scope, but understanding them is useful context:

- **MCP tool containers execute LLM-generated commands.** The allow-lists
  in `shared/mcp_auth.py` and per-tool envs are narrow by default, but
  they are *policy*, not *proof*. Binding any MCP tool to
  `cluster-admin` is a footgun and the per-tool READMEs (`tools/*/README.md`)
  call it out.
- **Hook policies** (`.claude/hooks.yaml`, the engine in
  `backends/claude/hooks.py`) are a filtering and observability layer.
  They are useful, but they are not a security boundary — an LLM that
  wants to exfiltrate via a hook-allowed path likely can. Design your
  auth and RBAC assuming hook policy may leak.
- **Local-dev escape hatches** — `CONVERSATIONS_AUTH_DISABLED=true` and
  `MCP_TOOL_AUTH_DISABLED=true` exist so operators don't invent worse
  bypasses. Both log loud startup warnings. They are intentional, not
  bugs.

## Coordinated disclosure

We prefer coordinated disclosure and will work with you on a timeline
that gives us room to fix the issue before details become public. Typical
industry practice is around 90 days from initial acknowledgment, but we
don't treat that as a contract — complex fixes may warrant more time, and
obvious ones less. We'll talk.

## Working with us

Good-faith security research on your own deployment, or on
deliberately-exposed test infrastructure, is welcome. Don't exfiltrate
real user data, don't persist unauthorized access once you've confirmed a
finding, and don't degrade production systems you don't own. If you're
uncertain whether your planned research is in bounds, email us before
you start — we'd rather answer a preflight question than interpret a
fait accompli.

This isn't a legal safe-harbor contract; it's a description of how we'd
like to work with researchers.

## Credit

We're happy to credit reporters in release notes or commit trailers.
Tell us how you'd like to be listed — by name, handle, or anonymously.

## Bug bounty

None. The project is pre-1.0 and privately funded. We can offer credit,
gratitude, and a fix that benefits everyone running the platform. That's
what we've got.
