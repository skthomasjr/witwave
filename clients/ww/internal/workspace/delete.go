package workspace

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"k8s.io/client-go/rest"

	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
)

// DeleteOptions controls the `ww workspace delete` flow.
type DeleteOptions struct {
	Name      string
	Namespace string

	// AssumeYes skips the confirmation prompt. --yes / WW_ASSUME_YES.
	AssumeYes bool
	// DryRun prints the plan and exits without calling the API server.
	DryRun bool
	// Wait blocks until the apiserver actually removes the CR. The
	// Workspace's refuse-delete finalizer means a delete blocks while any
	// agent still references it; --wait surfaces that visibly with a
	// progress dot per poll. Bounded by WaitTimeout.
	Wait        bool
	WaitTimeout time.Duration

	Out io.Writer
	In  io.Reader
}

// Delete removes the Workspace CR. The operator's refuse-delete-from-
// finalizer means a delete request blocks (kubectl-style "Terminating")
// while any WitwaveAgent still references it via spec.workspaceRefs[].
//
// We surface that case explicitly: the plan banner enumerates the
// currently-bound agents so the user knows up-front that the delete
// will hang until they unbind. With --wait we tail the CR until the
// apiserver finishes removing it (or the timeout fires).
func Delete(
	ctx context.Context,
	target *k8s.Target,
	cfg *rest.Config,
	opts DeleteOptions,
) error {
	if opts.Out == nil {
		return fmt.Errorf("DeleteOptions.Out is required")
	}
	if err := ValidateName(opts.Name); err != nil {
		return err
	}

	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}

	// Fetch the CR up-front so the banner can describe the bound-agents
	// blast radius (the operator refuses-delete while any are bound) and
	// a clean "not found" beats the one from the Delete call.
	cr, err := fetchWorkspaceCR(ctx, dyn, opts.Namespace, opts.Name)
	if err != nil {
		return err
	}

	bound := boundAgentsFromStatus(cr)

	plan := []k8s.PlanLine{
		{Key: "Action", Value: fmt.Sprintf("delete Workspace %q", opts.Name)},
	}
	if len(bound) == 0 {
		plan = append(plan, k8s.PlanLine{
			Key:   "Bound agents",
			Value: "(none — delete will complete immediately)",
		})
	} else {
		plan = append(plan, k8s.PlanLine{
			Key:   "Bound agents",
			Value: fmt.Sprintf("%d still bound — delete will block (refuse-delete finalizer)", len(bound)),
		})
		plan = append(plan, k8s.PlanLine{
			Key:   "Agents",
			Value: formatBoundAgents(bound),
		})
		plan = append(plan, k8s.PlanLine{
			Key:   "Hint",
			Value: "unbind first: `ww workspace unbind <agent> " + opts.Name + "`",
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

	if err := dyn.Resource(GVR()).Namespace(opts.Namespace).Delete(ctx, opts.Name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("Workspace %q not found in namespace %q", opts.Name, opts.Namespace)
		}
		return fmt.Errorf("delete workspace: %w", err)
	}
	fmt.Fprintf(opts.Out, "Delete request accepted for Workspace %s/%s.\n", opts.Namespace, opts.Name)

	if len(bound) > 0 {
		fmt.Fprintf(opts.Out, "Workspace will remain in Terminating until %d bound agent(s) unbind.\n", len(bound))
	}

	if !opts.Wait {
		fmt.Fprintln(opts.Out, "Skipping deletion wait. Check with `ww workspace status "+opts.Name+"` (404 means it's gone).")
		return nil
	}

	timeout := opts.WaitTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	fmt.Fprintf(opts.Out, "Waiting up to %s for the Workspace to be removed...\n", timeout)
	if err := waitForGone(ctx, dyn, opts.Namespace, opts.Name, timeout, opts.Out); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "Workspace %s/%s deleted.\n", opts.Namespace, opts.Name)
	return nil
}

// waitForGone polls the CR until a Get returns NotFound or the timeout
// elapses. Poll interval is modest (2s) — same cadence as the agent
// readiness wait.
func waitForGone(ctx context.Context, dyn dynamic.Interface, ns, name string, timeout time.Duration, out io.Writer) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for Workspace %q to be removed (still bound to one or more agents?)", timeout, name)
		}
		_, err := dyn.Resource(GVR()).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			fmt.Fprintf(out, "  (get failed: %v; retrying)\n", err)
		} else {
			fmt.Fprint(out, ".")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait cancelled")
		case <-time.After(2 * time.Second):
		}
	}
}

// boundAgentsFromStatus extracts the (namespace, name) pairs from the CR's
// status.boundAgents[]. Returns an empty slice when the field is missing
// or malformed.
func boundAgentsFromStatus(cr *unstructured.Unstructured) []boundAgent {
	raw, found, err := unstructured.NestedSlice(cr.Object, "status", "boundAgents")
	if err != nil || !found {
		return nil
	}
	out := make([]boundAgent, 0, len(raw))
	for _, a := range raw {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		ns, _ := m["namespace"].(string)
		if name == "" {
			continue
		}
		out = append(out, boundAgent{name: name, namespace: ns})
	}
	return out
}

type boundAgent struct {
	name      string
	namespace string
}

func formatBoundAgents(bound []boundAgent) string {
	parts := make([]string, 0, len(bound))
	for _, b := range bound {
		if b.namespace == "" {
			parts = append(parts, b.name)
		} else {
			parts = append(parts, b.namespace+"/"+b.name)
		}
	}
	return strings.Join(parts, ", ")
}
