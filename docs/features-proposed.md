# Features Proposed

Last updated: 2026-04-05 by nova (fifth pass — OpenHands v1.6.0 Kubernetes/RBAC, SDK budget API corrected to
max_budget_usd/max_turns, permission_mode="plan" confirmed, LangGraph v1.1 deferred nodes; added F-012, F-013)

See [competitive-landscape.md](competitive-landscape.md) for competitor analysis and research themes that inform these
proposals.

---

## Feature Proposals

### F-001 — AskUserQuestion tool enabled

**Status:** proposed

**Value:** Provides a HITL safety gate at zero architectural cost. Agents can use it selectively before destructive
actions (file deletion, git push, external API calls). The SDK already handles the full prompt/response cycle — it just
needs to be unlocked. Directly addresses Devin's key lesson: agents need to know when to ask. Note: `AskUserQuestion` is
available to main agents only — it is unavailable to subagents per SDK bug #12890. This project does not use subagents,
so this limitation is not blocking.

**Implementation:** Add `"AskUserQuestion"` to the `allowed_tools` list at `executor.py:156`. One element added to an
existing list. The operator receives the question in the active conversation; the agent awaits the reply before
proceeding. No session or bus changes needed.

**Risk:** Low — isolated to a single list; no effect on agents that never call the tool.

**Questions:** none.

---

### F-003 — Structured shared memory index

**Status:** proposed (on hold — requires shared Docker volume infrastructure)

**Value:** Markdown memory files work for prose notes but are fragile for structured data (timestamps, status flags,
team-wide facts). A shared index gives agents a lightweight structured store without introducing a database. Addresses
the memory gap vs. CrewAI and AutoGPT. Evidence of demand: the `heartbeat-test.md` already functions as a structured
shared log — agents naturally want this pattern. CrewAI's 2026 documented limitation (losing coordination state when a
crew ends) reinforces why persistent shared memory matters for compounding team intelligence.

**Implementation:** No Python code change required. Define the convention in `CLAUDE.md` and each agent's behavioral
config: `shared/index.yaml` is the structured store, agents read/write it by key, concurrent writes use a `.lock`
sentinel file. Document the schema and access pattern in CLAUDE.md. Requires a dedicated shared Docker volume so all
agents can access the same path.

**Risk:** Low — purely a file convention once the shared volume exists; no infrastructure change beyond the volume
mount. Concurrent write risk is low for a 3-agent team with infrequent writes.

**Questions:** Should the shared volume be added to `docker-compose.yml` as a named volume (simplest) or a host-mounted
path (inspectable)? Does the YAML schema need versioning from day one?

---

### F-009 — SDK hook integration for guardrails and audit

**Status:** proposed

