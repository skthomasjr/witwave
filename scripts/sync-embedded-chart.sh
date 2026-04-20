#!/usr/bin/env bash
# Sync charts/witwave-operator/ → clients/ww/internal/operator/embedded/
# so ww's go:embed has the latest chart contents.
#
# Run this after any edit to charts/witwave-operator/. CI invokes the
# same script with --check to guard against drift.
#
# Usage:
#   scripts/sync-embedded-chart.sh           # write
#   scripts/sync-embedded-chart.sh --check   # fail nonzero on drift
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$REPO_ROOT/charts/witwave-operator/"
DST="$REPO_ROOT/clients/ww/internal/operator/embedded/witwave-operator/"

if [[ ! -d "$SRC" ]]; then
  echo "error: source chart not found at $SRC" >&2
  exit 2
fi

MODE="write"
if [[ "${1:-}" == "--check" ]]; then
  MODE="check"
fi

if [[ "$MODE" == "check" ]]; then
  # Dry-run rsync with itemize-changes; any reported change is drift.
  CHANGES="$(rsync -rptgoDn --delete --itemize-changes "$SRC" "$DST" | grep -v '^\.' || true)"
  if [[ -n "$CHANGES" ]]; then
    echo "error: embedded chart is out of sync with charts/witwave-operator/" >&2
    echo "run: scripts/sync-embedded-chart.sh" >&2
    echo >&2
    echo "$CHANGES" >&2
    exit 1
  fi
  echo "embedded chart is in sync"
  exit 0
fi

mkdir -p "$DST"
rsync -a --delete "$SRC" "$DST"
echo "synced $SRC → $DST"
