package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigGet covers the redaction behaviour added for #1646: secret keys
// must never echo their value to stdout, while non-secret keys must continue
// to print verbatim so shell pipelines keep working.
func TestConfigGet(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	const secret = "super-secret-token-value"
	const plain = "auto"
	body := "[update]\n" +
		"mode = \"" + plain + "\"\n" +
		"\n" +
		"[profile.default]\n" +
		"token = \"" + secret + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write seed config: %v", err)
	}

	t.Run("secret key is redacted on stdout with stderr note", func(t *testing.T) {
		cmd := newConfigGetCmd()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"profile.default.token"})
		// Use --config via direct flag binding: newConfigGetCmd doesn't
		// own the flag (it lives on the root), so push the path through
		// WW_CONFIG which OpenWriter honours when no --config is set.
		t.Setenv("WW_CONFIG", cfgPath)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		gotOut := strings.TrimSpace(stdout.String())
		if gotOut != "<redacted>" {
			t.Fatalf("stdout = %q, want %q", gotOut, "<redacted>")
		}
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("stdout leaked the secret value: %q", stdout.String())
		}
		if !strings.Contains(stderr.String(), "Secret value redacted") {
			t.Fatalf("stderr missing redaction note: %q", stderr.String())
		}
		if strings.Contains(stderr.String(), secret) {
			t.Fatalf("stderr leaked the secret value: %q", stderr.String())
		}
	})

	t.Run("non-secret key prints value verbatim", func(t *testing.T) {
		cmd := newConfigGetCmd()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"update.mode"})
		t.Setenv("WW_CONFIG", cfgPath)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		gotOut := strings.TrimSpace(stdout.String())
		if gotOut != plain {
			t.Fatalf("stdout = %q, want %q", gotOut, plain)
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr should be empty for non-secret get, got %q", stderr.String())
		}
	})
}

// TestIsSecretKey pins the suffix-list / equality set that the
// `ww config get` redaction path consults to decide whether to
// replace a value with "<redacted>" before writing to stdout.
// TestConfigGet (above) covers the integration path with a small
// fixture set ("token" + "mode"); this test pins each entry in
// the static rule list so a future drop of one suffix (or
// case-sensitivity drift via the ToLower normalisation) trips a
// regression rather than silently widening the leak surface.
func TestIsSecretKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// Exact-match secret keys (case-insensitive).
		{"exact token", "token", true},
		{"exact password", "password", true},
		{"exact token upper", "TOKEN", true},
		{"exact password mixed", "PassWord", true},
		// Suffix-match secret keys (lowercased before comparison).
		{"suffix .token", "profile.default.token", true},
		{"suffix .run_token", "trigger.foo.run_token", true},
		{"suffix .password", "registry.password", true},
		{"suffix .bearer", "auth.bearer", true},
		{"suffix .secret", "kube.secret", true},
		{"suffix .token mixed case", "Profile.Default.Token", true},
		// Non-secret keys must remain visible.
		{"non-secret update.mode", "update.mode", false},
		{"non-secret bare name", "name", false},
		{"non-secret empty", "", false},
		{"non-secret tokenish suffix", "tokenizer", false},
		{"non-secret passwordish suffix", "passwordless", false},
		{"non-secret partial suffix .toke", "auth.toke", false},
		// Defensive: keys that contain a secret-marker mid-string but
		// neither equal nor end-with one stay visible (the rule list
		// is anchored on equality or trailing match only).
		{"token mid-string only", "token.id", false},
		{"password mid-string only", "password.note", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := isSecretKey(tc.in)
			if got != tc.want {
				t.Errorf("isSecretKey(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