**Value:** The Claude Agent SDK ships 18 hook events (`PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `Notification`,
`Stop`, `SubagentStart`, `SubagentStop`, `PreCompact`, `PermissionRequest`, `PermissionDenied`, `UserPromptSubmit`,
`SessionStart`, `SessionEnd`, `Setup`, `TeammateIdle`, `TaskCompleted`, `ConfigChange`, `WorktreeCreate/Remove`) that
are entirely unused by this project. This is the SDK's primary mechanism for programmatic safety and harness-level audit
— directly addressing the SWE-agent ACI philosophy gap and validated by LangGraph 2.0's first-class guardrail nodes.
Without hooks, this project relies entirely on agent judgment to avoid dangerous operations and on free-text log
scanning for post-hoc audit.

Three concrete, high-value uses with strong evidence of demand:

1. **`PreToolUse` guardrails with `updatedInput`**: The `PreToolUse` hook fires before any tool execution and supports
   two response modes — `permissionDecision: "deny"` (block the call) and `updatedInput` (rewrite tool arguments before
   execution). Rewriting enables ACI-style transforms: path sandboxing (redirect dangerous paths to safe locations),
   argument normalization, stripping destructive flags. Blocking enables outright prevention of catastrophic commands
   (e.g., `rm -rf /`). A `systemMessage` injected on block lets the agent understand why it was stopped and adjust.
   SWE-agent's mini-SWE-agent (65% SWE-bench in 100 lines) validates that hard-coded harness-layer constraints reduce
   cascading failures — the most common production failure mode.

2. **`PostToolUse`/`PostToolUseFailure` structured audit**: Hooks are a cleaner, more complete integration point than
   the current approach of inspecting `AssistantMessage` blocks in `run_query()` — they fire for every tool result
   regardless of message structure, and `PostToolUseFailure` captures errors the current implementation misses entirely.

3. **`Notification` forwarding with async hooks**: The SDK fires `Notification` events for agent status changes
   (`permission_prompt`, `idle_prompt`, `auth_success`). Using the `async: true` hook option routes these to a
   structured log line or an optional external webhook (Slack, PagerDuty) without blocking the agent loop.

**Implementation:** In `executor.py`'s `make_options()`, add a `hooks` dict to `ClaudeAgentOptions`. Three async
callback functions registered via `HookMatcher` (import from `claude_agent_sdk`):

- `PreToolUse` (no matcher — fires for all tools): first apply `updatedInput` rewrites from a configurable transform map
  (path prefixes, flag patterns), then check a configurable denylist of Bash patterns and protected file path prefixes;
  return `permissionDecision: "deny"` plus a `systemMessage` explaining the block if matched. Load config from
  `/home/agent/.claude/guardrails.json` if present (opt-in, hot-reloadable via `watchfiles`); fall back to a narrow
  built-in default (catastrophic patterns only: `rm -rf /`, `chmod -R 777 /`) if absent.

- `PostToolUse` / `PostToolUseFailure` (no matcher): emit a structured JSONL audit entry including tool name, session
  ID, wall time, and error status. This replaces or supplements the current `log_tool_event()` call in `run_query()`
  with an SDK-native path that captures errors more reliably.

- `Notification` (no matcher): emit a structured `INFO` log line; if `NOTIFY_WEBHOOK_URL` env var is set, fire an async
  HTTP POST using `async: true` hook option (non-blocking, errors caught and logged, never raised).

New optional file: `/home/agent/.claude/guardrails.json` —
`{"deny_bash_patterns": [...], "protected_paths": [...], "rewrite_paths": {...}}`. No Python changes outside
`executor.py` and `make_options()`.

**Risk:** Medium — hooks touch the core execution path. The `PreToolUse` deny logic must be conservative by default: a
built-in denylist that over-blocks will silently degrade agent capability, which is worse than no guardrail. The
`updatedInput` rewrite path requires careful testing — incorrect rewrites could cause subtle misbehavior. The
`PostToolUse` audit path is low-risk (additive logging). The `Notification` webhook is low-risk (async, errors caught).

**Questions:** What should the built-in default denylist contain — only provably catastrophic commands, or a broader
set? Should the denylist be additive (agent-specific `guardrails.json` extends a project-wide default in `CLAUDE.md`) or
per-agent only? Should the `PreToolUse` handler optionally call `AskUserQuestion` (F-001) for intermediate-risk
operations rather than outright denying them?

---

### F-010 — Budget cap per agent run

**Status:** proposed

**Value:** Prevents runaway API costs from stuck or looping agents. The current project has no budget or turn bound — a
looping agent can exhaust significant API quota with no operator warning. The Claude Agent SDK provides two
complementary controls: `max_budget_usd` caps total API spend in USD per session, and `max_turns` caps agentic turns
(tool-use round trips). Industry research in 2026 finds 90% of production agents are over-resourced; cost control is a
top operational concern. Both controls are directly addressable via env vars and one-line additions to `make_options()`.

**Implementation:** In `make_options()` in `executor.py`, read two optional env vars: `MAX_BUDGET_USD` (float) and
`MAX_TURNS` (integer). If set, pass as `max_budget_usd` and `max_turns` to `ClaudeAgentOptions` respectively. If not
set, omit — no cap applied. Entirely opt-in. No changes to execution path, bus, or session management. Per-agent limits
are configurable via distinct env vars in each service's `environment` block in `docker-compose.yml`. Both can be set
independently: `MAX_TURNS` is the lighter guardrail for runaway loops; `MAX_BUDGET_USD` is the financial ceiling.

**Risk:** Low — isolated to `make_options()`; agents without these env vars set are completely unaffected. Setting
`MAX_TURNS` too low would silently cut off legitimate long-running tasks, so defaults should be unset (no cap).

**Questions:** Should heartbeat and agenda runs share the same caps as A2A-triggered runs, or should each message kind
have its own limit? The `kind` field in `Message` would allow per-kind env vars (e.g., `MAX_TURNS_HEARTBEAT`,
`MAX_TURNS_AGENDA`). Should exceeding `max_budget_usd` raise a catchable exception or terminate the session silently?
The SDK behaviour on budget exhaustion needs verification before implementing error-handling logic.

---

### F-012 — Planning mode for agenda items

**Status:** proposed

**Value:** Complex agenda items that involve multi-file changes, architectural decisions, or irreversible side effects
benefit from a structured planning phase before execution. OpenHands v1.5.0 made the Planning Agent its headline feature
(two-phase Plan/Code workflow); Devin enforces plan-before-code as a hard checkpoint in its assign-and-review loop.
SWE-agent research confirms that agents which plan before acting produce fewer cascading failures. Without a planning
phase, agents jump directly into implementation — the most common source of wasted compute and operator trust erosion in
long-running autonomous runs. The Claude Agent SDK's `permission_mode="plan"` implements exactly this pattern natively:
read-only tool access plus a single writable plan file, preventing code changes until the plan is reviewed. This serves
both individuals (safer autonomous runs) and enterprise teams (auditable intent before action). Complexity is opt-in —
only agenda items that set the frontmatter flag are affected.

**Implementation:** In `agenda.py`'s `parse_agenda_file()`, recognize a `mode` frontmatter field. When `mode: plan` is
set, store it on the `AgendaItem` dataclass (new optional `mode` field, default `None`). In `run_agenda_item()`, pass
the mode value through to `executor.py` via `Message.metadata["permission_mode"]`. In `executor.py`'s `make_options()`,
read `permission_mode` from the message metadata dict; if set to `"plan"`, pass `permission_mode="plan"` to
`ClaudeAgentOptions`. The SDK handles the rest: tools are read-only except for a single writable plan file. No changes
to `bus.py`, `heartbeat.py`, or `main.py`. New frontmatter field documented in agent `CLAUDE.md` files. Example agenda
item:

```yaml
---
name: Architecture Review
schedule: "0 9 * * 1"
mode: plan
---
Review the codebase for architectural issues and produce a plan.
```

**Risk:** Low — opt-in via agenda frontmatter; only items that explicitly set `mode: plan` are affected. The SDK's
`plan` permission mode is a documented, stable option. Default execution path is unchanged for all existing items.

**Questions:** Should the plan file path be configurable (e.g., `plan_file` frontmatter) or always the SDK default?
After a planning run produces a plan file, what is the intended follow-up workflow — does the operator manually trigger
a code-mode run against the same session, or should the harness support a `mode: plan-then-execute` two-phase option?
Manual handoff is simpler and safer for the initial implementation.

---

### F-013 — On-demand agenda item trigger via HTTP

**Status:** proposed

**Value:** The current architecture is purely schedule-driven (cron). Agents cannot react to external events — a GitHub
webhook, a Slack notification, a completed CI/CD run, or a monitoring alert cannot directly trigger an agent to act.
This is the core workflow pattern behind Devin (reads tickets from Linear/Jira/GitHub/Slack), OpenHands enterprise
integrations, and the multi-agent coordination research finding that hybrid orchestration (schedule + event-driven)
outperforms either approach alone. An HTTP trigger endpoint closes the gap between this project's autonomous scheduled
model and reactive, event-driven workflows without requiring external message brokers or new infrastructure. This serves
both individuals (trigger from a local script or curl) and enterprises (wire into existing webhook-based toolchains).
Kubernetes-native: the endpoint is already behind a `Service`/`Ingress` and can be secured with standard network
policies.

**Implementation:** Add a `POST /trigger/{name}` route in `main.py`. The handler accepts an optional JSON body
`{"prompt": "override text", "session_id": "optional-override"}`. It looks up the named agenda item in
`AgendaRunner._items` and fires a `Message` onto the bus with the item's configured content (or the override prompt if
provided), its configured session ID (or override), and `kind=f"trigger:{name}"`. Returns 202 with the session ID if
enqueued, 404 if the item name is not found, 409 if the item is currently running (`item.running is True`). Requires
`AgendaRunner` to expose a public `trigger(name, prompt_override, session_id_override)` async method and for `main.py`
to capture the `agenda_runner` instance in a module-level reference accessible to the route handler. Optional
authentication via `TRIGGER_AUTH_TOKEN` env var — if set, the endpoint requires a matching
`Authorization: Bearer <token>` header; if unset, the endpoint is open (trusted-network model, standard for Kubernetes
internal services).

**Risk:** Medium — adds a new HTTP surface area to the container. Authentication must be opt-in but clearly documented;
an unauthenticated trigger endpoint is acceptable for Kubernetes internal services but a security hole if exposed
publicly. The 409 conflict handling (already-running items) needs testing. Exposing `AgendaRunner` to the route handler
requires a module-level reference, which is a minor coupling increase in `main.py`.

**Questions:** Should triggered runs use the agenda item's configured `session_id` (continuity with scheduled runs) or a
fresh UUID per trigger (isolation)? Should the response body include the agent's output (synchronous, blocking) or only
the session ID for async polling via A2A? Blocking would simplify webhook integrations but risks timeouts on
long-running items.
