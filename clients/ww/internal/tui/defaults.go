package tui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/skthomasjr/witwave/clients/ww/internal/agent"
)

// tuiDefaults captures the values the create-agent modal pre-fills.
// Resolved at modal-open time by loadTUIDefaults from three layered
// sources (highest priority first):
//
//  1. WW_TUI_DEFAULT_* environment variables — explicit operator
//     pinning. Sourced from .env or set ad-hoc; always wins.
//  2. ~/.witwave/tui-defaults.toml (or the next candidate in the
//     same precedence chain ww's own config.toml uses) — auto-saved
//     by saveTUIDefaults after each successful create.
//  3. Hard-coded fallbacks — sensible starting values for a fresh
//     install with no saved state and no env overrides.
//
// File format is TOML to match the rest of ww's user-level config
// (config.toml lives in the same directory). Schema is flat — no
// [section] header — so the file reads cleanly when a user opens it
// to hand-edit.
//
// Auto-save runs only when phase 1 (CR Create) succeeds. Failed
// creates can't poison the next launch with bad values, but a
// successful create that later fails to scaffold the gitOps repo
// still updates last-used (the CR exists; the scaffold's a
// retryable bonus).
type tuiDefaults struct {
	Namespace       string `toml:"namespace,omitempty"`
	Backend         string `toml:"backend,omitempty"`
	Team            string `toml:"team,omitempty"`
	CreateNamespace bool   `toml:"create_namespace"`
	AuthMode        string `toml:"auth_mode,omitempty"`
	AuthValue       string `toml:"auth_value,omitempty"`
	GitOpsRepo      string `toml:"gitops_repo,omitempty"`
}

// defaultsFileName is the basename for the auto-managed TUI defaults
// file. Sibling to config.toml in whichever ww config dir the user
// happens to use (~/.witwave/, $XDG_CONFIG_HOME/ww/, …).
const defaultsFileName = "tui-defaults.toml"

// loadTUIDefaults resolves the layered defaults. Never errors — a
// missing or corrupt saved file is treated as "no saved state" and
// the hard-coded fallbacks (+ any env overrides) take effect. The
// TUI must launch even when ~/.witwave/ is missing or unreadable.
func loadTUIDefaults() tuiDefaults {
	// Layer 3 — hard-coded fallbacks (lowest priority).
	d := tuiDefaults{
		Namespace:       agent.DefaultAgentNamespace,
		Backend:         agent.DefaultBackend,
		CreateNamespace: true,
		AuthMode:        "none",
	}

	// Layer 2 — saved file (overrides fallbacks where set).
	if saved, ok := readSavedDefaults(); ok {
		if saved.Namespace != "" {
			d.Namespace = saved.Namespace
		}
		if saved.Backend != "" {
			d.Backend = saved.Backend
		}
		if saved.Team != "" {
			d.Team = saved.Team
		}
		// CreateNamespace is bool — the saved file always carries a
		// concrete value (no omitempty on the toml tag), so a saved
		// `false` correctly suppresses the fallback `true`.
		d.CreateNamespace = saved.CreateNamespace
		if saved.AuthMode != "" {
			d.AuthMode = saved.AuthMode
		}
		if saved.AuthValue != "" {
			d.AuthValue = saved.AuthValue
		}
		if saved.GitOpsRepo != "" {
			d.GitOpsRepo = saved.GitOpsRepo
		}
	}

	// Layer 1 — env vars (highest priority). Explicit pin from .env
	// or ad-hoc shell export overrides anything the tool guessed.
	if v := strings.TrimSpace(os.Getenv("WW_TUI_DEFAULT_NAMESPACE")); v != "" {
		d.Namespace = v
	}
	if v := strings.TrimSpace(os.Getenv("WW_TUI_DEFAULT_BACKEND")); v != "" {
		d.Backend = v
	}
	if v := strings.TrimSpace(os.Getenv("WW_TUI_DEFAULT_TEAM")); v != "" {
		d.Team = v
	}
	if v := strings.TrimSpace(os.Getenv("WW_TUI_DEFAULT_CREATE_NAMESPACE")); v != "" {
		d.CreateNamespace = parseBoolOrDefault(v, d.CreateNamespace)
	}
	if v := strings.TrimSpace(os.Getenv("WW_TUI_DEFAULT_AUTH_MODE")); v != "" {
		d.AuthMode = v
	}
	if v := strings.TrimSpace(os.Getenv("WW_TUI_DEFAULT_AUTH_VALUE")); v != "" {
		d.AuthValue = v
	}
	if v := strings.TrimSpace(os.Getenv("WW_TUI_DEFAULT_GITOPS_REPO")); v != "" {
		d.GitOpsRepo = v
	}

	return d
}

