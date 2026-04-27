#!/usr/bin/env bash
# check-runbook-coverage.sh — verify every alert defined in
# charts/witwave/templates/prometheusrule.yaml has a matching anchor
# in docs/runbooks.md (#1698).
#
# `runbook_url` annotations on every alert point at corresponding
# sections of docs/runbooks.md. The convention is documented at
# docs/runbooks.md:6-8: "New alerts MUST add a section here with a
# matching `runbook_url` annotation — CI does not yet enforce this
# but the convention is load-bearing for on-call ergonomics." This
# script closes the CI gap.
#
# Lowercases the alert name (Prometheus markdown convention) and
# searches docs/runbooks.md for a matching `## <lowercased>` H2
# anchor. PVC alerts share a single heading
# `## witwavepvcfillwarning / witwavepvcfillcritical` so the script
# accepts slash-separated multi-anchor headings.
#
# Exit codes:
#   0  every alert has a runbook anchor
#   1  one or more alerts missing (names printed to stderr)
#   2  prerequisites missing (helm/python3) or render failed
#
# Usage:
#   scripts/check-runbook-coverage.sh

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart_dir="${repo_root}/charts/witwave"
runbooks="${repo_root}/docs/runbooks.md"

if ! command -v helm >/dev/null 2>&1; then
  echo "check-runbook-coverage: helm not on PATH" >&2
  exit 2
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "check-runbook-coverage: python3 not on PATH" >&2
  exit 2
fi
if [[ ! -f "${runbooks}" ]]; then
  echo "check-runbook-coverage: ${runbooks} not found" >&2
  exit 2
fi

rendered="$(helm template "${chart_dir}" \
  --set prometheusRule.enabled=true \
  --set metrics.enabled=true \
  2>/dev/null || true)"

if [[ -z "${rendered}" ]]; then
  echo "check-runbook-coverage: helm template produced no output" >&2
  exit 2
fi

export REPO_ROOT="${repo_root}"
printf '%s' "${rendered}" | python3 "${repo_root}/scripts/check_runbook_coverage.py"
