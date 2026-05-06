---
name: docs-links
description:
  Validate internal markdown links, anchors, and file-path references across the repo's documentation. Auto-fixes
  unambiguous cases (file renamed and the new path is discoverable from git history); logs ambiguous cases to
  deferred-findings memory. Trigger when the user says "check doc links", "fix broken links", or as a step inside the
  `docs-scan` orchestrator.
version: 0.1.0
---

# docs-links

Find and (when safe) fix broken references inside markdown. Three classes of reference are in scope:

1. **Inline links** to other markdown files: `[text](path/to/file.md)`
2. **Anchor links** within the same file: `[text](#section-id)`
3. **Cross-doc anchors**: `[text](other/file.md#section-id)`

Path references inside prose (backtick-quoted paths like `` `src/foo/bar.go` ``) are out of scope for Tier 1 — those
land in `docs-verify` later. External `https://` links are also out of scope here (separate concern: network-dependent,
flaky, not mechanical).

## Instructions

Read these from CLAUDE.md:

- **`<checkout>`** — local working-tree path

### 1. Verify the source tree is in place

```sh
git -C <checkout> rev-parse --show-toplevel
```

If the checkout is missing, stand down — log to memory, exit.

### 2. Walk every markdown file and extract internal references

For each `*.md` file (honoring `.prettierignore` / `.markdownlintignore` so kira doesn't chase generated content):

```sh
git -C <checkout> ls-files '*.md' \
  | grep -v -F -f <checkout>/.prettierignore 2>/dev/null \
  || true
```

For each file, extract every markdown link of the form `[text](target)` where `target` is NOT prefixed by `http://`,
`https://`, or `mailto:`. Use the inline regex: `\[([^\]]*)\]\(([^)]+)\)`.

Split each `target` into `path` and `#anchor` parts. Empty path means "same file" anchor.

### 3. Validate each reference

For each `(file, link, target)` triple:

| Target shape              | Check                                               |
| ------------------------- | --------------------------------------------------- |
| `#anchor` (same-file)     | Anchor slug exists in the same file's headings      |
| `relative/file.md`        | File exists at the resolved path                    |
| `relative/file.md#anchor` | File exists AND the anchor slug exists in that file |

Anchor slug rules: GitHub-flavoured markdown lowercases the heading text, replaces spaces with `-`, strips punctuation
other than `-` and `_`. E.g. `## Step 4 — Deploy kira` → `step-4--deploy-kira` (em dash collapses to two hyphens).

A reference is **broken** if any check fails.

### 4. Classify each broken reference

For each broken reference, decide whether the fix is **unambiguous** (autofix) or **ambiguous** (memory-log):

**Unambiguous** — auto-fix applies when ALL hold:

- Target is a path (not just an anchor)
- The path doesn't exist now BUT git history shows the file was renamed:

  ```sh
  git -C <checkout> log --diff-filter=R --follow --format= \
    --name-status --all -- <new-or-old-path>
  ```

  If the rename trail surfaces exactly ONE current path that matches the old broken target, rewrite the link to point at
  the new path. (Multiple matches or unclear lineage → ambiguous.)

**Ambiguous** — log to deferred-findings memory:

- Anchor not found and no rename history connects to a renamed heading
- Path not found and no clean rename match
- Cross-doc anchor where the file is found but the anchor isn't (could be an intentional preview link or a missed rename
  of the heading itself)

### 5. Apply unambiguous fixes

For each unambiguous fix, edit the markdown file in place. Update the link target while preserving the link text. Use
Edit tool (exact match the full `[text](old)` pattern → `[text](new)`).

### 6. Log ambiguous findings to memory

For each ambiguous case, append a one-line entry to your deferred-findings memory:

```
- doc-links: <file>:<line> — broken link to <target> (reason: <anchor-not-found | path-not-found | rename-ambiguous>)
```

If a deferred-findings memory file doesn't exist yet, create
`/workspaces/witwave-self/memory/agents/kira/project_doc_findings.md` (type: project, see your CLAUDE.md → Memory
section for the frontmatter convention).

### 7. Report

Return a structured summary:

- Files where references were auto-fixed (count + paths)
- Auto-fixes applied (count + before/after pairs)
- Ambiguous findings logged (count + memory-file path)

Do NOT commit. The orchestrator handles batching.

## When to invoke

- Inside `docs-scan` (the orchestrator).
- On-demand: "check doc links", "find broken references", "fix link drift".
- After a refactor that renames or moves files — link drift is the most common collateral damage.

## Out of scope for this skill

- External URL validation (network calls; separate concern).
- Path references in prose (backticked paths) that aren't formatted as markdown links — `docs-verify` (Tier 2).
- Anchor renames in target files (we update inbound links to match a renamed file, not inbound links to match a renamed
  heading — too easy to misidentify intent).
- Code references (e.g. function names mentioned in prose) — out of scope for any docs-\* skill; that's a code/docs
  consistency concern.
- Committing or pushing — the orchestrator owns that.
