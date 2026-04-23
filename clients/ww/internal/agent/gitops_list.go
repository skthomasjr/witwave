package agent

import (
	"context"
	"fmt"
	"io"

	"k8s.io/client-go/rest"
)

// GitListOptions are inputs to `ww agent git list`.
type GitListOptions struct {
	Agent     string
	Namespace string
	Out       io.Writer
}

// GitList prints the agent's configured gitSyncs + mappings in a
// human-readable form. Missing gitSyncs section → friendly "no gitSync
// configured" one-liner with a hint to the attach verb.
func GitList(ctx context.Context, cfg *rest.Config, opts GitListOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("GitListOptions.Out is required")
	}
	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}
	cr, err := fetchAgentCR(ctx, dyn, opts.Namespace, opts.Agent)
	if err != nil {
		return err
	}

	syncs, err := readGitSyncs(cr)
	if err != nil {
		return err
	}
	if len(syncs) == 0 {
		fmt.Fprintf(opts.Out,
			"WitwaveAgent %s/%s has no gitSyncs configured.\n"+
				"Attach one with: ww agent git add %s --repo <owner/repo>\n",
			opts.Namespace, opts.Agent, opts.Agent,
		)
		return nil
	}

	fmt.Fprintf(opts.Out, "WitwaveAgent %s/%s gitSyncs:\n", opts.Namespace, opts.Agent)
	for _, s := range syncs {
		renderGitSyncSummary(opts.Out, s)
	}

	// Surface the mappings too — users want to see where files are
	// actually being materialised, not just that sync entries exist.
	harnessMaps, err := readHarnessGitMappings(cr)
	if err != nil {
		return err
	}
	if len(harnessMaps) > 0 {
		fmt.Fprintln(opts.Out, "\n  Harness mappings:")
		for _, m := range harnessMaps {
			sync, _ := m["gitSync"].(string)
			src, _ := m["src"].(string)
			dest, _ := m["dest"].(string)
			fmt.Fprintf(opts.Out, "    [%s] %s → %s\n", sync, src, dest)
		}
	}

	backends, err := readBackends(cr)
	if err != nil {
		return err
	}
	for _, b := range backends {
		name, _ := b["name"].(string)
		mappings, _ := b["gitMappings"].([]interface{})
		if len(mappings) == 0 {
			continue
		}
		fmt.Fprintf(opts.Out, "\n  Backend %s mappings:\n", name)
		for _, raw := range mappings {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			sync, _ := m["gitSync"].(string)
			src, _ := m["src"].(string)
			dest, _ := m["dest"].(string)
			fmt.Fprintf(opts.Out, "    [%s] %s → %s\n", sync, src, dest)
		}
	}
	return nil
}
