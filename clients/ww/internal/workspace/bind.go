package workspace

import (
	"context"
	"fmt"
	"io"

	"k8s.io/client-go/rest"
)

// BindOptions controls `ww workspace bind <agent> <workspace>`.
//
// AgentNamespace and WitwaveWorkspaceNamespace are tracked separately because
// future v1.x cross-namespace binding is plausible — the operator's
// status.boundAgents schema already records namespace explicitly so the
// CLI can flag mismatches without a CRD change later. v1alpha1 only
// supports same-namespace binding (the operator matches `metadata.namespace
// == workspaceRef.namespace` only); we reject cross-namespace asks
// loudly here so users see the limitation up-front.
type BindOptions struct {
	Agent                     string
	AgentNamespace            string
	WitwaveWorkspace          string
	WitwaveWorkspaceNamespace string

	AssumeYes bool
	DryRun    bool
	Out       io.Writer
	In        io.Reader
}

// Bind adds the named workspace to a WitwaveAgent.Spec.WorkspaceRefs[].
// Idempotent: re-binding the same (agent, workspace) is a no-op with a
// clear log line. Verifies the workspace exists before mutating the
// agent so a typo doesn't write a dangling reference.
func Bind(ctx context.Context, cfg *rest.Config, opts BindOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("BindOptions.Out is required")
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
	wsNS := opts.WitwaveWorkspaceNamespace
	if wsNS == "" {
		wsNS = opts.AgentNamespace
	}
	if wsNS != opts.AgentNamespace {
		return fmt.Errorf(
			"cross-namespace binding not supported in v1alpha1 (agent in %q, workspace in %q); the operator only matches same-namespace refs",
			opts.AgentNamespace, wsNS,
		)
	}

	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}

	// Verify the workspace exists in the same namespace before touching
	// the agent — otherwise a typo would happily write a dangling ref the
	// operator silently ignores.
	if _, err := fetchWitwaveWorkspaceCR(ctx, dyn, wsNS, opts.WitwaveWorkspace); err != nil {
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
	for _, r := range refs {
		if name, _ := r["name"].(string); name == opts.WitwaveWorkspace {
			fmt.Fprintf(opts.Out, "WitwaveAgent %s/%s is already bound to WitwaveWorkspace %q — no change.\n",
				opts.AgentNamespace, opts.Agent, opts.WitwaveWorkspace)
			return nil
		}
	}

	fmt.Fprintf(opts.Out, "\nAction:    bind WitwaveAgent %q to WitwaveWorkspace %q in %s\n",
		opts.Agent, opts.WitwaveWorkspace, opts.AgentNamespace)
	if len(refs) == 0 {
		fmt.Fprintln(opts.Out, "  was:  (no workspaceRefs)")
	} else {
		fmt.Fprintf(opts.Out, "  was:  %d existing ref(s)\n", len(refs))
	}
	fmt.Fprintf(opts.Out, "  now:  + workspaceRefs[name=%q]\n", opts.WitwaveWorkspace)
	fmt.Fprintln(opts.Out, "  Operator will reconcile workspace mounts onto the agent's pods.")

	if opts.DryRun {
		fmt.Fprintln(opts.Out, "Dry-run mode — no API calls made.")
		return nil
	}

	refs = append(refs, map[string]interface{}{"name": opts.WitwaveWorkspace})
	if err := writeWorkspaceRefs(cr, refs); err != nil {
		return fmt.Errorf("set workspaceRefs: %w", err)
	}
	if _, err := updateAgentCR(ctx, dyn, cr); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "WitwaveAgent %s/%s now bound to WitwaveWorkspace %q.\n",
		opts.AgentNamespace, opts.Agent, opts.WitwaveWorkspace)
	return nil
}
