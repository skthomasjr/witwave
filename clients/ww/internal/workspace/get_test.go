package workspace

import (
	"context"
	"strings"
	"testing"
)

func TestGet_Table(t *testing.T) {
	cr := seedWorkspace("witwave", "default", func(spec map[string]interface{}) {
		spec["volumes"] = []interface{}{
			map[string]interface{}{"name": "source", "size": "50Gi"},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Get(context.Background(), nil, GetOptions{
		Name:      "witwave",
		Namespace: "default",
		Output:    OutputFormatTable,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := out.String()
	mustContain(t, body, "NAMESPACE")
	mustContain(t, body, "witwave")
	mustContain(t, body, "Pending")
}

func TestGet_YAML(t *testing.T) {
	cr := seedWorkspace("witwave", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Get(context.Background(), nil, GetOptions{
		Name:      "witwave",
		Namespace: "default",
		Output:    OutputFormatYAML,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := out.String()
	mustContain(t, body, "kind: Workspace")
	mustContain(t, body, "name: witwave")
}

func TestGet_NotFound(t *testing.T) {
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	err := Get(context.Background(), nil, GetOptions{
		Name:      "missing",
		Namespace: "default",
		Output:    OutputFormatTable,
		Out:       captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error; got %v", err)
	}
}

func TestGet_InvalidName(t *testing.T) {
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	err := Get(context.Background(), nil, GetOptions{
		Name:      "Bad_Name",
		Namespace: "default",
		Out:       captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "DNS-1123") {
		t.Errorf("expected validation error; got %v", err)
	}
}
