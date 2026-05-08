#!/usr/bin/env bash
#
# smoke-ww-agent.sh — end-to-end smoke test of the `ww agent` subtree
# against a real Kubernetes cluster.
#
# Exercises every verb shipped in v0.7.0:
#
#     create   list   status   send   logs   events   delete
#
# Plus the upstream prerequisites: the CLI itself, cluster reachability,
# and an installed operator. Uses the echo backend so no API keys are
# required — the only prerequisites are a `kubectl` context pointed at a
# cluster you can write to, and `ww` 0.7.0+ on your $PATH.
#
# The script creates one throwaway WitwaveAgent, exercises the CLI
# against it, and deletes it at the end (skipped when WW_SMOKE_KEEP=true).
# Safe to run multiple times in parallel — each invocation picks a
# unique agent name so concurrent runs don't collide.
#
# ---------------------------------------------------------------------
# Environment knobs
# ---------------------------------------------------------------------
#
#     WW_BIN          path/name of the ww binary (default: ww)
#     WW_SMOKE_NS     namespace to create the agent in
#                     (default: `witwave` — the ww default). Always
#                     passed explicitly to every ww invocation; the
#                     script provisions the namespace if missing via
#                     `--create-namespace` on the first create call.
#     WW_SMOKE_AGENT  agent name to create
#                     (default: smoke-<unix-timestamp>)
#     WW_SMOKE_KEEP   "true" to skip the final delete — useful when
#                     something fails and you want to poke at state
#                     (default: false)
#
#     WW_SMOKE_GITHUB_REPO
#                     When set, enables Phase 5 (gitOps round-trip):
#                     scaffold → git add → rename → remove --remove-
#                     repo-folder → clean. The repo must be writable
#                     by the invoking user's git credentials (gh auth
#                     token / credential helper / ssh agent). Each
#                     run creates + removes a `smoke-git-<ts>/`
#                     subdirectory, so concurrent runs don't collide.
#                     Leave unset to skip the gitOps phase entirely —
#                     the baseline smoke (phases 0–3 + teardown) runs
#                     either way.
#
# Usage:
#     ./scripts/smoke-ww-agent.sh
#     WW_BIN=/usr/local/bin/ww-beta ./scripts/smoke-ww-agent.sh
#     WW_SMOKE_NS=sandbox WW_SMOKE_KEEP=true ./scripts/smoke-ww-agent.sh
#     WW_SMOKE_GITHUB_REPO=you/witwave-smoke ./scripts/smoke-ww-agent.sh
#
# Exit codes:
#     0   all checks passed
#     1   one or more checks failed
#     2   prerequisite not met (no ww, no cluster, no operator)

set -euo pipefail

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

WW_BIN="${WW_BIN:-ww}"
WW_SMOKE_AGENT="${WW_SMOKE_AGENT:-smoke-$(date +%s)}"
WW_SMOKE_KEEP="${WW_SMOKE_KEEP:-false}"

# Always run inside a concrete, non-"default" namespace. "default" is a
# shared free-for-all; ww agents get their own blast radius. The CLI's
# own default for --namespace is `witwave`, so we mirror it here and pass
# -n explicitly on every invocation. Use --create-namespace on the first
# create call so the smoke can bootstrap a virgin cluster.
WW_SMOKE_NS="${WW_SMOKE_NS:-witwave}"

# --yes across every mutating call. The smoke is a scripted flow — no
# humans expected to type "y" mid-run.
export WW_ASSUME_YES=true

# Colors. Disabled automatically when stdout isn't a TTY so CI logs
# don't end up with ANSI soup.
if [[ -t 1 ]]; then
  C_RESET=$'\033[0m'
  C_GREEN=$'\033[0;32m'
  C_RED=$'\033[0;31m'
  C_DIM=$'\033[0;2m'
  C_BOLD=$'\033[1m'
else
  C_RESET="" C_GREEN="" C_RED="" C_DIM="" C_BOLD=""
fi

PASSED=0
FAILED=0
FAILED_NAMES=()

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# pass / fail prefix lines. Each step logs a one-liner summary so CI
# grep-style diagnostics work.
pass() {
  PASSED=$((PASSED + 1))
  printf '%s✓%s %s\n' "$C_GREEN" "$C_RESET" "$1"
}
fail() {
  FAILED=$((FAILED + 1))
  FAILED_NAMES+=("$1")
  printf '%s✗%s %s\n' "$C_RED" "$C_RESET" "$1"
  if [[ -n "${2:-}" ]]; then
    printf '%s  %s%s\n' "$C_DIM" "$2" "$C_RESET"
  fi
}
section() {
  printf '\n%s── %s ──%s\n' "$C_BOLD" "$1" "$C_RESET"
}

