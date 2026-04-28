package workspace

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

	"github.com/witwave-ai/witwave/clients/ww/internal/agent"
	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
)

// ---------------------------------------------------------------------------
// Shared test helpers for the workspace package — mirrors
// internal/agent/cr_helpers_test.go so test setup is uniform across
// the two packages.
// ---------------------------------------------------------------------------

// withFakeClients swaps clientFactory with constructors that always
// return the provided fakes. Returns a restore function that must be
// deferred via t.Cleanup.
func withFakeClients(t *testing.T, dyn dynamic.Interface, k8sClient kubernetes.Interface) func() {
	t.Helper()
	original := clientFactory
	clientFactory = struct {
		dyn  func(*rest.Config) (dynamic.Interface, error)
		kube func(*rest.Config) (kubernetes.Interface, error)
	}{
		dyn:  func(*rest.Config) (dynamic.Interface, error) { return dyn, nil },
		kube: func(*rest.Config) (kubernetes.Interface, error) { return k8sClient, nil },
	}
	return func() { clientFactory = original }
}

// makeFakeDynamic builds a dynamic.Interface that serves both WitwaveWorkspace
// and WitwaveAgent CRs. The unified scheme means a single dyn fake can
// drive bind/unbind tests (which touch agents) alongside the workspace
// verb tests (which touch workspaces).
func makeFakeDynamic(objs ...runtime.Object) dynamic.Interface {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		GVR():       Kind + "List",
		agent.GVR(): agent.Kind + "List",
	}
	return fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

// makeFakeK8s returns a typed kubernetes.Interface pre-populated with
// the given core objects (typically Namespaces tests want pre-existing).
func makeFakeK8s(objs ...runtime.Object) kubernetes.Interface {
	return k8sfake.NewSimpleClientset(objs...)
}

// seedWitwaveWorkspace returns an unstructured WitwaveWorkspace CR the fake dynamic
// client can round-trip. The mutateSpec closure lets a test add volumes,
// secrets, configFiles, or status without nesting a giant literal.
func seedWitwaveWorkspace(name, namespace string, mutateSpec func(spec map[string]interface{})) *unstructured.Unstructured {
	spec := map[string]interface{}{}
	if mutateSpec != nil {
		mutateSpec(spec)
	}
	return &unstructured.Unstructured{
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
}

// seedWitwaveWorkspaceWithStatus mirrors seedWitwaveWorkspace but also sets a status
// stanza — handy for delete/status tests that assert on bound-agent
// rendering or condition reporting.
func seedWitwaveWorkspaceWithStatus(name, namespace string, mutateSpec, mutateStatus func(map[string]interface{})) *unstructured.Unstructured {
	cr := seedWitwaveWorkspace(name, namespace, mutateSpec)
	status := map[string]interface{}{}
	if mutateStatus != nil {
		mutateStatus(status)
	}
	cr.Object["status"] = status
	return cr
}

// seedAgentRef returns an unstructured WitwaveAgent CR for bind/unbind
// tests. By default no workspaceRefs are present; the mutateSpec closure
// can populate them or set additional fields.
func seedAgentRef(name, namespace string, mutateSpec func(spec map[string]interface{})) *unstructured.Unstructured {
	spec := map[string]interface{}{
		"port": int64(8000),
		"image": map[string]interface{}{
			"repository": "ghcr.io/witwave-ai/images/harness",
			"tag":        "test",
		},
		"backends": []interface{}{
			map[string]interface{}{
				"name": "echo",
				"port": int64(8001),
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
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": agent.APIVersionString(),
			"kind":       agent.Kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": spec,
		},
	}
}

// readWitwaveWorkspace fetches a seeded CR from the fake client.
func readWitwaveWorkspace(t *testing.T, dyn dynamic.Interface, namespace, name string) *unstructured.Unstructured {
	t.Helper()
	cr, err := dyn.Resource(GVR()).Namespace(namespace).Get(
		context.Background(), name, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("readWitwaveWorkspace(%s/%s): %v", namespace, name, err)
	}
	return cr
}

// readAgentRef fetches a seeded agent CR from the fake client.
func readAgentRef(t *testing.T, dyn dynamic.Interface, namespace, name string) *unstructured.Unstructured {
	t.Helper()
	cr, err := dyn.Resource(agent.GVR()).Namespace(namespace).Get(
		context.Background(), name, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("readAgentRef(%s/%s): %v", namespace, name, err)
	}
	return cr
}

// captureOut returns a new buffer + a cleanup. Tests pass the buffer as
// opts.Out and then assert on its contents.
func captureOut() *bytes.Buffer {
	return &bytes.Buffer{}
}

// mustContain fails the test when haystack doesn't contain needle.
func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected output to contain %q; full output:\n%s", needle, haystack)
	}
}

// mustNotContain fails the test when haystack does contain needle.
func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("expected output to NOT contain %q; full output:\n%s", needle, haystack)
	}
}

// smokeTarget returns a minimal *k8s.Target suitable for preflight
// rendering in unit tests. AssumeYes=true skips the prompt, so these
// fields are cosmetic but Confirm() still dereferences the pointer.
func smokeTarget() *k8s.Target {
	return &k8s.Target{
		Context:   "fake-context",
		Cluster:   "fake-cluster",
		Server:    "https://fake.invalid",
		Namespace: "default",
	}
}
