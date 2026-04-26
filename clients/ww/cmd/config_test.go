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
