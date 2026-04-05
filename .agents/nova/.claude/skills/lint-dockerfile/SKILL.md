Run hadolint against the Dockerfile in the workspace, fixing violations.

Steps:

1. Download hadolint if not already present at `/tmp/hadolint`:

   ```sh
   curl -sSL https://github.com/hadolint/hadolint/releases/latest/download/hadolint-Linux-x86_64 \
     -o /tmp/hadolint && chmod +x /tmp/hadolint
   ```

2. Run hadolint against the Dockerfile using the repo's config:

   ```sh
   /tmp/hadolint --config the repo root/.hadolint.yaml \
     the repo root/Dockerfile
   ```

3. For each violation, read the Dockerfile and fix the issue in place. Common fixes:

   - DL3059 (multiple consecutive RUN): consolidate into a single `RUN` with `&&`
   - DL4006 (set pipefail): add `SHELL ["/bin/bash", "-o", "pipefail", "-c"]` before the first `RUN`
   - SC2086 (unquoted variables): quote shell variables
   - Any other warnings: fix per the hadolint rule description; if the fix would change behaviour rather than just
     style, log it and skip rather than risk a regression

4. Re-run hadolint to confirm zero warnings. Log any that cannot be safely auto-fixed but do not fail.

5. Do not modify any file other than `Dockerfile`.
