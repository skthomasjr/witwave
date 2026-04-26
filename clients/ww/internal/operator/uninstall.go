package operator

import (
	"context"
	"fmt"
	"io"

	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// UninstallOptions — inputs to the uninstall flow.
type UninstallOptions struct {
	Namespace string
	// DeleteCRDs — remove the operator's CRDs too. Refuses when any CRs
	// exist (cascade delete would orphan user data); --force overrides.
	DeleteCRDs bool
	// Force — override the CR-existence safety gate when DeleteCRDs is
	// true. Documented as dangerous: the confirm prompt gets the
	// user-facing "N agents will be deleted" warning before we run.
	Force     bool
	AssumeYes bool
	DryRun    bool
	Out       io.Writer
	In        io.Reader
}

// Uninstall runs:
//
//  1. Verify a release exists in the target namespace.
//  2. CR-existence safety gate (refuses --delete-crds when live CRs
//     exist, unless --force).
//  3. Preflight banner + confirmation (with explicit warning when
//     --delete-crds --force would cascade-delete live CRs).
//  4. Helm uninstall.
//  5. Optional CRD delete when --delete-crds was set.
//
// Default behaviour (no --delete-crds) preserves CRDs + CRs — the
// release goes, but user data stays. Operators who really want a full
// scrub pass --delete-crds and, if CRs exist, --force.
func Uninstall(ctx context.Context, target *k8s.Target, cfg *rest.Config, flags *genericclioptions.ConfigFlags, opts UninstallOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("UninstallOptions.Out is required")
	}

	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build dynamic client: %w", err)
	}

	// Step 1 — release must exist.
	rel, err := LookupRelease(ctx, k8sClient, opts.Namespace, ReleaseName)
	if err != nil {
		return fmt.Errorf("look up existing release: %w", err)
	}
	if rel == nil {
		return fmt.Errorf("no %s release found in namespace %s — nothing to uninstall",
			ReleaseName, opts.Namespace)
	}

	// Step 2 — CR-existence safety gate. Only needed when --delete-crds
	// is in play (#1551). Without --delete-crds, CRDs are preserved, so
	// running a cluster-wide list of every managed CR on every plain
	// uninstall is wasted RBAC + API-server load just to print a "CRDs
	// preserved" banner.
	var counts map[string]int
	totalCRs := 0
	if opts.DeleteCRDs {
		counts, err = CountCRs(ctx, dyn)
		if err != nil {
			return fmt.Errorf("count CRs: %w", err)
		}
		for _, n := range counts {
			totalCRs += n
		}
		if totalCRs > 0 && !opts.Force {
			return fmt.Errorf("refusing to delete CRDs: %d managed custom resources still exist "+
				"(%d WitwaveAgent + %d WitwavePrompt). Delete them first with "+
				"`kubectl delete witwaveagent --all --all-namespaces` etc., or re-run "+
				"with --force (which will cascade-delete these resources along with the CRDs)",
				totalCRs, counts["WitwaveAgent"], counts["WitwavePrompt"])
		}
	}

	// Step 3 — preflight banner.
	plan := []k8s.PlanLine{
		{Key: "Action", Value: fmt.Sprintf("uninstall %s from %s", ReleaseName, opts.Namespace)},
		{Key: "Release", Value: fmt.Sprintf("%s (rev %d, chart %s)", rel.Name, rel.Revision, rel.ChartVersion)},
	}
	if opts.DeleteCRDs {
		if opts.Force && totalCRs > 0 {
			plan = append(plan, k8s.PlanLine{
				Key:   "WARNING",
				Value: fmt.Sprintf("--delete-crds --force will cascade-delete %d CRs", totalCRs),
			})
		} else {
			plan = append(plan, k8s.PlanLine{
				Key:   "CRDs",
				Value: "will be deleted (no CRs exist; safe)",
			})
		}
	} else {
		plan = append(plan, k8s.PlanLine{
			Key:   "CRDs",
			Value: "preserved (use --delete-crds to remove them too)",
		})
	}
	proceed, err := k8s.Confirm(opts.Out, opts.In, target, plan, k8s.PromptOptions{
		AssumeYes: opts.AssumeYes,
		DryRun:    opts.DryRun,
	})
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	// Step 4 — Helm uninstall.
	fmt.Fprintf(opts.Out, "Uninstalling Helm release %s …\n", ReleaseName)
	helm, err := NewHelmClient(flags, opts.Namespace, ReleaseName, StderrLog(opts.Out))
	if err != nil {
		return fmt.Errorf("helm client: %w", err)
	}
	if _, err := helm.Uninstall(ctx); err != nil {
		return fmt.Errorf("helm uninstall step: %w", err)
	}
	fmt.Fprintf(opts.Out, "Release %s uninstalled.\n", ReleaseName)

	// Step 5 — optional CRD delete.
	if opts.DeleteCRDs {
		fmt.Fprintln(opts.Out, "Deleting CRDs…")
		if err := deleteOperatorCRDs(ctx, dyn); err != nil {
			return fmt.Errorf("delete CRDs: %w", err)
		}
		fmt.Fprintln(opts.Out, "CRDs deleted.")
	}
	return nil
}

// deleteOperatorCRDs removes the operator's CRDs. Cascade-delete of
// any attached CRs is the apiserver's default behaviour; ww does NOT
// add a finalizer-strip dance here — if a CR has a finalizer and no
// controller is running (we just uninstalled the operator), the CR
// will hang. That's a user-visible cleanup step documented as a
// caveat in the `--delete-crds --force` prompt.
func deleteOperatorCRDs(ctx context.Context, dyn dynamic.Interface) error {
	gvr := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	for _, name := range crdNames {
		err := dyn.Resource(gvr).Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete CRD %s: %w", name, err)
		}
	}
	return nil
}
