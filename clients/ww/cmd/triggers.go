package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newTriggersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "triggers",
		Short: "Inspect inbound HTTP triggers",
		Long: "Fetches /triggers from the harness and lists every inbound HTTP\n" +
			"trigger with its endpoint, last fire, fire count, and last outcome.\n" +
			"Run without a subcommand to default to `list`; use `view <name>`\n" +
			"to see the full configuration of a single trigger.",
	}
	cmd.AddCommand(newTriggersListCmd(), newTriggersViewCmd())
	cmd.RunE = func(cc *cobra.Command, args []string) error { return runTriggersList(cc) }
	return cmd
}

func newTriggersListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all triggers",
		Long: "Fetches /triggers from the harness and prints one row per trigger\n" +
			"with NAME, ENDPOINT, LAST_FIRE, COUNT, and OUTCOME.",
		RunE: func(cc *cobra.Command, args []string) error {
			return runTriggersList(cc)
		},
	}
}

func runTriggersList(cc *cobra.Command) error {
	ctx := cc.Context()
	c := ClientFromCtx(ctx)
	out := OutFromCtx(ctx)
	entries, err := fetchSnapshot(ctx, c, "/triggers")
	if err != nil {
		return handleErr(out, err)
	}
	return printList(out, entries, [][2]string{
		{"NAME", "name"},
		{"ENDPOINT", "endpoint"},
		{"LAST_FIRE", "last_fire,last_run,last"},
		{"COUNT", "fire_count,count"},
		{"OUTCOME", "last_outcome,outcome"},
	})
}

func newTriggersViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view <name>",
		Short: "View a single trigger's full details",
		Long: "Fetches /triggers from the harness and prints the full configuration\n" +
			"for the named trigger. Matches by trigger name OR endpoint path so\n" +
			"either form resolves the same record.",
		Args: cobra.ExactArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			ctx := cc.Context()
			c := ClientFromCtx(ctx)
			out := OutFromCtx(ctx)
			entries, err := fetchSnapshot(ctx, c, "/triggers")
			if err != nil {
				return handleErr(out, err)
			}
			e := findEntryByName(entries, args[0], "name", "endpoint")
			if e == nil {
				return logicalErr(fmt.Errorf("trigger %q not found", args[0]))
			}
			return printView(out, e)
		},
	}
}
