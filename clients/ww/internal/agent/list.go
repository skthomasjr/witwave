package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// ListOptions controls which WitwaveAgent CRs are returned.
type ListOptions struct {
	// Namespace to list. Empty string + AllNamespaces=true → cluster-wide.
	Namespace     string
	AllNamespaces bool
	Out           io.Writer
}

// List renders a table of WitwaveAgent CRs to opts.Out. Columns match
// the CRD's additionalPrinterColumns so operators see the same fields
// they'd see from `kubectl get wwa`.
func List(ctx context.Context, cfg *rest.Config, opts ListOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("ListOptions.Out is required")
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build dynamic client: %w", err)
	}

	var items *unstructured.UnstructuredList
	if opts.AllNamespaces {
		items, err = dyn.Resource(GVR()).List(ctx, metav1.ListOptions{})
	} else {
		items, err = dyn.Resource(GVR()).Namespace(opts.Namespace).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}

	if len(items.Items) == 0 {
		if opts.AllNamespaces {
			fmt.Fprintln(opts.Out, "No WitwaveAgents found in any namespace.")
		} else {
			fmt.Fprintf(opts.Out, "No WitwaveAgents found in namespace %q.\n", opts.Namespace)
		}
		return nil
	}

	tw := tabwriter.NewWriter(opts.Out, 0, 2, 2, ' ', 0)
	if opts.AllNamespaces {
		fmt.Fprintln(tw, "NAMESPACE\tNAME\tPHASE\tREADY\tBACKENDS\tAGE")
	} else {
		fmt.Fprintln(tw, "NAME\tPHASE\tREADY\tBACKENDS\tAGE")
	}
	for i := range items.Items {
		row := agentRow(&items.Items[i])
		if opts.AllNamespaces {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				row.namespace, row.name, row.phase, row.ready, row.backends, row.age,
			)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				row.name, row.phase, row.ready, row.backends, row.age,
			)
		}
	}
	return tw.Flush()
}

type row struct {
	namespace, name, phase, ready, backends, age string
}

func agentRow(cr *unstructured.Unstructured) row {
	r := row{
		namespace: cr.GetNamespace(),
		name:      cr.GetName(),
		phase:     readPhase(cr),
		age:       formatAge(cr.GetCreationTimestamp().Time),
	}
	if r.phase == "" {
		r.phase = "Pending"
	}

	// status.readyReplicas is int in the CRD; render as a string for table
	// symmetry with other columns. Missing → "0".
	if v, found, err := unstructured.NestedInt64(cr.Object, "status", "readyReplicas"); err == nil && found {
		r.ready = fmt.Sprintf("%d", v)
	} else {
		r.ready = "0"
	}

	// spec.backends[*].name joined with commas — matches the CRD's
	// `.spec.backends[*].name` additionalPrinterColumn.
	if backends, found, err := unstructured.NestedSlice(cr.Object, "spec", "backends"); err == nil && found {
		var names []string
		for _, b := range backends {
			m, ok := b.(map[string]interface{})
			if !ok {
				continue
			}
			if n, ok := m["name"].(string); ok {
				names = append(names, n)
			}
		}
		r.backends = strings.Join(names, ",")
	}
	if r.backends == "" {
		r.backends = "-"
	}
	return r
}

// formatAge mirrors kubectl's age column: 10s, 5m, 2h, 3d. Intentionally
// lossy — the precise timestamp is available via `ww agent status`.
func formatAge(t time.Time) string {
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
