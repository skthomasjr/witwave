package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newContinuationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "continuations",
		Aliases: []string{"conts"},
		Short:   "Inspect continuation chains",
	}
	cmd.AddCommand(newContinuationsListCmd(), newContinuationsViewCmd())
	cmd.RunE = func(cc *cobra.Command, args []string) error { return runContinuationsList(cc) }
	return cmd
}

func newContinuationsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all continuations",
		RunE: func(cc *cobra.Command, args []string) error {
			return runContinuationsList(cc)
		},
	}
}

func runContinuationsList(cc *cobra.Command) error {
	ctx := cc.Context()
	c := ClientFromCtx(ctx)
	out := OutFromCtx(ctx)
	entries, err := fetchSnapshot(ctx, c, "/continuations")
	if err != nil {
		return handleErr(out, err)
	}
	return printList(out, entries, [][2]string{
		{"NAME", "name"},
		{"UPSTREAM", "continues-after,continues_after,upstream"},
		{"LAST_FIRE", "last_fire,last_run,last"},
		{"COUNT", "fire_count,count"},
		{"OUTCOME", "last_outcome,outcome"},
	})
}

func newContinuationsViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view <name>",
		Short: "View a single continuation's full details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			ctx := cc.Context()
			c := ClientFromCtx(ctx)
			out := OutFromCtx(ctx)
			entries, err := fetchSnapshot(ctx, c, "/continuations")
			if err != nil {
				return handleErr(out, err)
			}
			e := findEntryByName(entries, args[0])
			if e == nil {
				return logicalErr(fmt.Errorf("continuation %q not found", args[0]))
			}
			return printView(out, e)
		},
	}
}
