// Tests for the output writer's mode selection + YAML support (#1707).
package output

import (
	"bytes"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// ----- mode selection -------------------------------------------------------

func TestNewDefaultsToHuman(t *testing.T) {
	w := New(&bytes.Buffer{}, &bytes.Buffer{}, false, false, false)
	if w.Mode != ModeHuman {
		t.Errorf("Mode: want ModeHuman, got %v", w.Mode)
	}
	if w.IsJSON() || w.IsYAML() {
		t.Errorf("IsJSON/IsYAML should both be false in human mode")
	}
}

func TestNewJSONFlagSetsModeJSON(t *testing.T) {
	w := New(&bytes.Buffer{}, &bytes.Buffer{}, true, false, false)
	if w.Mode != ModeJSON {
		t.Errorf("Mode: want ModeJSON, got %v", w.Mode)
	}
	if !w.IsJSON() {
		t.Errorf("IsJSON should be true in JSON mode")
	}
}

func TestNewJSONCompactFlagSetsModeJSONCompact(t *testing.T) {
	w := New(&bytes.Buffer{}, &bytes.Buffer{}, true, true, false)
	if w.Mode != ModeJSONCompact {
		t.Errorf("Mode: want ModeJSONCompact, got %v", w.Mode)
	}
	if !w.IsJSON() {
		t.Errorf("IsJSON should be true in compact JSON mode (covers both JSON variants)")
	}
}

func TestNewYAMLFlagSetsModeYAML(t *testing.T) {
	w := New(&bytes.Buffer{}, &bytes.Buffer{}, false, false, true)
	if w.Mode != ModeYAML {
		t.Errorf("Mode: want ModeYAML, got %v", w.Mode)
	}
	if !w.IsYAML() {
		t.Errorf("IsYAML should be true in YAML mode")
	}
	if w.IsJSON() {
		t.Errorf("IsJSON should be false in YAML mode")
	}
}

func TestNewYAMLBeatsJSONWhenBothSet(t *testing.T) {
	// Documented behavior: YAML wins over JSON when both flags are set.
	// Caller is expected to validate flag mutex upstream — this guards
	// against silently producing JSON when user asked for YAML.
	w := New(&bytes.Buffer{}, &bytes.Buffer{}, true, false, true)
	if w.Mode != ModeYAML {
		t.Errorf("YAML must win over JSON when both flags set; got %v", w.Mode)
	}
}

// ----- EmitYAML -------------------------------------------------------------

func TestEmitYAMLProducesParseableOutput(t *testing.T) {
	stdout := &bytes.Buffer{}
	w := New(stdout, &bytes.Buffer{}, false, false, true)
	type entry struct {
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
	}
	in := []entry{
		{Name: "daily", Schedule: "0 9 * * *"},
		{Name: "weekly", Schedule: "0 9 * * 1"},
	}
	if err := w.EmitYAML(in); err != nil {
		t.Fatalf("EmitYAML returned err: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "name: daily") {
		t.Errorf("YAML missing expected `name: daily` line; got: %s", out)
	}
	// Round-trip: parse the YAML back and verify the structure survived.
	var roundTrip []entry
	if err := yaml.Unmarshal(stdout.Bytes(), &roundTrip); err != nil {
		t.Fatalf("emitted YAML failed to parse: %v\noutput:\n%s", err, out)
	}
	if len(roundTrip) != 2 || roundTrip[0].Name != "daily" || roundTrip[1].Schedule != "0 9 * * 1" {
		t.Errorf("round-trip lost structure: %+v", roundTrip)
	}
}

func TestEmitYAMLUsesJSONFieldTags(t *testing.T) {
	// sigs.k8s.io/yaml uses the json: struct tags. Verify the camelCase
	// output kubectl/helm operators expect.
	stdout := &bytes.Buffer{}
	w := New(stdout, &bytes.Buffer{}, false, false, true)
	type item struct {
		MaxTokens int `json:"maxTokens"`
	}
	if err := w.EmitYAML(item{MaxTokens: 4096}); err != nil {
		t.Fatalf("EmitYAML returned err: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "maxTokens:") {
		t.Errorf("YAML should use the JSON tag (maxTokens), got: %s", out)
	}
}
