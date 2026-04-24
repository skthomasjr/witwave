package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/skthomasjr/witwave/clients/ww/internal/agent"
)

// tuiDefaults captures the values the create-agent modal pre-fills.
// Resolved at modal-open time by loadTUIDefaults from three layered
// sources (highest priority first):
//
//  1. WW_TUI_DEFAULT_* environment variables — explicit operator
//     pinning. Sourced from .env or set ad-hoc; always wins.
//  2. ~/.config/ww/tui-defaults.json — auto-saved by saveTUIDefaults
//     after each successful create. Fills in the "last-used"
//     muscle-memory layer.
//  3. Hard-coded fallbacks — sensible starting values for a fresh
//     install with no saved state and no env overrides.
//
// Auto-save runs only when phase 1 (CR Create) succeeds. Failed
// creates can't poison the next launch with bad values, but a
// successful create that later fails to scaffold the gitOps repo
// still updates last-used (the CR exists; the scaffold's a
// retryable bonus).
type tuiDefaults struct {
	Namespace       string `json:"namespace,omitempty"`
	Backend         string `json:"backend,omitempty"`
	Team            string `json:"team,omitempty"`
	CreateNamespace bool   `json:"create_namespace"`
	AuthMode        string `json:"auth_mode,omitempty"`
	AuthValue       string `json:"auth_value,omitempty"`
	GitOpsRepo      string `json:"gitops_repo,omitempty"`
}

// loadTUIDefaults resolves the layered defaults. Never errors — a
// missing or corrupt saved file is treated as "no saved state" and
// the hard-coded fallbacks (+ any env overrides) take effect. This
// is deliberate: the TUI must launch even when the config dir is
// missing or unreadable.
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
		// concrete value (json:"create_namespace" without omitempty),
		// so a saved `false` correctly suppresses the fallback `true`.
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
// create back to ~/.config/ww/tui-defaults.json. Best-effort — write
// failures are silent because the CR has already landed and there's
// nothing actionable for the user about a failed config write at
// that moment. Worst case: next launch starts from the previous
// saved state (or fallbacks), which is still better than no defaults.
//
// Notably skips the agent name — every create is for a different
// agent, so persisting the previous name would be more confusing
// than helpful on next launch.
func saveTUIDefaults(d tuiDefaults) {
	path, err := defaultsFilePath()
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
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	_ = enc.Encode(d) // best-effort
}

// readSavedDefaults loads the on-disk JSON. Returns (zero, false)
// for any error so callers can treat "no saved state" uniformly
// (missing file, empty file, malformed JSON all end up here).
func readSavedDefaults() (tuiDefaults, bool) {
	var d tuiDefaults
	path, err := defaultsFilePath()
	if err != nil {
		return d, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return d, false
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return d, false
	}
	return d, true
}

// defaultsFilePath resolves to ~/.config/ww/tui-defaults.json (or
// the OS-equivalent under UserConfigDir). Sibling to ww's existing
// config.toml — same dir, separate file, no risk of clobbering the
// connection-settings store.
func defaultsFilePath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "ww", "tui-defaults.json"), nil
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
