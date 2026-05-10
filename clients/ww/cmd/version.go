package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print ww version and build info",
		Long: "Prints the ww CLI's semver, the git commit it was built from, and\n" +
			"the build date. Default output is human-readable on a single line:\n" +
			"  ww v0.23.9 (commit abc1234, built 2026-05-10)\n\n" +
			"Pass --short for just the bare semver (e.g. `v0.23.9`), suitable\n" +
			"for scripted pipelines like `ww version --short | xargs -I{} ...`.\n\n" +
			"This command runs before/without config loading, so it works in a\n" +
			"freshly-installed shell with no $HOME/.witwave/config.toml present.",
		RunE: func(cc *cobra.Command, args []string) error {
			if short {
				fmt.Fprintln(cc.OutOrStdout(), Version)
				return nil
			}
			fmt.Fprintf(cc.OutOrStdout(), "ww %s (commit %s, built %s)\n", Version, Commit, BuildDate)
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print just the semver")
	// version runs before/without config; skip PersistentPreRunE.
	cmd.PersistentPreRunE = func(_ *cobra.Command, _ []string) error { return nil }
	return cmd
}
