---
name: agent-deploy
description:
  Canonical recipe for creating, upgrading, and verifying WitwaveAgent deployments. Use this skill before running ANY
  `kubectl patch` against a WitwaveAgent CR — manual patches inevitably strip sibling fields (gitMappings, credentials,
  env, storage.mounts) and break the agent silently. Trigger when the user says "deploy a new agent", "roll the agents",
  "bump the agent image", "upgrade evan to claude:0.X.Y", or any variant. Also trigger before any change to
  `.spec.image.tag` or `.spec.backends[*].image.tag`.
version: 1.0.0
---

# agent-deploy

Canonical paths for changing the WitwaveAgent CRs. The bottom-line rule:

> **Never `kubectl patch --type=merge` on `spec.backends[*]`.** Merge-patch on an array element REPLACES the element.
> Sibling fields (`gitMappings`, `storage`, `credentials`, `env`, `port`) get silently dropped, the pod boots 3/3 Ready
> per K8s probes, but the agent has no skills mounted and stands down every heartbeat. 2026-05-10 incident: ~17.5 hours
> of dead time before the strip was caught.

Three shapes covered: (1) initial create, (2) image bump (the one that bit us), (3) field tweak. Each has its own
canonical path; pick the right one.

## Shape 1 — Create a new agent

Always `ww agent create`. Document of record: `docs/bootstrap.md`.

```sh
ww agent create <name> \
  --namespace witwave-self \
  --workspace witwave-self \
  --with-persistence \
  --backend claude \
  --harness-env TASK_TIMEOUT_SECONDS=7200 \
  --harness-env CONVERSATIONS_AUTH_DISABLED=true \
  --backend-env claude:TASK_TIMEOUT_SECONDS=7200 \
  --backend-env claude:CONVERSATIONS_AUTH_DISABLED=true \
  --backend-secret-from-env claude=CLAUDE_CODE_OAUTH_TOKEN \
  --backend-secret-from-env claude=GITHUB_TOKEN_<NAME>:GITHUB_TOKEN \
  --backend-secret-from-env claude=GITHUB_USER_<NAME>:GITHUB_USER \
  --gitsync-bundle https://github.com/witwave-ai/witwave.git@main:.agents/self/<name> \
  --gitsync-secret-from-env GITSYNC_USERNAME:GITSYNC_PASSWORD
```

`ww agent create` knows to wire `gitMappings`, `storage.mounts`, `credentials`, port allocation (8000 for harness, 8001
for claude backend), and the workspace volumes. None of these are negotiable; they're what the agent needs to function.

## Shape 2 — Bump an image tag (the dangerous one)

Today there's NO `ww agent set-image` or `ww agent upgrade` subcommand. Until it exists, do this:

### Path A (preferred when the image versions are released and tagged)

Use `kubectl patch --type=json` with explicit `replace` ops on JUST the tag fields:

```sh
TAG="0.23.4"  # or whatever
NS="witwave-self"
for agent in iris evan kira nova zora finn; do
  kubectl patch witwaveagent "$agent" -n "$NS" --type=json -p '[
    {"op":"replace","path":"/spec/image/tag","value":"'"$TAG"'"},
    {"op":"replace","path":"/spec/backends/0/image/tag","value":"'"$TAG"'"}
  ]'
done
```

JSON-patch with explicit paths is surgical: it touches ONLY the named fields. Sibling fields stay intact.

**Bump harness AND claude together.** Different versions have port-binding compat expectations (claude:0.23.4 binds port
8000 directly; harness:0.17.0 also binds 8000 → port-clash in shared netns → CrashLoopBackOff). The version-pair is
treated as a unit.

### Path B (when in doubt — re-render the CR from chart values)

If the agent's CR has drifted from canonical (which can happen after multiple manual patches, or if you're unsure what's
there), the safest recovery is to delete + recreate via `ww agent create`. Lose pod uptime; gain a known-good config.

## Shape 3 — Change a non-image field (env, gitMappings, storage)

Same rule: NEVER `--type=merge` on `backends[*]`. Either:

- Use `--type=json` with explicit operations on the target path, OR
- Re-run `ww agent create --replace` (when that flag exists), OR
- Render the CR from chart values via `helm template` and `kubectl apply -f`.

For env-only tweaks:
`kubectl patch witwaveagent <name> -n <ns> --type=json -p '[{"op":"add","path":"/spec/backends/0/env/-","value":{"name":"NEW_VAR","value":"x"}}]'`.

