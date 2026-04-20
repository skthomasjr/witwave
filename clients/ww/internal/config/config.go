// Package config loads ww's layered configuration: defaults → file →
// env vars → command-line flags.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Profile is a single named set of connection settings in the config
// file. Only known fields are honored; unknown keys are ignored so
// future ww versions can add fields without breaking older callers.
type Profile struct {
	BaseURL  string `toml:"base_url"`
	Token    string `toml:"token"`
	RunToken string `toml:"run_token"`
	Timeout  string `toml:"timeout"` // duration string, optional
}

// UpdateConfig is the on-disk [update] block that controls the
// "newer version available" check and optional delegated upgrade. All
// fields are optional; zero values resolve to package defaults in the
// update package.
type UpdateConfig struct {
	// Mode is one of off, notify, prompt, auto. Empty string resolves
	// to the notify default. See the update package for semantics.
	Mode string `toml:"mode"`
	// Interval is the duration string ("24h", "1h", "7d"-style). Empty
	// string resolves to the 24h default. Zero or negative values are
	// rejected by the update package's parser and fall back.
	Interval string `toml:"interval"`
	// Channel selects stable-only vs include-prerelease candidates.
	// One of stable, beta. Empty string resolves to stable.
	Channel string `toml:"channel"`
}

// File is the on-disk TOML shape.
type File struct {
	Profile map[string]Profile `toml:"profile"`
	Update  UpdateConfig       `toml:"update"`
}

// Resolved carries the final effective settings for a single CLI
// invocation.
type Resolved struct {
	Profile  string
	BaseURL  string
	Token    string
	RunToken string
	Timeout  time.Duration
	Update   UpdateConfig // raw strings; parsed by the update package
}

// FlagOverrides carries the command-line values. Empty-string means
// "unset" for the layering algorithm.
type FlagOverrides struct {
	Profile  string
	BaseURL  string
	Token    string
	RunToken string
	Timeout  time.Duration // zero means unset
}

// DefaultBaseURL is the compiled-in fallback used when no other source
// provides a base URL.
const DefaultBaseURL = "http://localhost:8000"

// DefaultTimeout is used when no source sets a timeout.
const DefaultTimeout = 30 * time.Second

// Load applies the precedence rules: flag > env > file > default.
// cfgPath may be empty to use the XDG default. The returned Resolved
// is usable even if the config file does not exist.
func Load(cfgPath string, overrides FlagOverrides, getenv func(string) string) (Resolved, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	profile := overrides.Profile
	if profile == "" {
		profile = getenv("WW_PROFILE")
	}
	if profile == "" {
		profile = "default"
	}

	var file File
	path := cfgPath
	if path == "" {
		path = defaultConfigPath(getenv)
	}
	if path != "" {
		if st, err := os.Stat(path); err == nil {
			// #1395: refuse non-regular files (named pipes, devices) so
			// a hostile XDG_CONFIG_HOME pointing at /dev/stdin or a
			// named FIFO can't hang the CLI or exfiltrate via stdout.
			if !st.Mode().IsRegular() {
				return Resolved{}, fmt.Errorf(
					"config path %s is not a regular file (mode=%s)",
					path, st.Mode(),
				)
			}
			// #1358: warn (but proceed) when config.toml is readable by
			// others — bearer tokens live plaintext inside. Unix-only
			// check; Windows permission model differs and this block
			// is a no-op there (Mode().Perm() returns a best-effort
			// value).
			if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
				fmt.Fprintf(os.Stderr,
					"ww: warning: config %s has permissive mode %04o; "+
						"bearer tokens are readable by other users. "+
						"Run: chmod 600 %s\n",
					path, st.Mode().Perm(), path,
				)
			}
			if _, err := toml.DecodeFile(path, &file); err != nil {
				return Resolved{}, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	}
	fileProf := file.Profile[profile]

	// Apply layering.
	r := Resolved{
		Profile:  profile,
		BaseURL:  firstNonEmpty(overrides.BaseURL, getenv("WW_BASE_URL"), fileProf.BaseURL, DefaultBaseURL),
		Token:    firstNonEmpty(overrides.Token, getenv("WW_TOKEN"), fileProf.Token),
		RunToken: firstNonEmpty(overrides.RunToken, getenv("WW_RUN_TOKEN"), fileProf.RunToken),
	}

	timeout := overrides.Timeout
	if timeout == 0 {
		if v := getenv("WW_TIMEOUT"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return r, fmt.Errorf("WW_TIMEOUT %q: %w", v, err)
			}
			timeout = d
		}
	}
	if timeout == 0 && fileProf.Timeout != "" {
		d, err := time.ParseDuration(fileProf.Timeout)
		if err != nil {
			return r, fmt.Errorf("profile %q timeout %q: %w", profile, fileProf.Timeout, err)
		}
		timeout = d
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	r.Timeout = timeout

	// [update] block: file values feed through; env vars override.
	// The update package parses the strings at use-time and applies
	// its own defaults, so we pass whatever we have through raw.
	r.Update = file.Update
	if v := getenv("WW_UPDATE_MODE"); v != "" {
		r.Update.Mode = v
	}
	if v := getenv("WW_UPDATE_CHANNEL"); v != "" {
		r.Update.Channel = v
	}
	if v := getenv("WW_UPDATE_INTERVAL"); v != "" {
		r.Update.Interval = v
	}
	return r, nil
}

// defaultConfigPath returns the path to ww/config.toml inside the
// per-OS user config directory. XDG_CONFIG_HOME (via getenv) always
// wins so the env-override seam remains testable on every platform;
// otherwise os.UserConfigDir() provides the OS-appropriate location
// (%AppData%\ww\config.toml on Windows, ~/Library/Application
// Support/ww/config.toml on macOS, ~/.config/ww/config.toml on Linux).
func defaultConfigPath(getenv func(string) string) string {
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ww", "config.toml")
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "ww", "config.toml")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Dump writes a redacted summary of the resolved config for --verbose.
func (r Resolved) Dump(w io.Writer) {
	fmt.Fprintf(w, "profile=%s base_url=%s timeout=%s token=%s run_token=%s\n",
		r.Profile, r.BaseURL, r.Timeout,
		redact(r.Token), redact(r.RunToken),
	)
}

func redact(s string) string {
	if s == "" {
		return "<unset>"
	}
	if len(s) <= 4 {
		return "***"
	}
	return s[:2] + "…" + s[len(s)-2:]
}
