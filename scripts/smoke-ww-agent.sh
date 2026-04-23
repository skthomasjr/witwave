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
#                     (default: kubeconfig context's ns, else `default`)
#     WW_SMOKE_AGENT  agent name to create
#                     (default: smoke-<unix-timestamp>)
#     WW_SMOKE_KEEP   "true" to skip the final delete — useful when
#                     something fails and you want to poke at state
#                     (default: false)
#
# Usage:
#     ./scripts/smoke-ww-agent.sh
#     WW_BIN=/usr/local/bin/ww-beta ./scripts/smoke-ww-agent.sh
#     WW_SMOKE_NS=sandbox WW_SMOKE_KEEP=true ./scripts/smoke-ww-agent.sh
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
    local label="$1"; shift
    LAST_OUTPUT="$("$@" 2>&1)" && LAST_STATUS=0 || LAST_STATUS=$?
    if [[ $LAST_STATUS -ne 0 ]]; then
        printf '%s  (exit %d — output follows)%s\n' "$C_DIM" "$LAST_STATUS" "$C_RESET"
        printf '%s  ---%s\n' "$C_DIM" "$C_RESET"
        printf '%s' "$LAST_OUTPUT" | sed "s|^|$C_DIM  | |" | sed "s|$| $C_RESET|"
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

cleanup() {
    if [[ "$WW_SMOKE_KEEP" == "true" ]]; then
        printf '\n%sKEEP mode:%s leaving agent %q in namespace %q for inspection.\n' \
            "$C_BOLD" "$C_RESET" "$WW_SMOKE_AGENT" "${WW_SMOKE_NS:-<default>}"
        printf '   Delete manually with: %s agent delete %s%s\n' \
            "$WW_BIN" "$WW_SMOKE_AGENT" "$([[ -n "${WW_SMOKE_NS:-}" ]] && echo " -n $WW_SMOKE_NS")"
        return
    fi
    printf '\n%sCleanup:%s deleting agent %q\n' "$C_BOLD" "$C_RESET" "$WW_SMOKE_AGENT"
    local ns_flag=""
    [[ -n "${WW_SMOKE_NS:-}" ]] && ns_flag="-n $WW_SMOKE_NS"
    # shellcheck disable=SC2086
    "$WW_BIN" agent delete "$WW_SMOKE_AGENT" $ns_flag --yes >/dev/null 2>&1 || \
        printf '  %s(delete failed — manual cleanup may be needed)%s\n' "$C_DIM" "$C_RESET"
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

run_captured "cluster reachable" "$WW_BIN" status
if [[ $LAST_STATUS -eq 0 ]] || grep -qE 'context|cluster|namespace' <<<"$LAST_OUTPUT"; then
    pass "cluster reachable (ww status exited)"
else
    fail "cluster not reachable" "ensure kubeconfig is set and cluster is up"
    exit 2
fi

run_captured "operator installed" "$WW_BIN" operator status
if [[ $LAST_STATUS -eq 0 ]]; then
    pass "witwave-operator responding"
else
    fail "witwave-operator not responding" "run: $WW_BIN operator install"
    exit 2
fi

# ---------------------------------------------------------------------------
# Phase 1 — CR lifecycle (create → list → status)
# ---------------------------------------------------------------------------

section "Phase 1 — CR lifecycle (${WW_SMOKE_AGENT})"

ns_flag=""
[[ -n "${WW_SMOKE_NS:-}" ]] && ns_flag="-n $WW_SMOKE_NS"

# dry-run first — no API calls, just banner rendering
# shellcheck disable=SC2086
run_captured "agent create --dry-run" "$WW_BIN" agent create "$WW_SMOKE_AGENT" $ns_flag --dry-run
if [[ $LAST_STATUS -eq 0 ]] && expect_grep "create WitwaveAgent"; then
    pass "dry-run banner renders"
else
    fail "dry-run" "banner missing or non-zero exit"
fi

# real create + wait for Ready
# shellcheck disable=SC2086
run_captured "agent create (real)" "$WW_BIN" agent create "$WW_SMOKE_AGENT" $ns_flag --timeout 3m
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
# Phase 4 — teardown (delete tested explicitly; cleanup hook handles
# the agent if this block is skipped by an earlier failure)
# ---------------------------------------------------------------------------

section "Phase 4 — teardown"

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
