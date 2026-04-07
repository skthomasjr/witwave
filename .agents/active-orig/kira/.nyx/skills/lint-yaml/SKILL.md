Run yamllint across all YAML files in the workspace, fixing violations.

Steps:

1. Verify yamllint is available: `yamllint --version`. It is pre-installed in the image — if missing, report the error
   and stop.

2. Run yamllint across all YAML files using the repo's config:

   ```sh
   yamllint -c <repo-root>/.yamllint.yaml \
     <repo-root>/**/*.yml \
     <repo-root>/**/*.yaml \
     <repo-root>/*.yml \
     <repo-root>/*.yaml
   ```

3. For each violation, read the affected file and fix the issue in place. Common fixes:

   - trailing spaces: remove trailing whitespace
   - wrong indentation: align to the correct level (2 spaces standard)
   - missing document start (`---`): add at top of file if required by context
   - too many blank lines: collapse to one
   - line too long: reflow or restructure the value; use YAML block scalars (`|` or `>`) where appropriate
   - truthy values: replace bare `yes`/`no`/`on`/`off` with `true`/`false`

4. Re-run yamllint to confirm zero violations. Log any that cannot be auto-fixed but do not fail.

5. Do not modify source code files (`.py`, `Dockerfile`, etc.) or Markdown files.
