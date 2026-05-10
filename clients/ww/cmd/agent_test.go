// Tests for the pure-helper logic in agent.go — the auth-mode selection
// path that every `ww agent git add` invocation runs before handing off
// to the agent.GitAdd k8s plumbing. Mirrors the table-driven pure-helper
// shape used in internal/output/output_test.go and
// internal/conversation/client_test.go; the HTTP / k8s paths are
// exercised end-to-end against a real cluster.
package cmd

import (
	"testing"

	"github.com/witwave-ai/witwave/clients/ww/internal/agent"
)

// TestChooseAuthMode covers the auth-mode selection switch that maps the
// three mutually-exclusive `ww agent git add --auth-*` flags onto a
// single GitAuthMode for downstream consumers. The function is documented
// as accepting "exactly one set" (mutual-exclusivity is enforced at
// validation time by assertOneAuthMode) but the switch is also defined
// over each individual case in priority order — pin that order here so
// a future refactor can't silently flip secret vs. fromGH precedence.
func TestChooseAuthMode(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		fromGH bool
		envVar string
		want   agent.GitAuthMode
	}{
		{"all empty resolves to none", "", false, "", agent.GitAuthNone},
		{"secret set resolves to existing-secret", "my-secret", false, "", agent.GitAuthExistingSecret},
		{"fromGH set resolves to from-gh", "", true, "", agent.GitAuthFromGH},
		{"env set resolves to from-env", "", false, "GH_TOKEN", agent.GitAuthFromEnv},
		// Precedence cases — assertOneAuthMode rejects multi-set inputs
		// before we get here in real callers, but pin the switch order
		// so a future pruning of the validator can't quietly downgrade
		// what chooseAuthMode picks for a misconfigured caller.
		{"secret beats fromGH when both set", "my-secret", true, "", agent.GitAuthExistingSecret},
		{"secret beats env when both set", "my-secret", false, "GH_TOKEN", agent.GitAuthExistingSecret},
		{"fromGH beats env when both set", "", true, "GH_TOKEN", agent.GitAuthFromGH},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := chooseAuthMode(tc.secret, tc.fromGH, tc.envVar)
			if got != tc.want {
				t.Errorf("chooseAuthMode(secret=%q, fromGH=%v, env=%q) = %v, want %v",
					tc.secret, tc.fromGH, tc.envVar, got, tc.want)
			}
		})
	}
}

// TestAssertOneAuthMode covers the mutual-exclusivity validator that
// runs before chooseAuthMode in the `ww agent git add` flow. The
// function returns an error when more than one of (ExistingSecret,
// Mode==GitAuthFromGH, EnvVar) is set; zero or one set is allowed
// (zero resolves to GitAuthNone for public repos). Pin both the
// allow-list and the reject-list so a future flag addition can't
// silently slip past the validator.
func TestAssertOneAuthMode(t *testing.T) {
	cases := []struct {
		name    string
		auth    agent.GitAuthResolver
		wantErr bool
	}{
		{"empty resolver allowed (public repo)", agent.GitAuthResolver{}, false},
		{"existing-secret only allowed", agent.GitAuthResolver{ExistingSecret: "my-secret"}, false},
		{"from-gh only allowed", agent.GitAuthResolver{Mode: agent.GitAuthFromGH}, false},
		{"from-env only allowed", agent.GitAuthResolver{EnvVar: "GH_TOKEN"}, false},
		{"existing-secret + from-gh rejected", agent.GitAuthResolver{ExistingSecret: "s", Mode: agent.GitAuthFromGH}, true},
		{"existing-secret + from-env rejected", agent.GitAuthResolver{ExistingSecret: "s", EnvVar: "GH_TOKEN"}, true},
		{"from-gh + from-env rejected", agent.GitAuthResolver{Mode: agent.GitAuthFromGH, EnvVar: "GH_TOKEN"}, true},
		{"all three rejected", agent.GitAuthResolver{ExistingSecret: "s", Mode: agent.GitAuthFromGH, EnvVar: "GH_TOKEN"}, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := assertOneAuthMode(tc.auth)
			if tc.wantErr && err == nil {
				t.Errorf("assertOneAuthMode(%+v): want error, got nil", tc.auth)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("assertOneAuthMode(%+v): want nil, got %v", tc.auth, err)
			}
		})
	}
}
