---
name: lint
description: Lint one or more files, auto-detecting the type and running the appropriate tool
argument-hint: "<file> [file ...]"
---

Lint the files provided in the arguments, dispatching to the correct tool based on each file's extension.

**Arguments:** $ARGUMENTS

Steps:

1. Parse the arguments as a space-separated list of file paths.
2. Group files by type:
   - `.py` → run `/lint-python`
   - `.md` → run `/lint-markdown`
   - `.yaml` or `.yml` → run `/lint-yaml`
   - `Dockerfile` (exact filename, no extension) → run `/lint-dockerfile`
   - Any other extension → log a warning that the file type is unsupported and skip it.
3. For each group, run the corresponding lint skill once — do not run the same skill multiple times if multiple files of
   the same type are passed.
4. Report the outcome: which skills were run and whether each passed or had remaining violations.
