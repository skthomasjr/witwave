---
name: risk-discover
description:
  Analyze one or all components of the witwave platform for risks and record findings as tracked risks. Trigger when the
  user says "find risks", "discover risks", "look for risks", "scan for risks", "search for risks", or "run risk
  discover" — with or without a component name.
version: 1.1.0
---

# risk-discover

Analyze a specific component of the witwave platform for risks.

## Instructions

**Step 1: Identify the component(s).**

Read the Components table in `<repo-root>/README.md` to determine which directory corresponds to what the user said. Map
natural language to the table — "Claude backend", "Claude agent", or just "Claude" all map to `backends/claude/`.
"Orchestrator", "witwave", or "router" map to `harness/`. "UI", "interface", "dashboard", or "frontend" map to
`clients/dashboard/`.

If the user specifies "all" or does not specify a component, run this skill against every component in the Components
table in sequence. Complete all steps for each component before moving to the next.

**Step 2: Understand the component's role in the overall architecture.**

Read the root `README.md` and the component's own `README.md` to understand what this component does, what calls it,
what it calls, and how it fits into the overall system. Then read `AGENTS.md` for any additional architectural context.
This understanding is required before analyzing code — risks in isolation are often not risks at all, and risks that
matter are the ones that threaten integration points, shared state, or long-running operation. Pay particular attention
to shared code and utilities called by multiple components, as risks there have wider blast radius.

**Step 3: Read all source files in that directory.**

Read all source files in the identified directory. Do not skip any file.

**Step 4: Analyze for risks.**

Always perform a full, independent analysis of the source — do not assume that previously filed risks represent all
known risks. Every invocation of this skill is a fresh evaluation.

Focus exclusively on real risks — not bugs, not missing features, not style preferences. Look for code that works today
but is fragile, insecure, hard to maintain, or likely to break under foreseeable conditions. Categorize each finding as
one of:

- **Security** — credentials, secrets, or tokens in code or config; unvalidated external input; insecure defaults;
  overly permissive access
- **Reliability** — missing timeouts or retries on external calls; no circuit breaking; silent degradation under
  failure; assumptions about external service availability
- **Maintainability** — deeply coupled logic that makes changes dangerous; duplicated critical logic with no single
  source of truth; undocumented invariants that future developers are likely to violate
- **Performance** — unbounded growth (memory, queues, log files); blocking calls in async paths; operations that scale
  poorly with load
- **Observability** — silent failures with no logging or metrics; error paths that swallow context; conditions that
  would be impossible to diagnose in production

**Step 5: Validate each candidate against intentional design before filing.**

The most common failure mode in this skill is filing a candidate that the surrounding code has already addressed —
either at the cited site or somewhere else in the same file / shared module / chart. Before filing each candidate,
re-read the full surrounding context — not just the cited line range — and DROP the candidate if any of these apply.

The patterns to look for (these mirror the bug-discover Step 5 list, adapted for risk-shaped findings):

- **Inline comments documenting the choice.** A comment near the cited code that says "this is intentional", references
  a prior issue (`#NNNN`), or explains a documented tradeoff usually means the risk has been considered and accepted.
  The witwave codebase relies heavily on inline `#NNNN` references — search a 20-line window above and below for them.
- **Mitigation already in place at a different layer.** A "missing timeout" candidate is often resolved by
  `context.WithTimeout` further up the call path, or by an asyncio `wait_for` wrapper at the caller. A "no retry"
  candidate is often resolved by controller-runtime's outer rate limiter, by a `with_kube_retry` helper, or by an HTTP
  client's built-in retry policy. Read what calls the function before flagging an internal gap.
- **Cap / sweeper / bound exists elsewhere.** "Unbounded growth" candidates are common false positives — check whether a
  `MAX_*` constant, periodic sweeper, LRU eviction, or `setMaxSize` enforces a bound somewhere in the same file, in a
  shared module, or in the chart's resource limits. A queue without a `maxsize` parameter may still be bounded by the
  caller's rate-limit.
- **Silent-failure candidates have a metric counter.** "No observability" candidates are often false because a
  `_total{reason="..."}` Prometheus counter or structured-log line exists at the failure site. Grep for the function
  name in `metrics.py` files before flagging.
- **Insecure-default candidates are documented escape hatches.** Several witwave env vars are deliberately
  default-closed (`CONVERSATIONS_AUTH_TOKEN`, `MCP_TOOL_AUTH_TOKEN`, `ADHOC_RUN_AUTH_TOKEN`) with explicit
  `*_DISABLED=true` escape hatches for local dev. The escape hatch logs a loud startup warning. That posture is
  intentional and documented in AGENTS.md / READMEs — flagging it as "insecure default" is a misread.
- **Maintainability candidates that flag a documented design.** "Deeply coupled" or "duplicated logic" findings are
  often deliberate — the harness backends share a metrics surface by design (claude is the superset; codex/gemini track
  placeholders), shared modules like `shared/redact.py` and `shared/session_binding.py` are intentionally
  single-source-of-truth across all backends. If AGENTS.md or a module docstring describes the coupling, it's a design
  choice not a maintainability risk.

A risk candidate is more likely to be **real** when: the cited code has no nearby `#NNNN` references, no surrounding
mitigation at any layer, no metric counter or logging at the failure site, and the failure mode is reachable from a
clear external trigger that no other code path validates. A risk candidate is more likely to be **false** when: the
candidate is a generalised industry concern that the witwave codebase already has documented mitigation for.

If any of the above resolve the concern, drop the candidate. Quality over quantity — a clean output is better than a
noisy one.

**Step 6: Validate uncertain findings.**

Before reporting, identify any findings where you are not confident the risk is real or where the mitigation is
non-obvious. For those, do a web search to check whether the risk is a recognized problem in the industry and whether
the proposed mitigation is standard practice. A finding is uncertain if any of the following apply:

- It involves an operational or configuration choice (e.g., "should X be secured by default?") rather than a clear code
  defect
- It would require significant effort to mitigate but the payoff is unclear
- You are not sure whether it applies to this architecture specifically

If a search confirms the finding is real and well-understood, proceed to file it. If the search shows it is not standard
practice or does not apply, drop the finding. Briefly note what you searched for and what you found in the risk write-up
when this step influenced the decision.

**Step 7: Report findings.**

For each risk found, report:

- File and line number
- Category (security, reliability, maintainability, performance, observability)
- What the risk is
- What condition would cause it to manifest
- What the impact would be if it manifests
- A suggested mitigation

For each risk found, record it as a tracked issue. Every issue **must** be filed with at minimum the labels `risk` and
`pending`. Include the component, file and line number, category, condition, impact, and suggested mitigation. Do not
mitigate the risk — only report and record it. An issue filed without labels is invalid — verify labels are present
after filing.

If a sufficiently similar risk already exists, do not file a duplicate. Instead, add a comment to the existing risk
noting that it has been re-identified. The comment must include:

- The agent of record that re-identified it
- The name and version of this skill (see the frontmatter of this file)

Report risks in descending order of severity — security issues first; reliability risks next; then maintainability,
performance, and observability.

If no risks are found, say so clearly. Do not pad the output with non-risks.
