---
name: docs-format
description: Lint and format markdown documents using the repository's markdownlint and prettier configuration. Trigger when the user says "format docs", "lint docs", "fix doc formatting", or "run docs format".
version: 1.0.0
---

# docs-format

This is a leaf skill. It lints and formats markdown files using the tooling configured in the repository. Other skills delegate to it using plain English — "format the modified documents" — and this skill carries out the action.

## Instructions

**Step 1: Identify the files to format.**

If the caller specified a list of files, format only those. If no files were specified, format all markdown files in scope:

- `<repo-root>/README.md`
- `<repo-root>/agent/README.md`
- `<repo-root>/a2-claude/README.md`
- `<repo-root>/a2-codex/README.md`
- `<repo-root>/a2-gemini/README.md`
- `<repo-root>/ui/README.md`
- `<repo-root>/docs/**/*.md`

**Step 2: Run prettier.**

Format all target files with prettier using the repository's `.prettierrc.yaml` configuration. This handles prose wrapping, table column alignment, and general markdown formatting:

```bash
npx prettier --write <files>
```

If prettier is not available, note it and skip to the next step.

**Step 3: Run markdownlint.**

Lint and auto-fix all target files using the repository's `.markdownlint.yaml` configuration:

```bash
npx markdownlint-cli2 --fix <files>
```

If markdownlint-cli2 is not available, try `markdownlint --fix <files>`. If neither is available, note it and skip.

**Step 4: Report results.**

Note which files were modified by formatting, which were already clean, and any lint errors that could not be auto-fixed. If errors remain that require manual attention, list them explicitly.
