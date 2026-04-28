package workspace

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

// OutputFormat selects the rendering shape for `ww workspace list` and
// `ww workspace get`. Default is human-friendly table; -o yaml / -o json
// emit the underlying unstructured shape verbatim so users can pipe into
// kubectl / jq without re-shaping.
type OutputFormat string

const (
	// OutputFormatTable is the default human-friendly columnar render.
	OutputFormatTable OutputFormat = "table"
	// OutputFormatYAML emits sigs.k8s.io/yaml of the underlying object(s).
	OutputFormatYAML OutputFormat = "yaml"
	// OutputFormatJSON emits json.MarshalIndent of the underlying object(s).
	OutputFormatJSON OutputFormat = "json"
)

// ParseOutputFormat normalises the -o flag value. Empty defaults to
// OutputFormatTable. Anything else returns a usage error.
func ParseOutputFormat(raw string) (OutputFormat, error) {
	switch raw {
	case "", "table":
		return OutputFormatTable, nil
	case "yaml":
		return OutputFormatYAML, nil
	case "json":
		return OutputFormatJSON, nil
	}
	return "", fmt.Errorf("unsupported output format %q (valid: table, yaml, json)", raw)
}

// ListOptions controls which WitwaveWorkspace CRs are returned.
type ListOptions struct {
	Namespace     string
	AllNamespaces bool
	Output        OutputFormat
	Out           io.Writer
}

// WitwaveWorkspaceSummary is a render-ready view of one WitwaveWorkspace. Flat enough
// for a tabwriter row; retains a pointer to the raw unstructured object
// so YAML / JSON emission doesn't need a second round-trip.
type WitwaveWorkspaceSummary struct {
	Namespace string
	Name      string
	// Volumes counts spec.volumes[].
	Volumes int
	// BoundAgents counts status.boundAgents[].
	BoundAgents int
	// Phase is derived from status.conditions[type=Ready] — "Ready" when
	// True, "Pending" until the controller updates the field, otherwise
	// the condition's reason.
	Phase string
	// Created is the CR's creation timestamp.
	Created time.Time
	// Raw is the underlying CR for downstream YAML / JSON rendering.
	Raw *unstructured.Unstructured
}

// ListWitwaveWorkspaces returns summaries for the workspaces in scope. Shared
// data path between `ww workspace list` and any future TUI surface.
func ListWitwaveWorkspaces(ctx context.Context, cfg *rest.Config, opts ListOptions) ([]WitwaveWorkspaceSummary, error) {
	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return nil, err
	}
	var (
		items *unstructured.UnstructuredList
	)
	if opts.AllNamespaces {
		items, err = dyn.Resource(GVR()).List(ctx, metav1.ListOptions{})
	} else {
		items, err = dyn.Resource(GVR()).Namespace(opts.Namespace).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	out := make([]WitwaveWorkspaceSummary, 0, len(items.Items))
	for i := range items.Items {
		out = append(out, workspaceSummary(&items.Items[i]))
	}
	return out, nil
}

func workspaceSummary(cr *unstructured.Unstructured) WitwaveWorkspaceSummary {
	s := WitwaveWorkspaceSummary{
		Namespace: cr.GetNamespace(),
		Name:      cr.GetName(),
		Created:   cr.GetCreationTimestamp().Time,
		Phase:     readPhase(cr),
		Raw:       cr,
	}
	if vols, found, err := unstructured.NestedSlice(cr.Object, "spec", "volumes"); err == nil && found {
		s.Volumes = len(vols)
	}
	if agents, found, err := unstructured.NestedSlice(cr.Object, "status", "boundAgents"); err == nil && found {
		s.BoundAgents = len(agents)
	}
	if s.Phase == "" {
		s.Phase = "Pending"
	}
	return s
}

// readPhase derives a phase label from the CR's status.conditions[].
// Looks for the canonical Ready condition (mirrors the controller's
// WitwaveWorkspaceConditionReady constant). Returns "" when no conditions are
// recorded yet — caller substitutes "Pending".
func readPhase(cr *unstructured.Unstructured) string {
	conds, found, err := unstructured.NestedSlice(cr.Object, "status", "conditions")
	if err != nil || !found {
		return ""
	}
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ != "Ready" {
			continue
		}
		status, _ := m["status"].(string)
		if status == "True" {
			return "Ready"
		}
		if reason, _ := m["reason"].(string); reason != "" {
			return reason
		}
		return "NotReady"
	}
	return ""
}

// List renders WitwaveWorkspace CRs to opts.Out in the requested OutputFormat.
// table is default; yaml / json emit the raw underlying objects so users
// can pipe to kubectl / jq.
func List(ctx context.Context, cfg *rest.Config, opts ListOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("ListOptions.Out is required")
	}
	summaries, err := ListWitwaveWorkspaces(ctx, cfg, opts)
	if err != nil {
		return err
	}

	switch opts.Output {
	case OutputFormatYAML, OutputFormatJSON:
		return emitListMachine(opts.Out, summaries, opts.Output)
	}

	if len(summaries) == 0 {
		if opts.AllNamespaces {
			fmt.Fprintln(opts.Out, "No WitwaveWorkspaces found in any namespace.")
		} else {
			fmt.Fprintf(opts.Out, "No WitwaveWorkspaces found in namespace %q.\n", opts.Namespace)
		}
		return nil
	}
	tw := tabwriter.NewWriter(opts.Out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAMESPACE\tNAME\tVOLUMES\tAGENTS\tAGE\tSTATUS")
	for _, s := range summaries {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n",
			s.Namespace, s.Name, s.Volumes, s.BoundAgents, FormatAge(s.Created), s.Phase,
		)
	}
	return tw.Flush()
}

// emitListMachine emits the list as a single Kubernetes-style List
// envelope ({apiVersion, kind: WitwaveWorkspaceList, items: [...]}) so a `ww
// workspace list -o yaml | kubectl apply -f -` round-trip holds.
func emitListMachine(out io.Writer, summaries []WitwaveWorkspaceSummary, format OutputFormat) error {
	items := make([]interface{}, 0, len(summaries))
	for _, s := range summaries {
		items = append(items, s.Raw.Object)
	}
	envelope := map[string]interface{}{
		"apiVersion": APIVersionString(),
		"kind":       Kind + "List",
		"items":      items,
	}
	return emitMachine(out, envelope, format)
}

// emitMachine is the shared yaml/json emitter for List + Get.
func emitMachine(out io.Writer, v interface{}, format OutputFormat) error {
	switch format {
	case OutputFormatYAML:
		buf, err := yaml.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal yaml: %w", err)
		}
		_, err = out.Write(buf)
		return err
	case OutputFormatJSON:
		buf, err := jsonMarshalIndent(v)
		if err != nil {
			return fmt.Errorf("marshal json: %w", err)
		}
		buf = append(buf, '\n')
		_, err = out.Write(buf)
		return err
	}
	return fmt.Errorf("unsupported output format %q", format)
}

// FormatAge mirrors kubectl's age column: 10s, 5m, 2h, 3d. Identical
// shape to internal/agent.FormatAge so workspace + agent tables read the
// same way.
func FormatAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
