package update

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Tests for #1616: timeouts on brew/go shell-outs and credential env
// stripping. Anchored on the exported helpers commandWithTimeout +
// sanitizeShellEnv so we don't have to install a fake `brew` on $PATH
// or stand up integration plumbing.

func TestSanitizeShellEnvStripsCredentialKeys(t *testing.T) {
	in := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/Users/test",
		"GH_TOKEN=ghp_pretendtoken",
		"GITHUB_TOKEN=anothertoken",
		"ANTHROPIC_API_KEY=sk-ant-fake",
		"OPENAI_API_KEY=sk-fake",
		"GIT_TOKEN=fake",
		"NOT_A_KEY", // malformed, no '=' — must pass through
		"BENIGN_VAR=keepme",
	}
	got := sanitizeShellEnv(in)

	want := map[string]bool{
		"PATH=/usr/bin:/bin": true,
		"HOME=/Users/test":   true,
		"NOT_A_KEY":          true,
		"BENIGN_VAR=keepme":  true,
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

func TestSanitizeShellEnvEmpty(t *testing.T) {
	if got := sanitizeShellEnv(nil); len(got) != 0 {
		t.Errorf("sanitizeShellEnv(nil) = %v, want empty", got)
	}
}

func TestCommandWithTimeoutKillsHangingChild(t *testing.T) {
	// `sleep` is universally available on POSIX; skip on Windows where
	// the path differs and ww isn't a supported install target anyway.
	if runtime.GOOS == "windows" {
		t.Skip("sleep semantics differ on windows; ww isn't installed via brew there")
	}

	// Use a very short timeout, then sleep much longer. The context
	// deadline must fire and SIGKILL the child before the sleep
	// completes.
	cmd, cancel := commandWithTimeout(context.Background(), 100*time.Millisecond, "sleep", "30")
	defer cancel()

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil — child was not killed")
	}
	// Either an *exec.ExitError (signalled) or the wrapped context
	// deadline error counts as success — what we care about is that
	// the process did not run for the full 30s.
	if elapsed > 5*time.Second {
		t.Errorf("child ran for %v, expected timeout near 100ms", elapsed)
	}

	// Sanity check: confirm the env was actually substituted
	// (cmd.Env != nil) so we know commandWithTimeout did its job
	// regardless of the kill path.
	if cmd.Env == nil {
		t.Error("cmd.Env should be non-nil after commandWithTimeout (set to sanitised os.Environ)")
	}

	// Stash unused symbol references so static-check doesn't gripe.
	var _ *exec.ExitError = (*exec.ExitError)(nil)
	_ = errors.New
}

// Tests for the binary-self-upgrade path added 2026-05-07. shellQuoteSingle
// must produce POSIX-safe output for any path string; dirWritable must
// distinguish writable from non-writable directories without leaving
// debris behind.

func TestShellQuoteSingle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "/usr/local/bin", "'/usr/local/bin'"},
		{"with spaces", "/Users/Test User/bin", "'/Users/Test User/bin'"},
		{"with single quote", "/Users/o'malley/bin", `'/Users/o'\''malley/bin'`},
		{"with dollar", "/opt/${PREFIX}/bin", "'/opt/${PREFIX}/bin'"},
		{"with backtick", "/opt/`evil`/bin", "'/opt/`evil`/bin'"},
		{"empty", "", "''"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellQuoteSingle(tc.in); got != tc.want {
				t.Errorf("shellQuoteSingle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDirWritableHappyPath(t *testing.T) {
	dir := t.TempDir()
	if err := dirWritable(dir); err != nil {
		t.Errorf("dirWritable(%q) = %v, want nil for a fresh TempDir", dir, err)
	}
	// Confirm no probe debris remains — t.TempDir is auto-cleaned but
	// dirWritable's contract is that it cleans up after itself even
	// outside test scopes.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(%q): %v", dir, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dirWritable left debris in dir: %v", names)
	}
}

func TestDirWritableDeniedNonExistent(t *testing.T) {
	// A directory that doesn't exist must surface as an error so the
	// caller suggests sudo / move-to-owned-dir.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if err := dirWritable(missing); err == nil {
		t.Errorf("dirWritable(%q) = nil, expected an error for nonexistent dir", missing)
	}
}

func TestDirWritableDeniedReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only directory permissions are different on Windows; ww auto-upgrade not supported there yet")
	}
	if os.Geteuid() == 0 {
		t.Skip("cannot meaningfully test denied write permission as root")
	}
	dir := t.TempDir()
	// 0500 = readable + executable but not writable.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("os.Chmod: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }() // restore so TempDir cleanup works

	if err := dirWritable(dir); err == nil {
		t.Errorf("dirWritable(%q) = nil, expected permission error on 0500-mode dir", dir)
	}
}
