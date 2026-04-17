---
name: docs-refine
description: Review and update project documentation to ensure it is accurate, complete, and consistent with the current codebase. Trigger when the user says "refine docs", "update the docs", "review the docs", "check the documentation", "update documentation", or "run docs refine" — with or without a specific document or component name.
version: 1.3.0
---

# docs-refine

Review project documentation against the current codebase and update it in place. This skill edits documents directly — it does not file issues.

## Scope

The documents covered by this skill are:

- `<repo-root>/README.md` — root project overview
- `<repo-root>/CONTRIBUTING.md` — contribution model (issue-only)
- `<repo-root>/harness/README.md` — nyx-harness orchestrator component (was `agent/README.md`)
- `<repo-root>/backends/a2-claude/README.md` — Claude backend component
- `<repo-root>/backends/a2-codex/README.md` — Codex backend component
- `<repo-root>/backends/a2-gemini/README.md` — Gemini backend component
- `<repo-root>/dashboard/README.md` — Vue 3 dashboard component (was `ui/README.md`)
- `<repo-root>/operator/README.md` — Kubernetes operator (Go) component
- `<repo-root>/tools/README.md` — MCP component index
- `<repo-root>/tools/kubernetes/README.md` — mcp-kubernetes MCP server
- `<repo-root>/tools/helm/README.md` — mcp-helm MCP server
- `<repo-root>/charts/nyx/README.md` — nyx Helm chart (agents)
- `<repo-root>/charts/nyx-operator/README.md` — nyx-operator Helm chart
- `<repo-root>/tests/README.md` — smoke-test suite index
- `<repo-root>/docs/` — all files, including the `prompts/` subdirectory

Excluded from this skill: `AGENTS.md`, `CLAUDE.md`, `.claude/skills/`, individual `tests/NNN-*.md` specs,
and issue templates under `.github/ISSUE_TEMPLATE/`.

## Instructions

**Step 1: Identify the documents to review.**

If the user specifies a document or component (e.g. "refine docs for the Codex backend", "update the architecture doc"), scope the review to that document and any directly related documents. Otherwise review all documents in scope.

**Step 2: Read the document and its source.**

For each document:
- Read the document in full
- Read the source files it describes — component source directories, config files, docker-compose files, and any other files the document references or summarizes
- Read any related in-scope documents that share content with this one (e.g. if reviewing a component README, also note what the root README says about that component)

Do not update a document based on memory or inference alone — verify every claim against the current state of the code.

**Step 3: Identify what needs to change.**

For each document, look for:

- **Inaccuracies** — statements that contradict the current code, config, or behavior (e.g. a port number that changed, an environment variable that was renamed, a feature that was removed)
- **Gaps** — capabilities, configuration options, or behaviors that exist in the code but are not documented
- **Staleness** — references to files, directories, services, or concepts that no longer exist
- **Inconsistencies** — content that conflicts with what another in-scope document says about the same thing (e.g. a port table in the root README that disagrees with a component README)

Do not change content that is accurate, complete, and consistent. Do not rewrite for style alone.

**Step 4: Research if needed.**

If verifying a claim requires understanding a technology, protocol, or SDK behavior that is not evident from the code — for example, confirming how an SDK initializes sessions or what a protocol field means — do a targeted web search before updating the document.

**Step 5: Update the document.**

Edit only what needs to change. Preserve the document's existing structure and voice. Do not restructure, reformat, or rewrite sections that are accurate.

When updating shared content that appears in multiple documents (e.g. the port table, the component list), update all affected documents in the same pass so they stay consistent.

**Step 6: Record what changed.**

After updating each document, briefly note:
- What was changed and why (inaccuracy, gap, staleness, or inconsistency)
- What source or evidence the update was based on

If no changes were needed, say so clearly. Do not pad the output.

**Step 7: Format the documents.**

Once all documents have been updated, format all modified documents. This ensures consistent prose wrapping, table column alignment, and lint compliance.

**Step 8: Commit the changes.**

Once all documents have been reviewed, updated, and formatted, stage all modified documentation files, commit them with a message that summarizes what was updated and why, and push. Do not commit or push unrelated files.
