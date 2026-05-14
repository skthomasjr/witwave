#!/usr/bin/env bash
# check-operator-startup-default.sh — assert the witwave-operator chart
# renders the documented 600s startup grace window when a user supplies
# a partial values override that omits the probes block entirely (#1642).
#
# Background: values.yaml documents the default as
# `failureThreshold: 60` (=> 600s grace at periodSeconds: 10), but the
# template's go-template `default` fallback used `30` until #1642. When
# a user passed a values file that did not declare `probes.startup.*`,
# Helm rendered `failureThreshold: 30` from the template default and
# silently halved the documented grace window — masked by anyone using
# the full default values.yaml because that file declares 60 explicitly.
#
# This test renders the chart with a values file that contains ONLY
# `replicaCount: 2` (no probes block at all), forcing the template
# default to take effect, and asserts the rendered Deployment has
# `failureThreshold: 60` under startupProbe.
#
# Exit codes:
#   0  template default is the documented 60
#   1  template default drifted (rendered value printed to stderr)
#   2  prerequisites missing (helm) or render failed
#
# Usage:
#   scripts/check-operator-startup-default.sh

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart_dir="${repo_root}/charts/witwave-operator"

if ! command -v helm >/dev/null 2>&1; then
  echo "helm not found on PATH" >&2
  exit 2
fi

tmp_values="$(mktemp -t witwave-operator-startup-vals.XXXXXX.yaml)"
trap 'rm -f "${tmp_values}"' EXIT

cat >"${tmp_values}" <<'EOF'
# Minimal partial-override values for #1642 regression check.
# Intentionally omits any `probes:` block so the template's
# go-template default is the only thing producing failureThreshold.
replicaCount: 2
EOF

rendered="$(helm template witwave-operator "${chart_dir}" -f "${tmp_values}" 2>/dev/null)" || {
  echo "helm template failed for ${chart_dir}" >&2
  exit 2
}

# Pull the failureThreshold line that immediately follows the
# startupProbe block. awk is used (not grep -A) so the matcher is
# anchored to the startupProbe section and won't drift if the
# liveness/readiness blocks are reordered.
startup_failure_threshold="$(
  echo "${rendered}" | awk '
    /^[[:space:]]*startupProbe:[[:space:]]*$/ { in_startup = 1; next }
    in_startup && /^[[:space:]]*[a-zA-Z]+Probe:[[:space:]]*$/ { in_startup = 0 }
    in_startup && /^[[:space:]]*failureThreshold:/ {
      sub(/^[[:space:]]*failureThreshold:[[:space:]]*/, "")
      print
      exit
    }
  '
)"

if [[ -z "${startup_failure_threshold}" ]]; then
  echo "could not locate startupProbe.failureThreshold in rendered Deployment" >&2
  echo "---- rendered output ----" >&2
  echo "${rendered}" >&2
  exit 1
fi

if [[ "${startup_failure_threshold}" != "60" ]]; then
  echo "startupProbe.failureThreshold drifted: expected 60, got ${startup_failure_threshold}" >&2
  echo "values.yaml documents 60 (=> 600s grace at periodSeconds: 10) — the" >&2
  echo "template's go-template \`default\` fallback in deployment.yaml must match." >&2
  exit 1
fi

echo "ok: startupProbe.failureThreshold=60 (600s grace) with partial values override"
