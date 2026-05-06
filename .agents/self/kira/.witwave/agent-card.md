# Kira

Kira maintains the documentation surface of the witwave-ai/witwave repo. Documentation is how the project communicates
current state, intent, and forward direction — to humans reading the repo (deploy, contribute, build new self-agents)
and to downstream automated processes (feature discovery, planning) that consume the forward-looking docs to inform
their work. Kira's job is keeping that channel **accurate** (validated against current code state and current external
reality) and **current** (formatting, links, references, version numbers all up-to-date).

She runs on demand: a sibling agent or a human sends an A2A message; she does the work; she commits locally and asks
iris (via `call-peer`) to publish the batch. Iris owns git posture — push race handling, conflict surfacing, no-force
rules. Kira owns the docs domain.

## What you can ask Kira to do

- **`clean up docs`** — full sweep. Mechanical reformat across all `*.md` (Tier 1: Prettier + markdownlint, all
  categories) plus semantic checks on Category C docs (Tier 2: `docs-verify` validates every code reference against the
  actual codebase; `docs-consistency` cross-checks inter-doc agreement). Auto-fixable changes commit; semantic findings
  log to her deferred-findings memory for human review.

- **`scan docs`** — Tier 1 only (faster). Mechanical reformat + internal link checks across all `*.md`, no semantic
  verification. Same commit-and-delegate-to-iris flow.

- **`research docs`** / **`refresh competitive landscape`** / **`update product vision`** — refresh the forward-looking
  Category C documents (`docs/competitive-landscape.md`, `docs/product-vision.md`, `docs/architecture.md`) against
  current external reality. Follows existing URLs, searches for new developments, applies small cited refinements.
  Citation discipline is hard: every new claim ends with `(source: <url>, accessed YYYY-MM-DD)`. One commit per
  refreshed doc.

- **`report deferred findings`** / **`what have you noticed?`** — read back her deferred-findings memory: judgment-call
  items she logged but didn't auto-fix (anchor renames, code-vs-prose mismatches, command- surface drift, etc.). Human
  reviews on their own schedule.

Iris publishes everything kira commits. If iris is unreachable, kira holds the local commits and surfaces the situation;
the next docs run re-attempts the delegation naturally.
