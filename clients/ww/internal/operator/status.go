package operator

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Status is the fully-assembled report `ww operator status` prints.
// Populated by GatherStatus; rendered by (*Status).Render.
type Status struct {
	Namespace string
	Release   *ReleaseInfo // nil when not installed here
	Pods      []PodSummary
	CRDs      []CRDInfo
	CRCounts  map[string]int
	// WWVersion is the ww binary version (populated by the caller —
	// cmd/version.go's Version constant). Used only for the skew display.
	WWVersion string
}

// GatherStatus performs the read-only probes for the status command.
// Requires a REST config; does NOT mutate cluster state. All probes are
// best-effort — pod list failures, CRD list failures etc. are surfaced
// as structured errors but partial results are still returned so users
// see whatever we could gather.
func GatherStatus(ctx context.Context, cfg *rest.Config, ns, wwVersion string) (*Status, error) {
	k8s, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}

	s := &Status{Namespace: ns, WWVersion: wwVersion}

	rel, err := LookupRelease(ctx, k8s, ns, ReleaseName)
	if err != nil {
		return s, err
	}
	s.Release = rel

	pods, err := ListOperatorPods(ctx, k8s, ns)
	if err != nil {
		return s, err
	}
	s.Pods = pods

	crds, err := InspectCRDs(ctx, dyn)
	if err != nil {
		return s, err
	}
	s.CRDs = crds

	counts, err := CountCRs(ctx, dyn)
	if err != nil {
		return s, err
	}
	s.CRCounts = counts

	return s, nil
}

// Render writes a human-readable block describing the operator's state
// to `out`. Matches the shape committed in issue #1477.
func (s *Status) Render(out io.Writer) {
	fmt.Fprintln(out, "Witwave Operator")

	// Install block
	fmt.Fprintf(out, "  Namespace:      %s\n", s.Namespace)
	if s.Release == nil {
		fmt.Fprintln(out, "  Release:        (not installed)")
	} else {
		fmt.Fprintf(out, "  Release:        %s (Helm, rev %d, %s)\n",
			s.Release.Name, s.Release.Revision, s.Release.Status)
		fmt.Fprintf(out, "  Chart version:  %s\n", s.Release.ChartVersion)
		if s.Release.AppVersion != "" {
			fmt.Fprintf(out, "  App version:    %s\n", s.Release.AppVersion)
		}
	}
	if s.WWVersion != "" {
		fmt.Fprintf(out, "  ww version:     %s  %s\n", s.WWVersion, skewLabel(s.WWVersion, s.Release))
	}
	fmt.Fprintln(out)

	// Pods block
	fmt.Fprintln(out, "Pods")
	if len(s.Pods) == 0 {
		fmt.Fprintln(out, "  (none matching app.kubernetes.io/name=witwave-operator)")
	} else {
		tw := tabwriter.NewWriter(out, 2, 2, 2, ' ', 0)
		for _, p := range s.Pods {
			leader := ""
			if p.IsLeader {
				leader = " (leader)"
			}
			fmt.Fprintf(tw, "  %s\t%s%s\n", p.Name, p.Phase, leader)
		}
		_ = tw.Flush()
	}
	fmt.Fprintln(out)

	// CRDs block
	fmt.Fprintln(out, "CRDs")
	for _, c := range s.CRDs {
		if !c.Found {
			fmt.Fprintf(out, "  %-36s (absent)\n", c.Name)
			continue
		}
		fmt.Fprintf(out, "  %-36s %s\n", c.Name, joinVersions(c.Versions))
	}
	fmt.Fprintln(out)

	// Reconciles block
	fmt.Fprintln(out, "Reconciles managed")
	for _, kind := range []string{"WitwaveAgent", "WitwavePrompt"} {
		n, ok := s.CRCounts[kind]
		if !ok {
			continue
		}
		fmt.Fprintf(out, "  %-15s %d\n", kind+":", n)
	}
}

// skewLabel returns the "(match)" / "(patch skew)" / "(minor skew)" /
// "(major skew)" decoration for the ww-version line. Naïve string
// compare for now — we bump to proper semver comparison when we start
// enforcing skew policy.
func skewLabel(wwVersion string, rel *ReleaseInfo) string {
	if rel == nil || rel.AppVersion == "" {
		return ""
	}
	ww := stripV(wwVersion)
	rv := stripV(rel.AppVersion)
	if ww == rv {
		return "(match)"
	}
	// Very coarse — any mismatch shows as "skew" for the status page; the
	// upgrade command does the fine-grained check.
	return "(skew)"
}

func stripV(s string) string {
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		return s[1:]
	}
	return s
}

func joinVersions(vs []string) string {
	if len(vs) == 0 {
		return "(no versions reported)"
	}
	out := ""
	for i, v := range vs {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}
