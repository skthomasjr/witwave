package agent

import (
	"strings"
	"testing"
)

func TestParseBackendEnvs_HappyPath(t *testing.T) {
	t.Parallel()
	got, err := ParseBackendEnvs([]string{
		"claude:TASK_TIMEOUT_SECONDS=2700",
		"claude:LOG_LEVEL=debug",
		"codex:LOG_LEVEL=info",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 backend buckets, got %d (%v)", len(got), got)
	}
	if got["claude"]["TASK_TIMEOUT_SECONDS"] != "2700" {
		t.Errorf("claude TASK_TIMEOUT_SECONDS = %q, want 2700", got["claude"]["TASK_TIMEOUT_SECONDS"])
	}
	if got["claude"]["LOG_LEVEL"] != "debug" {
		t.Errorf("claude LOG_LEVEL = %q, want debug", got["claude"]["LOG_LEVEL"])
	}
	if got["codex"]["LOG_LEVEL"] != "info" {
		t.Errorf("codex LOG_LEVEL = %q, want info", got["codex"]["LOG_LEVEL"])
	}
}

func TestParseBackendEnvs_Empty(t *testing.T) {
	t.Parallel()
	got, err := ParseBackendEnvs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestParseBackendEnvs_RejectsDuplicateKey(t *testing.T) {
	t.Parallel()
	_, err := ParseBackendEnvs([]string{
		"claude:TASK_TIMEOUT_SECONDS=2700",
		"claude:TASK_TIMEOUT_SECONDS=900",
	})
	if err == nil {
		t.Fatal("want error on duplicate (backend, KEY); got nil")
	}
	if !strings.Contains(err.Error(), "given twice") {
		t.Errorf("error %q should mention the dup; got: %v", err, err)
	}
}

func TestParseBackendEnvs_BadShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{"missing-colon", "TASK_TIMEOUT_SECONDS=2700"},
		{"missing-equals", "claude:TASK_TIMEOUT_SECONDS"},
		{"empty-backend", ":TASK_TIMEOUT_SECONDS=2700"},
		{"empty-key", "claude:=2700"},
		{"empty-value", "claude:TASK_TIMEOUT_SECONDS="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseBackendEnvs([]string{tc.raw})
			if err == nil {
				t.Fatalf("want error for %q; got nil", tc.raw)
			}
			if !strings.Contains(err.Error(), "--backend-env") {
				t.Errorf("error should mention the flag name; got: %v", err)
			}
		})
	}
}

func TestApplyBackendEnvs_StampsOnMatchingSpec(t *testing.T) {
	t.Parallel()
	specs := []BackendSpec{
		{Name: "claude", Type: "claude", Port: 8001},
		{Name: "codex", Type: "codex", Port: 8002},
	}
	envs := map[string]map[string]string{
		"claude": {"TASK_TIMEOUT_SECONDS": "2700"},
	}
	out, err := ApplyBackendEnvs(specs, envs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := out[0].Env["TASK_TIMEOUT_SECONDS"]; got != "2700" {
		t.Errorf("claude env TASK_TIMEOUT_SECONDS = %q, want 2700", got)
	}
	if out[1].Env != nil {
		t.Errorf("codex spec.Env should be nil (no entry in map), got %v", out[1].Env)
	}
}

func TestApplyBackendEnvs_RejectsUnknownBackend(t *testing.T) {
	t.Parallel()
	specs := []BackendSpec{{Name: "claude", Type: "claude", Port: 8001}}
	envs := map[string]map[string]string{
		"typo-backend": {"FOO": "bar"},
	}
	_, err := ApplyBackendEnvs(specs, envs)
	if err == nil {
		t.Fatal("want error on unknown backend; got nil")
	}
	if !strings.Contains(err.Error(), "no backend named") {
		t.Errorf("error %q should mention the unknown backend; got: %v", err, err)
	}
}

func TestApplyBackendEnvs_MergesWithExisting(t *testing.T) {
	t.Parallel()
	specs := []BackendSpec{
		{Name: "claude", Type: "claude", Port: 8001, Env: map[string]string{"PRESET": "yes"}},
	}
	envs := map[string]map[string]string{
		"claude": {"TASK_TIMEOUT_SECONDS": "2700"},
	}
	out, err := ApplyBackendEnvs(specs, envs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0].Env["PRESET"] != "yes" {
		t.Errorf("merge should preserve existing keys; PRESET = %q", out[0].Env["PRESET"])
	}
	if out[0].Env["TASK_TIMEOUT_SECONDS"] != "2700" {
		t.Errorf("merge should add new keys; TASK_TIMEOUT_SECONDS = %q", out[0].Env["TASK_TIMEOUT_SECONDS"])
	}
}

func TestBuild_EmitsBackendEnv(t *testing.T) {
	t.Parallel()
	obj, err := Build(BuildOptions{
		Name:      "test-agent",
		Namespace: "test-ns",
		Backends: []BackendSpec{{
			Name: "claude",
			Type: "claude",
			Port: 8001,
			Env:  map[string]string{"TASK_TIMEOUT_SECONDS": "2700", "LOG_LEVEL": "debug"},
		}},
		CLIVersion: "dev",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	backends, _, err := unstructuredNestedSlice(obj.Object, "spec", "backends")
	if err != nil {
		t.Fatalf("read spec.backends: %v", err)
	}
	if len(backends) != 1 {
		t.Fatalf("want 1 backend, got %d", len(backends))
	}
	entry := backends[0].(map[string]interface{})
	envRaw, ok := entry["env"]
	if !ok {
		t.Fatal("spec.backends[0].env should be set when BackendSpec.Env is non-empty")
	}
	envList := envRaw.([]interface{})
	if len(envList) != 2 {
		t.Fatalf("want 2 env entries, got %d", len(envList))
	}
	// Output must be sorted by name for deterministic CR diffs:
	// LOG_LEVEL < TASK_TIMEOUT_SECONDS alphabetically.
	first := envList[0].(map[string]interface{})
	if first["name"] != "LOG_LEVEL" {
		t.Errorf("env[0].name = %q, want LOG_LEVEL (sort order)", first["name"])
	}
	second := envList[1].(map[string]interface{})
	if second["name"] != "TASK_TIMEOUT_SECONDS" {
		t.Errorf("env[1].name = %q, want TASK_TIMEOUT_SECONDS", second["name"])
	}
	if second["value"] != "2700" {
		t.Errorf("env[1].value = %q, want 2700", second["value"])
	}
}

// unstructuredNestedSlice is the test-local twin of
// k8s.io/apimachinery/pkg/apis/meta/v1/unstructured's NestedSlice
// — keeps this test free of the wider import path while still
// exercising the same map-traversal logic the real client uses.
func unstructuredNestedSlice(obj map[string]interface{}, fields ...string) ([]interface{}, bool, error) {
	cur := interface{}(obj)
	for _, f := range fields {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false, nil
		}
		cur, ok = m[f]
		if !ok {
			return nil, false, nil
		}
	}
	out, ok := cur.([]interface{})
	return out, ok, nil
}
