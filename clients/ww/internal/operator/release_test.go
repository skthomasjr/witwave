// Tests for the pure-logic helpers in release.go — the Helm-release
// revision-label parser, the CRD-not-found error sniffer, and the
// unstructured deep-map slice walker. The k8s-API paths in this file
// (LookupRelease, FindReleaseClusterWide, ListOperatorPods, InspectCRDs,
// CountCRs) are exercised end-to-end against a real cluster; here we
// cover the branches amenable to table-driven tests. Mirrors the
// table-driven shape in status_test.go::TestSkewLabel.
package operator

import "testing"

// TestParseRevisionLabel pins the int-ordering contract: empty + any
// non-decimal value resolves to -1 so a stray Secret with a malformed
// "version" label can never beat a real release in LookupRelease's
// max-revision loop.
func TestParseRevisionLabel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty label", "", -1},
		{"zero", "0", 0},
		{"single digit", "1", 1},
		{"two digits", "10", 10},
		{"three digits", "123", 123},
		{"alpha rejected", "abc", -1},
		{"alphanumeric rejected", "v1", -1},
		{"leading space rejected", " 5", -1},
		{"trailing space rejected", "5 ", -1},
		{"negative parsed", "-1", -1},
		{"float rejected", "1.5", -1},
		{"hex rejected", "0x10", -1},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := parseRevisionLabel(tc.in)
			if got != tc.want {
				t.Errorf("parseRevisionLabel(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
