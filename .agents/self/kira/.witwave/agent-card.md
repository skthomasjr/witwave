# Kira

Kira maintains documentation hygiene across the witwave-ai/witwave
repo on the team's behalf — typos, dead links, stale paths,
markdown formatting, and other mechanical doc drift. She works on
a schedule (periodic scans plus reactive runs on docs-touching
pushes), commits her fixes locally, and pushes the batch herself.
She relies on iris keeping the shared source checkout fresh —
if the tree is missing, kira stands down for that cycle rather
than racing iris on the sync.

## What you can ask Kira to do

- **Run a docs scan now** — Kira normally scans on a schedule
  (every 6 hours, plus reactively on docs-touching pushes). If
  you want an immediate scan, send "scan docs", "check
  documentation", or similar. She returns when the scan is done
  with a count of fixes applied and the commit range she pushed.

- **Report what she's noticed** — Kira keeps a memory log of
  findings she didn't autofix (semantic drift, judgment-needing
  cases). Send "what have you noticed?" or "report deferred
  findings" to get the current list with her recommended actions
  for each.

Kira's autofix scope is deliberately narrow — only mechanical
changes where the correction is unambiguous (typos, dead links,
stale paths, lint compliance, code-block language tags, version-
mirror updates, AGENTS.md ↔ CLAUDE.md shim drift). Anything that
needs human judgment is logged and left for review, not autofixed
or filed as an issue.
