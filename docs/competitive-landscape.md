# Competitive Landscape

Last updated: 2026-04-05 by nova (sixth pass — Devin 2.2 self-verification + Schedule Devins, Claude Agent SDK
HookMatcher callback API + task_budget + AgentDefinition, mini-SWE-agent v2 tool calling, CrewAI root_scope + Qdrant
Edge, Observability theme updated to reflect 70+ metrics)

---

## Positioning

Most autonomous agent tools are designed to be driven by a human sitting at a development machine — a CLI you run
locally, a UI you open in a browser, or an IDE extension you trigger manually. This project takes a different approach:
agents run as containerized services on infrastructure, operating autonomously on their own schedules without a human
present to start each task. The unit of deployment is a container, not a developer session. This makes it suitable for
running on remote servers, CI/CD infrastructure, or cloud-hosted environments where no interactive session exists —
closer in spirit to a daemon or a microservice than a developer tool.

That distinction shapes the comparison below. Each reference product is labeled with its autonomy model:

- **Human-driven** — a human initiates every task; the agent is a tool the human wields
- **Semi-autonomous** — can run unattended for a single task, but requires human setup and handoff per run
- **Autonomous** — runs persistently on a schedule without a human present; self-directed within defined boundaries

Most reference products are human-driven tools that happen to use agents internally. This project targets the autonomous
tier — infrastructure that hosts agents rather than a tool a developer runs.

---

## Reference Products

### OpenHands (formerly OpenDevin)

**Autonomy model:** Human-driven (tasks are initiated by a human via CLI or UI; the agent executes the task autonomously
but does not self-schedule)

OpenHands is an open-source autonomous coding platform with a composable Python SDK (`software-agent-sdk`), CLI, local
GUI, and cloud/enterprise deployment. Current version: v1.6.0 (March 30, 2026); 69,500+ GitHub stars. Scores 77.6+ on
SWEBench Verified; community benchmarks report 87% of bug tickets resolved same-day. Key differentiators: multi-LLM
support (Claude, GPT, any open-source model), deep integrations with Slack, Jira, Linear, GitHub, GitLab, Azure DevOps,
Bitbucket, and MCP servers.

**v1.5.0 headline feature — Planning Agent (BETA):** Implements a two-phase Plan/Code workflow. In Plan Mode, the agent
has read-only tool access except for a single writable file (`PLAN.md` in the workspace root) — deliberately preventing
premature code changes. The agent produces a structured plan with implementation steps, API signatures, and testing
strategy; for vague prompts it asks clarifying questions. Users then switch to Code Mode in the same conversation to
execute against the plan. Model preferences are configurable per mode (e.g., a stronger reasoning model for planning, a
faster model for coding). A **Task List Panel** provides real-time progress tracking for long-running sessions. A
**slash command menu** (type `/`) surfaces loaded agent skills for rapid selection.

**v1.6.0 — Kubernetes and hook support (March 30, 2026):** Kubernetes deployment with multi-user support and RBAC —
OpenHands can now be deployed as a production Kubernetes workload with access control. Hook support was added to the
platform, giving operators programmatic intercept points over agent execution. The `/clear` command allows starting a
fresh chat while preserving sandbox state. `/new` was added as a slash command. Global skills can be toggled on/off
per-workspace. Code block copy buttons added to the GUI.

**Agent coordination:** Sub-agent delegation is supported via a blocking parallel execution model — a parent agent
spawns sub-agents as independent conversations that inherit workspace context and model config. GUI-level sub-agent
visibility is tracked in GitHub issue #13030 (CLI/API only as of April 2026). **Microagents** — modular knowledge
snippets triggered by keywords in messages — enable repository-aware context injection via `AGENTS.md` files. $18.8M
Series A raised November 2025.

**Relative standing:** OpenHands has more enterprise integrations, multi-LLM flexibility, a planning/task-tracking
layer, and sub-agent coordination than this project. The Planning Agent's two-phase pattern (plan before code) is the
clearest recently-shipped capability this project lacks at the harness level. The Claude Agent SDK's `plan` permission
mode (read-only + plan file) provides the native primitive to implement the same pattern.

