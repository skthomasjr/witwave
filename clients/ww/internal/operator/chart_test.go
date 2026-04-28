package operator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestEmbeddedChartVersion(t *testing.T) {
	v, err := EmbeddedChartVersion()
	if err != nil {
		t.Fatalf("EmbeddedChartVersion: %v", err)
	}
	if v == "" {
		t.Fatal("EmbeddedChartVersion returned empty string — chart sync probably missed Chart.yaml")
	}
}

func TestLoadEmbeddedChart(t *testing.T) {
	ch, err := LoadEmbeddedChart()
	if err != nil {
		t.Fatalf("LoadEmbeddedChart: %v", err)
	}
	if ch.Metadata == nil {
		t.Fatal("embedded chart has no metadata")
	}
	if ch.Metadata.Name != "witwave-operator" {
		t.Errorf("chart name = %q; want witwave-operator", ch.Metadata.Name)
	}
	if len(ch.Templates) == 0 {
		t.Error("embedded chart has zero templates — sync is broken")
	}
	// CRDs live alongside templates in chart.CRDObjects() / files at the
	// crds/ path. Make sure we picked them up.
	var foundCRD bool
	for _, f := range ch.Files {
		if len(f.Name) >= 5 && f.Name[:5] == "crds/" {
			foundCRD = true
			break
		}
	}
	// Some Helm SDK versions route crds/ into ch.CRDs() instead of ch.Files.
	if !foundCRD && len(ch.CRDObjects()) == 0 && len(ch.CRDs()) == 0 {
		t.Error("no CRD files found in embedded chart — crds/ directory missing from sync")
	}
}

// TestEmbeddedCRDsCarryHelmKeepAnnotation guards against accidental loss
// of the helm.sh/resource-policy: keep annotation on shipped CRDs. Helm
// uses this annotation to decide whether to delete CRDs on `helm
// uninstall`; without it, removing the operator release tears down every
// WitwaveAgent and WitwavePrompt object in the cluster, which is
// catastrophic for in-place upgrades / accidental uninstalls.
//
// Lives in chart_test.go (rather than a chart-side helm test pod)
// because CRDs ship from the chart's crds/ directory, not its
// templates/, so they are never rendered by `helm template` and cannot
// be asserted by a hook-based test pod.
func TestEmbeddedCRDsCarryHelmKeepAnnotation(t *testing.T) {
	ch, err := LoadEmbeddedChart()
	if err != nil {
		t.Fatalf("LoadEmbeddedChart: %v", err)
	}

	// Collect raw CRD bytes from whichever surface the Helm SDK exposed
	// them on. Newer SDKs route crds/* through CRDObjects(); older ones
	// surface the same files via Files. We accept either.
	type crdBytes struct {
		name string
		data []byte
	}
	var crds []crdBytes
	for _, c := range ch.CRDObjects() {
		crds = append(crds, crdBytes{name: c.Filename, data: c.File.Data})
	}
	if len(crds) == 0 {
		for _, f := range ch.Files {
			if strings.HasPrefix(f.Name, "crds/") {
				crds = append(crds, crdBytes{name: f.Name, data: f.Data})
			}
		}
	}
	if len(crds) == 0 {
		t.Fatal("no CRDs found on embedded chart — sync is broken")
	}

	wantNames := map[string]bool{
		"witwave.ai_witwaveagents.yaml":  false,
		"witwave.ai_witwaveprompts.yaml": false,
		"witwave.ai_workspaces.yaml":     false,
	}

	type meta struct {
		Annotations map[string]string `json:"annotations"`
		Name        string            `json:"name"`
	}
	type crdDoc struct {
		Metadata meta `json:"metadata"`
	}

	for _, c := range crds {
		base := c.name
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if _, ok := wantNames[base]; !ok {
			// Unknown CRD shipped — surface it so we notice when the
			// operator gains a new CRD that should also carry the
			// annotation.
			t.Errorf("unexpected CRD %q in embedded chart; add it to wantNames or this assertion", base)
			continue
		}
		wantNames[base] = true

		// CRDs are single-document YAML; sigs.k8s.io/yaml handles the
		// leading `---` and YAML→JSON conversion in one shot.
		var doc crdDoc
		if err := yaml.Unmarshal(c.data, &doc); err != nil {
			t.Errorf("parse %s: %v", base, err)
			continue
		}
		got, ok := doc.Metadata.Annotations["helm.sh/resource-policy"]
		if !ok {
			t.Errorf("%s: missing helm.sh/resource-policy annotation; uninstall will delete every CR of this kind", base)
			continue
		}
		if got != "keep" {
			t.Errorf("%s: helm.sh/resource-policy = %q; want %q", base, got, "keep")
		}
	}

	for name, seen := range wantNames {
		if !seen {
			t.Errorf("expected CRD %q not found in embedded chart", name)
		}
	}
}
