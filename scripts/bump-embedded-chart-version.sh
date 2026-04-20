#!/usr/bin/env bash
# Rewrite the embedded operator chart's Chart.yaml to match the
# release version. Called from the ww goreleaser pre-hook so that a
# ww binary built from tag v0.5.1 reports chart version 0.5.1 via
# `ww operator status` — not the canonical 0.1.0 that lives in the
# repo on main.
#
# Why canonical stays at 0.1.0:
# Bumping the committed Chart.yaml on every release would need a
# push-back-from-CI dance that's historically fragile. Instead we
# keep the canonical chart at a stable placeholder (bumped only when
# template changes warrant a chart bump) and let the release path
# rewrite the embedded copy on the fly. Users pulling the chart from
# GHCR see the correct version because release-helm.yml's sed-bump
# does the same trick for the published tarball.
#
# Usage:
#   scripts/bump-embedded-chart-version.sh 0.5.1
#   scripts/bump-embedded-chart-version.sh            # derive from `git describe`
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CHART="$REPO_ROOT/clients/ww/internal/operator/embedded/witwave-operator/Chart.yaml"

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  # goreleaser sets GORELEASER_CURRENT_TAG when building from a tag.
  VERSION="${GORELEASER_CURRENT_TAG:-}"
fi
if [[ -z "$VERSION" ]]; then
  VERSION="$(git -C "$REPO_ROOT" describe --tags --abbrev=0 2>/dev/null || true)"
fi

# Strip a leading v if present.
VERSION="${VERSION#v}"

if [[ -z "$VERSION" ]]; then
  echo "error: could not determine version (no arg, no GORELEASER_CURRENT_TAG, no git tag)" >&2
  exit 2
fi

if [[ ! -f "$CHART" ]]; then
  echo "error: embedded chart not found at $CHART" >&2
  echo "       did scripts/sync-embedded-chart.sh run first?" >&2
  exit 2
fi

# sed -i has different portable semantics on BSD vs GNU; write to a
# backup and remove it to avoid platform-specific -i arg handling.
sed "s/^version:.*/version: $VERSION/" "$CHART" > "$CHART.tmp" && mv "$CHART.tmp" "$CHART"
sed "s/^appVersion:.*/appVersion: \"$VERSION\"/" "$CHART" > "$CHART.tmp" && mv "$CHART.tmp" "$CHART"

echo "bumped embedded witwave-operator chart to $VERSION"
