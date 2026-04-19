package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newTriggersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "triggers",
		Short: "Inspect inbound HTTP triggers",
	}
	cmd.AddCommand(newTriggersListCmd(), newTriggersViewCmd())
	cmd.RunE = func(cc *cobra.Command, args []string) error { return runTriggersList(cc) }
	return cmd
}

func newTriggersListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all triggers",
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
		Args:  cobra.ExactArgs(1),
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
