// Tests for the pure-helper functions in backend_spec.go that the
// `ww agent create --backend` flag handler composes over user
// input. Mirrors the table-driven shape used in
// internal/agent/scaffold_test.go and cmd/snapshot_test.go; the
// CR-construction path (Create) is exercised against a real cluster,
// so this file just pins the user-flag-parsing contract so a future
// rename in the `name:type` parser (or a typo in the duplicate
// detection) fails loudly rather than silently misrouting traffic
// between same-named backends.
package agent

import (
	"strings"
	"testing"
)

// TestParseBackendSpecs pins the `--backend` flag parser. Contract:
//   - empty input → single default (echo:echo at BackendPort(0))
//   - "type" shape → name=type, port=BackendPort(i)
//   - "name:type" shape → distinct name and type, both validated
//   - empty entry → error
//   - missing-half ":type" or "name:" → error
//   - invalid name (DNS-1123) → error wrapping the validator
//   - unknown type → error mentioning KnownBackends()
//   - duplicate name → error mentioning the duplicate
//
// Drift here would re-shape how every `ww agent create --backend ...`
// invocation maps to a CR. Pin every error branch's substring so
// CLI users keep getting the targeted message.
func TestParseBackendSpecs(t *testing.T) {
	t.Run("empty input returns single default echo backend", func(t *testing.T) {
		got, err := ParseBackendSpecs(nil)
		if err != nil {
			t.Fatalf("ParseBackendSpecs(nil) err = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("ParseBackendSpecs(nil) len = %d, want 1", len(got))
		}
		if got[0].Name != DefaultBackend {
			t.Errorf("default name = %q, want %q", got[0].Name, DefaultBackend)
		}
		if got[0].Type != DefaultBackend {
			t.Errorf("default type = %q, want %q", got[0].Type, DefaultBackend)
		}
		if got[0].Port != BackendPort(0) {
			t.Errorf("default port = %d, want %d", got[0].Port, BackendPort(0))
		}
	})

	t.Run("empty slice (not nil) also returns default", func(t *testing.T) {
		got, err := ParseBackendSpecs([]string{})
		if err != nil {
			t.Fatalf("ParseBackendSpecs([]) err = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
	})

	t.Run("single type shape implies name=type", func(t *testing.T) {
		got, err := ParseBackendSpecs([]string{"claude"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 1 || got[0].Name != "claude" || got[0].Type != "claude" {
			t.Errorf("got = %+v, want claude:claude", got)
		}
		if got[0].Port != BackendPort(0) {
			t.Errorf("port[0] = %d, want %d", got[0].Port, BackendPort(0))
		}
	})

	t.Run("explicit name:type shape splits correctly", func(t *testing.T) {
		got, err := ParseBackendSpecs([]string{"primary:claude"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got[0].Name != "primary" || got[0].Type != "claude" {
			t.Errorf("got = %+v, want primary/claude", got)
		}
	})

	t.Run("two-echo pattern: distinct names, same type, sequential ports", func(t *testing.T) {
		got, err := ParseBackendSpecs([]string{"echo-1:echo", "echo-2:echo"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0].Name != "echo-1" || got[0].Type != "echo" || got[0].Port != BackendPort(0) {
			t.Errorf("got[0] = %+v", got[0])
		}
		if got[1].Name != "echo-2" || got[1].Type != "echo" || got[1].Port != BackendPort(1) {
			t.Errorf("got[1] = %+v", got[1])
		}
	})

	t.Run("surrounding whitespace trimmed", func(t *testing.T) {
		got, err := ParseBackendSpecs([]string{" claude "})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got[0].Name != "claude" {
			t.Errorf("got[0].Name = %q, want %q", got[0].Name, "claude")
		}
	})

	t.Run("whitespace inside name:type split also trimmed", func(t *testing.T) {
		got, err := ParseBackendSpecs([]string{"primary : claude"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got[0].Name != "primary" || got[0].Type != "claude" {
			t.Errorf("got = %+v", got[0])
		}
	})

	errCases := []struct {
		name string
		raw  []string
		want string // err.Error() substring
	}{
		{"empty entry rejected", []string{""}, "empty value"},
		{"whitespace-only entry rejected", []string{"   "}, "empty value"},
		{"colon-only is empty name+empty type", []string{":"}, "both name and type required"},
		{"leading colon (empty name) rejected", []string{":claude"}, "both name and type required"},
		{"trailing colon (empty type) rejected", []string{"primary:"}, "both name and type required"},
		{"invalid DNS-1123 name rejected", []string{"Bad_Name:echo"}, "name"},
		{"unknown type rejected", []string{"primary:wubwub"}, "unknown type"},
		{"unknown type mentions valid types", []string{"x:wubwub"}, "echo"}, // KnownBackends() listing
		{"duplicate name across entries rejected", []string{"echo-1:echo", "echo-1:claude"}, "duplicate name"},
	}
	for _, tc := range errCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBackendSpecs(tc.raw)
			if err == nil {
				t.Fatalf("ParseBackendSpecs(%v) err = nil, want non-nil", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ParseBackendSpecs(%v) err = %q, want substring %q", tc.raw, err.Error(), tc.want)
			}
		})
	}
}

// TestPrimaryBackend pins the contract that scaffold's backend.yaml
// template depends on: the first BackendSpec in the parsed list is
// the conventional default routing target. The helper is a one-line
// index — but pinning the "no panic on >=1 element" boundary keeps
// a future refactor from accidentally introducing a slice-aware
// search (which could change which spec wins when names sort
// alphabetically differently from insertion order).
func TestPrimaryBackend(t *testing.T) {
	t.Run("first element returned for single-element list", func(t *testing.T) {
		specs := []BackendSpec{{Name: "alpha", Type: "echo", Port: 8001}}
		got := PrimaryBackend(specs)
		if got.Name != "alpha" {
			t.Errorf("PrimaryBackend(single) = %+v, want alpha", got)
		}
	})

	t.Run("insertion-order first wins over lexicographically smaller later entries", func(t *testing.T) {
		// Insertion order: beta, alpha. Insertion-order semantics
		// means beta wins (NOT alpha). If a future refactor sorts
		// the slice before picking, this test fails.
		specs := []BackendSpec{
			{Name: "beta", Type: "echo", Port: 8001},
			{Name: "alpha", Type: "echo", Port: 8002},
		}
		got := PrimaryBackend(specs)
		if got.Name != "beta" {
			t.Errorf("PrimaryBackend = %+v, want beta (insertion order)", got)
		}
	})

	t.Run("CredentialSecret/Storage/Env on first spec preserved", func(t *testing.T) {
		specs := []BackendSpec{
			{Name: "p", Type: "claude", CredentialSecret: "p-secret",
				Env: map[string]string{"LOG_LEVEL": "debug"}},
		}
		got := PrimaryBackend(specs)
		if got.CredentialSecret != "p-secret" {
			t.Errorf("CredentialSecret = %q, want %q", got.CredentialSecret, "p-secret")
		}
		if got.Env["LOG_LEVEL"] != "debug" {
			t.Errorf("Env[LOG_LEVEL] = %q, want %q", got.Env["LOG_LEVEL"], "debug")
		}
	})
}
