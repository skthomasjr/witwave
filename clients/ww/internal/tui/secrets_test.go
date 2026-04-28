// Coverage for resolveTUISecrets — the four auth-mode shapes the
// create modal can produce: existing-secret, inline pairs, env-lift,
// and the empty-pairs no-auth fallback (#1742).

package tui

import (
	"strings"
	"testing"

	"github.com/witwave-ai/witwave/clients/ww/internal/agent"
)

func TestResolveTUISecrets_ExistingSecretWinsOverPairs(t *testing.T) {
	got, err := resolveTUISecrets("claude", "my-secret",
		[]secretPair{{Key: "KEY", Value: "VALUE"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Mode != agent.BackendAuthExistingSecret {
		t.Errorf("mode: got %v want %v", got.Mode, agent.BackendAuthExistingSecret)
	}
	if got.ExistingSecret != "my-secret" {
		t.Errorf("ExistingSecret: got %q want my-secret", got.ExistingSecret)
	}
	if got.Backend != "claude" {
		t.Errorf("Backend: got %q want claude", got.Backend)
	}
}

func TestResolveTUISecrets_NoPairsYieldsNone(t *testing.T) {
	got, err := resolveTUISecrets("claude", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Mode != agent.BackendAuthNone {
		t.Errorf("mode: got %v want %v", got.Mode, agent.BackendAuthNone)
	}
}

func TestResolveTUISecrets_InlinePairsRoundTrip(t *testing.T) {
	got, err := resolveTUISecrets("claude", "",
		[]secretPair{
			{Key: "ANTHROPIC_API_KEY", Value: "sk-test"},
			{Key: "AWS_REGION", Value: "us-west-2"},
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Mode != agent.BackendAuthInline {
		t.Errorf("mode: got %v want %v", got.Mode, agent.BackendAuthInline)
	}
	if got.Inline["ANTHROPIC_API_KEY"] != "sk-test" {
		t.Errorf("ANTHROPIC_API_KEY: got %q", got.Inline["ANTHROPIC_API_KEY"])
	}
	if got.Inline["AWS_REGION"] != "us-west-2" {
		t.Errorf("AWS_REGION: got %q", got.Inline["AWS_REGION"])
	}
}

func TestResolveTUISecrets_EmptyKeyDropped(t *testing.T) {
	got, err := resolveTUISecrets("claude", "",
		[]secretPair{
			{Key: "", Value: "ignored"},
			{Key: "KEEP", Value: "v"},
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Mode != agent.BackendAuthInline {
		t.Errorf("expected inline mode, got %v", got.Mode)
	}
	if _, ok := got.Inline[""]; ok {
		t.Errorf("empty key must be dropped")
	}
	if got.Inline["KEEP"] != "v" {
		t.Errorf("KEEP missing: %v", got.Inline)
	}
}

func TestResolveTUISecrets_EmptyValueOnNonEmptyKeyRejects(t *testing.T) {
	_, err := resolveTUISecrets("claude", "",
		[]secretPair{{Key: "KEY", Value: ""}})
	if err == nil {
		t.Fatal("expected error for empty value on non-empty key")
	}
	if !strings.Contains(err.Error(), "empty value") {
		t.Errorf("expected message about empty value, got %v", err)
	}
}

func TestResolveTUISecrets_BareDollarRejects(t *testing.T) {
	_, err := resolveTUISecrets("claude", "",
		[]secretPair{{Key: "KEY", Value: "$"}})
	if err == nil {
		t.Fatal("expected error for bare $ value")
	}
	if !strings.Contains(err.Error(), "bare `$`") {
		t.Errorf("expected message about bare $, got %v", err)
	}
}

func TestResolveTUISecrets_EnvLiftSuccess(t *testing.T) {
	t.Setenv("WW_TUI_TEST_KEY", "lifted-value")
	got, err := resolveTUISecrets("claude", "",
		[]secretPair{{Key: "KEY", Value: "$WW_TUI_TEST_KEY"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Inline["KEY"] != "lifted-value" {
		t.Errorf("env-lift failed: %v", got.Inline)
	}
}

func TestResolveTUISecrets_EnvLiftMissingRejects(t *testing.T) {
	// Make sure the var is unset.
	t.Setenv("WW_TUI_TEST_KEY_MISSING", "")
	_, err := resolveTUISecrets("claude", "",
		[]secretPair{{Key: "KEY", Value: "$WW_TUI_TEST_KEY_MISSING"}})
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
	if !strings.Contains(err.Error(), "WW_TUI_TEST_KEY_MISSING") {
		t.Errorf("expected message naming the missing var, got %v", err)
	}
}

func TestResolveTUISecrets_DuplicateKeyRejects(t *testing.T) {
	_, err := resolveTUISecrets("claude", "",
		[]secretPair{
			{Key: "KEY", Value: "first"},
			{Key: "KEY", Value: "second"},
		})
	if err == nil {
		t.Fatal("expected error for duplicate key")
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("expected message about duplicate, got %v", err)
	}
}