# run_captured <label> <command...>
#   Runs a command, captures stdout+stderr, stashes status. Called by
#   every check so assertion-level logic doesn't have to duplicate
#   tee/pipe plumbing.
LAST_OUTPUT=""
LAST_STATUS=0
run_captured() {
  local label="$1"
  shift
  LAST_OUTPUT="$("$@" 2>&1)" && LAST_STATUS=0 || LAST_STATUS=$?
  if [[ $LAST_STATUS -ne 0 ]]; then
    printf '%s  (exit %d — output follows)%s\n' "$C_DIM" "$LAST_STATUS" "$C_RESET"
    printf '%s  ---%s\n' "$C_DIM" "$C_RESET"
    # Indent each output line with a dim prefix. Avoid sed's delimiter
    # shenanigans (BSD sed on macOS parses `|` inside a substitution
    # as a flag separator) by using a pure-bash loop.
    while IFS= read -r line; do
      printf '%s  %s%s\n' "$C_DIM" "$line" "$C_RESET"
    done <<<"$LAST_OUTPUT"
    printf '%s  ---%s\n' "$C_DIM" "$C_RESET"
  fi
}

# expect_grep <substring>: assert LAST_OUTPUT contains substring.
expect_grep() {
  if ! grep -qF -- "$1" <<<"$LAST_OUTPUT"; then
    return 1
  fi
  return 0
}

# ---------------------------------------------------------------------------
# Cleanup — always runs
# ---------------------------------------------------------------------------

# Phase 5 sets these when it runs so cleanup can tear them down too.
# Kept at top-level so `trap cleanup EXIT` sees them from any failure point.
WW_SMOKE_GIT_AGENT=""
WW_SMOKE_GIT_REPO=""

cleanup() {
  if [[ "$WW_SMOKE_KEEP" == "true" ]]; then
    printf '\n%sKEEP mode:%s leaving agent %q in namespace %q for inspection.\n' \
      "$C_BOLD" "$C_RESET" "$WW_SMOKE_AGENT" "${WW_SMOKE_NS:-<default>}"
    printf '   Delete manually with: %s agent delete %s%s\n' \
      "$WW_BIN" "$WW_SMOKE_AGENT" "$([[ -n "${WW_SMOKE_NS:-}" ]] && echo " -n $WW_SMOKE_NS")"
    if [[ -n "$WW_SMOKE_GIT_AGENT" ]]; then
      printf '   Delete manually with: %s agent delete %s%s\n' \
        "$WW_BIN" "$WW_SMOKE_GIT_AGENT" "$([[ -n "${WW_SMOKE_NS:-}" ]] && echo " -n $WW_SMOKE_NS")"
      printf '   Remove scaffold: clone %s and `git rm -r .agents/%s`\n' \
        "$WW_SMOKE_GIT_REPO" "$WW_SMOKE_GIT_AGENT"
    fi
    return
  fi
  printf '\n%sCleanup:%s deleting agent %q\n' "$C_BOLD" "$C_RESET" "$WW_SMOKE_AGENT"
  local ns_flag=""
  [[ -n "${WW_SMOKE_NS:-}" ]] && ns_flag="-n $WW_SMOKE_NS"
  # shellcheck disable=SC2086
  "$WW_BIN" agent delete "$WW_SMOKE_AGENT" $ns_flag --yes >/dev/null 2>&1 ||
    printf '  %s(delete failed — manual cleanup may be needed)%s\n' "$C_DIM" "$C_RESET"

  # Phase 5 teardown: git agent CR + scaffolded directory on the remote.
  # Best-effort; never fails the overall run.
  if [[ -n "$WW_SMOKE_GIT_AGENT" ]]; then
    printf '%sCleanup:%s deleting git agent %q\n' "$C_BOLD" "$C_RESET" "$WW_SMOKE_GIT_AGENT"
    # shellcheck disable=SC2086
    "$WW_BIN" agent delete "$WW_SMOKE_GIT_AGENT" $ns_flag --yes >/dev/null 2>&1 || true
    if [[ -n "$WW_SMOKE_GIT_REPO" ]]; then
      cleanup_scaffold_dir "$WW_SMOKE_GIT_REPO" "$WW_SMOKE_GIT_AGENT"
    fi
  fi
}

