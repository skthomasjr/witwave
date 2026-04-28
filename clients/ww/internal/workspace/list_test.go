package workspace

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestList_Empty(t *testing.T) {
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := List(context.Background(), nil, ListOptions{
		Namespace:     "default",
		AllNamespaces: false,
		Output:        OutputFormatTable,
		Out:           out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "No WitwaveWorkspaces found in namespace")
}

func TestList_Table_HappyPath(t *testing.T) {
	a := seedWitwaveWorkspaceWithStatus("alpha", "default", func(spec map[string]interface{}) {
		spec["volumes"] = []interface{}{
			map[string]interface{}{"name": "source", "size": "10Gi"},
		}
	}, func(status map[string]interface{}) {
		status["conditions"] = []interface{}{
			map[string]interface{}{"type": "Ready", "status": "True"},
		}
		status["boundAgents"] = []interface{}{
			map[string]interface{}{"name": "iris", "namespace": "default"},
		}
	})
	b := seedWitwaveWorkspace("beta", "default", nil)
	dyn := makeFakeDynamic(a, b)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := List(context.Background(), nil, ListOptions{
		Namespace:     "default",
		AllNamespaces: false,
		Output:        OutputFormatTable,
		Out:           out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := out.String()
	mustContain(t, body, "NAMESPACE")
	mustContain(t, body, "alpha")
	mustContain(t, body, "beta")
	mustContain(t, body, "Ready")
	mustContain(t, body, "Pending")
}

func TestList_AllNamespaces(t *testing.T) {
	a := seedWitwaveWorkspace("alpha", "ns-a", nil)
	b := seedWitwaveWorkspace("beta", "ns-b", nil)
	dyn := makeFakeDynamic(a, b)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := List(context.Background(), nil, ListOptions{
		AllNamespaces: true,
		Output:        OutputFormatTable,
		Out:           out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := out.String()
	mustContain(t, body, "ns-a")
	mustContain(t, body, "ns-b")
}

func TestList_YAML_RoundTrips(t *testing.T) {
	a := seedWitwaveWorkspace("alpha", "default", func(spec map[string]interface{}) {
		spec["volumes"] = []interface{}{
			map[string]interface{}{"name": "source", "size": "10Gi"},
		}
	})
	dyn := makeFakeDynamic(a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := List(context.Background(), nil, ListOptions{
		Namespace: "default",
		Output:    OutputFormatYAML,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := out.String()
	mustContain(t, body, "kind: WitwaveWorkspaceList")
	mustContain(t, body, "name: alpha")
	mustContain(t, body, "size: 10Gi")
}

func TestList_JSON_RoundTrips(t *testing.T) {
	a := seedWitwaveWorkspace("alpha", "default", nil)
	dyn := makeFakeDynamic(a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := List(context.Background(), nil, ListOptions{
		Namespace: "default",
		Output:    OutputFormatJSON,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, `"kind": "WitwaveWorkspaceList"`) {
		t.Errorf("expected JSON envelope; got\n%s", body)
	}
}

func TestReadPhase_DerivesFromConditions(t *testing.T) {
	cr := seedWitwaveWorkspaceWithStatus("a", "default", nil, func(status map[string]interface{}) {
		status["conditions"] = []interface{}{
			map[string]interface{}{"type": "Ready", "status": "False", "reason": "ProvisioningFailed"},
		}
	})
	if got := readPhase(cr); got != "ProvisioningFailed" {
		t.Errorf("readPhase = %q; want ProvisioningFailed", got)
	}
}

func TestWitwaveWorkspaceSummary_CountsBoundAgents(t *testing.T) {
	cr := seedWitwaveWorkspaceWithStatus("a", "default", nil, func(status map[string]interface{}) {
		status["boundAgents"] = []interface{}{
			map[string]interface{}{"name": "iris", "namespace": "default"},
			map[string]interface{}{"name": "nova", "namespace": "default"},
		}
	})
	s := workspaceSummary(cr)
	if s.BoundAgents != 2 {
		t.Errorf("BoundAgents = %d; want 2", s.BoundAgents)
	}
}

// Quiet the unused-import warning when only some assertions are exercised.
var _ = unstructured.Unstructured{}
