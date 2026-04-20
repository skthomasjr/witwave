package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/skthomasjr/witwave/clients/ww/internal/k8s"
	"github.com/skthomasjr/witwave/clients/ww/internal/operator"
	"github.com/spf13/cobra"
)

// operatorFlags are inherited by every `ww operator *` subcommand so
// the kubeconfig + context + namespace discovery stays uniform across
// install / upgrade / status / uninstall.
type operatorFlags struct {
	kubeconfig string
	context    string
	namespace  string
	// --yes — skip preflight confirmation on production-looking targets.
	// Also honoured via WW_ASSUME_YES=true.
	assumeYes bool
	// --dry-run — print the plan banner and exit without mutating. Read
	// by install/upgrade/uninstall; ignored by status.
	dryRun bool
}

func bindOperatorFlags(cmd *cobra.Command, f *operatorFlags) {
	cmd.PersistentFlags().StringVar(&f.kubeconfig, "kubeconfig", "",
		"Path to kubeconfig (overrides KUBECONFIG env var and ~/.kube/config)")
	cmd.PersistentFlags().StringVar(&f.context, "context", "",
		"Kubeconfig context to use (defaults to current-context)")
	cmd.PersistentFlags().StringVarP(&f.namespace, "namespace", "n", operator.DefaultNamespace,
		"Namespace the operator is installed in")
}

// bindMutatingFlags adds --yes and --dry-run to mutating subcommands
// (install/upgrade/uninstall). Keeps read-only subcommands free of them.
func bindMutatingFlags(cmd *cobra.Command, f *operatorFlags) {
	cmd.Flags().BoolVarP(&f.assumeYes, "yes", "y", false,
		"Skip the preflight confirmation prompt (or set WW_ASSUME_YES=true)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false,
		"Print the plan and exit without applying any changes")
}

// resolveTarget runs the kubeconfig loader and returns the populated
// Target + REST config. Surfaces the friendly "no kubeconfig / no context"
// error when applicable.
func (f *operatorFlags) resolveTarget() (*k8s.Target, *k8s.Resolver, error) {
	r, err := k8s.NewResolver(k8s.Options{
		KubeconfigPath: f.kubeconfig,
		Context:        f.context,
		Namespace:      f.namespace,
	})
	if err != nil {
		return nil, nil, err
	}
	return r.Target(), r, nil
}

// newOperatorCmd is the parent command for `ww operator *`. Has no
// direct behaviour; cobra will print help if no subcommand runs.
func newOperatorCmd() *cobra.Command {
	f := &operatorFlags{}
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Manage the witwave-operator install (CRD controller) on a Kubernetes cluster",
		Long: "Install, upgrade, inspect, or uninstall the witwave-operator Helm release.\n\n" +
			"The operator is a cluster-scoped singleton — one install per cluster. These\n" +
			"commands use the ambient kubeconfig (--kubeconfig / KUBECONFIG / ~/.kube/config)\n" +
			"and the current-context by default; override with --context.",
	}
	bindOperatorFlags(cmd, f)

	cmd.AddCommand(newOperatorStatusCmd(f))
	cmd.AddCommand(newOperatorInstallCmd(f))
	cmd.AddCommand(newOperatorUpgradeCmd(f))
	cmd.AddCommand(newOperatorUninstallCmd(f))
	return cmd
}

// ---------------------------------------------------------------------------
// status — read-only; ships now.
// ---------------------------------------------------------------------------

func newOperatorStatusCmd(f *operatorFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the witwave-operator install state on the target cluster",
		Long: "Reads (no cluster mutations) the witwave-operator Helm release, its\n" +
			"pod(s), the CRDs it owns, and the live count of WitwaveAgent and\n" +
			"WitwavePrompt CRs. Prints an \"operator not installed\" block when\n" +
			"no release is found in the target namespace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOperatorStatus(cmd.Context(), f)
		},
	}
}

func runOperatorStatus(ctx context.Context, f *operatorFlags) error {
	target, resolver, err := f.resolveTarget()
	if err != nil {
		return err
	}
	// status is read-only — no confirmation, but still print the header
	// so users see which cluster they're looking at.
	fmt.Printf("Target cluster: %s  (context: %s)\n",
		cmpDisplay(target.Cluster, target.Server), target.Context)
	fmt.Println()

	cfg, err := resolver.REST()
	if err != nil {
		return err
	}

	s, err := operator.GatherStatus(ctx, cfg, f.namespace, Version)
	if err != nil {
		// Render whatever we got before returning the error — users
		// still see partial info.
		if s != nil {
			s.Render(os.Stdout)
			fmt.Fprintln(os.Stderr)
		}
		return fmt.Errorf("gather status: %w", err)
	}
	s.Render(os.Stdout)
	return nil
}

// cmpDisplay prefers the cluster nickname over the raw server URL — EKS /
// GKE ARNs can be long, and when both are present the nickname is what
// users actually identify with.
func cmpDisplay(cluster, server string) string {
	if cluster != "" {
		return cluster
	}
	return server
}

// ---------------------------------------------------------------------------
// install / upgrade / uninstall — stubs until the Helm SDK is integrated.
// Each subcommand wires --yes / --dry-run today so the flag surface is
// stable when the implementation lands. Currently returns a clear
// "not yet implemented" error pointing at the tracking issue.
// ---------------------------------------------------------------------------

func newOperatorInstallCmd(f *operatorFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the witwave-operator Helm release (embedded chart)",
		Long: "Installs the embedded witwave-operator chart into the target cluster.\n\n" +
			"Not yet implemented — see https://github.com/skthomasjr/witwave/issues/1477\n" +
			"for scope, decisions, and progress. Runs the Helm-SDK install path,\n" +
			"singleton detection, RBAC preflight, and the CRD server-side apply.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("ww operator install not yet implemented — see #1477")
		},
	}
	bindMutatingFlags(cmd, f)
	return cmd
}

func newOperatorUpgradeCmd(f *operatorFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the witwave-operator Helm release to the embedded chart version",
		Long: "Upgrades in place. CRDs are server-side applied first, then the Helm\n" +
			"upgrade runs.\n\n" +
			"Not yet implemented — see https://github.com/skthomasjr/witwave/issues/1477.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("ww operator upgrade not yet implemented — see #1477")
		},
	}
	bindMutatingFlags(cmd, f)
	return cmd
}

func newOperatorUninstallCmd(f *operatorFlags) *cobra.Command {
	var deleteCRDs bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the witwave-operator Helm release",
		Long: "Removes the operator's Helm release. By default CRDs and the CRs\n" +
			"they own are preserved — use --delete-crds to remove them too\n" +
			"(requires --force when any CRs still exist).\n\n" +
			"Not yet implemented — see https://github.com/skthomasjr/witwave/issues/1477.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = deleteCRDs
			return fmt.Errorf("ww operator uninstall not yet implemented — see #1477")
		},
	}
	bindMutatingFlags(cmd, f)
	cmd.Flags().BoolVar(&deleteCRDs, "delete-crds", false,
		"Also delete the WitwaveAgent / WitwavePrompt CRDs (refuses when live CRs exist; --force overrides)")
	return cmd
}
