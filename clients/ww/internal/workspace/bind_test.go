package workspace

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestBind_HappyPath_FromEmpty(t *testing.T) {
	ws := seedWitwaveWorkspace("witwave", "default", nil)
	a := seedAgentRef("iris", "default", nil)
	dyn := makeFakeDynamic(ws, a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := Bind(context.Background(), nil, BindOptions{
		Agent:          "iris",
		AgentNamespace: "default",
		WitwaveWorkspace:      "witwave",
		AssumeYes:      true,
		Out:            out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), `now bound to WitwaveWorkspace "witwave"`)

	updated := readAgentRef(t, dyn, "default", "iris")
	refs, found, err := unstructured.NestedSlice(updated.Object, "spec", "workspaceRefs")
	if err != nil || !found || len(refs) != 1 {
		t.Fatalf("expected one workspaceRef; got found=%v refs=%+v err=%v", found, refs, err)
	}
	if name, _ := refs[0].(map[string]interface{})["name"].(string); name != "witwave" {
		t.Errorf("ref name = %q; want %q", name, "witwave")
	}
}

func TestBind_Idempotent(t *testing.T) {
	ws := seedWitwaveWorkspace("witwave", "default", nil)
	a := seedAgentRef("iris", "default", func(spec map[string]interface{}) {
		spec["workspaceRefs"] = []interface{}{
			map[string]interface{}{"name": "witwave"},
		}
	})
	dyn := makeFakeDynamic(ws, a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := Bind(context.Background(), nil, BindOptions{
		Agent:          "iris",
		AgentNamespace: "default",
		WitwaveWorkspace:      "witwave",
		AssumeYes:      true,
		Out:            out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "already bound")
}

func TestBind_AppendsToExisting(t *testing.T) {
	ws := seedWitwaveWorkspace("witwave", "default", nil)
	a := seedAgentRef("iris", "default", func(spec map[string]interface{}) {
		spec["workspaceRefs"] = []interface{}{
			map[string]interface{}{"name": "other"},
		}
	})
	dyn := makeFakeDynamic(ws, a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := Bind(context.Background(), nil, BindOptions{
		Agent:          "iris",
		AgentNamespace: "default",
		WitwaveWorkspace:      "witwave",
		AssumeYes:      true,
		Out:            captureOut(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := readAgentRef(t, dyn, "default", "iris")
	refs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "workspaceRefs")
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs; got %d", len(refs))
	}
}

func TestBind_WitwaveWorkspaceMissing(t *testing.T) {
	a := seedAgentRef("iris", "default", nil)
	dyn := makeFakeDynamic(a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	err := Bind(context.Background(), nil, BindOptions{
		Agent:          "iris",
		AgentNamespace: "default",
		WitwaveWorkspace:      "nope",
		AssumeYes:      true,
		Out:            captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), `WitwaveWorkspace "nope" not found`) {
		t.Errorf("expected workspace-not-found error; got %v", err)
	}
}

func TestBind_AgentMissing(t *testing.T) {
	ws := seedWitwaveWorkspace("witwave", "default", nil)
	dyn := makeFakeDynamic(ws)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	err := Bind(context.Background(), nil, BindOptions{
		Agent:          "nope",
		AgentNamespace: "default",
		WitwaveWorkspace:      "witwave",
		AssumeYes:      true,
		Out:            captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), `WitwaveAgent "nope" not found`) {
		t.Errorf("expected agent-not-found error; got %v", err)
	}
}

func TestBind_RejectsCrossNamespace(t *testing.T) {
	ws := seedWitwaveWorkspace("witwave", "ws-ns", nil)
	a := seedAgentRef("iris", "agent-ns", nil)
	dyn := makeFakeDynamic(ws, a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	err := Bind(context.Background(), nil, BindOptions{
		Agent:              "iris",
		AgentNamespace:     "agent-ns",
		WitwaveWorkspace:          "witwave",
		WitwaveWorkspaceNamespace: "ws-ns",
		AssumeYes:          true,
		Out:                captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "cross-namespace binding not supported") {
		t.Errorf("expected cross-namespace rejection; got %v", err)
	}
}

func TestBind_DryRun_DoesNotMutate(t *testing.T) {
	ws := seedWitwaveWorkspace("witwave", "default", nil)
	a := seedAgentRef("iris", "default", nil)
	dyn := makeFakeDynamic(ws, a)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Bind(context.Background(), nil, BindOptions{
		Agent:          "iris",
		AgentNamespace: "default",
		WitwaveWorkspace:      "witwave",
		DryRun:         true,
		Out:            out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "Dry-run mode")
	updated := readAgentRef(t, dyn, "default", "iris")
	if _, found, _ := unstructured.NestedSlice(updated.Object, "spec", "workspaceRefs"); found {
		t.Error("workspaceRefs should NOT have been written in dry-run")
	}
}
