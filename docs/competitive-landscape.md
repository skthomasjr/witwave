# Competitive Landscape

Last updated: 2026-04-19 by local-agent (tenth pass — aggressive cut of Research Themes from deep
research-synthesis bibliography to one-paragraph navigational scaffolding per theme, pointing at
Reference Products for current state. Industry statistics that were previously inline have been retired;
they drift invisibly and can't be kept honest without quarterly refresh discipline. The preceding ninth
pass earlier today did the full Reference Products refresh: added 5 competitors (OpenClaw, kagent,
Amazon Bedrock AgentCore, Microsoft Agent Framework + Foundry, Cloudflare Agent Cloud); dropped 2
(AutoGPT, SWE-agent); corrected LangGraph v2.0 framing; bumped 5 entries to April 2026 releases;
added Category references appendix; revised Positioning + Gap Analysis for the three market realities
that have coalesced in the last 60 days.)

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

A secondary positioning axis: **real-time observability with a pinned wire contract**. Because agents run as
services rather than developer sessions, operators need a live window into fleet behaviour — not periodic
pulls or webhook fan-out. The platform exposes a versioned Server-Sent Events stream (`/events/stream`) with
14 typed event shapes (`job.fired`, `webhook.delivered`, `conversation.turn`, `tool.use`, `trace.span`, …),
`Last-Event-ID` resume, and per-session drill-down streams that carry token-level `conversation.chunk`
events. Every client (web dashboard, `ww` CLI, future mobile) consumes the same schema documented in
`docs/events/`. Most reference products either don't ship a live observability stream or couple it to a
proprietary UI; publishing the schema as a first-class multi-client contract is a differentiator.

**Category context (April 2026).** Three market realities shape the comparisons below:

1. **"Agent Fabric / Agent Mesh / Agent Cloud"** has coalesced as the enterprise-category name in the last
   60 days — Cloudflare, Salesforce, MuleSoft, ServiceNow, Equinix, Nutanix all use one of these three
   phrases. This project's harness + A2A + multi-backend routing sits squarely in that category
   architecturally, though the project's positioning language is still "autonomous-agent infrastructure."
2. **Kubernetes-native agent infrastructure is no longer empty space.** kagent is in the CNCF sandbox,
   OpenClaw has a dedicated operator, OpenHands v1.6 added Kubernetes deployment + RBAC (March 2026), and
   kubernetes.io published an "Agent Sandbox" post. What was a wide lane is now contested — differentiation
   moves to specifics (multi-backend routing under one identity, scheduler-primitives breadth, etc.).
3. **A2A + MCP + OpenTelemetry are now the assumed baseline tripod.** Every 2026 launch — Microsoft Agent
   Framework, kagent, Bedrock AgentCore Gateway, Cloudflare Agent Cloud — leads with all three. Shipping
   them is no longer a differentiator; *how* they compose (cross-pod topology, per-named-agent routing,
   published event schema) is where the differentiation now lives.

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

The Claude Agent SDK (renamed from Claude Code SDK, late 2025) is the runtime this project builds on. **The Claude
Agent SDK for Python was formally released on 2026-04-18** (bundles Claude Code CLI; requires Python 3.10+) — the
SDK is now a first-party supported product line, not a thin wrapper. Claude Code shipped 30+ releases during a
five-week sprint in April 2026. Recent notables:

- **Ultraplan early preview (Apr 6–10, 2026):** Cloud-drafted plans with a web editor; plans can run locally or
  remotely. Pushes Claude Code further toward cloud-hosted agent execution.
- **`ant` CLI:** A new standalone command-line client for the Claude API with native Claude Code integration and
  YAML-versioned API resources — Anthropic's bid on the "agent infrastructure race" positioning.
- **Focus view, stronger permissions + sandbox handling, richer status line, better resume/transcript reliability,
  improved Bash + MCP stability.** Iteration-level polish across every edge of the CLI.

Key SDK capabilities not yet wired into this project:

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
delegates to managed Devins — each a full isolated VM — that work in parallel. Architecturally close to this
project's agenda + A2A delegation model, except Devin infers the schedule from natural language rather than requiring
explicit cron expressions.

