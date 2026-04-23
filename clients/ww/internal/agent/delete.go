package agent

import (
	"context"
	"fmt"
	"io"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/skthomasjr/witwave/clients/ww/internal/k8s"
)

// DeleteOptions controls the `ww agent delete` flow.
type DeleteOptions struct {
	Name      string
	Namespace string
	AssumeYes bool
	DryRun    bool
	Out       io.Writer
	In        io.Reader
}

// Delete removes the WitwaveAgent CR. The operator handles pod/Service
// teardown via owner references — we don't cascade manually. Prints a
// preflight banner before acting (DESIGN.md KC-4).
func Delete(
	ctx context.Context,
	target *k8s.Target,
	cfg *rest.Config,
	opts DeleteOptions,
) error {
	if opts.Out == nil {
		return fmt.Errorf("DeleteOptions.Out is required")
	}

	plan := []k8s.PlanLine{
		{Key: "Action", Value: fmt.Sprintf("delete WitwaveAgent %q", opts.Name)},
		{Key: "Cascade", Value: "operator removes pods + Services via owner refs"},
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

	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build dynamic client: %w", err)
	}

	if err := dyn.Resource(GVR()).Namespace(opts.Namespace).Delete(ctx, opts.Name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("WitwaveAgent %q not found in namespace %q", opts.Name, opts.Namespace)
		}
		return fmt.Errorf("delete agent: %w", err)
	}
	fmt.Fprintf(opts.Out, "Deleted WitwaveAgent %s from namespace %s.\n", opts.Name, opts.Namespace)
	return nil
}
