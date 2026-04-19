package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func writeCfg(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_Defaults(t *testing.T) {
	r, err := Load("", FlagOverrides{}, fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != DefaultBaseURL {
		t.Errorf("base: %q", r.BaseURL)
	}
	if r.Profile != "default" {
		t.Errorf("profile: %q", r.Profile)
	}
	if r.Timeout != DefaultTimeout {
		t.Errorf("timeout: %v", r.Timeout)
	}
}

func TestLoad_FileValues(t *testing.T) {
	p := writeCfg(t, `
[profile.default]
base_url = "http://from-file:9000"
token = "file-tok"
run_token = "file-run"
timeout = "45s"
`)
	r, err := Load(p, FlagOverrides{}, fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "http://from-file:9000" {
		t.Errorf("base: %q", r.BaseURL)
	}
	if r.Token != "file-tok" {
		t.Errorf("token: %q", r.Token)
	}
	if r.RunToken != "file-run" {
		t.Errorf("run: %q", r.RunToken)
	}
	if r.Timeout != 45*time.Second {
		t.Errorf("timeout: %v", r.Timeout)
	}
}

func TestLoad_EnvBeatsFile(t *testing.T) {
	p := writeCfg(t, `
[profile.default]
base_url = "http://file:1"
token = "file-tok"
`)
	env := fakeEnv(map[string]string{
		"WW_BASE_URL": "http://env:2",
		"WW_TOKEN":    "env-tok",
	})
	r, err := Load(p, FlagOverrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "http://env:2" {
		t.Errorf("base: %q", r.BaseURL)
	}
	if r.Token != "env-tok" {
		t.Errorf("token: %q", r.Token)
	}
}

func TestLoad_FlagBeatsAll(t *testing.T) {
	p := writeCfg(t, `
[profile.default]
base_url = "http://file:1"
`)
	env := fakeEnv(map[string]string{
		"WW_BASE_URL": "http://env:2",
		"WW_TOKEN":    "env-tok",
	})
	r, err := Load(p, FlagOverrides{BaseURL: "http://flag:3", Token: "flag-tok"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "http://flag:3" {
		t.Errorf("base: %q", r.BaseURL)
	}
	if r.Token != "flag-tok" {
		t.Errorf("token: %q", r.Token)
	}
}

func TestLoad_ProfileSwitch(t *testing.T) {
	p := writeCfg(t, `
[profile.default]
base_url = "http://def"

[profile.prod]
base_url = "http://prod"
token = "prod-tok"
`)
	r, err := Load(p, FlagOverrides{Profile: "prod"}, fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "http://prod" {
		t.Errorf("base: %q", r.BaseURL)
	}
	if r.Token != "prod-tok" {
		t.Errorf("token: %q", r.Token)
	}
}

func TestLoad_MissingFile_OK(t *testing.T) {
	r, err := Load("/does/not/exist/ww.toml", FlagOverrides{}, fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != DefaultBaseURL {
		t.Errorf("base: %q", r.BaseURL)
	}
}

func TestLoad_BadDurationInFile(t *testing.T) {
	p := writeCfg(t, `
[profile.default]
timeout = "not-a-duration"
`)
	_, err := Load(p, FlagOverrides{}, fakeEnv(nil))
	if err == nil {
		t.Fatal("want error on bad duration")
	}
}

func TestLoad_XDGOverride(t *testing.T) {
	p := writeCfg(t, `
[profile.default]
base_url = "http://xdg"
`)
	// Put config.toml at <tempdir>/ww/config.toml so XDG resolution finds it.
	root := t.TempDir()
	wwDir := filepath.Join(root, "ww")
	if err := os.MkdirAll(wwDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	_ = os.WriteFile(filepath.Join(wwDir, "config.toml"), data, 0o600)

	env := fakeEnv(map[string]string{"XDG_CONFIG_HOME": root})
	r, err := Load("", FlagOverrides{}, env)
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "http://xdg" {
		t.Errorf("base: %q", r.BaseURL)
	}
}
