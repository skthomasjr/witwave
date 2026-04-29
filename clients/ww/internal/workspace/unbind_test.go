package workspace

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestUnbind_HappyPath(t *testing.T) {
	a := seedAgentRef("iris", "default", func(spec map[string]interface{}) {
		spec["workspaceRefs"] = []interface{}{
			map[string]interface{}{"name": "witwave"},
			map[string]interface{}{"name": "other"},
		}
	})
	dyn := makeFakeDynamic(a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Unbind(context.Background(), nil, UnbindOptions{
		Agent:            "iris",
		AgentNamespace:   "default",
		WitwaveWorkspace: "witwave",
		AssumeYes:        true,
		Out:              out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "no longer bound")
	updated := readAgentRef(t, dyn, "default", "iris")
	refs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "workspaceRefs")
	if len(refs) != 1 {
		t.Fatalf("expected 1 remaining ref; got %d", len(refs))
	}
	if name, _ := refs[0].(map[string]interface{})["name"].(string); name != "other" {
		t.Errorf("remaining ref name = %q; want %q", name, "other")
	}
}

func TestUnbind_LastEntry_RemovesField(t *testing.T) {
	a := seedAgentRef("iris", "default", func(spec map[string]interface{}) {
		spec["workspaceRefs"] = []interface{}{
			map[string]interface{}{"name": "witwave"},
		}
	})
	dyn := makeFakeDynamic(a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	err := Unbind(context.Background(), nil, UnbindOptions{
		Agent:            "iris",
		AgentNamespace:   "default",
		WitwaveWorkspace: "witwave",
		AssumeYes:        true,
		Out:              captureOut(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := readAgentRef(t, dyn, "default", "iris")
	if _, found, _ := unstructured.NestedSlice(updated.Object, "spec", "workspaceRefs"); found {
		t.Error("workspaceRefs should have been cleared after removing the last entry")
	}
}

func TestUnbind_NotBound_NoOp(t *testing.T) {
	a := seedAgentRef("iris", "default", nil)
	dyn := makeFakeDynamic(a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Unbind(context.Background(), nil, UnbindOptions{
		Agent:            "iris",
		AgentNamespace:   "default",
		WitwaveWorkspace: "witwave",
		AssumeYes:        true,
		Out:              out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "is not bound")
}

func TestUnbind_DryRun_DoesNotMutate(t *testing.T) {
	a := seedAgentRef("iris", "default", func(spec map[string]interface{}) {
		spec["workspaceRefs"] = []interface{}{
			map[string]interface{}{"name": "witwave"},
		}
	})
	dyn := makeFakeDynamic(a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Unbind(context.Background(), nil, UnbindOptions{
		Agent:            "iris",
		AgentNamespace:   "default",
		WitwaveWorkspace: "witwave",
		DryRun:           true,
		Out:              out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "Dry-run mode")
	updated := readAgentRef(t, dyn, "default", "iris")
	refs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "workspaceRefs")
	if len(refs) != 1 {
		t.Errorf("workspaceRefs should still have 1 entry after dry-run; got %d", len(refs))
	}
}
