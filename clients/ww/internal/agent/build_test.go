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
	if err != nil || !found || repo != "ghcr.io/skthomasjr/images/harness" {
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
		Backend:   "mistral", // not yet supported
	})
	if err == nil {
		t.Fatal("expected error for unknown backend; got nil")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("unexpected error %q; want 'unknown backend' substring", err)
	}
}

func TestBuild_InvalidName(t *testing.T) {
	t.Parallel()
	_, err := Build(BuildOptions{Name: "Hello", Namespace: "default"})
	if err == nil {
		t.Fatal("expected name validation error; got nil")
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
