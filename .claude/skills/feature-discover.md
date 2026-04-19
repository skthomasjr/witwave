---
name: feature-discover
description: Analyze open ready requests and derive feature issues from them. Trigger when the user says "discover features", "find features", "run feature discover", "derive features", or "generate features from requests".
version: 1.2.0
---

# feature-discover

Analyze each open request that has been marked as ready and derive one or more feature issues from it.

## Instructions

**Step 1: Find all open ready requests.**

Look up all open requests marked as ready. Process each one in turn. For each request, follow Steps 2–6.

---

**Step 2: Understand the request.**

Read the full issue body and comment thread. The body is the canonical record — the comments are the conversation that shaped it. Together they tell you what the requester wants, why they want it, and any constraints or preferences they expressed along the way.

Pay attention to the `**Summary:**` field in the body — the request-dialog skill maintains this as a synthesized understanding of the request. Use it as your starting point, but read the full thread to catch nuance the summary may not capture.

---

**Step 3: Understand the codebase.**

Read `<repo-root>/README.md` and `<repo-root>/AGENTS.md` to understand the platform's architecture and intended design. Then read the source files most relevant to what the request is asking for.

This step is required. A feature derived without understanding the codebase may duplicate existing capability, conflict with the architecture, or miss the natural seam where new work should attach. The goal is to understand not just what exists, but how the request fits — or doesn't fit — into the current system.

---

**Step 4: Identify the features.**

Using the request and your understanding of the codebase, determine what discrete units of work are needed to fulfill the request. A request may yield one feature or several — split along natural boundaries of capability, component, or responsibility. Do not split artificially; do not merge distinct capabilities into one.

For each feature, establish:

- **What** — what capability is being added and what it does
- **Value** — why this is worth building; what problem it solves or what it enables, grounded in what the requester said
- **Component** — which part of the system this touches (harness, claude, codex, gemini, dashboard, operator, charts, mcp, cli, or cross-cutting)
- **Design** — a suggested implementation approach based on how the codebase currently works; not a full spec, but enough to orient whoever picks it up
- **Dependencies** — any other features or existing issues that must be resolved first

Apply the same analytical discipline used in gap-discovery: look at what the system actually does, what the request actually asks for, and what work genuinely needs to happen to bridge the two. Do not invent scope beyond what the request supports.

---

**Step 5: Research and validate.**

Before filing, do any research needed to produce a well-informed feature write-up. This step has two parts:

First, identify any features where the implementation approach is non-obvious or where it's unclear whether the capability already exists in some form. For those, do a web search to check whether the approach is standard practice and well-understood. If a search confirms the approach is sound, proceed. If it reveals a better approach, use that instead.

Second, if the request touches an unfamiliar domain, third-party service, external API, or technology not currently used in the codebase, research it enough to write a credible design. Look for how others have solved the same problem, what the standard patterns are, and whether there are known pitfalls.

Note what you searched for and what you found in the design field whenever this step influenced the output.

---

**Step 6: File the features and close the request.**

Before filing anything, look up all features — open and closed — that are linked to this request. A feature is linked if its body references the request issue number. Review each one to understand what has already been filed and what has already been completed.

For each feature derived in Step 4:
- If the same capability was previously filed and is still open, do not file a duplicate. Note on the existing feature that it was re-identified, and include a link to the request.
- If the same capability was previously filed and is already closed (implemented or wont-fix), do not re-file it. It has already been addressed.
- If the capability is genuinely new — not covered by any prior feature, open or closed — file the feature, linking back to the originating request. Do not implement the feature — only record it.

After all features are filed, comment on the original request listing each feature created with links. Leave the request open — the conversation may continue and further features may be derived from it later.

If no features can be derived — for example, because the request maps entirely to existing capability — say so clearly in a comment on the request. Leave it open for the same reason.
