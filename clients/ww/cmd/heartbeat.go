package cmd

import (
	"github.com/spf13/cobra"
)

func newHeartbeatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Inspect the heartbeat schedule and recent fires",
		Long: "Fetches /heartbeat from the harness and prints the configured\n" +
			"heartbeat (interval, payload, last-fire metadata). Run without\n" +
			"a subcommand to default to `view`. Each agent has at most one\n" +
			"heartbeat configured; if none is set the command warns and exits.",
	}
	cmd.AddCommand(newHeartbeatViewCmd())
	cmd.RunE = func(cc *cobra.Command, args []string) error { return runHeartbeatView(cc) }
	return cmd
}

func newHeartbeatViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view",
		Short: "Show the configured heartbeat",
		Long: "Fetches /heartbeat from the harness and prints the full heartbeat\n" +
			"record (interval, payload, last-fire metadata). Warns if no\n" +
			"heartbeat is configured for the agent.",
		RunE: func(cc *cobra.Command, args []string) error {
			return runHeartbeatView(cc)
		},
	}
}

func runHeartbeatView(cc *cobra.Command) error {
	ctx := cc.Context()
	c := ClientFromCtx(ctx)
	out := OutFromCtx(ctx)
	entries, err := fetchSnapshot(ctx, c, "/heartbeat")
	if err != nil {
		return handleErr(out, err)
	}
	if len(entries) == 0 {
		out.Warnf("no heartbeat configured")
		return nil
	}
	return printView(out, entries[0])
}