# cleanup_scaffold_dir clones the repo, git-rm's the scaffolded directory,
# commits + pushes. Mirrors the ad-hoc cleanup a human would do; no CLI
# verb exists for "undo scaffold" because the scaffolded layout is meant
# to live on past the CLI's involvement.
cleanup_scaffold_dir() {
  local repo="$1" name="$2"
  local tmp
  tmp="$(mktemp -d)"
  printf '%s  (cleaning up %s/.agents/%s via git clone)%s\n' \
    "$C_DIM" "$repo" "$name" "$C_RESET"
  # Resolve the repo URL shape the same way the CLI does — accept
  # owner/repo and expand to an https URL. Anything more exotic (full
  # URL, ssh) is used verbatim.
  local url="$repo"
  if [[ "$repo" != *://* && "$repo" != git@* ]]; then
    url="https://github.com/${repo}.git"
  fi
  if ! git clone --depth 1 --quiet "$url" "$tmp" 2>/dev/null; then
    printf '  %s(clone failed — scaffold dir left in repo; delete manually)%s\n' \
      "$C_DIM" "$C_RESET"
    rm -rf "$tmp"
    return
  fi
  (
    cd "$tmp" || exit 0
    if [[ -d ".agents/${name}" ]]; then
      git rm -r --quiet ".agents/${name}" >/dev/null 2>&1 || true
      git -c user.email="smoke@ww.local" -c user.name="ww smoke" \
        commit --quiet -m "Clean up smoke scaffold ${name}" >/dev/null 2>&1 || true
      git push --quiet >/dev/null 2>&1 ||
        printf '  %s(push failed — scaffold dir left in repo; delete manually)%s\n' \
          "$C_DIM" "$C_RESET"
    fi
  )
  rm -rf "$tmp"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Phase 0 — prerequisites
# ---------------------------------------------------------------------------

section "Prerequisites"

run_captured "ww binary" "$WW_BIN" version
if [[ $LAST_STATUS -eq 0 ]]; then
  version_line="$(grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' <<<"$LAST_OUTPUT" | head -1 || true)"
  pass "ww binary present (${version_line:-unknown version})"
else
  fail "ww binary not found or failed" "WW_BIN=$WW_BIN"
  exit 2
fi

# Cluster reachability + operator installation are checked in one shot
# via `ww operator status`, which necessarily touches the Kubernetes API
# and then introspects the operator Helm release. Merging the two into a
# single probe avoids an orthogonal ww status call that would require a
# configured harness URL — unrelated to whether the cluster is reachable.
#
# Content check matters: `ww operator status` exits 0 even when the
# operator is absent (it successfully reported "(not installed)"), so we
# have to grep the output to tell "installed" from "not installed" /
# "CRDs absent". Missing CRDs are fatal — no point continuing to the
# agent lifecycle phase because every create/list call would 404 on the
# dynamic client.
run_captured "cluster + operator" "$WW_BIN" operator status
if [[ $LAST_STATUS -ne 0 ]]; then
  if grep -qEi 'kubeconfig|no.+context|connection refused|no such host|i/o timeout' <<<"$LAST_OUTPUT"; then
    fail "cluster not reachable" "ensure kubeconfig is set and the cluster is up"
  else
    fail "operator status probe failed" "run: $WW_BIN operator status (inspect manually)"
  fi
  exit 2
fi
if grep -qE '\(not installed\)|absent' <<<"$LAST_OUTPUT"; then
  # Self-heal: idempotently install the operator via --if-missing. Uses
  # --yes because we're in a scripted flow. If the cluster isn't a
  # local-cluster context the install command's own prompt-skip
  # heuristic won't fire; --yes covers both cases.
  printf '%s… operator missing; installing (--if-missing)%s\n' "$C_DIM" "$C_RESET"
  run_captured "operator install --if-missing" "$WW_BIN" operator install --if-missing --yes
  if [[ $LAST_STATUS -ne 0 ]]; then
    fail "operator install" "check: $WW_BIN operator install --help"
    exit 2
  fi
  pass "operator installed via --if-missing"
else
  pass "cluster reachable + witwave-operator installed"
fi

# ---------------------------------------------------------------------------
# Phase 1 — CR lifecycle (create → list → status)
# ---------------------------------------------------------------------------

section "Phase 1 — CR lifecycle (${WW_SMOKE_AGENT})"

ns_flag=""
[[ -n "${WW_SMOKE_NS:-}" ]] && ns_flag="-n $WW_SMOKE_NS"

# dry-run first — no API calls, just banner rendering
# shellcheck disable=SC2086
run_captured "agent create --dry-run" "$WW_BIN" agent create "$WW_SMOKE_AGENT" $ns_flag --create-namespace --dry-run
if [[ $LAST_STATUS -eq 0 ]] && expect_grep "create WitwaveAgent"; then
  pass "dry-run banner renders"
else
  fail "dry-run" "banner missing or non-zero exit"
fi

# real create + wait for Ready
# shellcheck disable=SC2086
run_captured "agent create (real)" "$WW_BIN" agent create "$WW_SMOKE_AGENT" $ns_flag --create-namespace --timeout 3m
if [[ $LAST_STATUS -eq 0 ]] && expect_grep "is ready"; then
  pass "agent created and reported Ready"
else
  fail "create" "expected 'is ready' confirmation"
fi

# shellcheck disable=SC2086
run_captured "agent list" "$WW_BIN" agent list $ns_flag
if [[ $LAST_STATUS -eq 0 ]] && expect_grep "$WW_SMOKE_AGENT"; then
  pass "agent appears in list"
else
  fail "list" "agent name not found in table output"
fi

# shellcheck disable=SC2086
run_captured "agent status" "$WW_BIN" agent status "$WW_SMOKE_AGENT" $ns_flag
if [[ $LAST_STATUS -eq 0 ]] && expect_grep "Ready"; then
  pass "status reports Ready phase"
else
  fail "status" "expected Phase: Ready"
fi

# ---------------------------------------------------------------------------
# Phase 2 — interaction (send / logs / events)
# ---------------------------------------------------------------------------

section "Phase 2 — interaction"

# shellcheck disable=SC2086
run_captured "agent send (default)" "$WW_BIN" agent send "$WW_SMOKE_AGENT" "ping from smoke test" $ns_flag
if [[ $LAST_STATUS -eq 0 ]] && expect_grep "ping from smoke test"; then
  pass "send round-trip (echo quoted the prompt back)"
else
  fail "send" "did not find prompt text in response"
fi

# shellcheck disable=SC2086
run_captured "agent send --raw" "$WW_BIN" agent send "$WW_SMOKE_AGENT" "raw-envelope" $ns_flag --raw
if [[ $LAST_STATUS -eq 0 ]] && expect_grep '"jsonrpc"'; then
  pass "send --raw emits JSON-RPC envelope"
else
  fail "send --raw" "expected jsonrpc field in raw output"
fi

# shellcheck disable=SC2086
run_captured "agent logs (harness)" "$WW_BIN" agent logs "$WW_SMOKE_AGENT" $ns_flag --no-follow --tail 5
if [[ $LAST_STATUS -eq 0 ]]; then
  pass "logs harness container (non-follow)"
else
  fail "logs harness" "non-zero exit"
fi

# shellcheck disable=SC2086
run_captured "agent logs -c echo" "$WW_BIN" agent logs "$WW_SMOKE_AGENT" $ns_flag -c echo --no-follow --tail 5
if [[ $LAST_STATUS -eq 0 ]]; then
  pass "logs echo sidecar (non-follow)"
else
  fail "logs echo" "non-zero exit — container may not exist or be running"
fi

# shellcheck disable=SC2086
run_captured "agent events" "$WW_BIN" agent events "$WW_SMOKE_AGENT" $ns_flag --since 10m
if [[ $LAST_STATUS -eq 0 ]]; then
  pass "events snapshot"
else
  fail "events" "non-zero exit"
fi

# ---------------------------------------------------------------------------
# Phase 3 — validation + edge cases
# ---------------------------------------------------------------------------

section "Phase 3 — input validation"

run_captured "invalid name (uppercase)" "$WW_BIN" agent create "Hello-BadName" --dry-run
if [[ $LAST_STATUS -ne 0 ]] && expect_grep "DNS-1123"; then
  pass "uppercase name rejected with DNS-1123 message"
else
  fail "uppercase name" "expected non-zero exit + DNS-1123 message"
fi

long_name="$(printf 'a%.0s' {1..60})"
run_captured "invalid name (too long)" "$WW_BIN" agent create "$long_name" --dry-run
if [[ $LAST_STATUS -ne 0 ]] && expect_grep "maximum"; then
  pass "over-limit name rejected with length message"
else
  fail "over-limit name" "expected non-zero exit + length message"
fi

# re-create the same agent: should surface AlreadyExists cleanly
# shellcheck disable=SC2086
run_captured "duplicate create" "$WW_BIN" agent create "$WW_SMOKE_AGENT" $ns_flag --no-wait
if [[ $LAST_STATUS -ne 0 ]] && expect_grep "already exists"; then
  pass "duplicate create surfaces AlreadyExists"
else
  fail "duplicate create" "expected non-zero exit + 'already exists'"
fi

# ---------------------------------------------------------------------------
# Phase 5 — gitOps round-trip (scaffold → git add/list → rename → remove
# --remove-repo-folder → git remove). Gated on WW_SMOKE_GITHUB_REPO because
# it mutates a remote repo and needs credentials; leave unset to skip.
#
# Uses a dedicated multi-backend agent (`smoke-git-<ts>`) so the Phase 1
# agent stays untouched. Cleanup is deferred to the EXIT trap, which
# deletes the git agent CR and git-rm's `.agents/<name>/` from the repo
# on a best-effort basis.
# ---------------------------------------------------------------------------

if [[ -n "${WW_SMOKE_GITHUB_REPO:-}" ]]; then
  WW_SMOKE_GIT_AGENT="smoke-git-$(date +%s)"
  WW_SMOKE_GIT_REPO="$WW_SMOKE_GITHUB_REPO"

  section "Phase 5 — gitOps round-trip (${WW_SMOKE_GIT_AGENT} → ${WW_SMOKE_GITHUB_REPO})"

  # 5.1 — scaffold dry-run. Verifies the scaffolder parses --repo and
  # --backend without touching the remote.
  run_captured "scaffold --dry-run" \
    "$WW_BIN" agent scaffold "$WW_SMOKE_GIT_AGENT" \
    --repo "$WW_SMOKE_GITHUB_REPO" \
    --backend "echo-1:echo" --backend "echo-2:echo" \
    --dry-run
  if [[ $LAST_STATUS -eq 0 ]] && expect_grep "Scaffold"; then
    pass "scaffold --dry-run renders plan"
  else
    fail "scaffold --dry-run" "expected plan banner"
  fi

  # 5.2 — real scaffold: writes .agents/<name>/ to the remote.
  run_captured "scaffold (real)" \
    "$WW_BIN" agent scaffold "$WW_SMOKE_GIT_AGENT" \
    --repo "$WW_SMOKE_GITHUB_REPO" \
    --backend "echo-1:echo" --backend "echo-2:echo"
  if [[ $LAST_STATUS -eq 0 ]]; then
    pass "scaffold committed + pushed to ${WW_SMOKE_GITHUB_REPO}"
  else
    fail "scaffold" "remote push failed; check git credentials"
  fi

  # 5.3 — create the multi-backend CR so git add has something to wire.
  # shellcheck disable=SC2086
  run_captured "create git agent" \
    "$WW_BIN" agent create "$WW_SMOKE_GIT_AGENT" $ns_flag --create-namespace \
    --backend "echo-1:echo" --backend "echo-2:echo" --timeout 3m
  if [[ $LAST_STATUS -eq 0 ]] && expect_grep "is ready"; then
    pass "multi-backend git agent created + Ready"
  else
    fail "create git agent" "expected 'is ready' confirmation"
  fi

  # 5.4 — attach the gitSync. Public repo path — no auth flag needed.
  # shellcheck disable=SC2086
  run_captured "git add" \
    "$WW_BIN" agent git add "$WW_SMOKE_GIT_AGENT" $ns_flag \
    --repo "$WW_SMOKE_GITHUB_REPO"
  if [[ $LAST_STATUS -eq 0 ]] && expect_grep "Attached gitSync"; then
    pass "git add wired the repo"
  else
    fail "git add" "expected 'Attached gitSync' confirmation"
  fi

  # 5.5 — list verifies the sync landed on the CR.
  # shellcheck disable=SC2086
  run_captured "git list" \
    "$WW_BIN" agent git list "$WW_SMOKE_GIT_AGENT" $ns_flag
  if [[ $LAST_STATUS -eq 0 ]] && expect_grep "$WW_SMOKE_GITHUB_REPO"; then
    pass "git list shows configured sync"
  else
    fail "git list" "repo URL missing from list output"
  fi

  # 5.6 — backend rename (CR + repo folder, in one shot). Requires the
  # gitSync from 5.4 — repo-side `git mv` only runs when exactly one
  # sync is wired.
  # shellcheck disable=SC2086
  run_captured "backend rename" \
    "$WW_BIN" agent backend rename "$WW_SMOKE_GIT_AGENT" "echo-2" "echo-2-renamed" $ns_flag
  if [[ $LAST_STATUS -eq 0 ]] && expect_grep "Renamed backend"; then
    pass "backend rename updated CR + repo"
  else
    fail "backend rename" "expected 'Renamed backend' confirmation"
  fi

  # 5.7 — backend remove --remove-repo-folder (clones, git rm's the
  # backend's .<name>/ folder, rewrites backend.yaml, pushes).
  # shellcheck disable=SC2086
  run_captured "backend remove --remove-repo-folder" \
    "$WW_BIN" agent backend remove "$WW_SMOKE_GIT_AGENT" "echo-2-renamed" $ns_flag \
    --remove-repo-folder
  if [[ $LAST_STATUS -eq 0 ]] && expect_grep "Removed backend"; then
    pass "backend remove + repo-folder cleanup"
  else
    fail "backend remove" "expected 'Removed backend' confirmation"
  fi

  # 5.8 — atomic cleanup via `ww agent delete --purge`. Exercises the
  # three-phase repo-wipe + CR-delete + Secret-reap in one call, which
  # is the shape users are meant to reach for when decommissioning an
  # agent. On success, the trap's cleanup_scaffold_dir fallback is
  # a no-op (the dir's already gone), which confirms purge did its
  # job end-to-end.
  # shellcheck disable=SC2086
  run_captured "agent delete --purge" \
    "$WW_BIN" agent delete "$WW_SMOKE_GIT_AGENT" $ns_flag --purge --yes
  if [[ $LAST_STATUS -eq 0 ]] && expect_grep "Deleted WitwaveAgent"; then
    pass "agent delete --purge wiped CR + repo folder"
    # Tell the trap's fallback not to re-try the repo cleanup — the
    # CLI already did it. (Leave WW_SMOKE_GIT_AGENT set so KEEP mode
    # can still refer to it by name.)
    WW_SMOKE_GIT_REPO=""
  else
    fail "agent delete --purge" "expected 'Deleted WitwaveAgent' confirmation"
  fi
else
  printf '\n%s(Phase 5 skipped — set WW_SMOKE_GITHUB_REPO=owner/repo to enable)%s\n' \
    "$C_DIM" "$C_RESET"
fi

# ---------------------------------------------------------------------------
# Phase 6 — teardown (delete tested explicitly; cleanup hook handles
# the agent if this block is skipped by an earlier failure)
# ---------------------------------------------------------------------------

section "Phase 6 — teardown"

# shellcheck disable=SC2086
run_captured "agent delete" "$WW_BIN" agent delete "$WW_SMOKE_AGENT" $ns_flag --yes
if [[ $LAST_STATUS -eq 0 ]] && expect_grep "Deleted WitwaveAgent"; then
  pass "delete removed the CR"
  # Disable the cleanup hook since we just deleted successfully —
  # re-running it would print a confusing "delete failed" message.
  trap - EXIT
else
  fail "delete" "non-zero exit or missing confirmation"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

printf '\n%s── Summary ──%s\n' "$C_BOLD" "$C_RESET"
printf '%sPassed:%s %d\n' "$C_GREEN" "$C_RESET" "$PASSED"
printf '%sFailed:%s %d\n' "$C_RED" "$C_RESET" "$FAILED"

if [[ $FAILED -gt 0 ]]; then
  printf '\n%sFailed checks:%s\n' "$C_RED" "$C_RESET"
  for name in "${FAILED_NAMES[@]}"; do
    printf '  - %s\n' "$name"
  done
  exit 1
fi

printf '\n%sAll green.%s\n' "$C_GREEN" "$C_RESET"
exit 0
