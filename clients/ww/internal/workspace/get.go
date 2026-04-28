package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"k8s.io/client-go/rest"
)

// jsonMarshalIndent is package-internal so emitMachine can reuse it
// without importing encoding/json into a half-dozen files.
func jsonMarshalIndent(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// GetOptions controls `ww workspace get`.
type GetOptions struct {
	Name      string
	Namespace string
	Output    OutputFormat
	Out       io.Writer
}

// Get fetches a single Workspace and emits it in the requested
// OutputFormat. Default (table) is identical to the list-of-one
// rendering — consistent with how `kubectl get ws <name>` behaves.
func Get(ctx context.Context, cfg *rest.Config, opts GetOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("GetOptions.Out is required")
	}
	if err := ValidateName(opts.Name); err != nil {
		return err
	}
	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}
	cr, err := fetchWorkspaceCR(ctx, dyn, opts.Namespace, opts.Name)
	if err != nil {
		return err
	}

	switch opts.Output {
	case OutputFormatYAML, OutputFormatJSON:
		return emitMachine(opts.Out, cr.Object, opts.Output)
	}
	// Default: same column shape as `ww workspace list`, scoped to one row.
	s := workspaceSummary(cr)
	tw := tabwriter.NewWriter(opts.Out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAMESPACE\tNAME\tVOLUMES\tAGENTS\tAGE\tSTATUS")
	fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n",
		s.Namespace, s.Name, s.Volumes, s.BoundAgents, FormatAge(s.Created), s.Phase,
	)
	return tw.Flush()
}
