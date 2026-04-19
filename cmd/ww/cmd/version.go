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
