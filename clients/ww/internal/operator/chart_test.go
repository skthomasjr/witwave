package operator

import (
	"testing"
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
