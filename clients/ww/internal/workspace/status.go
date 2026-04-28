package workspace

import (
	"context"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

// StatusOptions controls the `ww workspace status` rendering.
type StatusOptions struct {
	Name      string
	Namespace string
	Out       io.Writer
}

// Status fetches the Workspace CR and prints a curated, human-readable
// view: identity, volumes (with reclaim policy), conditions, and bound
// agents. Mirrors the shape of `ww operator status` so operators see the
// same structure across the two CR families.
func Status(ctx context.Context, cfg *rest.Config, opts StatusOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("StatusOptions.Out is required")
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
	renderStatus(opts.Out, cr)
	return nil
}

func renderStatus(out io.Writer, cr *unstructured.Unstructured) {
	name := cr.GetName()
	ns := cr.GetNamespace()
	phase := readPhase(cr)
	if phase == "" {
		phase = "Pending"
	}

	fmt.Fprintf(out, "Workspace: %s\n", name)
	fmt.Fprintf(out, "Namespace: %s\n", ns)
	fmt.Fprintf(out, "Phase:     %s\n", phase)
	if ts := cr.GetCreationTimestamp(); !ts.IsZero() {
		fmt.Fprintf(out, "Age:       %s\n", FormatAge(ts.Time))
	}
	fmt.Fprintln(out)

	renderVolumes(out, cr)
	renderSecrets(out, cr)
	renderConfigFiles(out, cr)
	renderConditions(out, cr)
	renderBoundAgents(out, cr)
}

func renderVolumes(out io.Writer, cr *unstructured.Unstructured) {
	vols, found, err := unstructured.NestedSlice(cr.Object, "spec", "volumes")
	if err != nil || !found || len(vols) == 0 {
		fmt.Fprintln(out, "Volumes: (none)")
		fmt.Fprintln(out)
		return
	}
	fmt.Fprintln(out, "Volumes:")
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  NAME\tSIZE\tCLASS\tACCESS\tRECLAIM\tMOUNT")
	for _, v := range vols {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		size, _ := m["size"].(string)
		if size == "" {
			size = "-"
		}
		class, _ := m["storageClassName"].(string)
		if class == "" {
			class = "(default)"
		}
		access, _ := m["accessMode"].(string)
		if access == "" {
			access = "ReadWriteMany"
		}
		reclaim, _ := m["reclaimPolicy"].(string)
		if reclaim == "" {
			reclaim = "Delete"
		}
		mount, _ := m["mountPath"].(string)
		if mount == "" {
			mount = "(derived)"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", name, size, class, access, reclaim, mount)
	}
	_ = tw.Flush()
	fmt.Fprintln(out)
}

func renderSecrets(out io.Writer, cr *unstructured.Unstructured) {
	items, found, err := unstructured.NestedSlice(cr.Object, "spec", "secrets")
	if err != nil || !found || len(items) == 0 {
		return
	}
	fmt.Fprintln(out, "Secrets:")
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  NAME\tMODE")
	for _, s := range items {
		m, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		mode := "(reference only)"
		if mp, _ := m["mountPath"].(string); mp != "" {
			mode = "mount " + mp
		} else if env, _ := m["envFrom"].(bool); env {
			mode = "envFrom"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", name, mode)
	}
	_ = tw.Flush()
	fmt.Fprintln(out)
}

func renderConfigFiles(out io.Writer, cr *unstructured.Unstructured) {
	items, found, err := unstructured.NestedSlice(cr.Object, "spec", "configFiles")
	if err != nil || !found || len(items) == 0 {
		return
	}
	fmt.Fprintln(out, "ConfigFiles:")
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  SOURCE\tMOUNT")
	for _, c := range items {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		source := "(unknown)"
		if cm, _ := m["configMap"].(string); cm != "" {
			source = "configMap " + cm
		} else if inline, ok := m["inline"].(map[string]interface{}); ok {
			if n, _ := inline["name"].(string); n != "" {
				source = "inline " + n
			} else {
				source = "inline"
			}
		}
		mount, _ := m["mountPath"].(string)
		if sub, _ := m["subPath"].(string); sub != "" {
			mount = mount + " (subPath=" + sub + ")"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", source, mount)
	}
	_ = tw.Flush()
	fmt.Fprintln(out)
}

func renderConditions(out io.Writer, cr *unstructured.Unstructured) {
	conds, found, err := unstructured.NestedSlice(cr.Object, "status", "conditions")
	if err != nil || !found || len(conds) == 0 {
		fmt.Fprintln(out, "Conditions: (none — controller hasn't reconciled yet)")
		fmt.Fprintln(out)
		return
	}
	fmt.Fprintln(out, "Conditions:")
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  TYPE\tSTATUS\tREASON\tMESSAGE")
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		status, _ := m["status"].(string)
		reason, _ := m["reason"].(string)
		if reason == "" {
			reason = "-"
		}
		msg, _ := m["message"].(string)
		if msg == "" {
			msg = "-"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", typ, status, reason, msg)
	}
	_ = tw.Flush()
	fmt.Fprintln(out)
}

func renderBoundAgents(out io.Writer, cr *unstructured.Unstructured) {
	agents, found, err := unstructured.NestedSlice(cr.Object, "status", "boundAgents")
	if err != nil || !found || len(agents) == 0 {
		fmt.Fprintln(out, "Bound agents: (none — bind one with `ww workspace bind <agent> "+cr.GetName()+"`)")
		return
	}
	type agentRef struct{ name, namespace string }
	rows := make([]agentRef, 0, len(agents))
	for _, a := range agents {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		n, _ := m["name"].(string)
		ns, _ := m["namespace"].(string)
		rows = append(rows, agentRef{name: n, namespace: ns})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].namespace != rows[j].namespace {
			return rows[i].namespace < rows[j].namespace
		}
		return rows[i].name < rows[j].name
	})
	fmt.Fprintf(out, "Bound agents (%d):\n", len(rows))
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  NAMESPACE\tNAME")
	for _, r := range rows {
		fmt.Fprintf(tw, "  %s\t%s\n", r.namespace, r.name)
	}
	_ = tw.Flush()
}
