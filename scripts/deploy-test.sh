#!/usr/bin/env bash
# Deploy (or upgrade) the witwave-test smoke stack using credentials from .env.
#
# Usage: scripts/deploy-test.sh
#
# Sources .env at the repo root, verifies required vars are present, and
# runs `helm upgrade --install witwave-test ./charts/witwave -f values-test.yaml`
# passing credentials as --set flags. Uses the inline-credentials pattern
# (gitSync.credentials, backends.credentials) with acknowledgeInsecureInline=true
# so the chart renders Secrets for us; no manual `kubectl create secret` needed.
#
# Required in .env:
#   CLAUDE_CODE_OAUTH_TOKEN  — all enabled a2-claude backends use this
#   GITSYNC_USERNAME         — GitHub username for git-sync HTTPS auth
#   GITSYNC_PASSWORD         — GitHub PAT for git-sync HTTPS auth
#
# Optional in .env (placeholders used when absent — disabled backends ignore):
#   OPENAI_API_KEY           — a2-codex backends
#   GEMINI_API_KEY           — a2-gemini backends
#
# Optional environment variables (override before invoking the script):
#   RELEASE_NAME             — helm release name (default: witwave-test)
#   NAMESPACE                — target namespace (default: witwave-test)
#   VALUES_FILE              — values overlay path (default: ./charts/witwave/values-test.yaml)

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

RELEASE_NAME="${RELEASE_NAME:-witwave-test}"
NAMESPACE="${NAMESPACE:-witwave-test}"
VALUES_FILE="${VALUES_FILE:-./charts/witwave/values-test.yaml}"

# ---------------------------------------------------------------------------
# Load .env
# ---------------------------------------------------------------------------
if [[ ! -f .env ]]; then
  echo "error: missing .env at repo root ($(pwd)/.env)"
  echo "hint:  copy the team's .env template and fill in your tokens"
  exit 2
fi
set -a
# shellcheck disable=SC1091
source .env
set +a

# ---------------------------------------------------------------------------
# Validate required vars
# ---------------------------------------------------------------------------
missing=()
for var in CLAUDE_CODE_OAUTH_TOKEN GITSYNC_USERNAME GITSYNC_PASSWORD; do
  if [[ -z "${!var:-}" ]]; then missing+=("$var"); fi
done
if [[ ${#missing[@]} -gt 0 ]]; then
  echo "error: .env is missing required vars: ${missing[*]}"
  exit 3
fi

# ---------------------------------------------------------------------------
# GHCR image-pull secret. The chart's imagePullSecrets list references
# `ghcr-credentials` by name; the chart can't create an image-pull Secret
# itself (those need a dockerconfigjson from `docker login`), so we pre-create
# it here from the dev's local ~/.docker/config.json. Anyone who hasn't run
# `docker login ghcr.io` won't have this entry and the script fails loudly.
# ---------------------------------------------------------------------------
kubectl create namespace "$NAMESPACE" 2>/dev/null || true

if ! kubectl get secret ghcr-credentials -n "$NAMESPACE" >/dev/null 2>&1; then
  docker_cfg="${HOME}/.docker/config.json"
  if [[ ! -f "$docker_cfg" ]]; then
    echo "error: no ~/.docker/config.json — run 'docker login ghcr.io' first"
    exit 4
  fi
  if ! python3 -c "import json; d=json.load(open('$docker_cfg')); exit(0 if 'ghcr.io' in d.get('auths', {}) else 1)"; then
    echo "error: ~/.docker/config.json has no ghcr.io entry — run 'docker login ghcr.io' first"
    exit 4
  fi
  tmpfile=$(mktemp -t ghcr-dockerconfig.XXXXXX.json)
  python3 -c "
import json, sys
d = json.load(open('$docker_cfg'))
json.dump({'auths': {'ghcr.io': d['auths']['ghcr.io']}}, open('$tmpfile','w'))
"
  kubectl create secret generic ghcr-credentials \
    --from-file=.dockerconfigjson="$tmpfile" \
    --type=kubernetes.io/dockerconfigjson \
    -n "$NAMESPACE" >/dev/null
  rm -f "$tmpfile"
  echo "created image-pull secret: ghcr-credentials"
fi

# ---------------------------------------------------------------------------
# helm upgrade --install
# ---------------------------------------------------------------------------
echo ""
echo "helm upgrade --install $RELEASE_NAME (-f $VALUES_FILE -n $NAMESPACE)"
helm upgrade --install "$RELEASE_NAME" ./charts/witwave \
  -f "$VALUES_FILE" \
  --set-string gitSync.credentials.username="$GITSYNC_USERNAME" \
  --set-string gitSync.credentials.token="$GITSYNC_PASSWORD" \
  --set gitSync.credentials.acknowledgeInsecureInline=true \
  --set-string "backends.credentials.secrets.CLAUDE_CODE_OAUTH_TOKEN=$CLAUDE_CODE_OAUTH_TOKEN" \
  --set-string "backends.credentials.secrets.OPENAI_API_KEY=${OPENAI_API_KEY:-placeholder-no-openai-key}" \
  --set-string "backends.credentials.secrets.GEMINI_API_KEY=${GEMINI_API_KEY:-placeholder-no-gemini-key}" \
  --set backends.credentials.acknowledgeInsecureInline=true \
  -n "$NAMESPACE" --create-namespace
