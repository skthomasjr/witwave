package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/skthomasjr/witwave/clients/ww/internal/config"
	"github.com/spf13/cobra"
)

// isSecretKey reports whether a config key names a credential that must not
// be echoed back to the terminal (shell history, scrollback, or CI logs).
// Matches anything ending in `.token`, `.run_token`, or `.password`, plus
// the top-level `token` key if present. Keep the list conservative — it's
// cheaper to redact a non-secret than to leak a real one.
func isSecretKey(key string) bool {
	k := strings.ToLower(key)
	if k == "token" || k == "password" {
		return true
	}
	for _, suffix := range []string{".token", ".run_token", ".password", ".bearer", ".secret"} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// newConfigCmd builds the `ww config` subcommand tree. Verbs:
//
//	ww config path                  — print the active config file path
//	ww config get <key>             — read a value from the config file
//	ww config set <key> <value>     — write a value back to disk
//	ww config unset <key>           — remove a value from the config file
//	ww config list-keys             — print the allowlisted keys + shapes
//
// All verbs respect --config (from the root command) when set; they
// otherwise resolve to the first existing file in the default search
// path. A new file is created at $HOME/.witwave/config.toml on first
// `set` if no config exists yet.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Get, set, and inspect ww configuration values",
		Long: "Read and write ww's on-disk configuration. Values live in a TOML\n" +
			"file — by default at $HOME/.witwave/config.toml, falling back to\n" +
			"the XDG or platform config dir if that's where your existing file\n" +
			"lives. Use --config (global flag) to target a specific file.",
		// Subcommands handle their own config loading; prevent root's
		// PersistentPreRunE from trying to connect to a harness.
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error { return nil },
	}
	cmd.AddCommand(newConfigPathCmd())
	cmd.AddCommand(newConfigGetCmd())
	cmd.AddCommand(newConfigSetCmd())
	cmd.AddCommand(newConfigUnsetCmd())
	cmd.AddCommand(newConfigListKeysCmd())
	return cmd
}

// rootConfigFlag climbs the command tree to the root and returns the
// --config flag value. Works for any depth of subcommand; empty string
// means "use default discovery."
func rootConfigFlag(cc *cobra.Command) string {
	for c := cc; c != nil; c = c.Parent() {
		if f := c.Flags().Lookup("config"); f != nil && f.Value.String() != "" {
			return f.Value.String()
		}
	}
	return ""
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path of the active config file (whether it exists or not)",
		Args:  cobra.NoArgs,
		RunE: func(cc *cobra.Command, _ []string) error {
			w, err := config.OpenWriter(rootConfigFlag(cc), os.Getenv)
			if err != nil {
				return err
			}
			existed := "will be created on first `ww config set`"
			if w.Existed() {
				existed = "exists"
			}
			fmt.Printf("%s  (%s)\n", w.Path(), existed)
			return nil
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print the current value of a config key",
		Long: "Reads the active config file and prints the value for the given\n" +
			"key. Dotted notation for nested keys (e.g. update.mode,\n" +
			"profile.default.base_url). Prints an empty line and exits 0 when\n" +
			"the key is unset, so shell pipelines can distinguish empty-value\n" +
			"from error.",
		Args: cobra.ExactArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			w, err := config.OpenWriter(rootConfigFlag(cc), os.Getenv)
			if err != nil {
				return err
			}
			fmt.Println(w.Get(args[0]))
			return nil
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Write a config key's value back to the config file",
		Long: "Validates <key> against the allowlist and <value> against the\n" +
			"key's schema, then updates the on-disk config file. Creates the\n" +
			"file (and parent dir) at $HOME/.witwave/config.toml if no config\n" +
			"exists yet.\n\n" +
			"Run `ww config list-keys` for the supported keys.",
		Args: cobra.ExactArgs(2),
		RunE: func(cc *cobra.Command, args []string) error {
			w, err := config.OpenWriter(rootConfigFlag(cc), os.Getenv)
			if err != nil {
				return err
			}
			if err := w.Set(args[0], args[1]); err != nil {
				return err
			}
			created := !w.Existed()
			if err := w.Save(); err != nil {
				return err
			}
			action := "updated"
			if created {
				action = "created"
			}
			// Redact credential values so shell history, scrollback, and
			// CI logs never see the raw token. Non-secret keys still echo
			// as before so operators can verify the write.
			if isSecretKey(args[0]) {
				fmt.Fprintf(os.Stderr, "%s %s (%s = <redacted>)\n", action, w.Path(), args[0])
			} else {
				fmt.Fprintf(os.Stderr, "%s %s (%s = %s)\n", action, w.Path(), args[0], args[1])
			}
			return nil
		},
	}
}

func newConfigUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove a config key from the config file",
		Long: "Removes the given key from the on-disk config file. No-op when\n" +
			"the key isn't currently set. The file is left in place (even if\n" +
			"unset leaves it empty).",
		Args: cobra.ExactArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			w, err := config.OpenWriter(rootConfigFlag(cc), os.Getenv)
			if err != nil {
				return err
			}
			if err := w.Unset(args[0]); err != nil {
				return err
			}
			if err := w.Save(); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "unset %s from %s\n", args[0], w.Path())
			return nil
		},
	}
}

func newConfigListKeysCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-keys",
		Short: "Print every config key `ww config set` accepts",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			keys := config.SettableKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].Key < keys[j].Key })
			for _, k := range keys {
				fmt.Printf("%-30s  %s\n", k.Key, k.Description)
			}
			return nil
		},
	}
}
