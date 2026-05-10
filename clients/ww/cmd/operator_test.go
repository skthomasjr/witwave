// Tests for the pure-helper functions in operator.go that the
// `ww operator status` table-rendering pipeline composes over each
// kubeconfig context. Mirrors the table-driven shape used in
// snapshot_test.go and validate_test.go; the install / upgrade /
// uninstall paths are exercised against a real cluster, so this file
// just pins the helper contracts so a future rename or precedence
// flip in cluster-vs-server display fails loudly instead of silently
// changing what users see in the operator-status table.
package cmd

import "testing"

// TestCmpDisplay pins the cluster-nickname-vs-server-URL precedence
// helper used by the `ww operator status` row renderer. Contract:
// cluster nickname wins when present; server URL is the fallback
// when cluster is the empty string. Pin the boundary cases so a
// future tweak (say, "always show server" or "show both") updates
// the helper and these expectations together.
func TestCmpDisplay(t *testing.T) {
	cases := []struct {
		name    string
		cluster string
		server  string
		want    string
	}{
		{"both empty returns empty", "", "", ""},
		{"cluster set, server empty returns cluster", "prod-east", "", "prod-east"},
		{"cluster empty, server set returns server", "", "https://1.2.3.4", "https://1.2.3.4"},
		{"both set returns cluster (cluster precedence)", "prod-east", "https://1.2.3.4", "prod-east"},
		{"cluster whitespace is non-empty so wins", " ", "https://1.2.3.4", " "},
		{"long ARN-style server returned as-is when cluster empty", "", "arn:aws:eks:us-east-1:123456789012:cluster/x", "arn:aws:eks:us-east-1:123456789012:cluster/x"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := cmpDisplay(tc.cluster, tc.server)
			if got != tc.want {
				t.Errorf("cmpDisplay(%q, %q) = %q, want %q", tc.cluster, tc.server, got, tc.want)
			}
		})
	}
}
