package workspace

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDelete_HappyPath_NoFlags(t *testing.T) {
	cr := seedWorkspace("witwave", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:      "witwave",
		Namespace: "default",
		AssumeYes: true,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := out.String()
	mustContain(t, body, "Delete request accepted")
	mustNotContain(t, body, "still bound")

	// Fake dynamic client actually removes the object on Delete().
	if _, err := dyn.Resource(GVR()).Namespace("default").Get(
		context.Background(), "witwave", metav1.GetOptions{},
	); err == nil {
		t.Error("CR should have been deleted")
	}
}

func TestDelete_BannerSurfacesBoundAgents(t *testing.T) {
	cr := seedWorkspaceWithStatus("witwave", "default", nil, func(status map[string]interface{}) {
		status["boundAgents"] = []interface{}{
			map[string]interface{}{"name": "iris", "namespace": "default"},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:      "witwave",
		Namespace: "default",
		AssumeYes: true,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := out.String()
	mustContain(t, body, "1 still bound")
	mustContain(t, body, "iris")
	mustContain(t, body, "ww workspace unbind")
}

func TestDelete_NotFound(t *testing.T) {
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:      "missing",
		Namespace: "default",
		AssumeYes: true,
		Out:       captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error; got %v", err)
	}
}

func TestDelete_DryRun_DoesNotCallAPI(t *testing.T) {
	cr := seedWorkspace("witwave", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	out := captureOut()
	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:      "witwave",
		Namespace: "default",
		DryRun:    true,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "Dry-run mode")
	if _, err := dyn.Resource(GVR()).Namespace("default").Get(
		context.Background(), "witwave", metav1.GetOptions{},
	); err != nil {
		t.Errorf("CR should still exist after dry-run; got err %v", err)
	}
}

func TestDelete_InvalidName(t *testing.T) {
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:      "Bad_Name",
		Namespace: "default",
		AssumeYes: true,
		Out:       captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "DNS-1123") {
		t.Errorf("expected validation error; got %v", err)
	}
}
