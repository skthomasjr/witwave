package workspace

import (
	"context"
	"fmt"
	"io"

	"k8s.io/client-go/rest"
)

// UnbindOptions controls `ww workspace unbind <agent> <workspace>`.
type UnbindOptions struct {
	Agent          string
	AgentNamespace string
	WitwaveWorkspace      string

	AssumeYes bool
	DryRun    bool
	Out       io.Writer
	In        io.Reader
}

// Unbind removes the named workspace from a WitwaveAgent.Spec.WorkspaceRefs[].
// Idempotent: unbinding an agent that wasn't bound is a no-op with a
// clear log line. Does NOT delete the WitwaveWorkspace itself — the operator's
// refuse-delete finalizer keeps the workspace alive until every agent
// that references it unbinds, after which `ww workspace delete` cleans up.
func Unbind(ctx context.Context, cfg *rest.Config, opts UnbindOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("UnbindOptions.Out is required")
	}
	if err := ValidateName(opts.WitwaveWorkspace); err != nil {
		return fmt.Errorf("workspace name %q: %w", opts.WitwaveWorkspace, err)
	}
	if opts.Agent == "" {
		return fmt.Errorf("agent name is required")
	}
	if opts.AgentNamespace == "" {
		return fmt.Errorf("agent namespace is required")
	}

	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}
	cr, err := fetchAgentCR(ctx, dyn, opts.AgentNamespace, opts.Agent)
	if err != nil {
		return err
	}

	refs, err := readWorkspaceRefs(cr)
	if err != nil {
		return err
	}
	filtered := make([]map[string]interface{}, 0, len(refs))
	removed := false
	for _, r := range refs {
		if name, _ := r["name"].(string); name == opts.WitwaveWorkspace {
			removed = true
			continue
		}
		filtered = append(filtered, r)
	}

	if !removed {
		fmt.Fprintf(opts.Out, "WitwaveAgent %s/%s is not bound to WitwaveWorkspace %q — no change.\n",
			opts.AgentNamespace, opts.Agent, opts.WitwaveWorkspace)
		return nil
	}

	fmt.Fprintf(opts.Out, "\nAction:    unbind WitwaveAgent %q from WitwaveWorkspace %q in %s\n",
		opts.Agent, opts.WitwaveWorkspace, opts.AgentNamespace)
	fmt.Fprintf(opts.Out, "  was:  %d ref(s) including %q\n", len(refs), opts.WitwaveWorkspace)
	fmt.Fprintf(opts.Out, "  now:  %d ref(s)\n", len(filtered))
	fmt.Fprintln(opts.Out, "  Operator will drop the workspace mounts from the agent's pods on next reconcile.")

	if opts.DryRun {
		fmt.Fprintln(opts.Out, "Dry-run mode — no API calls made.")
		return nil
	}

	if err := writeWorkspaceRefs(cr, filtered); err != nil {
		return fmt.Errorf("set workspaceRefs: %w", err)
	}
	if _, err := updateAgentCR(ctx, dyn, cr); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "WitwaveAgent %s/%s no longer bound to WitwaveWorkspace %q.\n",
		opts.AgentNamespace, opts.Agent, opts.WitwaveWorkspace)
	return nil
}
