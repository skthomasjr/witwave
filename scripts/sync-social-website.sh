#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/sync-social-website.sh <destination-directory>

Copies social/website/ into a destination directory with symlinks resolved.
This is intended for publishing the static site to witwave-ai.github.io.

Notes:
  - Symlinks are resolved so content/whitepapers/*.md becomes real files.
  - .git/ is never touched in the destination.
  - CNAME is preserved if it already exists in the destination, so GitHub Pages
    custom-domain settings are not accidentally removed by the sync.
USAGE
}

if [ "$#" -ne 1 ]; then
  usage >&2
  exit 2
fi

repo_root="$(git rev-parse --show-toplevel)"
source_dir="${repo_root}/social/website/"
destination_dir="$1"

if [ ! -d "$source_dir" ]; then
  echo "source directory not found: $source_dir" >&2
  exit 1
fi

mkdir -p "$destination_dir"

rsync -aL --delete \
  --exclude '.git/' \
  --exclude 'CNAME' \
  "$source_dir" "$destination_dir/"
