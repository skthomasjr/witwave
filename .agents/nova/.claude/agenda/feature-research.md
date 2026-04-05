---
name: Feature Research
description:
  Researches the competitive landscape of autonomous agent products and produces a critically evaluated feature proposal
  list in docs/.
schedule: "0 */6 * * *"
enabled: true
---

Research the competitive landscape of autonomous agent products and update the docs in `<repo-root>/docs/` with findings
and proposals.

Steps:

1. Read `<repo-root>/README.md`, `<repo-root>/CLAUDE.md`, and all files under `<repo-root>/docs/` to understand the
   current state and direction of the project. Pay particular attention to `docs/product-vision.md` — it defines the
   target audience, deployment target (Kubernetes), and design principles. All feature proposals must be evaluated
   against this vision: does this feature serve the individual, the enterprise, or both? Does it align with
   Kubernetes-first deployment? Does it respect the principle that complexity is opt-in?
2. Read all source files under `<repo-root>/` using the glob pattern `**/*.py` and also read `Dockerfile` and
   `docker-compose.yml` to understand what is already built and how it works. Form a clear picture of the current
   capabilities and limitations before researching anything external.
3. Research the following products using WebSearch and WebFetch. Prefer primary sources — official docs, GitHub repos,
   release notes, changelogs, and credible community discussion (Hacker News, Reddit, GitHub issues, developer blogs).
   For each product, focus on: what features users actively praise or request, what problems they solve, and what
   distinguishes them architecturally:
   - **OpenHands** (formerly OpenDevin) — treat this as the primary reference. This project is philosophically similar —
     containerized agents, file-based configuration, Claude Code as the runtime. Research OpenHands deeply: its most
     active GitHub issues, recently merged features, community discussions, and what users say they love or wish it had.
     Search "OpenHands features", "OpenHands GitHub issues", "OpenHands community feedback", "what does OpenHands do
     well"
   - **Claude Code** — Anthropic's agentic CLI and agent SDK; focus on SDK-level capabilities not yet used by this
     project
   - **Devin** — autonomous software engineer by Cognition; focus on workflow and human-in-the-loop patterns
   - **SWE-agent** — focus on its Agent-Computer Interface (ACI) design philosophy
   - **CrewAI** — focus on multi-agent memory and coordination patterns
   - **LangGraph** — focus on durable execution and human-in-the-loop interrupt/resume
   - Search broadly for "autonomous agent features 2026", "multi-agent coordination patterns", "agentic coding tools
     most wanted features", "OpenHands vs alternatives"
4. Identify gaps between what competitors offer and what this project currently provides. Group findings into themes
   (e.g., memory, coordination, tooling, observability, human-in-the-loop, security). Weight OpenHands findings most
   heavily — it is the closest architectural peer.
5. Apply a high bar before proposing a feature. Ask: Is this a must-have that meaningfully improves agent autonomy,
   reliability, or usefulness? Is there strong evidence users actually want it — not just that it is technically
   interesting? Would it fit naturally into the existing architecture without requiring a rewrite? Features that are
   nice-to-have, speculative, or primarily cosmetic should be discarded. Aim for quality over quantity — propose only
   features you would confidently recommend implementing next.
6. Update `<repo-root>/docs/competitive-landscape.md` with the following structure:

   ## Reference Products

   A concise summary of what each product does well and where this project stands relative to them. One `###` section
   per product, ending with a **Relative standing:** paragraph.

   ## Research Themes

   One `###` section per theme. Each section contains:

   - What competitors do in this area
   - What users value most
   - Candidate features for this project (only those that passed critical evaluation)

7. Update `<repo-root>/docs/features-proposed.md` with feature proposals. The file contains only `## Feature Proposals`
   — one `### F-XXX` section per feature with these labeled fields:

   - **Status:** — `proposed`, `approved`, `rejected`, or `promoted`
   - **Value:** — why it matters and what problem it solves
   - **Implementation:** — concrete description: what would change, which files, what new components
   - **Risk:** — Low / Medium / High with brief justification
   - **Questions:** — open questions; set to `none` if none. Never approve while questions are unresolved.

   If the file already exists, read all existing proposals first to determine the highest ID currently assigned. New
   proposals must continue from that number. Never modify the ID of an existing proposal. Preserve any features whose
   **Status** is `approved`, `rejected`, or `promoted` — do not modify or remove them. Update or replace only `proposed`
   entries based on the latest research.

8. When a feature is fully implemented (all TODO items marked `[x]`), move it to
   `<repo-root>/docs/features-completed.md` — do not delete it from `features-proposed.md` until it appears there.
9. Run `/lint-markdown` to fix any markdown violations in the files you modified.
10. Do not modify any source files, `TODO.md`, or any file outside of `docs/`. Do not do anything else.
