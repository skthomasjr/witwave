package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestWriter_SetThenReadBack(t *testing.T) {
	// Use a tempdir as $HOME so `OpenWriter` with no --config/WW_CONFIG
	// resolves to $HOME/.witwave/config.toml without touching the real
	// user home directory.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "") // force fall-through

	w, err := OpenWriter("", os.Getenv)
	if err != nil {
		t.Fatalf("OpenWriter: %v", err)
	}
	wantPath := filepath.Join(home, ".witwave", "config.toml")
	if w.Path() != wantPath {
		t.Errorf("Path = %q, want %q", w.Path(), wantPath)
	}
	if w.Existed() {
		t.Errorf("Existed = true; tempdir should be fresh")
	}

	if err := w.Set("update.mode", "auto"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := w.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File now exists with 0600 perms and contains the value.
	st, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat written file: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 0600", perm)
	}
	raw, _ := os.ReadFile(wantPath)
	// Viper may emit TOML with either single or double quotes.
	if !strings.Contains(string(raw), `mode = "auto"`) && !strings.Contains(string(raw), `mode = 'auto'`) {
		t.Errorf("file missing key: %s", raw)
	}

	// Round-trip: a fresh Load picks up the value.
	r, err := Load("", FlagOverrides{}, os.Getenv)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if r.Update.Mode != "auto" {
		t.Errorf("Update.Mode after round-trip = %q, want auto", r.Update.Mode)
	}
	if r.LoadedFrom != wantPath {
		t.Errorf("LoadedFrom = %q, want %q", r.LoadedFrom, wantPath)
	}
}

func TestWriter_UnknownKeyRejected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	w, _ := OpenWriter("", os.Getenv)
	err := w.Set("update.mod", "auto") // typo — "mod" instead of "mode"
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("error doesn't mention unknown key: %v", err)
	}
	// No file was written because Save wasn't called.
	if _, err := os.Stat(filepath.Join(home, ".witwave", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("file should not exist after failed Set")
	}
}

func TestWriter_InvalidValueRejected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	w, _ := OpenWriter("", os.Getenv)
	cases := []struct {
		key, val string
	}{
		{"update.mode", "wubwub"},
		{"update.channel", "nightly"},
		{"update.interval", "tomorrow"},
		{"profile.default.base_url", "not-a-url"},
		{"profile.default.base_url", "ftp://wrong-scheme"},
		{"profile.default.timeout", "-1s"},
	}
	for _, tc := range cases {
		if err := w.Set(tc.key, tc.val); err == nil {
			t.Errorf("Set(%s=%s) should fail", tc.key, tc.val)
		}
	}
}

func TestWriter_SaveWithoutChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	w, _ := OpenWriter("", os.Getenv)
	if err := w.Save(); err == nil {
		t.Error("Save without Set should error")
	}
}

