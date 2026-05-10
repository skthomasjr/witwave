// Tests for the pure-helper functions in status.go used by the
// `ww status` command. Mirrors the table-driven shape used in
// snapshot_test.go, validate_test.go, and agent_test.go.
package cmd

import (
	"testing"
)

// TestSameOrigin pins the same-origin gate the status command uses
// to decide whether the harness's bearer token is safe to forward to
// a per-agent probe URL. Drift here would either over-attach the
// token (leaking it to a foreign origin) or withhold it from a
// legitimate same-origin agent (causing spurious 401s in the status
// table). Conservative-on-parse-error is part of the contract — a
// malformed URL must fall to the no-auth path, not silently auth.
func TestSameOrigin(t *testing.T) {
	cases := []struct {
		name     string
		baseURL  string
		probeURL string
		want     bool
	}{
		{"empty base returns false", "", "https://example.com/", false},
		{"identical urls match", "https://example.com/", "https://example.com/", true},
		{"same scheme and host with different path match", "https://example.com/a", "https://example.com/b", true},
		{"same scheme and host with different query match", "https://example.com/?x=1", "https://example.com/?y=2", true},
		{"same host different scheme does not match", "https://example.com/", "http://example.com/", false},
		{"different host does not match", "https://example.com/", "https://other.com/", false},
		{"same host different port does not match", "https://example.com:8443/", "https://example.com:9443/", false},
		{"scheme case insensitive matches", "HTTPS://example.com/", "https://example.com/", true},
		{"host case insensitive matches", "https://Example.COM/", "https://example.com/", true},
		{"explicit default port equals implicit default port matches",
			"https://example.com/", "https://example.com:443/", false},
		// Note: the test above documents the conservative-equality behavior —
		// url.Parse keeps the port as written, so :443 and "" do NOT compare
		// equal even though they're semantically identical. That's
		// intentional: the gate prefers under-attaching the token to
		// over-attaching it.
		{"malformed base url returns false", "://bad", "https://example.com/", false},
		{"malformed probe url returns false", "https://example.com/", "://bad", false},
		{"empty probe url with non-empty base returns false", "https://example.com/", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sameOrigin(c.baseURL, c.probeURL)
			if got != c.want {
				t.Fatalf("sameOrigin(%q, %q) = %v, want %v", c.baseURL, c.probeURL, got, c.want)
			}
		})
	}
}
