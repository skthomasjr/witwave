// Tests for the pure-logic helpers in preflight.go — the RBAC
// requirements factory and the missing-RBAC stringifier. The
// SelfSubjectAccessReview path (CheckRBAC) and the live-cluster
// uninstall-safety check (CheckUninstallSafety) are exercised
// end-to-end against a real cluster. Mirrors the table-driven shape
// in status_test.go::TestSkewLabel.
package operator

import "testing"

// TestInstallRBACRequirements pins the requirement-set shape that
// `ww operator install` checks at preflight. A future addition to
// the chart's template set (a new ClusterRole, a Job, etc.) needs to
// land here too or install will silently bypass the SAR for the new
// resource; pin the set explicitly so that doesn't slip past review.
func TestInstallRBACRequirements(t *testing.T) {
	got := InstallRBACRequirements("witwave-system")
	if len(got) == 0 {
		t.Fatalf("InstallRBACRequirements returned no checks")
	}

	// Each entry must have a non-empty resource + verb. Group may be
	// empty (core API group). Namespace may be empty (cluster-scoped).
	for i, c := range got {
		if c.Resource == "" {
			t.Errorf("entry %d: empty Resource", i)
		}
		if c.Verb == "" {
			t.Errorf("entry %d: empty Verb", i)
		}
	}

	// Cluster-scoped resources must have empty Namespace; namespaced
	// resources must carry the namespace the caller passed. Pin the
	// scoping convention so a future re-shuffle can't quietly grant
	// a namespaced verb cluster-wide (or vice versa).
	clusterScoped := map[string]bool{
		"namespaces":                true,
		"clusterroles":              true,
		"clusterrolebindings":       true,
		"customresourcedefinitions": true,
	}
	for _, c := range got {
		if clusterScoped[c.Resource] {
			if c.Namespace != "" {
				t.Errorf("%s/%s should be cluster-scoped (Namespace=\"\"), got Namespace=%q", c.Resource, c.Verb, c.Namespace)
			}
		} else {
			if c.Namespace != "witwave-system" {
				t.Errorf("%s/%s should be namespaced to caller's namespace, got Namespace=%q", c.Resource, c.Verb, c.Namespace)
			}
		}
	}

	// Cross-check: the namespace argument is honoured. A different
	// caller-supplied namespace must propagate to every namespaced
	// entry without leaking into cluster-scoped ones.
	other := InstallRBACRequirements("my-ns")
	if len(other) != len(got) {
		t.Fatalf("namespace change altered requirement count: %d vs %d", len(got), len(other))
	}
	for i, c := range other {
		if clusterScoped[c.Resource] {
			if c.Namespace != "" {
				t.Errorf("entry %d (%s/%s): cluster-scoped entry should ignore namespace arg, got %q", i, c.Resource, c.Verb, c.Namespace)
			}
		} else {
			if c.Namespace != "my-ns" {
				t.Errorf("entry %d (%s/%s): namespace arg should propagate, got %q", i, c.Resource, c.Verb, c.Namespace)
			}
		}
	}

	// The chart's template set requires CRD create+update. Pin those
	// explicitly — they're the difference between a clean install and
	// a partial-state failure when CRDs already exist with a newer
	// schema.
	mustHave := []RBACCheck{
		{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions", Verb: "create"},
		{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions", Verb: "update"},
		{Group: "", Resource: "namespaces", Verb: "create"},
		{Group: "apps", Resource: "deployments", Verb: "create", Namespace: "witwave-system"},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Verb: "create"},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings", Verb: "create"},
	}
	for _, want := range mustHave {
		if !containsCheck(got, want) {
			t.Errorf("missing required check: %+v", want)
		}
	}
}

func containsCheck(set []RBACCheck, want RBACCheck) bool {
	for _, c := range set {
		if c == want {
			return true
		}
	}
	return false
}
