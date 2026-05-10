// Tests for the pure-helper functions in status.go that govern the
// `ww status` multi-agent fan-out probe. Mirrors the table-driven
// shape used in validate_test.go and agent_test.go; the HTTP path is
// exercised end-to-end against a real harness, here we just pin the
// same-origin auth-forwarding gate so a future tweak (subdomain
// match, scheme tolerance, etc.) is a deliberate decision rather
// than silently leaking the conversations bearer to a foreign host.
package cmd

import "testing"

// TestSameOrigin pins the conservative same-origin check that gates
// whether `ww status` forwards the conversations bearer to a probed
// agent URL. The contract: scheme+host (incl. port) must match
// case-insensitively; any parse error returns false (caller withholds
// auth). Critical-path security — getting it wrong leaks the bearer
// across origins.
func TestSameOrigin(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		probe   string
		want    bool
	}{
		// Negative paths
		{"empty base returns false", "", "https://example.com", false},
		{"empty probe returns false (no scheme/host to compare)", "https://example.com", "", false},
		{"both empty returns false", "", "", false},
		{"unparseable base returns false", "://bad", "https://example.com", false},
		{"different host returns false", "https://example.com", "https://other.com", false},
		{"different scheme returns false (https vs http)", "https://example.com", "http://example.com", false},
		{"different port returns false", "https://example.com:8443", "https://example.com:9443", false},
		{"port vs no-port returns false", "https://example.com:8443", "https://example.com", false},
		{"subdomain returns false (foo.example.com vs example.com)", "https://example.com", "https://api.example.com", false},
		{"path-only probe returns false (no scheme)", "https://example.com", "/some/path", false},

		// Positive paths
		{"identical scheme+host matches", "https://example.com", "https://example.com", true},
		{"matching scheme+host with different paths", "https://example.com/api", "https://example.com/probe", true},
		{"scheme is case-insensitive (HTTPS vs https)", "HTTPS://example.com", "https://example.com", true},
		{"host is case-insensitive (Example.COM vs example.com)", "https://Example.COM", "https://example.com", true},
		{"matching port matches", "https://example.com:8443", "https://example.com:8443", true},
		{"http origin matches itself", "http://localhost:8000", "http://localhost:8000/v1/agents", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := sameOrigin(tc.base, tc.probe)
			if got != tc.want {
				t.Errorf("sameOrigin(%q, %q) = %v, want %v", tc.base, tc.probe, got, tc.want)
			}
		})
	}
}
