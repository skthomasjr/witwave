Run prettier and markdownlint across all Markdown files in the workspace, fixing violations.

Steps:

1. Verify tools are available: `markdownlint --version && prettier --version`. Both are pre-installed in the image — if
   either is missing, report the error and stop.

2. Run prettier to format all Markdown files, which aligns table columns and normalises whitespace:

   ```sh
   find /home/agent/workspace -name "*.md" | \
     xargs prettier --config /home/agent/workspace/.prettierrc.yaml --write
   ```

3. Run markdownlint across all Markdown files using the repo's config:

   ```sh
   find /home/agent/workspace -name "*.md" | \
     xargs markdownlint --config /home/agent/workspace/.markdownlint.yaml
   ```

4. For each violation, read the affected file and fix the issue in place. Common fixes:

   - MD009 (trailing spaces): remove trailing whitespace from the flagged lines
   - MD010 (hard tabs): replace tabs with spaces
   - MD012 (multiple blank lines): collapse to a single blank line
   - MD013 (line length): reflow all long lines to fit within 120 characters. Reflow means re-wrapping at word
     boundaries — never truncating or changing content. Apply the correct continuation indent for the block type:
     - Plain paragraph: continuation starts at column 0
     - List item (`-` or `*` or `1.`): continuation indented 2 spaces to align with the text after the marker
     - Nested list item: continuation indented to match the nested level
     - Blockquote (`>`): each wrapped line gets the same `>` prefix
     - The only lines that must not be reflowed are bare URLs and lines inside fenced code blocks. Every other long line
       — including list items in `TODO.md` — must be reflowed.
   - MD022/MD023 (heading spacing): add blank lines around headings
   - MD031/MD032 (fenced code block spacing): add blank lines around fenced code blocks
   - MD047 (file ending): ensure file ends with a single newline

5. Re-run markdownlint to confirm zero violations. Log any that cannot be auto-fixed but do not fail.

6. Do not modify source code files (`.py`, `Dockerfile`, etc.). Do not modify `TODO.md` frontmatter or the `[x]`/`[ ]`
   checkbox state of any item. Line-length reflow is permitted on all lines including checked items — only the checkbox
   state and frontmatter are protected.

**Frontmatter rule:** Many `.md` files begin with a YAML frontmatter block between `---` delimiters. Frontmatter is
parsed as YAML at runtime. Long values may be reflowed, but only using a proper YAML block scalar — never by inserting a
bare line break mid-value. Use `>-` (folded, no trailing newline) for prose values:

```yaml
description: >-
  This is a long description that has been safely reflowed across multiple lines using a folded block scalar.
```

Never produce this (invalid YAML):

```yaml
description: This is a long description that has been broken across lines without a block scalar.
```