**Devin-in-Windsurf (2026-04-15):** Cognition integrated cloud Devin with Windsurf local dev, letting developers
hand off tasks between a local IDE session and a remote Devin instance on the same repo. Plus progressive web app
installation (desktop + mobile), browser-tab favicon session-status dots, a **PR Digest** (read-only view of
Devin-session PRs for users who haven't yet connected GitHub), **GitHub Enterprise Server support** in the Review
flow, repository-level Review permission enforcement, and IDP (Okta) groups management UI in Enterprise settings.
The IDE-adjacent integration + enterprise identity / review-governance posture is Cognition's 2026-Q2 theme.

**Relative standing:** Devin is a vertical product; this project is infrastructure. Transferable lessons: show the plan
before acting, structure work for parallel execution, make agent actions observable mid-run, and carry state across
scheduled runs. The self-verification loop (plan → code → review → fix) and self-scheduling are the strongest new
patterns. This project's agenda system already provides scheduled execution with session continuity; Devin's "Schedule
Devins" validates the model while highlighting the value of event-driven triggers (F-013) and planning mode (F-012) as
complements to cron.

### Hermes Agent (NousResearch)

**Autonomy model:** Autonomous (runs persistently on user-controlled infrastructure; connects to messaging platforms and
operates proactively — the closest architectural peer to this project in the new 2026 open-source landscape)

Hermes Agent (MIT, NousResearch, released February 2026, **v0.10.0 on 2026-04-16**) is built around the thesis that an
agent should learn from completed work and get measurably better the longer it runs. Ships weekly. Key capabilities:
persistent memory via prompt-injected files + SQLite FTS5 with LLM-powered summarization; **auto-generated skills**
— after completing a complex task the agent writes a new skill document for future reuse (FTS5 now indexes 118+
bundled + generated skills, with top matches prepended to context); six terminal backends (local, Docker, SSH,
Daytona, Singularity, Modal); 40+ built-in tools; **multi-platform messaging gateway — 16 supported platforms**
(Telegram, Discord, Slack, WhatsApp, Signal, iMessage via BlueBubbles, WeChat/WeCom, Android/Termux native, CLI, …).

**v0.9.0 (2026-04-13, "The Everywhere Release"):** Android/Termux native, iMessage via BlueBubbles, WeChat/WeCom
callback mode, Fast Mode (`/fast`), local web dashboard, background-process monitoring, native xAI (Grok) + Xiaomi
MiMo providers, pluggable context engine.

**v0.10.0 (2026-04-16, "The Tool Gateway Release"):** Nous Portal subscribers get web search, image gen, TTS, and
browser automation (Firecrawl, FAL/FLUX 2 Pro, OpenAI TTS, Browser Use) bundled without separate API keys — a
subscription-bundled tool gateway that is a new monetization vector for the category.

**Relative standing:** Hermes Agent is the most direct architectural peer in the open-source world on the
consumer / personal-assistant axis. Its layered memory stack (FTS5 + LLM summarization + pluggable providers) and
auto-skill-generation are materially ahead of this project's flat markdown files and static skill documents. Its
messaging-first gateway is out of scope for this project's A2A/HTTP model. The auto-skill-generation pattern
remains the most transferable idea.

### CrewAI

**Autonomy model:** Human-driven (a crew is instantiated and kicked off by Python code a human runs; event-driven Flows
add reactivity but crews do not self-schedule — they are called)

Multi-agent orchestration framework. **Current: v1.14.2 (2026-04-17).** Headline 2025–2026 capabilities:
**unified Memory class** (LLM-inferred hierarchical scopes, composite recall scoring, non-blocking background saves,
`crewai memory` terminal browser), **Tool search** (dynamic tool injection — loads only tools relevant to the current
task rather than the full allow-list), Qdrant Edge for on-device vector storage, Enterprise Control Plane with
real-time tracing.

**v1.14.0 (2026-04-07) — checkpoint/resume primitives:** First-class `CheckpointConfig` auto-checkpointing, `checkpoint
list` / `checkpoint info` CLI, `SqliteProvider` checkpoint store, runtime-state checkpointing with event-system refactor,
`guardrail_type` + name labels on traces. SSRF and path-traversal protections added to RAG tools. Excluded embedding
vectors from memory serialization (token savings). Bumped `litellm ≥1.83.0` to pick up a CVE patch (CVE-2026-35030).

**v1.14.2 (2026-04-17):** Fix for `flow_finished` event after HITL resume; `cryptography` bump to 46.0.7 for
CVE-2026-39892. The two CVE patches in a single minor cycle signal CrewAI maturing its enterprise-security posture.

**Relative standing:** CrewAI's unified structured memory with composite recall remains the clearest memory gap
relative to this project. Its new tool search (dynamic, task-aware tool injection — loading only tools relevant to
the current prompt) is the state of the art here and a real gap; this project has static `ALLOWED_TOOLS` per agent.
Checkpoint/resume primitives at v1.14.0 advance the durability story. This project uses A2A for coordination
(distributed, standard, network-based); CrewAI uses in-process Python calls (tighter coupling, lower latency).

### LangGraph / LangGraph Platform

**Autonomy model:** Human-driven to semi-autonomous (graphs are triggered by external events or human calls;
**LangGraph Platform** adds persistent deployment + event-driven triggers, pushing toward semi-autonomous)

**Current: LangGraph v1.1.0 (2026-03-10) + LangGraph Platform GA (late 2025).** An earlier pass of this doc
mislabeled deferred nodes and node-level caching as "v2.0" features — they are **v1.x** features shipped during the
2025 LangGraph Release Week. There is no v2.0 on PyPI as of April 2026; the stable line is v1.x.

**Key v1.x capabilities (accumulated through v1.1.0):**

- **HITL via `interrupt()`** with structured payloads + resume via `Command(resume=value)`.
- **Checkpointing mandatory** at graph initialization, with PostgreSQL checkpointer pooling for multi-tenant
  deployments.
- **Guardrail nodes as first-class primitives** (content filtering, per-user/per-thread/global rate limiting, audit
  logging with field redaction).
- **MCPToolkit** for standardized MCP integration.
- **Native A2A integration** — cross-framework agent-to-agent over message brokers, confirming A2A as the emerging
  coordination protocol.
- **Deferred nodes** (v1.x) — delay node execution until all upstream paths complete; canonical map-reduce / consensus
  / multi-agent fan-out-fan-in implementation.
- **Node-level caching** (v1.x) — cache individual node results to skip redundant computation during iterative
  development and replay.
- **Type-safe `invoke()` / `stream()` via `version="v2"`** with Pydantic / dataclass coercion of state values.
- **Deploy CLI** (`langgraph deploy`) pushes a graph to LangGraph Platform in one step.

**LangGraph Platform (GA, late 2025):** Purpose-built runtime for long-running, stateful agents. Durable state
persistence, resume-from-interruption, built-in HITL, streaming. **~400 companies running it in production** as of
the March 2026 LangChain newsletter. The Platform — not the library alone — is the right reference for a
production-grade comparable to this project's harness + scheduler surface.

**Relative standing:** LangGraph Platform is now a peer production runtime; its checkpointing model validates F-005
(implemented). Declarative guardrail nodes and A2A integration reinforce F-009 direction. HITL `interrupt()`
redesign reinforces the value of F-001. This project's differentiator vs. LangGraph Platform is **multi-backend
routing under one named-agent identity** (LangGraph Platform is single-framework — agents are LangGraph-authored),
plus the full scheduler-primitive surface (jobs / tasks / triggers / heartbeats / continuations / webhooks) vs.
LangGraph's graph-execution model.

### A2A Protocol (Ecosystem)

**Autonomy model:** Protocol-level (A2A defines how agents communicate regardless of autonomy model; this project uses
it as the coordination layer between autonomous agents)

**A2A v1.0 is now the stable version.** Governance has been donated to the **Linux Foundation** as an official
project; one-year anniversary milestone (2026-04-09) reports 150+ participating organizations and 22k+ GitHub stars.
Production deployments include Azure AI Foundry and Amazon Bedrock AgentCore (both of which embed A2A as their
native cross-agent protocol). v1.0 adds **Signed Agent Cards** — cryptographic signatures on Agent Cards to prevent
forgery and card-redirect attacks, closing a real multi-tenant security gap.

The broader protocol ecosystem continues to be four layers: **MCP** (agent-to-tool), **A2A v1.0** (agent-to-agent),
**ACP** (lightweight async messaging), and **UCP** (agentic commerce — co-developed with Shopify, Visa, Mastercard).
Native A2A support is now present in LangGraph v1.x, Microsoft Foundry Agent Service, kagent, and Amazon Bedrock
AgentCore. The W3C AI Agent Protocol Community Group is working toward official web standards (expected 2026–2027).

**Relative standing:** This project already implements A2A as a first-class citizen — harness routes any inbound
message to backend agents, named agents are reachable from peer named agents over A2A, and the hybrid
orchestrator-plus-local-mesh topology identified in 2026 matches this project's heartbeat + delegation design.
v1.0's Signed Agent Cards is the next conformance milestone — verifying signatures on inbound agent cards before
accepting requests is a straightforward gap to close.

### OpenClaw (Peter Steinberger / community)

**Autonomy model:** Autonomous (self-hosted personal agent, runs 24/7, messaging-driven; the closest philosophical
peer to this project in the open-source world)

OpenClaw originated as "Clawdbot" in November 2025, was renamed "Moltbot" on 2026-01-27 under Anthropic trademark
pressure, and three days later settled on **OpenClaw**. **Over 160,000 GitHub stars.** Runs on user-controlled
infrastructure (notable community trend: a Mac Mini hardware rush for 24/7 hosting). Access surface is chat UIs in
Signal / Telegram / Discord / WhatsApp. Connects to Claude, DeepSeek, and OpenAI models.

**Kubernetes posture:** A dedicated `openclaw-rocks/openclaw-operator` explicitly offers "production-grade security,
observability, and lifecycle management" — a direct parallel to this project's nyx-operator. AWS published a "Run
OpenClaw on Amazon Lightsail" blog; NVIDIA shipped **NemoClaw** safety tooling for it. Security posture is a
publicly-acknowledged weakness (third-party skills remote-code-execution, exposed instances).

**Relative standing:** OpenClaw is the single strongest direct open-source competitor to this project. It ships
containerized, multi-backend (Claude / OpenAI / DeepSeek), operator-managed, 24/7 autonomous — nearly every axis we
position around. Key differentiators in this project's favor: (1) stronger safety posture via the `hooks.yaml`
declarative policy + MCP allow-list + session-id HMAC binding; (2) A2A-native team coordination vs. OpenClaw's
single-agent framing; (3) a published event-stream wire contract that multiple clients (dashboard, `ww` CLI, future
mobile) consume. OpenClaw's differentiator in its favor: category-leading install base + community skill ecosystem.

### kagent (Solo.io / CNCF sandbox)

**Autonomy model:** Autonomous, Kubernetes-native

Open-source framework for building, deploying, and running AI agents on Kubernetes. Initial announce March 2025;
contributed to CNCF sandbox at KubeCon EU 2025; active 2026 development. Built on **A2A + ADK + MCP**, with pre-built
tools for Prometheus, pod logs, and standard Kubernetes APIs — a direct overlap with this project's
mcp-kubernetes / mcp-helm / mcp-prometheus surface. Runtime is Microsoft AutoGen. CNCF backing gives kagent distribution
weight this project doesn't have.

**Relative standing:** Our nearest cloud-native OSS competitor. Both projects are Kubernetes-native and lead with
A2A + MCP; kagent doesn't offer a multi-backend router analogous to this project's `backend.yaml` routing across
Claude / Codex / Gemini under one named-agent identity, and uses AutoGen rather than direct SDK wrappers. The
clearest question for our positioning: "multi-backend under one identity" and "scheduler-primitives-first" (jobs +
tasks + heartbeats + triggers + continuations + webhooks) are the defensible differentiators vs. kagent's
AutoGen-runtime-plus-prebuilt-tools approach.

### Amazon Bedrock AgentCore (AWS)

**Autonomy model:** Autonomous, managed cloud

Managed platform for "securely deploy and operate AI agents at any scale" — preview 2025; **Policy GA 2026-03-03,
Evaluations GA 2026-03-31.** Surface includes a runtime, a gateway (tool/MCP access), memory, identity,
observability, policy (governance), and evaluations (quality). Covers the same infrastructure concerns as this
project's harness, but as an AWS-managed service. Locked to Bedrock-hosted models.

**Relative standing:** Mandatory hyperscaler reference. AgentCore's Policy + Evaluations track directly against this
project's hook policy engine + emerging smoke-test surface. Differentiators: we're open-source, self-hosted, and
model-backend-agnostic (Claude / Codex / Gemini); AgentCore is closed, managed, Bedrock-only. The competitive
dynamic is hyperscaler-managed-SaaS vs. self-hosted-Kubernetes — classic split.

### Microsoft Agent Framework + Foundry Agent Service (Microsoft)

**Autonomy model:** Semi-autonomous (orchestration framework + managed runtime)

**Agent Framework:** Open-source framework (Python + .NET) for building and orchestrating multi-agent workflows,
public preview late 2025. First-class A2A, MCP, and OpenTelemetry — exactly the same tripod we ship.

**Foundry Agent Service:** GA announced March 2026. OpenAI Responses-compatible API; hosts DeepSeek, xAI, Meta,
LangChain, LangGraph models (in addition to Azure OpenAI). Directly overlaps this project's cross-backend
orchestration. Differentiator is Azure-first deployment; not Kubernetes-operator-native.

**Relative standing:** The Microsoft entry in the category. A2A + MCP + OTel parity at the framework level
forecloses our "we ship these" differentiator from Option A framing — narrowing to *how* we compose them is the
right response. Microsoft's strength is Azure distribution and OpenAI Responses compatibility; ours is
infrastructure-as-code Kubernetes posture and multi-backend routing across three distinct LLM vendors rather than
a single API surface.

### Cloudflare Agent Cloud (Cloudflare)

**Autonomy model:** Autonomous, managed edge platform

Launched during **Agents Week (2026-04-13 to 2026-04-17)** — the same week this doc is being revised.

- **Cloudflare Mesh** — private-networking "single secure fabric" for agents / humans / multicloud; branded to
  secure the AI agent lifecycle end-to-end.
- **Dynamic Workers** — millisecond-spawn sandboxes for agent-generated code.
- **AI Gateway** — unifies 70+ models across 12+ providers (directly parallel to this project's multi-backend routing
  — but much broader).

**Relative standing:** Category-defining launch in the very week of this research. Cloudflare's positioning of
"Agent Cloud" is itself a category signal — "Agent Fabric / Mesh / Cloud" is consolidating as THE 2026 term for the
space. Our counter-positioning: Cloudflare runs on Workers (edge compute with millisecond spawn), while this
project runs Kubernetes Pods (persistent, stateful, per-agent filesystem). Different deployment models; some
workloads need one, some need the other. The AI Gateway is a serious differentiation challenge to our
backend-routing story — Cloudflare covers vastly more providers.

---

## Category references

These products anchor category vocabulary but aren't primary competitors — noted here so the doc's language aligns
with where the market is converging.

### NVIDIA NeMo Agent Toolkit (NAT)

Previously branded AIQ; renamed NAT in early 2026; **GTC 2026 (March 16–19) partner launch with ~16 platform
vendors** (Adobe, Atlassian, Box, Cadence, Cisco, CrowdStrike, SAP, Salesforce, ServiceNow, Siemens, Synopsys,
others) standardizing on it. Open-source library for connecting / evaluating / accelerating teams of agents;
framework-agnostic instrumentation across LangChain / LlamaIndex / CrewAI / Microsoft Semantic Kernel / Google ADK.
**FastMCP Workflow Publishing** lets NAT workflows publish as MCP servers — crossing the observability-to-tooling
boundary. Matters not as a head-to-head competitor but as a cross-cutting standardization layer that changes how
the rest of the landscape integrates.

### Salesforce Agent Fabric

Agent Fabric with Guided Determinism + centralized governance controls, Flex Gateway, Runtime Fabric support.
Positioned as "trusted agent control plane for a rapidly evolving multi-vendor AI landscape" — automated discovery,
authoring, and centralized LLM governance across vendors. Our harness is architecturally the same role (routing +
governance across multiple backends) in a Kubernetes-native form. Noted here because **"Agent Fabric" is becoming
the canonical enterprise category name** alongside "Agent Mesh" and "Agent Cloud."

---

## Research Themes

Thin navigational scaffolding — one paragraph per theme pointing at the relevant entries in Reference Products
and Gap Analysis for current state. Not a research bibliography; the competitor-specific detail lives with each
competitor's section (which ages on a clear cadence), and industry statistics that were previously inline have
been retired because they drift invisibly and can't be kept honest without quarterly refresh discipline.

### Memory

Persistent structured memory across runs. CrewAI's unified Memory class with LLM-inferred hierarchical scopes + composite
recall is the leading open-source implementation. Hermes Agent's SQLite FTS5 + auto-generated skills is the consumer-side
peer. This project uses flat markdown files, which work for prose notes but are fragile for structured data needing
reliable read/update. Candidate: shared structured memory index (F-003, on hold pending shared-volume infrastructure).

### Observability

Metrics + OpenTelemetry tracing + event stream. Now table stakes across the category — Bedrock AgentCore, kagent,
Cloudflare, and Microsoft Agent Framework all ship the tripod. This project's remaining edge is the published
multi-client event-stream wire contract (`docs/events/events.schema.json`) consumed by the dashboard + `ww` CLI + future
mobile — most competitors couple event observability to proprietary UIs. See Gap Analysis → Observability.

### Human-in-the-Loop

Approval gates before destructive actions. LangGraph's `interrupt()` with structured payloads is the reference pattern.
The Claude Agent SDK ships `AskUserQuestion` as a built-in HITL tool. Devin shows plan-before-code as a hard checkpoint.
This project has `AskUserQuestion` available but not yet enabled (F-001, open — one-line wiring change in `executor.py`).

### Guardrails / Safety

Prevention-first control hierarchy: hooks → human intervention → trace log. LangGraph ships declarative guardrail nodes.
The Claude Agent SDK's `PreToolUse` hook supports `updatedInput` for argument rewriting (not just blocking). This project
ships the hook runtime (`hooks.yaml` baseline + per-agent extensions, hot-reloaded) plus MCP command + cwd allow-lists;
see the Claude Code / SDK entry for the specific API surface and Gap Analysis → Safety for what remains.

### Coordination

Multi-agent delegation patterns. A2A v1.0 (Linux Foundation governance, 150+ organizations) is the emerging standard;
LangGraph Platform, Microsoft Agent Framework, and Bedrock AgentCore all integrate it natively. Research shows
hierarchical planner-worker topologies outperform flat "bag of agents" by ~17x on error compounding. This project's
hybrid heartbeat-orchestrator + A2A-delegation model aligns with the winning topology. Implemented: `delegate` skill +
`POST /triggers/{endpoint}` for event-driven dispatch (F-006).

### Durability / Crash Recovery

Checkpointing is mandatory in production systems post-LangGraph-1.x (which made it a hard requirement). Temporal.io's
durable workflow model is the broader 2026 reference architecture. CrewAI's v1.14.0 checkpoint/resume primitives are
now the OSS peer reference. This project has stale-checkpoint detection on startup (F-005); full session resume past
`resume=session_id` remains a longer-term follow-on.

### Tooling / MCP

MCP is under Linux Foundation governance (donated December 2025). Hundreds of community MCP servers cover browsers,
databases, APIs, system integrations. Native MCP support is ubiquitous across the landscape — table stakes. This project
ships three MCP tool servers (`mcp-kubernetes`, `mcp-helm`, `mcp-prometheus`), each bearer-auth-gated and call-budget-capped.
Dynamic *task-aware* tool injection (CrewAI's Tool Search — load only the tools relevant to the current prompt) is the
remaining frontier; see Gap Analysis → Tooling.

### Planning / Task Decomposition

Plan-before-code as a hard checkpoint pattern. OpenHands's Planning Agent (read-only until `PLAN.md` is finalized) + the
Claude Agent SDK's `permission_mode="plan"` are the reference implementations. Research confirms planning phases
produce fewer cascading failures. This project has neither a planning mode nor a plan-gate (F-012, open).

### Safety / Governance

Microsoft's Agent Governance Toolkit (MIT license, 2026-04-02) is the first toolkit to address all 10 OWASP Agentic Top 10
risks with deterministic sub-millisecond policy enforcement. EU AI Act high-risk obligations take effect August 2026;
Colorado AI Act becomes enforceable June 2026 (verify specifics before quoting — regulation dates shift). This project's
`hooks.yaml` declarative policy engine provides the enforcement primitive; the gap is OWASP-category labelling on rules
so it becomes a direct comparable to the MS toolkit. See Gap Analysis → Safety for specifics.

### Self-Improvement / Lifelong Learning

The closed learning loop: execution → skill synthesis → future reuse. Hermes Agent auto-generates skill documents after
completing complex tasks; Google's Always-On Memory Agent continuously consolidates in the background. The 2026 frame:
"can the agent remember what it learned yesterday and do it better tomorrow?" This project has the skill-document
infrastructure (`.claude/skills/`, `.codex/skills/`, `.gemini/skills/`) but no execution-to-skill synthesis path.
Candidate: post-task skill synthesis that evaluates whether a completed run yielded a reusable pattern.

### Cost / Token Management

Token budgeting + context-usage monitoring to prevent runaway bills and silent tail-end degradation. The Claude Agent
SDK ships `task_budget` (per-session cap) and `get_context_usage()` (real-time consumption by category). CrewAI tracks
token usage in `LLMCallCompletedEvent`. Production agents are widely over-resourced (industry finding; verify current
figure before citing). This project has `get_context_usage()` wired; `task_budget` is proposed but unimplemented (F-010).

---

## Gap Analysis

_Last updated: 2026-04-07 by local-agent_

- **Memory and knowledge management:** Flat markdown memory files work for prose notes but are fragile for structured
  data. Hermes Agent (NousResearch, February 2026) ships SQLite FTS5 with LLM-powered summarization and a pluggable
  memory provider interface — the gap vs. this project is widening. CrewAI's documented 2026 limitation (losing
  coordination state when a crew ends) confirms that persistent structured shared memory is a meaningful differentiator.
  F-003 (shared memory index) remains on hold pending shared volume infrastructure.

- **Human-in-the-loop and approval gates:** `AskUserQuestion` is available in the SDK but not yet enabled in this
  project (F-001, open). LangGraph 2.0's redesigned `interrupt()` with structured payloads and Claude Code's
  community-reported demand for approval gates before destructive operations both confirm this is a consistently-wanted
  primitive. Enabling it is a one-line change.

- **Multi-agent coordination and delegation:** A2A-based delegation is implemented (F-006, closed). LangGraph v1.1's
  deferred nodes provide the reference pattern for fan-out/fan-in coordination this project does not yet support. The
  winning production topology (orchestrator + local mesh) aligns with current design; the gap is fan-out task
  distribution with result aggregation.

- **Scheduling and event-driven triggers:** Inbound HTTP triggers and outbound webhooks are implemented — triggers serve
  `POST /triggers/{endpoint}` endpoints with HMAC auth; webhooks deliver filtered outbound HTTP notifications with LLM
  extraction, retry, and HMAC signing. Devin's self-scheduling validates the agenda model. The remaining gap is dynamic
  tooling and deeper external system integrations.

- **Observability and debuggability:** Metrics + distributed tracing are now baseline across the space —
  Bedrock AgentCore ships observability by default, kagent includes Prometheus as a pre-built tool, Cloudflare
  Agent Cloud and Microsoft Agent Framework lead with OpenTelemetry. Shipping `backend_*` metrics +
  `traceparent` propagation is therefore no longer a differentiator; it's entry to the category. The actual
  differentiator is the **published event-stream wire contract consumed by multiple independent clients** —
  `/events/stream` with 14 typed event shapes documented in `docs/events/events.schema.json`, same schema
  consumed by the web dashboard today and by the `ww` CLI + future mobile clients. Most competitors' event
  observability is coupled to their proprietary UI (AgentCore console, LangSmith, Devin IDE embed); publishing
  it as a first-class multi-client contract is the remaining edge. Remaining gap: per-agent RED / USE
  dashboards bundled as default Grafana JSON (partially started via `charts/nyx/dashboards/`).

- **Safety and guardrails:** Microsoft released the Agent Governance Toolkit (April 2, 2026, MIT license) — the first
  toolkit to address all 10 OWASP Agentic Top 10 risks with deterministic, sub-millisecond policy enforcement. OWASP
  published the Top 10 for Agentic Applications in December 2025. EU AI Act high-risk obligations take effect
  August 2026. This project has a two-layer declarative policy system: a conservative built-in **baseline** of
  deny rules (shipped in the claude executor) plus **per-agent extensions** loaded from `hooks.yaml` and
  hot-reloaded at runtime. PostToolUse audit writes one row per tool call to `tool-activity.jsonl` for a
  forensic trail. MCP transport is separately gated by command + cwd allow-lists
  (`MCP_ALLOWED_COMMANDS` / `MCP_ALLOWED_COMMAND_PREFIXES` / `MCP_ALLOWED_CWD_PREFIXES` + positional-script
  rejection in `mcp_command_args_safe()`). The remaining gap is **OWASP-category labelling** — rules in
  `hooks.yaml` are ad-hoc-named today; mapping each to the OWASP Agentic Top 10 categories (`A01:
  prompt-injection`, `A02: tool-misuse`, etc.) would turn the declarative layer into a direct comparable
  with Microsoft's toolkit.

- **Tooling and integrations (MCP, webhooks, APIs):** MCP configuration is implemented (F-004, closed).
  Outbound webhooks and inbound triggers are implemented. **Static `ALLOWED_TOOLS` is implemented on
  claude** (hot-reloadable via `settings.json`) and **scaffolded on gemini** (env + reload counter in
  place, pending the hand-rolled AFC loop). Three MCP tool servers ship (`mcp-kubernetes`, `mcp-helm`,
  `mcp-prometheus`); each enforces its own bearer auth and a per-(server, tool) call-budget knob
  (`mcp_tool_budget_exhausted_total`). Dynamic *task-aware* tool injection (CrewAI's tool search —
  loading only the tools relevant to the current prompt rather than the full allow-list) is still the
  open frontier.

- **Kubernetes-native agent infrastructure (contested lane, April 2026):** The position this project has
  held is no longer uncontested. kagent (CNCF sandbox, Solo.io), OpenClaw's dedicated operator, OpenHands
  v1.6 Kubernetes + RBAC support, and kubernetes.io's "Agent Sandbox" blog all now occupy the same lane.
  Differentiators that *do* hold up head-to-head with these: (1) **multi-backend routing under one named
  agent identity** — Claude / Codex / Gemini behind `backend.yaml` routing rules with per-concern dispatch
  (heartbeat to claude, jobs to codex, etc.) is unique in the Kubernetes-native OSS set; competitors are
  mostly single-framework (kagent on AutoGen) or single-model. (2) **Scheduler primitive breadth** — jobs,
  tasks, heartbeats, triggers, continuations, webhooks as first-class `.nyx/` frontmatter files. kagent
  and OpenClaw don't ship equivalents. (3) **Per-agent cross-pod topology** — harness + backends + shared
  MCP tools is a production-ready shape that OpenClaw's single-agent framing doesn't match. (4)
  **Declarative CRD lifecycle via `NyxAgent` + `NyxPrompt`** going through a dedicated operator with
  status phases, finalizers, and multi-tenant manifest ConfigMaps. kagent is closer to ours on this axis
  but uses CRDs only for agent definition, not prompt lifecycle.

- **Cost and resource management:** `task_budget` env var is proposed (#69, open) but not implemented. Industry finding:
  90% of production agents are over-resourced in 2026; cost control is treated as a first-class architectural concern.
  The per-message-kind budget split (separate caps for heartbeat, agenda, A2A-triggered runs) is an open question in #69
  that would unlock fine-grained cost control.

- **Self-improvement and lifelong learning:** Hermes Agent's auto-generated skills (writing a new skill document after
  completing a complex task) and Google's Always-On Memory Agent (continuous ingestion + background consolidation)
  represent a new category this project does not yet address. The project already has a skill document system; closing
  the loop from execution → post-task skill synthesis → capability accumulation is a novel and high-value direction with
  no existing open issue.