### Claude Code / Claude Agent SDK

**Autonomy model:** Human-driven (Claude Code is an interactive CLI; the Agent SDK is a library for building agents, not
an autonomous runtime by itself — this project is the autonomous harness built on top of it)

The Claude Agent SDK (renamed from Claude Code SDK, late 2025) is the runtime this project builds on. We pin `0.1.55`;
the current release is `0.1.56` (delta: fix for silent truncation of large MCP tool results over 50K chars). Key SDK
capabilities not yet wired into this project:

**Hooks system — Python callback API via `HookMatcher`:** The SDK overview page confirms hooks are registered as Python
callback functions in `ClaudeAgentOptions`, not file-based config. Example from SDK docs:
`hooks={"PostToolUse": [HookMatcher(matcher="Edit|Write", hooks=[log_file_change])]}`. The `matcher` is a regex on tool
names; `hooks` is a list of async callback functions. Available events include `PreToolUse`, `PostToolUse`, `Stop`,
`SessionStart`, `SessionEnd`, `UserPromptSubmit`, and more. Entirely unused by this project. Key underused capabilities
within the hooks system:

- **`updatedInput` in `PreToolUse`**: rewrite tool arguments before execution — not just block or allow, but actively
  transform (e.g., sandbox path redirection, argument normalization, stripping dangerous flags). This enables ACI-style
  constraints at the harness layer without prompting.
- **`async: true` option**: fire-and-forget hooks using `asyncTimeout` — log writes and webhook POSTs don't block the
  agent loop.
- **`systemMessage` output**: any hook can inject model-visible guidance when an action is blocked or modified.

**Budget and turn control:** `task_budget` (v0.1.51) caps token budget per session. `maxTurns` is available as an
`AgentDefinition` field for subagent turn limits. Both are unset in this project — a stuck or looping agent can exhaust
quota with no bound. `get_context_usage()` (0.1.52) exposes real-time token consumption by category, enabling proactive
warnings before context exhaustion causes silent failure.

**In-process custom tools:** The `@tool()` decorator and `create_sdk_mcp_server()` factory allow defining custom tools
as plain Python functions inside the harness process — no subprocess, no IPC overhead, no separate MCP server to manage.
Tools are passed via `mcp_servers={"name": sdk_server}` in `ClaudeAgentOptions`. Entirely unused by this project.
Enables lightweight harness-native tools (e.g., a structured status reporter, a bus-aware escalation tool) without the
operational weight of an external MCP server.

**Session management (0.1.49–0.1.51):** `fork_session()`, `delete_session()`, `tag_session()`, `rename_session()` — not
exposed by this harness. `RateLimitEvent`, `TaskStarted`, `TaskProgress`, `TaskNotification` typed messages also
available.

**Programmatic subagent definitions (0.1.49–0.1.51):** `AgentDefinition` accepts `description`, `prompt`, `tools`,
`disallowedTools`, `maxTurns`, `initialPrompt`, `skills`, `memory`, and `mcpServers`. Passed via
`agents={"name": AgentDefinition(...)}` in `ClaudeAgentOptions`. Enables the harness to define specialized subagents
programmatically without file-based configuration. Entirely unused by this project.

**Advanced execution options:** `enable_file_checkpointing` enables file-change tracking for session rewinding. `effort`
sets thinking depth (`"low"`, `"medium"`, `"high"`, `"max"`). `plugins` accepts a list of `SdkPluginConfig` objects for
custom plugins loaded from local paths. All unused by this project.

**Permission modes:** Five modes — `default`, `dontAsk`, `acceptEdits`, `bypassPermissions`, `plan` — set via
`permission_mode` in `ClaudeAgentOptions`. The `plan` mode (read-only execution + single writable plan file) is
confirmed in current SDK docs and mirrors OpenHands's Planning Agent pattern exactly. **`AskUserQuestion`** is available
for HITL (main agents only — unavailable to subagents per SDK bug #12890; this project does not use subagents, so not
blocking).

