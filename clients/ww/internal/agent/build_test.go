package agent

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestBuild_Defaults(t *testing.T) {
	t.Parallel()

	obj, err := Build(BuildOptions{
		Name:       "hello",
		Namespace:  "default",
		CLIVersion: "0.6.0",
		CreatedBy:  "ww agent create hello",
	})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}

	if got, want := obj.GetAPIVersion(), APIVersionString(); got != want {
		t.Errorf("apiVersion = %q; want %q", got, want)
	}
	if got, want := obj.GetKind(), Kind; got != want {
		t.Errorf("kind = %q; want %q", got, want)
	}
	if got, want := obj.GetName(), "hello"; got != want {
		t.Errorf("name = %q; want %q", got, want)
	}
	if got, want := obj.GetNamespace(), "default"; got != want {
		t.Errorf("namespace = %q; want %q", got, want)
	}
	if got, want := obj.GetLabels()[LabelManagedBy], LabelManagedByWW; got != want {
		t.Errorf("managed-by label = %q; want %q", got, want)
	}
	if got, want := obj.GetAnnotations()[AnnotationCreatedBy], "ww agent create hello"; got != want {
		t.Errorf("created-by annotation = %q; want %q", got, want)
	}

	// spec.backends[0].name defaults to echo.
	backends, found, err := unstructured.NestedSlice(obj.Object, "spec", "backends")
	if err != nil || !found {
		t.Fatalf("spec.backends missing: found=%v err=%v", found, err)
	}
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(backends))
	}
	backend, _ := backends[0].(map[string]interface{})
	if got, want := backend["name"], DefaultBackend; got != want {
		t.Errorf("backend name = %v; want %v", got, want)
	}

	// spec.image resolves to a harness image at the given version.
	repo, found, err := unstructured.NestedString(obj.Object, "spec", "image", "repository")
	if err != nil || !found || repo != "ghcr.io/witwave-ai/images/harness" {
		t.Errorf("spec.image.repository = %q (found=%v err=%v); want harness repo", repo, found, err)
	}
	tag, _, _ := unstructured.NestedString(obj.Object, "spec", "image", "tag")
	if tag != "0.6.0" {
		t.Errorf("spec.image.tag = %q; want 0.6.0", tag)
	}
}

func TestBuild_UnknownBackend(t *testing.T) {
	t.Parallel()
	_, err := Build(BuildOptions{
		Name:      "hello",
		Namespace: "default",
		Backends:  []BackendSpec{{Name: "mistral", Type: "mistral", Port: 8001}},
	})
	if err == nil {
		t.Fatal("expected error for unknown backend; got nil")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("unexpected error %q; want 'unknown type' substring", err)
	}
}

