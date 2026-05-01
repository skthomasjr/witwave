package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// resolveDesiredTags — priority chain (Tag → Harness/Backend → CLIVersion)
// ---------------------------------------------------------------------------

func TestResolveDesiredTags_TagWinsOverEverything(t *testing.T) {
	current := imageTagSnapshot{
		HarnessTag:  "0.11.13",
		BackendTags: map[string]string{"claude": "0.11.13"},
	}
	got, err := resolveDesiredTags(current, UpgradeOptions{
		Tag:        "0.11.14",
		CLIVersion: "0.11.10", // should be ignored
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.HarnessTag != "0.11.14" {
		t.Errorf("HarnessTag = %q; want 0.11.14", got.HarnessTag)
	}
	if got.BackendTags["claude"] != "0.11.14" {
		t.Errorf("BackendTags[claude] = %q; want 0.11.14", got.BackendTags["claude"])
	}
}

func TestResolveDesiredTags_StripsLeadingV(t *testing.T) {
	current := imageTagSnapshot{
		HarnessTag:  "0.11.13",
		BackendTags: map[string]string{"claude": "0.11.13"},
	}
	got, err := resolveDesiredTags(current, UpgradeOptions{Tag: "v0.11.14"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.HarnessTag != "0.11.14" {
		t.Errorf("HarnessTag = %q; want 0.11.14 (leading v stripped)", got.HarnessTag)
	}
}

func TestResolveDesiredTags_HarnessTagOverridesBackendsTakeDefault(t *testing.T) {
	current := imageTagSnapshot{
		HarnessTag:  "0.11.13",
		BackendTags: map[string]string{"claude": "0.11.13", "codex": "0.11.13"},
	}
	got, err := resolveDesiredTags(current, UpgradeOptions{
		HarnessTag: "0.11.14",
		CLIVersion: "0.11.13", // backends fall through to this
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.HarnessTag != "0.11.14" {
		t.Errorf("HarnessTag = %q; want 0.11.14", got.HarnessTag)
	}
	if got.BackendTags["claude"] != "0.11.13" {
		t.Errorf("BackendTags[claude] = %q; want 0.11.13 (CLI version)", got.BackendTags["claude"])
	}
}

func TestResolveDesiredTags_BackendTagPerBackend(t *testing.T) {
	current := imageTagSnapshot{
		HarnessTag:  "0.11.13",
		BackendTags: map[string]string{"claude": "0.11.13", "codex": "0.11.13"},
	}
	got, err := resolveDesiredTags(current, UpgradeOptions{
		BackendTags: map[string]string{"claude": "0.11.14"},
		CLIVersion:  "0.11.13",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.BackendTags["claude"] != "0.11.14" {
		t.Errorf("explicit backend override didn't apply; got %q", got.BackendTags["claude"])
	}
	if got.BackendTags["codex"] != "0.11.13" {
		t.Errorf("non-overridden backend should fall through to CLI version; got %q", got.BackendTags["codex"])
	}
}

func TestResolveDesiredTags_UnknownBackendNameRejects(t *testing.T) {
	current := imageTagSnapshot{
		HarnessTag:  "0.11.13",
		BackendTags: map[string]string{"claude": "0.11.13"},
	}
	_, err := resolveDesiredTags(current, UpgradeOptions{
		BackendTags: map[string]string{"clade": "0.11.14"}, // typo
		CLIVersion:  "0.11.14",
	})
	if err == nil {
		t.Fatal("expected error for unknown backend name")
	}
	if !strings.Contains(err.Error(), "no backend named") {
		t.Errorf("error doesn't mention the unknown backend name: %q", err)
	}
}

func TestResolveDesiredTags_DevBuildWithoutOverridesRejects(t *testing.T) {
	current := imageTagSnapshot{
		HarnessTag:  "0.11.13",
		BackendTags: map[string]string{"claude": "0.11.13"},
	}
	_, err := resolveDesiredTags(current, UpgradeOptions{CLIVersion: "dev"})
	if err == nil {
		t.Fatal("dev build with no --tag / --harness-tag / --backend-tag should reject")
	}
	if !strings.Contains(err.Error(), "dev build") {
		t.Errorf("error doesn't mention the dev build path: %q", err)
	}
}

// ---------------------------------------------------------------------------
// diffTransitions — only changed containers in the banner
// ---------------------------------------------------------------------------

func TestDiffTransitions_SkipsUnchanged(t *testing.T) {
	current := imageTagSnapshot{
		HarnessTag:  "0.11.13",
		BackendTags: map[string]string{"claude": "0.11.13", "codex": "0.11.13"},
	}
	desired := imageTagSnapshot{
		HarnessTag:  "0.11.14",
		BackendTags: map[string]string{"claude": "0.11.13", "codex": "0.11.14"},
	}
	got := diffTransitions(current, desired)
	if len(got) != 2 {
		t.Fatalf("expected 2 transitions (harness + codex); got %d: %+v", len(got), got)
	}
	// Sorted: harness first.
	if got[0].Container != "harness" {
		t.Errorf("transitions[0].Container = %q; want harness", got[0].Container)
	}
	if got[1].Container != "backend:codex" {
		t.Errorf("transitions[1].Container = %q; want backend:codex", got[1].Container)
	}
}

func TestDiffTransitions_AllUnchangedReturnsEmpty(t *testing.T) {
	current := imageTagSnapshot{
		HarnessTag:  "0.11.14",
		BackendTags: map[string]string{"claude": "0.11.14"},
	}
	got := diffTransitions(current, current)
	if len(got) != 0 {
		t.Errorf("expected zero transitions for identical snapshots; got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// buildUpgradePatch — JSON shape the dynamic client applies
// ---------------------------------------------------------------------------

func TestBuildUpgradePatch_Shape(t *testing.T) {
	desired := imageTagSnapshot{
		HarnessTag:  "0.11.14",
		BackendTags: map[string]string{"claude": "0.11.14", "codex": "0.11.14"},
	}
	raw, err := buildUpgradePatch(desired)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("patch is not valid JSON: %v\n%s", err, raw)
	}

	spec, ok := parsed["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("patch missing spec object: %s", raw)
	}
	image, ok := spec["image"].(map[string]interface{})
	if !ok || image["tag"] != "0.11.14" {
		t.Errorf("patch.spec.image.tag = %v; want 0.11.14", image)
	}

	backends, ok := spec["backends"].([]interface{})
	if !ok || len(backends) != 2 {
		t.Fatalf("patch.spec.backends = %v; want 2 entries", spec["backends"])
	}
	// Sorted by name → claude first, codex second.
	first := backends[0].(map[string]interface{})
	if first["name"] != "claude" {
		t.Errorf("patch.spec.backends[0].name = %v; want claude", first["name"])
	}
	if firstImg := first["image"].(map[string]interface{}); firstImg["tag"] != "0.11.14" {
		t.Errorf("patch.spec.backends[0].image.tag = %v; want 0.11.14", firstImg["tag"])
	}
}

func TestBuildUpgradePatch_OmitsEmptyTags(t *testing.T) {
	desired := imageTagSnapshot{
		HarnessTag:  "",
		BackendTags: map[string]string{"claude": "0.11.14"},
	}
	raw, err := buildUpgradePatch(desired)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(raw), `"image":{"tag":""`) {
		t.Errorf("empty harness tag should be omitted from the patch; got %s", raw)
	}
}
