package agent

import (
	"bytes"
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

// ---------------------------------------------------------------------------
// Shared test helpers for the CR-mutation verbs (GitAdd / GitRemove /
// GitList / BackendRemove / BackendRename).
//
// These live as a *_test.go file (not a tools/testutil package) so they
// can access the package-level clientFactory and unexported helpers
// without forcing those to be exported. The cost is that the helpers
// are only available inside the agent package's tests — which is where
// they're used, so that's fine.
// ---------------------------------------------------------------------------

// withFakeClients swaps clientFactory with constructors that always
// return the provided fakes. Returns a restore function that must be
// deferred. Use t.Cleanup(restore) idiomatically.
func withFakeClients(t *testing.T, dyn dynamic.Interface, k8s kubernetes.Interface) func() {
	t.Helper()
	original := clientFactory
	clientFactory = struct {
		dyn  func(*rest.Config) (dynamic.Interface, error)
		kube func(*rest.Config) (kubernetes.Interface, error)
	}{
		dyn:  func(*rest.Config) (dynamic.Interface, error) { return dyn, nil },
		kube: func(*rest.Config) (kubernetes.Interface, error) { return k8s, nil },
	}
	return func() { clientFactory = original }
}

// makeFakeDynamic builds a dynamic.Interface that serves WitwaveAgent
// CRs via the witwave.ai v1alpha1 GVR, pre-populated with the given
// unstructured objects.
func makeFakeDynamic(objs ...runtime.Object) dynamic.Interface {
	scheme := runtime.NewScheme()
	gvr := GVR()
	listKinds := map[schema.GroupVersionResource]string{
		gvr: Kind + "List",
	}
	return fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

// makeFakeK8s returns a typed kubernetes.Interface pre-populated with
// the given core objects (typically Secrets that tests want to be
// pre-existing on the cluster).
func makeFakeK8s(objs ...runtime.Object) kubernetes.Interface {
	return k8sfake.NewSimpleClientset(objs...)
}

// seedAgent returns an unstructured WitwaveAgent CR the fake dynamic
// client can round-trip. Keeps tests terse by accepting a builder
// closure that mutates the spec map — tests add backends, gitSyncs,
// or config entries via the closure rather than nesting a big literal.
func seedAgent(name, namespace string, mutateSpec func(spec map[string]interface{})) *unstructured.Unstructured {
	spec := map[string]interface{}{
		"port": int64(DefaultHarnessPort),
		"image": map[string]interface{}{
			"repository": "ghcr.io/witwave-ai/images/harness",
			"tag":        "test",
		},
		"backends": []interface{}{
			map[string]interface{}{
				"name": "echo",
				"port": int64(BackendPort(0)),
				"image": map[string]interface{}{
					"repository": "ghcr.io/witwave-ai/images/echo",
					"tag":        "test",
				},
			},
		},
	}
	if mutateSpec != nil {
		mutateSpec(spec)
	}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": APIVersionString(),
			"kind":       Kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": spec,
		},
	}
	return obj
}

// readAgent fetches a seeded CR from the fake client — shorthand for
// tests that assert post-mutation state.
func readAgent(t *testing.T, dyn dynamic.Interface, namespace, name string) *unstructured.Unstructured {
	t.Helper()
	cr, err := dyn.Resource(GVR()).Namespace(namespace).Get(
		context.Background(), name, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("readAgent(%s/%s): %v", namespace, name, err)
	}
	return cr
}

// captureOut returns a new buffer + a cleanup. Tests pass the buffer
// as opts.Out and then assert on its contents.
func captureOut() *bytes.Buffer {
	return &bytes.Buffer{}
}

// mustContain fails the test when `haystack` doesn't contain `needle`.
// Prints the full haystack on failure so assertions produce legible
// diagnostics.
func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected output to contain %q; full output:\n%s", needle, haystack)
	}
}

// mustNotContain fails the test when `haystack` does contain `needle`.
func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("expected output to NOT contain %q; full output:\n%s", needle, haystack)
	}
}
