---
name: platform-health
description:
  Read-only operational health check for the witwave platform. Use when asked to check platform health, investigate pod
  restarts, startup issues, operator health, release status, runtime storage, resource pressure, rollout drift, or
  whether it is safe to upgrade agents/operator. Produces a Green/Yellow/Red summary, distills problematic anomalies for
  Zora to route, and never mutates cluster or GitHub state without explicit human approval.
version: 0.1.0
---

# platform-health

Run a read-only reliability check across the self-team platform surface. The goal is to answer: _is anything about the
platform behaving oddly enough that Zora should route a fix?_ Prefer evidence over speculation.

## Hard boundaries

Default posture is read-only.

Allowed automatically:

- Inspect `ww`, `kubectl`, `gh`, git state, logs, events, PVCs, releases, tags, and workflow runs.
- Read own memory and peer memory.
- Write a concise report to your own memory namespace.
- Send Zora a distilled anomaly report when a finding looks problematic.

Requires explicit human approval in the triggering request:

- `ww operator upgrade`, `ww agent upgrade`, `ww update`, Helm upgrades, release reruns, or tag pushes.
- `kubectl patch`, `kubectl delete`, `kubectl rollout restart`, scale changes, or PVC mutations.
- Messaging peers other than Zora with operational instructions.
- Telling Zora which mutation to perform instead of handing her the evidence-backed finding.
- Any source-code commit or push.

If a fix requires mutation, stop at observation + Zora handoff unless the user already approved that exact mutation.

## Standard check

Use the namespace from the user request if supplied; otherwise default to `witwave-self` for agents and `witwave-system`
for the operator.

### 1. Tool availability

Check what is available before assuming it works:

```sh
command -v ww || true
command -v kubectl || true
command -v gh || true
```

If a tool is missing, continue with the remaining checks and list the missing capability as a platform gap.

### 2. Operator and CLI alignment

```sh
ww version
ww operator status --namespace witwave-system
ww update --check
```

Look for:

- `ww` version matching the operator app/chart version.
- Operator pod Ready.
- CRDs present.
- Managed CR counts plausible.
- Newer `ww` release available.
- Repeated reconciliation failures or version drift that suggests the operator is not applying desired state.

### 3. Agent readiness and activity

```sh
ww agent list --namespace witwave-self
ww team status --namespace witwave-self --since 1h
```

Look for:

- Any agent not `Ready`.
- Agents with no recent activity when they should have heartbeats.
- Surprising token spikes or conversation gaps.
- Backend counts that differ from expected topology.
- Agents that appear healthy at the CR level but stale or silent in activity status.

### 4. Kubernetes health details

```sh
kubectl get pods,pvc,witwaveagents,witwaveworkspaces --namespace witwave-self
kubectl get events --namespace witwave-self --sort-by=.lastTimestamp | tail -40
kubectl get pods --namespace witwave-system
kubectl get events --namespace witwave-system --sort-by=.lastTimestamp | tail -40
```

If an agent is degraded, inspect just that agent before broadening:

```sh
kubectl describe witwaveagent <agent> --namespace witwave-self
kubectl describe pod -l app.kubernetes.io/instance=<agent> --namespace witwave-self
kubectl logs -l app.kubernetes.io/instance=<agent> --namespace witwave-self --all-containers --tail=120
```

Do not dump huge logs into the final answer. Summarize the relevant lines and preserve exact error snippets only when
they identify the cause.

### 5. Runtime persistence posture

For each active self agent, verify runtime storage exists and is mounted where conversation/task continuity needs it:

- Harness has `/home/agent/logs` and `/home/agent/state` mounted.
- Backend has `/home/agent/logs` and `/home/agent/state` mounted when backend persistence is enabled.
- `TASK_STORE_PATH` points under `/home/agent/state/`.
- Runtime PVC exists and is Bound.

Use JSONPath or `jq` if available; otherwise use `kubectl describe` and summarize.

### 6. Release and CI posture

```sh
gh release list --repo witwave-ai/witwave --limit 5
gh run list --repo witwave-ai/witwave --limit 15
git -C /workspaces/witwave-self/source/witwave status -sb || true
git -C /workspaces/witwave-self/source/witwave log --oneline --decorate -8 || true
```

Look for:

- Failed release workflows after a tag.
- Red CI on `main`.
- A release published without all expected artifacts.
- Local checkout behind/ahead if the question is about source state.
- Repeated failed/recovered release attempts that point to a process bug.

### 7. Report

Return a compact report. If the status is Red, or if the same Yellow finding repeats across runs, distill it into a Zora
handoff.

```text
Status: Green | Yellow | Red
Scope: <namespace / release / agent / operator>
What changed: <one paragraph>
Findings:
- <finding with evidence>
- <finding with evidence>
Zora handoff: <none | sent | recommended>
Human approval needed: <yes/no, and for what>
```

Use status consistently:

- **Green** — no action needed; normal activity can continue.
- **Yellow** — degraded, drifting, or needs follow-up, but no immediate platform fire.
- **Red** — failing release, red CI on main, operator unhealthy, agent unavailable, repeated restarts, missing required
  persistence, or data-loss risk.

### 8. Zora handoff

When a finding is problematic enough to require team work, send Zora a concise A2A message via `call-peer`.

Handoff shape:

```text
Mira platform anomaly report

Status: <Yellow | Red>
Scope: <operator | agent | release | runtime-storage | resource-pressure>
What changed: <one paragraph>
Evidence:
- <command/result/error, summarized>
- <command/result/error, summarized>
Why it matters: <risk to team work, releases, persistence, or recovery>
Suggested owner category: <release | operator | defect | gap | docs | unknown>

Please route this to the right peer or stand the team down if you think it is not actionable.
```

Do not tell Zora to run a specific mutating command. She owns routing. You own evidence quality.

### 9. Memory log

Append a short entry to `/workspaces/witwave-self/memory/agents/mira/platform_health_log.md` when the filesystem is
available:

```markdown
## YYYY-MM-DDTHH:MMZ — platform-health

**Status:** Green | Yellow | Red **Scope:** <scope> **Summary:** <one sentence> **Zora handoff:** <none | sent |
recommended>
```

If memory is unavailable, report that explicitly; do not fail the whole check just because the log write failed.
