package operator

import (
	"context"
	"fmt"
	"io"

	"github.com/skthomasjr/witwave/clients/ww/internal/k8s"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// InstallOptions collects everything `ww operator install` needs beyond
// the resolved kubeconfig. Exposed so the cobra command stays focused
// on flag wiring.
type InstallOptions struct {
	Namespace string
	// Adopt — when true, proceed even if operator CRDs are already
	// present on the cluster without a Helm release (ActionAdoptCRDs).
	// Corresponds to `ww operator install --adopt`.
	Adopt bool
	// AssumeYes + DryRun — forwarded to the k8s.Confirm banner.
	AssumeYes bool
	DryRun    bool
	// Values — optional user-supplied chart values overlay. nil is fine
	// (chart defaults are sensible for the common case).
	Values map[string]interface{}
	// Out is where progress + banner lines go. Usually os.Stdout.
	Out io.Writer
	// In is where the confirmation prompt reads from. Usually os.Stdin.
	In io.Reader
}

// Install runs the full install flow:
//
//  1. Singleton detection matrix (refuses on existing install).
//  2. Preflight banner + user confirmation (skipped on local clusters
//     per the k8s.IsLocalCluster heuristic, or when --yes / --dry-run).
//  3. RBAC preflight via SelfSubjectAccessReview.
//  4. Ensure target namespace exists.
//  5. Helm install with the embedded chart.
//
// Any step that refuses or errors surfaces a diagnosable message to the
// caller; steps 3-5 error messages explicitly say which step failed so
// the RBAC-preflight caveat ("SAR passed but install failed") is
// distinguishable from an outright-denied preflight.
func Install(ctx context.Context, target *k8s.Target, cfg *rest.Config, flags *genericclioptions.ConfigFlags, opts InstallOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("InstallOptions.Out is required")
	}

	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build dynamic client: %w", err)
	}

	// Step 1 — singleton preflight.
	pre, err := CheckInstall(ctx, k8sClient, dyn)
	if err != nil {
		return fmt.Errorf("singleton preflight: %w", err)
	}
	switch pre.Action {
	case ActionCleanInstall:
		// fall through
	case ActionAdoptCRDs:
		if !opts.Adopt {
			return fmt.Errorf("%s\n(use --adopt to proceed; ww will overwrite the CRDs "+
				"with the embedded chart's versions)", pre.Reason)
		}
		fmt.Fprintln(opts.Out, "Adopting pre-existing CRDs — they will be replaced with the embedded chart's versions.")
	case ActionRefuseExists, ActionRefuseCorrupt:
		return fmt.Errorf("%w: %s", ErrPreflightRefused, pre.Reason)
	}

	// Load the embedded chart for the banner + the install call.
	ch, err := LoadEmbeddedChart()
	if err != nil {
		return fmt.Errorf("load embedded chart: %w", err)
	}

	// Step 2 — preflight banner + confirmation.
	plan := []k8s.PlanLine{
		{Key: "Action", Value: "install witwave-operator (embedded chart)"},
		{Key: "Chart", Value: fmt.Sprintf("%s %s (appVersion %s)", ch.Metadata.Name, ch.Metadata.Version, ch.Metadata.AppVersion)},
	}
	proceed, err := k8s.Confirm(opts.Out, opts.In, target, plan, k8s.PromptOptions{
		AssumeYes: opts.AssumeYes,
		DryRun:    opts.DryRun,
	})
	if err != nil {
		return err
	}
	if !proceed {
		// DryRun returns proceed=false with a banner already emitted;
		// explicit-N also ends up here. Either way, exit cleanly.
		return nil
	}

	// Step 3 — RBAC preflight (after confirmation so we don't surface
	// permission errors for a cluster the user was about to back out of).
	missing, err := CheckRBAC(ctx, k8sClient, InstallRBACRequirements(opts.Namespace))
	if err != nil {
		return fmt.Errorf("RBAC preflight: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("RBAC preflight failed — %s", FormatMissingRBAC(missing))
	}

	// Step 4 — namespace.
	if err := EnsureNamespace(ctx, k8sClient, opts.Namespace); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}

	// Step 5 — Helm install.
	fmt.Fprintf(opts.Out, "Installing %s %s into namespace %s …\n",
		ch.Metadata.Name, ch.Metadata.Version, opts.Namespace)
	helm, err := NewHelmClient(flags, opts.Namespace, ReleaseName, StderrLog(opts.Out))
	if err != nil {
		return fmt.Errorf("helm client: %w", err)
	}
	rel, err := helm.Install(ctx, ch, opts.Values)
	if err != nil {
		return fmt.Errorf("helm install step: %w", err)
	}
	fmt.Fprintf(opts.Out, "\nInstalled %s revision %d (%s). Run `ww operator status` to verify.\n",
		rel.Name, rel.Version, rel.Info.Status)
	return nil
}
