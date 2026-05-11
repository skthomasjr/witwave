// Tests for the pure-logic helpers in release.go — the Helm-release
// revision-label parser, the CRD-not-found error sniffer, and the
// unstructured deep-map slice walker. The k8s-API paths in this file
// (LookupRelease, FindReleaseClusterWide, ListOperatorPods, InspectCRDs,
// CountCRs) are exercised end-to-end against a real cluster; here we
// cover the branches amenable to table-driven tests. Mirrors the
// table-driven shape in status_test.go::TestSkewLabel.
package operator

import (
	"errors"
	"testing"
)

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

// TestIsCRDNotFound pins the substring-match contract that CountCRs
// relies on to distinguish "CRD absent" (report count=0, no error)
// from any other dynamic-client error (propagate as a real failure).
// The phrases come from the discovery client and aren't always
// wrapped with apierrors.IsNotFound, so the matcher is the safety
// net before the error escapes.
func TestIsCRDNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"unrelated error", errors.New("connection refused"), false},
		{"empty message", errors.New(""), false},
		{"no matches for kind phrase", errors.New(`no matches for kind "WitwaveAgent" in version "witwave.ai/v1alpha1"`), true},
		{"could not find the requested resource phrase", errors.New("could not find the requested resource (get witwaveagents.witwave.ai)"), true},
		{"phrase embedded in wrapped error", errors.New("list WitwaveAgent: no matches for kind"), true},
		{"phrase mid-message", errors.New("prefix: could not find the requested resource: suffix"), true},
		{"case-sensitive — uppercased miss", errors.New("NO MATCHES FOR KIND"), false},
		{"partial phrase miss", errors.New("no matches"), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := isCRDNotFound(tc.err)
			if got != tc.want {
				t.Errorf("isCRDNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestUnstructuredSlice pins the deep-map walker behaviour InspectCRDs
// uses to extract spec.versions[] from an unstructured CRD. The
// contract has three failure modes (empty path → error, broken-path →
// (nil,false,nil), non-slice leaf → (nil,false,nil)) plus the happy
// path. Pin each so a future map-shape change in the operator schema
// fails the test rather than silently returning an empty versions
// list.
func TestUnstructuredSlice(t *testing.T) {
	good := map[string]interface{}{
		"spec": map[string]interface{}{
			"versions": []interface{}{
				map[string]interface{}{"name": "v1alpha1"},
				map[string]interface{}{"name": "v1"},
			},
			"scope": "Namespaced",
		},
	}
	cases := []struct {
		name      string
		obj       map[string]interface{}
		path      []string
		wantLen   int // -1 means expect "not found / wrong type" (slice == nil, found == false)
		wantFound bool
		wantErr   bool
	}{
		{"empty path returns error", good, nil, -1, false, true},
		{"happy path returns slice", good, []string{"spec", "versions"}, 2, true, false},
		{"missing top-level key returns not-found", good, []string{"status"}, -1, false, false},
		{"missing intermediate key returns not-found", good, []string{"spec", "missing", "versions"}, -1, false, false},
		{"non-map intermediate returns not-found", good, []string{"spec", "scope", "name"}, -1, false, false},
		{"non-slice leaf returns not-found", good, []string{"spec", "scope"}, -1, false, false},
		{"empty map + happy-shape path returns not-found", map[string]interface{}{}, []string{"spec", "versions"}, -1, false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, found, err := unstructuredSlice(tc.obj, tc.path...)
			if (err != nil) != tc.wantErr {
				t.Errorf("err: want=%v, got=%v", tc.wantErr, err)
			}
			if found != tc.wantFound {
				t.Errorf("found: want=%v, got=%v", tc.wantFound, found)
			}
			if tc.wantLen >= 0 {
				if len(got) != tc.wantLen {
					t.Errorf("len(slice): want=%d, got=%d (slice=%v)", tc.wantLen, len(got), got)
				}
			} else {
				if got != nil {
					t.Errorf("slice should be nil for not-found/error case, got=%v", got)
				}
			}
		})
	}
}
