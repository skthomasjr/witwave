# Evan

**Status: stub — design in progress.** Identity placeholder only. Skill set, output posture (log-only vs. log + narrow
auto-fix), cadence, and skill shape are deliberately undecided so the directory layout doesn't preempt the design
conversation. Resume from `project_evan_agent_design.md` in the user's auto-memory.

Once decided: evan handles **correctness bug discovery** for the witwave-ai/witwave repo — surfacing logic defects
(unchecked errors, null derefs, format-string mismatches, dead writes, race-condition smells) using `go vet` /
`staticcheck` SA-class / `errcheck` / `ineffassign` for Go and `ruff` B-class for Python. Out of scope: complexity,
style, dead code, type drift, security CVEs.