func TestWriter_PreservesOtherKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	// Pre-seed the file with a profile and an unrelated update key.
	cfgDir := filepath.Join(home, ".witwave")
	_ = os.MkdirAll(cfgDir, 0o700)
	seed := `[profile.default]
base_url = "http://seeded"
token = "original-token"

[update]
channel = "beta"
`
	p := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(p, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	w, err := OpenWriter("", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Existed() {
		t.Error("Existed should be true for seeded file")
	}
	if err := w.Set("update.mode", "prompt"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := w.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload and verify all three keys coexist.
	r, err := Load("", FlagOverrides{}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "http://seeded" {
		t.Errorf("lost base_url: %q", r.BaseURL)
	}
	if r.Token != "original-token" {
		t.Errorf("lost token: %q", r.Token)
	}
	if r.Update.Channel != "beta" {
		t.Errorf("lost channel: %q", r.Update.Channel)
	}
	if r.Update.Mode != "prompt" {
		t.Errorf("didn't set mode: %q", r.Update.Mode)
	}
}

func TestWriter_Unset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	w, _ := OpenWriter("", os.Getenv)
	if err := w.Set("update.mode", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := w.Set("update.channel", "beta"); err != nil {
		t.Fatal(err)
	}
	if err := w.Save(); err != nil {
		t.Fatal(err)
	}

	// Reopen and unset one of the two; the other should survive.
	w2, _ := OpenWriter("", os.Getenv)
	if err := w2.Unset("update.mode"); err != nil {
		t.Fatal(err)
	}
	if err := w2.Save(); err != nil {
		t.Fatal(err)
	}

	r, _ := Load("", FlagOverrides{}, os.Getenv)
	if r.Update.Mode != "" {
		t.Errorf("mode should be unset, got %q", r.Update.Mode)
	}
	if r.Update.Channel != "beta" {
		t.Errorf("channel should remain beta, got %q", r.Update.Channel)
	}
}

func TestLoad_WitwaveDirWinsOverXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Seed BOTH locations with different base_urls. $HOME/.witwave/
	// should win because it comes first in the search order.
	xdgDir := filepath.Join(home, "xdg", "ww")
	witwaveDir := filepath.Join(home, ".witwave")
	_ = os.MkdirAll(xdgDir, 0o700)
	_ = os.MkdirAll(witwaveDir, 0o700)

	_ = os.WriteFile(filepath.Join(xdgDir, "config.toml"), []byte(`[profile.default]
base_url = "http://xdg"
`), 0o600)
	_ = os.WriteFile(filepath.Join(witwaveDir, "config.toml"), []byte(`[profile.default]
base_url = "http://witwave"
`), 0o600)

	env := fakeEnv(map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(home, "xdg"),
	})
	r, err := Load("", FlagOverrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "http://witwave" {
		t.Errorf("expected .witwave/ to win, got %q (loaded from %s)", r.BaseURL, r.LoadedFrom)
	}
}

func TestLoad_WWConfigEnvOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// File at a custom path — only reachable via WW_CONFIG.
	custom := writeCfg(t, `[profile.default]
base_url = "http://env-override"
`)
	env := fakeEnv(map[string]string{"WW_CONFIG": custom})
	r, err := Load("", FlagOverrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "http://env-override" {
		t.Errorf("WW_CONFIG should win, got %q", r.BaseURL)
	}
	if r.LoadedFrom != custom {
		t.Errorf("LoadedFrom = %q, want %q", r.LoadedFrom, custom)
	}
}

func TestLoad_ConfigFlagBeatsEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	envPath := writeCfg(t, `[profile.default]
base_url = "http://env"
`)
	flagPath := writeCfg(t, `[profile.default]
base_url = "http://flag"
`)
	env := fakeEnv(map[string]string{"WW_CONFIG": envPath})
	r, err := Load(flagPath, FlagOverrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "http://flag" {
		t.Errorf("--config should win over WW_CONFIG, got %q", r.BaseURL)
	}
}

// TestWriter_SaveTightensExistingPerms covers #1607: a config.toml that
// exists with loose perms (e.g. 0o644 from an older ww) gets tightened
// to 0o600 on the next Save, and the parent dir to 0o700.
func TestWriter_SaveTightensExistingPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("perm enforcement is unix-only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	cfgDir := filepath.Join(home, ".witwave")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(p, []byte(`[update]
mode = "notify"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Re-chmod to be sure (umask could have stripped bits on some
	// systems).
	if err := os.Chmod(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := OpenWriter("", os.Getenv)
	if err != nil {
		t.Fatalf("OpenWriter: %v", err)
	}
	if !w.Existed() {
		t.Fatal("Existed should be true for pre-seeded file")
	}
	if err := w.Set("update.mode", "auto"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := w.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat after save: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o after Save on existing 0644 file, want 0600", perm)
	}
	dst, err := os.Stat(cfgDir)
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if perm := dst.Mode().Perm(); perm != 0o700 {
		t.Errorf("parent perm = %o after Save on existing 0755 dir, want 0700", perm)
	}
}

// TestLoad_WarnsOnLoosePerms covers #1607's load-side rationale: the
// loader keeps loading (does not refuse) when config.toml has perms
// readable by others, but emits a loud WARN to stderr.
func TestLoad_WarnsOnLoosePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("perm enforcement is unix-only")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(`[profile.default]
base_url = "http://loose"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture stderr around the Load call. Restore in a deferred
	// step so a test failure can't strand the global handle.
	origStderr := os.Stderr
	r, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = wPipe
	t.Cleanup(func() { os.Stderr = origStderr })

	res, err := Load(p, FlagOverrides{}, fakeEnv(nil))
	_ = wPipe.Close()
	os.Stderr = origStderr

	if err != nil {
		t.Fatalf("Load should not refuse loose-perm file: %v", err)
	}
	if res.BaseURL != "http://loose" {
		t.Errorf("Load should have parsed file values; BaseURL=%q", res.BaseURL)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	stderr := buf.String()
	if !strings.Contains(stderr, "permissive mode") {
		t.Errorf("expected loose-perm warning on stderr, got: %q", stderr)
	}
	if !strings.Contains(stderr, p) {
		t.Errorf("expected warning to mention path %q, got: %q", p, stderr)
	}
}

func TestSettableKeys_AllValidatorsFunctional(t *testing.T) {
	// Each validator should accept at least one plausible value and
	// reject an obviously-bad one. Prevents a regression where a new
	// SettableKey ships with a nil Validate.
	cases := []struct {
		key, good, bad string
	}{
		{"update.mode", "notify", ""},
		{"update.channel", "stable", ""},
		{"update.interval", "24h", "tomorrow"},
		{"profile.default.base_url", "https://example.com", "not-a-url"},
		{"profile.default.token", "abc", ""},
		{"profile.default.run_token", "abc", ""},
		{"profile.default.timeout", "30s", "tomorrow"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if _, err := validateSet(tc.key, tc.good); err != nil {
				t.Errorf("validator rejected good value %q: %v", tc.good, err)
			}
			if _, err := validateSet(tc.key, tc.bad); err == nil {
				t.Errorf("validator accepted bad value %q", tc.bad)
			}
		})
	}
}

// TestSave_AtomicMode0600UnderUmask covers #1654: under the typical
// default umask of 0022 — which would normally cause a fresh os.Create
// to land at 0o644 — Save must produce a file at mode exactly 0o600.
//
// The previous implementation had a race window where Viper's
// WriteConfig (via os.Create) briefly exposed the inode at 0o644 before
// the post-write Chmod tightened it. After #1654 the rendered TOML is
// streamed into a CreateTemp+Chmod-managed temp file at 0o600 and then
// Renamed atomically over the target — there is no point in time where
// the final inode is observable at any mode but 0o600.
//
// The test asserts the structural property (final mode is 0o600 even
// with umask 0022). A direct race-window observation isn't reliably
// triggerable from Go user code; the structural guarantee is what the
// atomic-write pattern actually provides.
func TestSave_AtomicMode0600UnderUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("umask + perm assertions are unix-only")
	}

	// Force the standard "0022" umask for the duration of this test so
	// we don't depend on whatever the test runner inherited. Restore on
	// exit so neighbouring tests aren't affected.
	prev := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(prev) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	w, err := OpenWriter("", os.Getenv)
	if err != nil {
		t.Fatalf("OpenWriter: %v", err)
	}
	if err := w.Set("profile.default.token", "secret-bearer-token"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := w.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	wantPath := filepath.Join(home, ".witwave", "config.toml")
	st, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat saved file: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("first-write file perm = %o under umask 0022, want 0600", perm)
	}

	// Second Save (file now exists) must hold the same guarantee — the
	// pre-#1654 path's race window was on every Save, not only the
	// first, so this case is the more interesting regression check.
	w2, err := OpenWriter("", os.Getenv)
	if err != nil {
		t.Fatalf("OpenWriter (reopen): %v", err)
	}
	if !w2.Existed() {
		t.Fatal("second open should see the file as existing")
	}
	if err := w2.Set("profile.default.token", "rotated-bearer-token"); err != nil {
		t.Fatalf("Set (reopen): %v", err)
	}
	if err := w2.Save(); err != nil {
		t.Fatalf("Save (reopen): %v", err)
	}
	st2, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat saved file (reopen): %v", err)
	}
	if perm := st2.Mode().Perm(); perm != 0o600 {
		t.Errorf("subsequent-write file perm = %o under umask 0022, want 0600", perm)
	}

	// And: any temp files left behind by a botched Rename would sit
	// alongside the final file in the parent dir. Verify there are
	// none — the deferred Remove inside Save handles success cases too
	// (no-op when Rename consumed the temp), and a stray file would
	// signal a regression in the cleanup path.
	entries, err := os.ReadDir(filepath.Dir(wantPath))
	if err != nil {
		t.Fatalf("readdir parent: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".ww-config-") {
			t.Errorf("stray atomic-write temp file left behind: %s", name)
		}
	}
}