// saveTUIDefaults writes the values from the most recent successful
// create back to the ww config dir. Best-effort — write failures
// are silent because the CR has already landed and there's nothing
// actionable for the user about a failed config write at that
// moment. Worst case: next launch starts from the previous saved
// state (or fallbacks).
//
// Notably skips the agent name — every create is for a different
// agent, so persisting the previous name would be more confusing
// than helpful on next launch.
func saveTUIDefaults(d tuiDefaults) {
	path, err := defaultsFileWritePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_ = toml.NewEncoder(f).Encode(d) // best-effort
}

// readSavedDefaults walks the same path-precedence chain ww's own
// config.toml uses and returns the first parseable file's contents.
// Returns (zero, false) for any combination that doesn't yield a
// usable result — missing dirs, unreadable file, malformed TOML —
// so callers can treat "no saved state" as one uniform branch.
func readSavedDefaults() (tuiDefaults, bool) {
	for _, dir := range defaultsConfigDirs(os.Getenv) {
		path := filepath.Join(dir, defaultsFileName)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var d tuiDefaults
		if err := toml.Unmarshal(b, &d); err != nil {
			continue
		}
		return d, true
	}
	return tuiDefaults{}, false
}

// defaultsFileWritePath picks where saveTUIDefaults will write. We
// prefer to write next to an EXISTING user-managed config.toml so
// both files live together — that's what the user expects when
// they hand-edit. Falls back to the first writable candidate
// (typically $HOME/.witwave/) when no config.toml has been
// established yet.
func defaultsFileWritePath() (string, error) {
	dirs := defaultsConfigDirs(os.Getenv)
	if len(dirs) == 0 {
		return "", os.ErrNotExist
	}
	// Prefer the dir that already holds a config.toml.
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, "config.toml")); err == nil {
			return filepath.Join(dir, defaultsFileName), nil
		}
	}
	// No existing config.toml — write to the first candidate, which
	// matches ww's own preferred-write location.
	return filepath.Join(dirs[0], defaultsFileName), nil
}

// defaultsConfigDirs mirrors the path-precedence chain in
// internal/config.defaultConfigPaths so the TUI defaults file
// shares the discovery semantics as ww's user-level config.toml.
//
// Order:
//
//  1. $HOME/.witwave/ — brand-aligned dotfile dir, preferred.
//  2. $XDG_CONFIG_HOME/ww/ — XDG Base Directory Spec.
//  3. Platform-default user config dir (os.UserConfigDir()/ww/).
//
// getenv is parameterised so tests can drive every branch.
func defaultsConfigDirs(getenv func(string) string) []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".witwave"))
	}
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		dirs = append(dirs, filepath.Join(xdg, "ww"))
	}
	if ucd, err := os.UserConfigDir(); err == nil && ucd != "" {
		dirs = append(dirs, filepath.Join(ucd, "ww"))
	}
	return dirs
}

// parseBoolOrDefault accepts the usual truthy/falsy strings (true /
// false / 1 / 0 / yes / no / on / off, case-insensitive). Anything
// else falls back to the default — so a typo'd
// `WW_TUI_DEFAULT_CREATE_NAMESPACE=ywes` doesn't silently swap the
// behaviour to false.
func parseBoolOrDefault(s string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "y", "on":
		return true
	case "false", "0", "no", "n", "off":
		return false
	}
	return fallback
}