## Verification — required after ANY change

K8s "pod 3/3 Ready" is the container-liveness signal, NOT the workload-functional signal. A claude container can pass
`/health/ready` while the AI inside has zero skills mounted and can't do anything. Verify the workload layer:

```sh
# 1. Wait for pods to settle. Don't trust the first "settled" — multiple rolls may be in progress.
until [ "$(kubectl get pods -n witwave-self --no-headers 2>/dev/null \
    | grep -cE '0/3|1/3|2/3|CrashLoop|Init|Terminating|Error|Pending|ContainerCreating')" -eq 0 ]; do
  sleep 10
done

# 2. Verify all 6 pods 3/3 Running.
kubectl get pods -n witwave-self --no-headers

# 3. Verify the running images.
for pod in $(kubectl get pods -n witwave-self --no-headers | awk '{print $1}'); do
  agent=$(echo $pod | cut -d- -f1)
  imgs=$(kubectl get pod $pod -n witwave-self \
    -o jsonpath='{range .spec.containers[*]}{.name}={.image}{","}{end}' \
    | tr ',' '\n' | grep -E 'harness=|claude=' | tr '\n' ' ')
  echo "$agent: $imgs"
done

# 4. CRUCIAL: verify the workload — ZORA can find her skills.
ZP=$(kubectl get pod -n witwave-self -l app.kubernetes.io/name=zora --no-headers | awk '{print $1}')
kubectl exec $ZP -n witwave-self -c claude -- ls /home/agent/.claude/skills/

# Expected output:
#   call-peer
#   discover-peers
#   dispatch-team
#   git-identity
#   self-tidy
#   team-status
#   team-tidy
#
# If `dispatch-team` is missing → gitMappings was stripped → the agent is non-functional and
# you need to restore the full backends[0] spec via Path B above.

# 5. (Image-bump verification) Verify the toolchain in evan if you bumped Go-relevant images.
EP=$(kubectl get pod -n witwave-self -l app.kubernetes.io/name=evan --no-headers | awk '{print $1}')
kubectl exec $EP -n witwave-self -c claude -- bash -c 'go version; staticcheck --version | head -1'
```

## Common failure modes (learned the hard way 2026-05-10)

| Symptom                                                                                                   | Root cause                                                                                                       | Fix                                                                                                                                                  |
| --------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Pod CrashLoopBackOff with `address already in use` on port 8000                                           | claude+harness version skew (claude binds 8000 directly; harness:0.17.0 also binds 8000)                         | Bump both images to the same version OR set `backends[0].port: 8001` so claude uses the alternate port                                               |
| Pod 3/3 Running but zora stands down every tick with `"dispatch-team is not among the registered skills"` | `gitMappings` on `backends[0]` was stripped by a `--type=merge` patch — `/home/agent/.claude/` is empty          | Re-add `gitMappings: [{gitSync: witwave, src: .agents/self/<name>/.claude/, dest: /home/agent/.claude/}]` and `storage.mounts` to `backends[0]`      |
| Pod 3/3 Running but `/health/ready` returns 503                                                           | `CONVERSATIONS_AUTH_DISABLED=true` env var was stripped — auth fails closed                                      | Re-add `env: [{name: CONVERSATIONS_AUTH_DISABLED, value: "true"}]` to `backends[0]`                                                                  |
| New agent can't write to memory at `/workspaces/witwave-self/memory/agents/<name>/`                       | `workspaceRefs` missing OR pod's working-directory pinned to `/home/agent/workspace` outside the workspace mount | Add `--with-persistence` and `--workspace witwave-self` to `ww agent create`. For existing CRs, restore `spec.workspaceRefs: [{name: witwave-self}]` |
| Image bump succeeds but claude container instantly CrashLoops                                             | Wrong port allocation — `backends[0].port` set to 8000 (collides with harness)                                   | Set `backends[0].port: 8001` (the canonical claude backend port; harness owns 8000)                                                                  |

## Out of scope

- **Operator install / upgrade** — `ww operator install / upgrade / status` handles the witwave-operator deployment
  itself. Different surface from agent CRs.
- **MCP tool deploys** — `mcp-kubernetes`, `mcp-helm`, `mcp-prometheus` deploy via the chart's `mcpTools` block, not via
  WitwaveAgent CRs.
- **Workspace creation** — `WitwaveWorkspace` is a separate CR; create via `ww workspace create` (when that exists) or
  via chart values.
