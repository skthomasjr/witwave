package cmd

import (
	"fmt"

	"github.com/skthomasjr/witwave/clients/ww/internal/k8s"
	"github.com/skthomasjr/witwave/clients/ww/internal/tui"
	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
)

// newTuiCmd wires `ww tui` — the interactive terminal surface
// tracked in #1450. Currently a stub: single-screen welcome + live
// kubeconfig-context confirmation + tracking-issue pointer, no
// cluster API calls, no feature panels. Establishes the tview
// framework so future PRs add panels rather than set up
// infrastructure.
func newTuiCmd() *cobra.Command {
	var kubeconfig, contextName, namespace string
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive ww terminal UI (stub — full dashboard coming in #1450)",
		Long: "Launches a tview-based terminal UI for ww. Currently a\n" +
			"stub that shows a welcome banner and confirms the target\n" +
			"Kubernetes context you're about to work against. Full\n" +
			"operator status / logs / events / session panels are\n" +
			"tracked in #1450.\n\n" +
			"Exit with q, esc, or ctrl-c.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTui(kubeconfig, contextName, namespace, Version)
		},
	}
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "",
		"Path to kubeconfig (overrides KUBECONFIG env var and ~/.kube/config)")
	cmd.Flags().StringVar(&contextName, "context", "",
		"Kubeconfig context to use (defaults to current-context)")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "",
		"Namespace to display in the context block (defaults to the context's namespace)")
	return cmd
}

// runTui resolves the requested kubeconfig context (best-effort —
// a failure does NOT block launch) and hands the resulting Target
// + REST config (or the diagnostic string) to the tview application.
func runTui(kubeconfig, contextName, namespace, version string) error {
	var target *k8s.Target
	var cfg *rest.Config
	var contextErr string

	r, err := k8s.NewResolver(k8s.Options{
		KubeconfigPath: kubeconfig,
		Context:        contextName,
		Namespace:      namespace,
	})
	if err != nil {
		// Soft-fail: the TUI still launches + renders "No cluster
		// configured" in place of the context block. Stays useful
		// for first-time users who haven't wired a kubeconfig yet.
		contextErr = fmt.Sprintf("No cluster configured: %s", err)
	} else {
		target = r.Target()
		cfg, err = r.REST()
		if err != nil {
			contextErr = fmt.Sprintf("Could not build REST client: %s", err)
		}
	}

	return tui.Run(version, target, cfg, contextErr)
}
