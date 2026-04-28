package workspace

import (
	"context"
	"strings"
	"testing"
)

func TestStatus_Pending_NoConditions(t *testing.T) {
	cr := seedWorkspace("witwave", "default", func(spec map[string]interface{}) {
		spec["volumes"] = []interface{}{
			map[string]interface{}{
				"name":             "source",
				"size":             "50Gi",
				"storageClassName": "efs-sc",
				"accessMode":       "ReadWriteMany",
				"reclaimPolicy":    "Retain",
			},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Status(context.Background(), nil, StatusOptions{
		Name:      "witwave",
		Namespace: "default",
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := out.String()
	mustContain(t, body, "Workspace: witwave")
	mustContain(t, body, "Phase:     Pending")
	mustContain(t, body, "Volumes:")
	mustContain(t, body, "source")
	mustContain(t, body, "Retain")
	mustContain(t, body, "Conditions: (none")
	mustContain(t, body, "Bound agents: (none")
}

func TestStatus_Ready_WithBoundAgents(t *testing.T) {
	cr := seedWorkspaceWithStatus("witwave", "default", nil, func(status map[string]interface{}) {
		status["conditions"] = []interface{}{
			map[string]interface{}{
				"type":    "Ready",
				"status":  "True",
				"reason":  "Reconciled",
				"message": "all subresources up to date",
			},
		}
		status["boundAgents"] = []interface{}{
			map[string]interface{}{"name": "iris", "namespace": "default"},
			map[string]interface{}{"name": "nova", "namespace": "default"},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Status(context.Background(), nil, StatusOptions{
		Name:      "witwave",
		Namespace: "default",
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := out.String()
	mustContain(t, body, "Phase:     Ready")
	mustContain(t, body, "Bound agents (2):")
	mustContain(t, body, "iris")
	mustContain(t, body, "nova")
	if !strings.Contains(body, "Reconciled") {
		t.Errorf("expected condition reason in output; got\n%s", body)
	}
}

func TestStatus_NotFound(t *testing.T) {
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	err := Status(context.Background(), nil, StatusOptions{
		Name:      "missing",
		Namespace: "default",
		Out:       captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error; got %v", err)
	}
}
