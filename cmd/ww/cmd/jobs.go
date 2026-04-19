package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newJobsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Inspect scheduled jobs",
	}
	cmd.AddCommand(newJobsListCmd(), newJobsViewCmd())
	// Default to list when no subcommand is given.
	cmd.RunE = func(cc *cobra.Command, args []string) error {
		return runJobsList(cc)
	}
	return cmd
}

func newJobsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all jobs",
		RunE: func(cc *cobra.Command, args []string) error {
			return runJobsList(cc)
		},
	}
}

func runJobsList(cc *cobra.Command) error {
	ctx := cc.Context()
	c := ClientFromCtx(ctx)
	out := OutFromCtx(ctx)
	entries, err := fetchSnapshot(ctx, c, "/jobs")
	if err != nil {
		return handleErr(out, err)
	}
	return printList(out, entries, [][2]string{
		{"NAME", "name"},
		{"SCHEDULE", "schedule,cron"},
		{"NEXT_FIRE", "next_fire,next_run,next"},
		{"LAST_FIRE", "last_fire,last_run,last"},
		{"OUTCOME", "last_outcome,outcome"},
	})
}

func newJobsViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view <name>",
		Short: "View a single job's full details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			ctx := cc.Context()
			c := ClientFromCtx(ctx)
			out := OutFromCtx(ctx)
			entries, err := fetchSnapshot(ctx, c, "/jobs")
			if err != nil {
				return handleErr(out, err)
			}
			e := findEntryByName(entries, args[0])
			if e == nil {
				return logicalErr(fmt.Errorf("job %q not found", args[0]))
			}
			return printView(out, e)
		},
	}
}
