#!/usr/bin/env bash
# check-prom-rule-metrics.sh — verify every witwave-prefixed metric named in
# the chart's PrometheusRule alert expressions is actually emitted by
# production source code (#1682).
#
# An alert that references a metric name no source file declares does NOT
# fire — PromQL with a missing series simply returns no data. The "absence
# of an alert" is silently mistaken for "the system is healthy." This
# script catches the failure mode at CI time by extracting metric names
# from the rendered PrometheusRule and grep-ing the source for each.
#
# Scope: metrics with witwave-owned prefixes (backend_, harness_,
# witwaveagent_, witwaveprompt_, mcp_). Stock Kubernetes / cAdvisor / kubelet
# metrics (e.g. kubelet_volume_stats_*, up{}, container_*) are skipped.
#
# Exit codes:
#   0  every checked metric resolves to at least one source declaration
#   1  one or more metrics are unresolved (names printed to stderr)
#   2  prerequisites missing (helm/python3) or render failed
#
# Usage:
#   scripts/check-prom-rule-metrics.sh

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart_dir="${repo_root}/charts/witwave"

if ! command -v helm >/dev/null 2>&1; then
  echo "check-prom-rule-metrics: helm not on PATH" >&2
  exit 2
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "check-prom-rule-metrics: python3 not on PATH" >&2
  exit 2
fi

rendered="$(helm template "${chart_dir}" \
  --set prometheusRule.enabled=true \
  --set metrics.enabled=true \
  2>/dev/null || true)"

if [[ -z "${rendered}" ]]; then
  echo "check-prom-rule-metrics: helm template produced no output" >&2
  exit 2
fi

# Pipe rendered YAML to python via stdin so the script can use any
# punctuation (apostrophes, backticks) without bash interpreting it.
export REPO_ROOT="${repo_root}"
printf '%s' "${rendered}" | python3 "${repo_root}/scripts/check_prom_rule_metrics.py"