func TestBuild_MultipleBackends(t *testing.T) {
	t.Parallel()
	obj, err := Build(BuildOptions{
		Name:      "consensus",
		Namespace: "default",
		Backends: []BackendSpec{
			{Name: "echo-1", Type: "echo", Port: BackendPort(0)},
			{Name: "echo-2", Type: "echo", Port: BackendPort(1)},
		},
		CLIVersion: "0.7.5",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Two entries in spec.backends[].
	backends, found, err := unstructured.NestedSlice(obj.Object, "spec", "backends")
	if err != nil || !found || len(backends) != 2 {
		t.Fatalf("spec.backends: found=%v err=%v len=%d want 2",
			found, err, len(backends))
	}
	names := []string{}
	ports := []int64{}
	for _, b := range backends {
		m, _ := b.(map[string]interface{})
		name, _ := m["name"].(string)
		port, _ := m["port"].(int64)
		names = append(names, name)
		ports = append(ports, port)
	}
	if names[0] != "echo-1" || names[1] != "echo-2" {
		t.Errorf("backend names = %v; want [echo-1 echo-2]", names)
	}
	if ports[0] != 8001 || ports[1] != 8002 {
		t.Errorf("ports = %v; want [8001 8002] — distinct per PORT-2", ports)
	}
	// backend.yaml inline config should list both and route to first.
	raw, _, _ := unstructured.NestedSlice(obj.Object, "spec", "config")
	if len(raw) != 1 {
		t.Fatalf("expected 1 config entry, got %d", len(raw))
	}
	cfg, _ := raw[0].(map[string]interface{})
	content, _ := cfg["content"].(string)
	for _, want := range []string{
		"id: echo-1", "id: echo-2",
		"url: http://localhost:8001", "url: http://localhost:8002",
		"agent: echo-1", // every routing entry points at the primary
	} {
		if !strings.Contains(content, want) {
			t.Errorf("backend.yaml missing %q. content:\n%s", want, content)
		}
	}
}

func TestBuild_DuplicateBackendName(t *testing.T) {
	t.Parallel()
	_, err := Build(BuildOptions{
		Name:      "hello",
		Namespace: "default",
		Backends: []BackendSpec{
			{Name: "dup", Type: "echo", Port: 8001},
			{Name: "dup", Type: "claude", Port: 8002},
		},
	})
	if err == nil {
		t.Fatal("expected error for duplicate backend name")
	}
	if !strings.Contains(err.Error(), "duplicate name") {
		t.Errorf("error = %q; want 'duplicate name'", err)
	}
}

func TestBuild_InvalidName(t *testing.T) {
	t.Parallel()
	_, err := Build(BuildOptions{Name: "Hello", Namespace: "default"})
	if err == nil {
		t.Fatal("expected name validation error; got nil")
	}
}

func TestBuild_WithTeam_StampsLabel(t *testing.T) {
	t.Parallel()
	obj, err := Build(BuildOptions{
		Name:       "hello",
		Namespace:  "witwave",
		CLIVersion: "0.7.8",
		Team:       "alpha",
	})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	if got, want := obj.GetLabels()[TeamLabel], "alpha"; got != want {
		t.Errorf("team label = %q; want %q", got, want)
	}
	// Managed-by label still present — team stamp shouldn't clobber existing labels.
	if got, want := obj.GetLabels()[LabelManagedBy], LabelManagedByWW; got != want {
		t.Errorf("managed-by label = %q; want %q (Team shouldn't overwrite)", got, want)
	}
}

func TestBuild_NoTeam_OmitsLabel(t *testing.T) {
	t.Parallel()
	obj, err := Build(BuildOptions{
		Name:       "hello",
		Namespace:  "witwave",
		CLIVersion: "0.7.8",
	})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	if _, labelled := obj.GetLabels()[TeamLabel]; labelled {
		t.Error("no Team set → team label should be absent so the agent falls back to the namespace-wide manifest")
	}
}

func TestBuild_EmitsRuntimeStorage(t *testing.T) {
	t.Parallel()
	obj, err := Build(BuildOptions{
		Name:           "hello",
		Namespace:      "witwave",
		CLIVersion:     "0.7.8",
		RuntimeStorage: DefaultRuntimeStorageSpec(),
	})
	if err != nil {
		t.Fatalf("Build returned unexpected error: %v", err)
	}
	enabled, found, err := unstructured.NestedBool(obj.Object, "spec", "runtimeStorage", "enabled")
	if err != nil || !found || !enabled {
		t.Fatalf("runtimeStorage.enabled = %v found=%v err=%v, want true", enabled, found, err)
	}
	mounts, found, err := unstructured.NestedSlice(obj.Object, "spec", "runtimeStorage", "mounts")
	if err != nil || !found {
		t.Fatalf("runtimeStorage.mounts missing: found=%v err=%v", found, err)
	}
	if len(mounts) != 2 {
		t.Fatalf("runtimeStorage.mounts = %d, want 2", len(mounts))
	}
	state := mounts[1].(map[string]interface{})
	if state["subPath"] != "state" || state["mountPath"] != "/home/agent/state" {
		t.Errorf("state mount = %+v", state)
	}
}

func TestBuild_InvalidTeamName(t *testing.T) {
	t.Parallel()
	_, err := Build(BuildOptions{
		Name:       "hello",
		Namespace:  "witwave",
		CLIVersion: "0.7.8",
		Team:       "Invalid-UPPER",
	})
	if err == nil {
		t.Fatal("expected an error for non-DNS-1123 team name")
	}
	if !strings.Contains(err.Error(), "team name") {
		t.Errorf("error = %q; want 'team name' substring", err)
	}
}

func TestBuild_NamespaceRequired(t *testing.T) {
	t.Parallel()
	_, err := Build(BuildOptions{Name: "hello"})
	if err == nil {
		t.Fatal("expected namespace-required error; got nil")
	}
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("unexpected error %q; want 'namespace' substring", err)
	}
}

func TestSplitRepoTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ref, repo, tag string
	}{
		{"ghcr.io/org/name:1.2.3", "ghcr.io/org/name", "1.2.3"},
		{"ghcr.io/org/name:latest", "ghcr.io/org/name", "latest"},
		{"repo-only", "repo-only", ""},
		// Port-in-registry edge case: "localhost:5000/foo" has no tag.
		{"localhost:5000/foo", "localhost:5000/foo", ""},
		// Port-in-registry with tag: "localhost:5000/foo:tag".
		{"localhost:5000/foo:tag", "localhost:5000/foo", "tag"},
	}
	for _, tc := range cases {
		if got := splitRepo(tc.ref); got != tc.repo {
			t.Errorf("splitRepo(%q) = %q; want %q", tc.ref, got, tc.repo)
		}
		if got := splitTag(tc.ref); got != tc.tag {
			t.Errorf("splitTag(%q) = %q; want %q", tc.ref, got, tc.tag)
		}
	}
}