**Relative standing:** This project uses a growing but still narrow slice of the SDK — `ClaudeSDKClient` with
`get_context_usage()`, session resume, MCP config, per-agent model selection, and 70+ Prometheus metrics wrapping the
execution path. The hooks system (Python callback API via `HookMatcher`), `task_budget` for cost control, in-process
custom tools, `permission_mode="plan"` for structured task execution, and `AgentDefinition` for programmatic subagents
are the most actionable gaps. Each is a targeted addition to `executor.py`'s `make_options()` with no structural changes
to the project.

### Devin (Cognition)

**Autonomy model:** Semi-autonomous (a human assigns a task via Slack or web UI; Devin executes it end-to-end
unattended, then surfaces a PR for review — each task is human-initiated, not self-scheduled)

Devin was rebuilt on Claude Sonnet 4.5 in September 2025. MCP support was added, giving access to hundreds of external
tools via a standardized interface. Natively reads tickets from Linear, Jira, Slack, and GitHub; writes the
implementation, runs tests, and opens a PR. The workflow pattern in practice is an **"assign-and-review" loop**: teams
assign backlog items, Devin drafts PRs, engineers review output rather than individual steps and run multiple instances
in parallel. The embedded observable IDE (shell + editor + browser) allows engineers to watch or take over at any point.
Deployed by Goldman Sachs alongside 12,000 human engineers.

**Devin 2.2 (February 24, 2026) — self-verification and computer use:** Devin now implements a complete autonomous
development cycle: plan → code → review → auto-fix → PR — all before a human opens the PR. Computer use testing gives
Devin access to its own Linux desktop to launch and test desktop applications, with screen recordings for review.
Startup time was reduced 3x. The self-verification loop is the most complete closed-loop autonomous development cycle
shipped by any agent product.

**Schedule Devins (March 2026) — self-scheduling and parallel delegation:** Devin can now set up its own recurring
schedules from natural language descriptions, carrying state between runs via persistent notes. A coordinator Devin
delegates to managed Devins — each a full isolated VM — that work in parallel. This is architecturally close to this
project's agenda + A2A delegation model, but with a key difference: Devin infers the schedule from natural language
rather than requiring explicit cron expressions. The stateful scheduling pattern (each run builds on the previous)
validates this project's session-based agenda design.

**Relative standing:** Devin is a vertical product; this project is infrastructure. Transferable lessons: show the plan
before acting, structure work for parallel execution, make agent actions observable mid-run, and carry state across
scheduled runs. The self-verification loop (plan → code → review → fix) and self-scheduling are the strongest new
patterns. This project's agenda system already provides scheduled execution with session continuity; Devin's "Schedule
Devins" validates the model while highlighting the value of event-driven triggers (F-013) and planning mode (F-012) as
complements to cron.

### SWE-agent

**Autonomy model:** Human-driven (run as a CLI command against a specific GitHub issue; produces a patch, then stops —
no persistent runtime or scheduling)

The Princeton team's active 2026 development is **mini-SWE-agent**: achieving 65% on SWE-bench Verified in approximately
100 lines of Python. This is the strongest published validation of the ACI philosophy — a minimal, constrained tool set
with tight feedback loops achieves strong benchmark performance with near-zero scaffolding. SWE-bench Pro launched in
2026 with 1,865 multi-language tasks (vs. 500 Python-only for Verified); OpenAI stopped reporting Verified scores after
detecting training data contamination. Current open-source SOTA: Claude 4.5 Opus at 76.8% on bash-only. The v1.1.0
release shipped training trajectories for fine-tuning.

**Mini-SWE-agent v2 (2026):** A major rewrite that switches to native tool calling API by default (text-based action
parsing still available), adds multimodal input support, and reaches 74%+ on SWE-bench Verified with Gemini 3 Pro.
Research finding: randomly switching between GPT-5 and Sonnet 4 during a run boosts performance — model diversity within
a single run is a novel technique not yet explored by this project or competitors.

