package tui

import (
	"os"
	"strings"

	"github.com/skthomasjr/witwave/clients/ww/internal/agent"
	"github.com/skthomasjr/witwave/clients/ww/internal/config"
)

// tuiDefaults captures the values the create-agent modal pre-fills.
// Resolved at modal-open time by loadTUIDefaults from three layered
// sources (highest priority first):
//
//  1. WW_TUI_DEFAULT_* environment variables — explicit operator
//     pinning. Sourced from .env or set ad-hoc; always wins.
//  2. The `[tui.create_defaults]` block in ww's config.toml (typically
//     ~/.witwave/config.toml). Auto-saved by saveTUIDefaults after
//     each successful create. Lives in the SAME file as profiles +
//     update settings — one place for everything ww-managed.
//  3. Hard-coded fallbacks — sensible starting values for a fresh
//     install with no saved state and no env overrides.
//
// Auto-save runs only when phase 1 (CR Create) succeeds. Failed
// creates can't poison the next launch. A successful create whose
// later scaffold/git-add phases failed STILL writes last-used —
// the CR exists, the gitOps phases are retryable bonus, and the
// user shouldn't have to retype the repo URL when retrying via
// the CLI.
type tuiDefaults struct {
	Namespace       string
	Backend         string
	Team            string
	CreateNamespace bool

	// Two secrets fields that round-trip across launches:
	//
	//   ExistingSecret — pre-fills the "Existing Secret name
	//                    (optional)" InputField. References a pre-
	//                    built K8s Secret as-is.
	//   Secrets        — pre-fills the dynamic per-pair section.
	//                    Serialized as a list of "KEY=VALUE" strings
	//                    in TOML so the on-disk shape stays human-
	//                    editable. `$`-prefixed VALUEs are env-lifts
	//                    at submit time.
	ExistingSecret string
	Secrets        []secretPair

	GitOpsRepo string
}

// loadTUIDefaults resolves the layered defaults. Never errors — a
// missing or unreadable config file is treated as "no saved state"
// and the hard-coded fallbacks (+ any env overrides) take effect.
// The TUI must launch even when ~/.witwave/ is missing or unreadable.
func loadTUIDefaults() tuiDefaults {
	// Layer 3 — hard-coded fallbacks (lowest priority).
	d := tuiDefaults{
		Namespace:       agent.DefaultAgentNamespace,
		Backend:         agent.DefaultBackend,
		CreateNamespace: true,
	}

	// Layer 2 — saved file (overrides fallbacks where set). Single
	// source of truth: the same config.toml that holds profiles +
	// update settings. The internal/config package walks the
	// canonical search chain ($HOME/.witwave/, $XDG_CONFIG_HOME/ww/,
	// platform default).
	if saved, ok := config.LoadTUICreateDefaults(os.Getenv); ok {
		if saved.Namespace != "" {
			d.Namespace = saved.Namespace
		}
		if saved.Backend != "" {
			d.Backend = saved.Backend
		}
		if saved.Team != "" {
			d.Team = saved.Team
		}
		// CreateNamespace is bool — Viper / mapstructure deserialise a
		// missing key as the zero value (false). When the saved file
		// IS present (ok=true) the explicit save loop below always
		// writes a concrete value, so trusting `saved.CreateNamespace`
		// is correct here.
		d.CreateNamespace = saved.CreateNamespace
		if saved.ExistingSecret != "" {
			d.ExistingSecret = saved.ExistingSecret
		}
		// Saved secrets are []string of "KEY=VALUE" lines (TOML-
		// friendly shape). Parse back into typed pairs for the
		// form. Lines that don't parse are dropped — defensive
		// against a hand-edit that forgot the `=`.
		for _, raw := range saved.Secrets {
			eq := strings.Index(raw, "=")
			if eq < 0 {
				continue
			}
			d.Secrets = append(d.Secrets, secretPair{
				Key:   strings.TrimSpace(raw[:eq]),
				Value: raw[eq+1:],
			})
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
	if v := strings.TrimSpace(os.Getenv("WW_TUI_DEFAULT_EXISTING_SECRET")); v != "" {
		d.ExistingSecret = v
	}
	// SecretsBlock isn't env-pinnable — multi-line values don't
	// pair well with shell env vars. Users wanting a pinned set
	// of secrets edit ~/.witwave/config.toml directly under
	// `[tui.create_defaults] secrets_block = """…"""`.
	if v := strings.TrimSpace(os.Getenv("WW_TUI_DEFAULT_GITOPS_REPO")); v != "" {
		d.GitOpsRepo = v
	}

	return d
}

// saveTUIDefaults writes the values from the most recent successful
// create back to the `[tui.create_defaults]` block in config.toml.
// Best-effort — write failures are silent because the CR has already
// landed and there's nothing actionable for the user about a failed
// config write at that moment.
//
// Notably skips the agent name — every create is for a different
// agent, so persisting the previous name would be more confusing
// than helpful on next launch.
//
// Uses the internal/config Writer so the save is a merge-update:
// other blocks ([profile.*], [update]) in the same file are
// preserved untouched. Only the seven [tui.create_defaults] keys
// move.
func saveTUIDefaults(d tuiDefaults) {
	w, err := config.OpenWriter("", os.Getenv)
	if err != nil {
		return
	}
	// Serialise the typed pairs back to "KEY=VALUE" strings — the
	// on-disk shape the config schema stores. Empty KEYs are
	// already filtered by submit (a row with no KEY is "dropped"),
	// but defensive-skip here too in case a partial state is
	// passed in.
	pairs := make([]string, 0, len(d.Secrets))
	for _, p := range d.Secrets {
		k := strings.TrimSpace(p.Key)
		if k == "" {
			continue
		}
		pairs = append(pairs, k+"="+p.Value)
	}
	w.SetTUICreateDefaults(config.TUICreateDefaults{
		Namespace:       d.Namespace,
		Backend:         d.Backend,
		Team:            d.Team,
		CreateNamespace: d.CreateNamespace,
		ExistingSecret:  d.ExistingSecret,
		Secrets:         pairs,
		GitOpsRepo:      d.GitOpsRepo,
	})
	_ = w.Save() // best-effort
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
