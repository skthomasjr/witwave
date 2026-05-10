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
		Long: "Fetches /continuations from the harness and lists every continuation\n" +
			"with its upstream link, last fire, fire count, and last outcome.\n" +
			"Run without a subcommand to default to `list`; use `view <name>` to\n" +
			"see the full configuration of a single continuation.",
	}
	cmd.AddCommand(newContinuationsListCmd(), newContinuationsViewCmd())
	cmd.RunE = func(cc *cobra.Command, args []string) error { return runContinuationsList(cc) }
	return cmd
}

func newContinuationsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all continuations",
		Long: "Fetches /continuations from the harness and prints one row per\n" +
			"continuation with NAME, UPSTREAM, LAST_FIRE, COUNT, and OUTCOME.",
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
		Long: "Fetches /continuations from the harness and prints the full\n" +
			"configuration for the named continuation, including upstream\n" +
			"binding, last-fire metadata, and any per-continuation flags.",
		Args: cobra.ExactArgs(1),
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