**Relative standing:** mini-SWE-agent's 100-line implementation validates the core ACI principle more strongly than
ever: simplicity and constraints outperform complex scaffolding. The v2 model-diversity finding is notable — this
project already supports per-agent model selection via `CLAUDE_MODEL`, and per-agenda-item model selection via the
`model` frontmatter field, but not mid-run model switching. This project's tool list remains general-purpose with no
harness-level guardrails. The SDK's `PreToolUse` hook with `updatedInput` is the natural implementation point for
ACI-style argument transforms and blocking.

### AutoGPT

**Autonomy model:** Semi-autonomous to autonomous (platform mode runs agents as persistent services; classic CLI mode is
human-initiated per run; the managed platform supports scheduled and event-driven execution — the closest reference
product to this project's model)

AutoGPT (v0.6.9+) runs as a managed platform with a visual workflow builder, agent marketplace, and server/frontend
architecture. Maintains short-term and long-term memory with self-reflection after each action. 167,000+ GitHub stars.
Has shifted from a developer framework toward a no-code platform.

**Relative standing:** AutoGPT's tiered memory is more mature than this project's flat markdown files. Its platform
direction (visual builder, marketplace) is out of scope here.

### CrewAI

**Autonomy model:** Human-driven (a crew is instantiated and kicked off by Python code a human runs; event-driven Flows
add reactivity but crews do not self-schedule — they are called)

Multi-agent orchestration framework. The headline 2025–2026 development is the **unified Memory class**: a single
intelligent API replacing the prior four-way split, with LLM-inferred hierarchical scopes (filesystem-style paths like
`/project/alpha`), composite recall scoring (semantic similarity + recency decay + importance weighting), and
non-blocking background saves. A `crewai memory` terminal browser enables direct inspection. **RuntimeState
serialization** (v1.13.0, April 2026) enables snapshots of crew state for crash recovery. Token usage tracking landed in
`LLMCallCompletedEvent`. Qdrant Edge added for on-device vector storage. Enterprise Control Plane adds real-time tracing
and a unified control plane; 1.4B agentic automations claimed.

**March 2026 additions:** Automatic `root_scope` for hierarchical memory isolation — memory hierarchies are now inferred
automatically rather than requiring manual scope configuration. **Agent skills** were implemented as a first-class
concept. **Tool search** dynamically injects appropriate tools during execution rather than loading all tools upfront,
saving tokens. These additions bring CrewAI closer to this project's skill-document and tool-selection patterns.

A 2026 documented limitation: when a crew completes its task, coordination patterns, delegation decisions, and role
effectiveness data are lost — without shared persistent memory, teams cannot compound intelligence over time.

**Relative standing:** CrewAI's unified structured memory with composite recall and hierarchical scoping remains the
clearest memory gap relative to this project. RuntimeState serialization advances the durability story. CrewAI's new
tool search (dynamic tool injection) is interesting — this project's `ALLOWED_TOOLS` env var is static per agent. The
automatic `root_scope` for memory isolation validates the value of hierarchical scoping for multi-agent teams. This
project uses A2A for coordination (distributed, standard, network-based); CrewAI uses in-process Python calls (tighter
coupling, lower latency).

### LangGraph

**Autonomy model:** Human-driven to semi-autonomous (graphs are triggered by external events or human calls; LangGraph
Cloud adds persistent deployment and event-driven triggers, pushing toward semi-autonomous)

**LangGraph 2.0 (February 2026)** is a significant production-focused release. Key changes:

- **HITL redesigned:** `interrupt()` now uses structured payloads (replacing the exception-based `NodeInterrupt`) —
  intent is unambiguous, code is cleaner. Resume via `Command(resume=value)` unchanged.
- **Checkpointing mandatory:** Required at graph initialization (breaking change from 1.x). PostgreSQL checkpointers
  gained connection pooling (`pool_size`, `schema_name`) for multi-tenant production deployments.
- **Guardrail nodes as first-class primitives:** Content filtering, rate limiting (per-user/per-thread/global), and
  audit logging with field redaction are now declarative config rather than custom code.
- **MCPToolkit:** Standardized MCP integration for agent-to-tool connections.
- **A2A integration:** Cross-framework agent-to-agent communication via message brokers, confirming A2A as the emerging
  coordination protocol.
- **"Deep Agents":** Agents that plan, spawn subagents, and leverage file systems for complex multi-step tasks.

**LangGraph v1.1 (March 2026)** is a fully backwards-compatible follow-on release. Key additions:

- **Type-safe invoke/stream (`version="v2"`):** `invoke()` and `stream()` now accept `version="v2"` for structured
  `GraphOutput` and `StreamPart` return types — cleaner application-layer integration.
- **Pydantic and dataclass coercion:** State values are automatically coerced to declared model types in v2 mode.
- **Node caching:** Cache individual node results to skip redundant computation — especially useful for iterative
  development and replay workflows.
- **Deferred nodes:** Delay node execution until all upstream paths complete — the canonical implementation of
  map-reduce, consensus, and collaborative multi-agent fan-out/fan-in patterns.
- **Deploy CLI:** `langgraph deploy` pushes a graph to LangSmith Deployment in one step.
- **Bug fix:** Time-travel with interrupts and subgraphs now correctly restores parent checkpoint state.

**Relative standing:** LangGraph 2.0's mandatory checkpointing validates F-005 (implemented). Its declarative guardrail
nodes and A2A integration confirm the direction of F-009. The HITL `interrupt()` redesign reinforces the value of F-001.
The A2A integration confirms this project's protocol choice is converging with the broader ecosystem. LangGraph v1.1's
deferred nodes are the reference implementation of the map-reduce coordination pattern relevant to future multi-agent
work; node caching is the reference for memoized agenda runs.

### A2A Protocol (Ecosystem)

**Autonomy model:** Protocol-level (A2A defines how agents communicate regardless of autonomy model; this project uses
it as the coordination layer between autonomous agents)

The protocol ecosystem has expanded to four recognized layers: **MCP** (agent-to-tool), **A2A v0.3** (agent-to-agent —
gRPC transport, signed agent security cards, 150+ supporting organizations), **ACP** (lightweight async messaging), and
**UCP** (agentic commerce — co-developed with Shopify, Visa, Mastercard, January 2026). LangGraph 2.0 added native A2A
integration. **Microsoft Foundry** added an A2A Tool (Preview) in early 2026, letting Foundry agents call any
A2A-protocol endpoint with explicit auth and clean call/response semantics — further validating A2A as the
cross-platform agent communication standard. The W3C AI Agent Protocol Community Group is working toward official web
standards (expected 2026–2027). The winning production coordination topology: hybrid — a high-level orchestrator for
strategic decisions + local mesh networks for tactical execution.

This project already implements A2A and is well-positioned within this growing ecosystem. The hybrid topology insight
aligns with the project's existing design (heartbeat-driven orchestration + A2A delegation).

---

## Research Themes

### Memory

**What competitors do:** CrewAI now offers a unified Memory class with LLM-inferred hierarchical scopes
(`/project/alpha`-style paths), composite recall (semantic similarity + recency decay + importance weighting), and
non-blocking background saves with direct terminal inspection. AutoGPT persists long-term memory for self-reflection.
LangGraph stores full workflow state externally for checkpoint/resume. Research (Mem0, MemGPT) confirms purpose-built
retrieval memory outperforms long-context prompting for selective recall. CrewAI's 2026 limitation — losing coordination
state when a crew ends — highlights the value of persistent shared memory for compounding team intelligence.

**What users value most:** Agents that remember previous work across runs. The current markdown memory files work for
prose notes but are fragile for structured data — timestamps, status flags, team-wide facts — that need reliable
read/update semantics.

**Candidate features:** A shared structured memory index (YAML) at a well-known path that all agents read and write with
named keys. No infrastructure dependency beyond a shared Docker volume. (F-003, on hold pending shared volume.)

---

### Observability

**What competitors do:** OpenHands provides per-run tool-use traces. Devin 2.2 surfaces screen recordings of agent
testing. LangGraph emits OpenTelemetry-compatible spans — OTel-based tracing across all reasoning steps, tool calls, and
memory accesses is the 2026 emerging standard. CrewAI tracks token usage in `LLMCallCompletedEvent`. A new term — "AI
archaeology" — describes the difficulty of debugging long execution traces; good observability prevents this. 89% of
organizations have implemented agent observability in 2026.

**Where this project stands:** This project now has **70+ Prometheus metrics** across all subsystems — SDK query
duration/errors/tool calls, per-tool latency and error rates, context token usage and exhaustion events, bus queue depth
and processing duration, per-agenda-item duration/lag/success/error timestamps, heartbeat timing and skip counts,
session LRU cache utilization, MCP config reload tracking, health probe hit counts, startup duration, and more. This is
among the most comprehensive agent observability implementations in the ecosystem. The JSONL tool-use trace log (F-002)
provides the raw event layer. Context usage monitoring via `get_context_usage()` (F-011) provides proactive threshold
warnings.

**What users value most:** The ability to understand why an agent took a specific action, and to surface patterns (which
tools fail most, which agenda items are slowest) without reading free-text logs. Proactive warnings before context
limits silently degrade reliability. The next frontier is OpenTelemetry-compatible distributed tracing across
multi-agent workflows.

**Candidate features:** Context usage monitoring via `get_context_usage()` (F-011, implemented). Prometheus `/metrics`
endpoint with 70+ metrics (F-008, implemented).

---

### Human-in-the-Loop

**What competitors do:** LangGraph 2.0's declarative `interrupt()` pauses a graph node mid-execution and resumes after
human input with structured payloads. The Claude Agent SDK ships `AskUserQuestion` as a built-in HITL tool (main agents
only — unavailable to subagents per SDK bug #12890; this project does not use subagents). Devin shows its plan before
touching code. All production agent systems treat approval gates as a standard pattern.

**What users value most:** Targeted checkpoints before destructive or irreversible actions, without blocking routine
work. The narrower the gate, the less friction.

**Candidate features:** Enable `AskUserQuestion` in `executor.py` — a one-line change that unlocks the SDK's built-in
HITL primitive (F-001).

---

### Guardrails / Safety

**What competitors do:** mini-SWE-agent validates that minimal tools + tight constraints = better outcomes (65%
SWE-bench Verified in 100 lines). LangGraph 2.0 ships guardrail nodes as first-class primitives — content filtering,
rate limiting, and audit logging with field redaction as declarative config. The Claude Agent SDK's `PreToolUse` hook
now supports `updatedInput` — **rewriting tool arguments before execution**, not just blocking — enabling path
sandboxing, argument normalization, and flag stripping at the harness layer. 90% of production agents are
over-permissioned (2026 industry finding). The accepted control hierarchy: prevention first (hooks), then human
intervention (`AskUserQuestion`), then recording (trace log).

**What users value most:** Prevention of repeated error patterns (documented failure mode in SWE-agent research),
enforcement of security policies without polluting agent prompts, and audit trails for compliance. This is distinct from
F-001: hooks are automatic and programmatic; `AskUserQuestion` is agent-initiated and interactive.

**Candidate features:** SDK hook integration — `PreToolUse` (with `updatedInput`) for blocking/rewriting dangerous
operations, `PostToolUse`/`PostToolUseFailure` for harness-level audit, `Notification` for routing agent status to
external systems (F-009).

---

### Coordination

**What competitors do:** CrewAI uses in-process Python delegation. LangGraph 2.0 routes via graph edges, parallel
fan-out, and now native A2A integration. A2A (used by this project) is at v0.3 with 150+ supporting organizations.
Research shows structured planner-worker hierarchies significantly outperform flat "bag of agents" patterns:
unstructured swarms compound errors at up to 17x the rate of structured coordination. The winning production topology is
hybrid: a high-level orchestrator for strategic coordination + local mesh networks for tactical execution. Devin's
"Schedule Devins" (March 2026) adds self-scheduling with parallel delegation to managed Devins, each in an isolated VM —
validating this project's agenda-based scheduling model. Devin also reacts to external events (GitHub PRs, Jira tickets,
Slack messages) rather than only scheduled runs — event-driven triggers bridge autonomous agents to the rest of the
organization's toolchain.

**What users value most:** Assigning a task to a specific agent and getting a result back without manually constructing
A2A request payloads. Clear accountability in multi-agent workflows. The ability for external systems (CI/CD pipelines,
GitHub webhooks, monitoring alerts) to trigger a specific agent on demand without a cron schedule.

**Candidate features:** A `delegate` skill document that wraps A2A into a clean natural-language pattern for agents
(F-006, implemented). On-demand HTTP trigger endpoint for event-driven agent workflows (F-013).

---

### Durability / Crash Recovery

**What competitors do:** LangGraph 2.0 made checkpointing _mandatory_ (breaking change), reinforcing that it is no
longer optional for production systems. PostgreSQL checkpointers gained connection pooling for multi-tenant deployments.
AutoGPT persists task queues. Temporal.io's durable workflow pattern has become a 2026 reference architecture. Platforms
without checkpoint/resume are explicitly not considered production-ready.

**What users value most:** Long-running scheduled tasks should not silently restart from zero after a crash. Detection
is the minimum viable step; full resume requires SDK-level support.

**Candidate features:** F-005 (implemented — stale checkpoint detection and warning on startup). Full session resume is
a longer-term follow-on requiring SDK-level checkpoint/restore support beyond the current `resume=session_id` mechanism.

---

### Tooling / MCP

**What competitors do:** LangGraph 2.0 ships MCPToolkit for standardized MCP connections. The Claude Agent SDK,
OpenHands, and virtually every major agent platform support MCP natively. MCP is under Linux Foundation governance
(donated December 2025). Hundreds of community MCP servers cover browsers, databases, APIs, and system integrations.

**What users value most:** Browser automation and database access are the most-requested extensions beyond file/shell
operations.

**Candidate features:** Per-agent opt-in MCP configuration via `.claude/mcp.json` (F-004, implemented).

---

### Planning / Task Decomposition

**What competitors do:** OpenHands v1.5.0 made the Planning Agent its headline feature — a two-phase Plan/Code workflow
where the agent operates in a read-only mode until a structured `PLAN.md` is produced, then switches to execution mode.
Devin enforces the same pattern as a hard checkpoint: humans review and approve the plan before any code is written.
SWE-agent research confirms that agents which plan before acting produce fewer cascading failures. The Claude Agent SDK
ships `permission_mode="plan"` natively — read-only tool access + one writable plan file — as a first-class option in
`ClaudeAgentOptions`.

**What users value most:** Agents that think before acting on complex tasks — especially multi-file changes,
architectural decisions, or agenda items with irreversible side effects. A planning phase surfaces the agent's intent
before it touches production files, giving operators a low-friction checkpoint.

**Candidate features:** Planning mode for agenda items — opt-in via `mode: plan` frontmatter, passing
`permission_mode="plan"` to `ClaudeAgentOptions` (F-012).

---

### Cost / Token Management

**What competitors do:** The Claude Agent SDK provides `task_budget` (v0.1.51) to cap token budget per session, and
`maxTurns` as an `AgentDefinition` field for subagent turn limits. `get_context_usage()` (0.1.52) exposes real-time
token consumption by category. CrewAI v1.13.0 added token usage tracking in `LLMCallCompletedEvent`. Industry research
in 2026 finds 90% of production agents are over-resourced, with cost control emerging as a top operational concern. The
compounding reliability finding is relevant: at 85% per-step reliability, a 10-step workflow succeeds only ~20% of the
time end-to-end — context exhaustion silently degrades reliability at the tail end of long runs without any explicit
error.

**What users value most:** Predictable, bounded API costs per agent run. Proactive warnings before context limits cause
silent degradation. No runaway API bills from stuck or looping agents.

**Candidate features:** Budget cap per agent run via `task_budget` (F-010). Context usage monitoring via
`get_context_usage()` (F-011, implemented).
