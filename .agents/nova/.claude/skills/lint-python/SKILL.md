Run ruff across all Python files in the workspace, auto-fixing lint violations and formatting.

Steps:

1. Verify ruff is available: `ruff --version`. It is pre-installed in the image — if missing, report the error and stop.

2. Run ruff lint with auto-fix across all Python files:

   ```sh
   ruff check --fix --config ~/workspace/.ruff.toml \
     ~/workspace/agent/
   ```

3. Run ruff format across all Python files:

   ```sh
   ruff format --config ~/workspace/.ruff.toml \
     ~/workspace/agent/
   ```

4. Re-run ruff check (without --fix) to surface any remaining violations that could not be auto-fixed:

   ```sh
   ruff check --config ~/workspace/.ruff.toml \
     ~/workspace/agent/
   ```

5. For any remaining violations that ruff could not auto-fix, read the affected file and fix manually. Common cases:

   - F841 (unused variable): remove or replace with `_`
   - E501 (line too long, unfixable): reflow the line manually at a natural boundary
   - UP (pyupgrade): modernise syntax per the suggestion

6. Re-run ruff check to confirm zero violations. Log any that cannot be safely fixed but do not fail.

7. Do not modify any file other than `.py` files under `agent/`.
