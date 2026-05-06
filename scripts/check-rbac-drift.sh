#!/usr/bin/env bash
# check-rbac-drift.sh — verify the operator's canonical RBAC stays in
# sync with the chart-rendered ClusterRole.
#
# Compares the `rules:` section of operator/config/rbac/role.yaml
# (canonical, controller-gen output, hand-tweaked to split Secret
# verbs per #1613) against the rendered ClusterRole produced by
# `helm template charts/witwave-operator` with the default values
# (rbac.scope=cluster, rbac.secretsWrite=true).
#
# Both shapes MUST agree on every (apiGroup, resource, verb) tuple.
# The chart additionally renders a Helm-conditional Secret-write rule
# behind `rbac.secretsWrite`; this script renders with the default
# (true) so the rule is present and the diff stays byte-for-byte
# meaningful.
#
# Exit codes:
#   0  no drift detected
#   1  drift detected (diff printed to stderr)
#   2  prerequisites missing (helm/yq/python3) or render failed
#
# Usage:
#   scripts/check-rbac-drift.sh
#
# Not yet wired into GitHub Actions — track that integration as a
# follow-up. For now, run manually or from a pre-commit hook.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
canonical="${repo_root}/operator/config/rbac/role.yaml"
chart_dir="${repo_root}/charts/witwave-operator"

if ! command -v helm >/dev/null 2>&1; then
  echo "check-rbac-drift: helm not on PATH" >&2
  exit 2
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "check-rbac-drift: python3 not on PATH" >&2
  exit 2
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

rendered="${tmpdir}/rendered.yaml"
if ! helm template witwave-operator "${chart_dir}" \
  --namespace witwave-system \
  --set rbac.scope=cluster \
  --set rbac.secretsWrite=true \
  >"${rendered}" 2>"${tmpdir}/helm.err"; then
  echo "check-rbac-drift: helm template failed" >&2
  cat "${tmpdir}/helm.err" >&2
  exit 2
fi

# Extract and normalise rules from each source. We canonicalise by:
#   - sorting verbs within a rule
#   - sorting resources within a rule
#   - sorting rules by (apiGroups, resources, verbs) tuple
# That way ordering differences between controller-gen and the chart
# template don't show up as drift.
python3 - "${canonical}" "${rendered}" <<'PY'
import sys, yaml, json

def load_rules(path, kind, name_match=None):
    with open(path) as fh:
        docs = list(yaml.safe_load_all(fh))
    for doc in docs:
        if not isinstance(doc, dict):
            continue
        if doc.get("kind") != kind:
            continue
        meta = doc.get("metadata", {})
        if name_match is not None and name_match not in meta.get("name", ""):
            continue
        return doc.get("rules", []) or []
    return None

def canon(rules):
    out = []
    for r in rules:
        groups = sorted(r.get("apiGroups", []) or [])
        resources = sorted(r.get("resources", []) or [])
        verbs = sorted(r.get("verbs", []) or [])
        resource_names = sorted(r.get("resourceNames", []) or [])
        out.append({
            "apiGroups": groups,
            "resources": resources,
            "verbs": verbs,
            "resourceNames": resource_names,
        })
    out.sort(key=lambda r: (r["apiGroups"], r["resources"], r["verbs"], r["resourceNames"]))
    return out

canonical_path, rendered_path = sys.argv[1], sys.argv[2]

canonical_rules = load_rules(canonical_path, "ClusterRole")
if canonical_rules is None:
    print(f"check-rbac-drift: no ClusterRole in {canonical_path}", file=sys.stderr)
    sys.exit(2)

# The chart renders a leader-election Role + the manager ClusterRole.
# We want the manager ClusterRole specifically.
rendered_rules = load_rules(rendered_path, "ClusterRole", name_match="manager")
if rendered_rules is None:
    print(f"check-rbac-drift: no manager ClusterRole in rendered chart", file=sys.stderr)
    sys.exit(2)

a = canon(canonical_rules)
b = canon(rendered_rules)

if a == b:
    print("check-rbac-drift: OK — canonical role.yaml matches rendered chart ClusterRole")
    sys.exit(0)

print("check-rbac-drift: DRIFT detected between role.yaml and chart ClusterRole", file=sys.stderr)
import difflib
da = json.dumps(a, indent=2).splitlines()
db = json.dumps(b, indent=2).splitlines()
sys.stderr.write("\n".join(difflib.unified_diff(
    da, db,
    fromfile="operator/config/rbac/role.yaml",
    tofile="charts/witwave-operator (rendered, manager ClusterRole)",
    lineterm="",
)))
sys.stderr.write("\n")
sys.exit(1)
PY
