package agent

import (
	"runtime"
	"testing"
	"time"
)

// Tests for #1616: timeouts on git/gh shell-outs and credential env
// stripping. Anchored on commandWithTimeout + sanitizeShellEnv so we
// don't have to install a fake `git`/`gh` on $PATH.

func TestSanitizeShellEnvStripsCredentialKeys(t *testing.T) {
	in := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/Users/test",
		"GH_TOKEN=ghp_pretend",
		"GITHUB_TOKEN=another",
		"ANTHROPIC_API_KEY=sk-ant-fake",
		"OPENAI_API_KEY=sk-fake",
		"GIT_TOKEN=fake",
		"NOT_A_KEY",
		"BENIGN=keep",
	}
	got := sanitizeShellEnv(in)

	want := map[string]bool{
		"PATH=/usr/bin:/bin": true,
		"HOME=/Users/test":   true,
		"NOT_A_KEY":          true,
		"BENIGN=keep":        true,
	}
	if len(got) != len(want) {
		t.Fatalf("sanitizeShellEnv returned %d entries, want %d: %v", len(got), len(want), got)
	}
	for _, kv := range got {
		if !want[kv] {
			t.Errorf("unexpected entry survived sanitisation: %q", kv)
		}
	}
}

func TestCommandWithTimeoutKillsHangingChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep semantics differ on windows")
	}

	cmd, cancel := commandWithTimeout(100*time.Millisecond, "sleep", "30")
	defer cancel()

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil — child was not killed")
	}
	if elapsed > 5*time.Second {
		t.Errorf("child ran for %v, expected timeout near 100ms", elapsed)
	}
	if cmd.Env == nil {
		t.Error("cmd.Env should be non-nil (sanitised os.Environ) after commandWithTimeout")
	}
}
