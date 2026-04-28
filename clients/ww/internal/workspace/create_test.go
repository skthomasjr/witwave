package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestCreate_FromFlags_HappyPath(t *testing.T) {
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := Create(context.Background(), smokeTarget(), nil, CreateOptions{
		Name:      "witwave",
		Namespace: "default",
		Volumes: []VolumeSpec{
			{Name: "source", Size: "50Gi", StorageClassName: "efs-sc"},
		},
		Secrets: []SecretSpec{
			{Name: "github-token", EnvFrom: true},
		},
		AssumeYes: true,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), `Created Workspace witwave`)

	cr, err := dyn.Resource(GVR()).Namespace("default").Get(
		context.Background(), "witwave", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("Workspace should have been created: %v", err)
	}
	vols, found, err := unstructured.NestedSlice(cr.Object, "spec", "volumes")
	if err != nil || !found || len(vols) != 1 {
		t.Fatalf("expected exactly one volume; got found=%v vols=%+v err=%v", found, vols, err)
	}
	if got := cr.GetLabels()[LabelManagedBy]; got != LabelManagedByWW {
		t.Errorf("managed-by label = %q; want %q", got, LabelManagedByWW)
	}
}

func TestCreate_AlreadyExists(t *testing.T) {
	existing := seedWorkspace("witwave", "default", nil)
	dyn := makeFakeDynamic(existing)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := Create(context.Background(), smokeTarget(), nil, CreateOptions{
		Name:      "witwave",
		Namespace: "default",
		AssumeYes: true,
		Out:       out,
	})
	if err == nil {
		t.Fatal("expected AlreadyExists error; got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v; want 'already exists'", err)
	}
}

func TestCreate_DryRun_NoCRCreated(t *testing.T) {
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := Create(context.Background(), smokeTarget(), nil, CreateOptions{
		Name:      "witwave",
		Namespace: "default",
		Volumes:   []VolumeSpec{{Name: "source", Size: "10Gi"}},
		DryRun:    true,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "Dry-run mode")

	if _, err := dyn.Resource(GVR()).Namespace("default").Get(
		context.Background(), "witwave", metav1.GetOptions{},
	); err == nil {
		t.Error("CR should NOT have been created in dry-run mode")
	}
}

func TestCreate_FromFile_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	manifest := filepath.Join(tmp, "ws.yaml")
	if err := os.WriteFile(manifest, []byte(`
apiVersion: witwave.ai/v1alpha1
kind: Workspace
metadata:
  name: witwave
spec:
  volumes:
    - name: source
      size: 50Gi
      accessMode: ReadWriteMany
      storageClassName: efs-sc
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := Create(context.Background(), smokeTarget(), nil, CreateOptions{
		Namespace: "default",
		FromFile:  manifest,
		AssumeYes: true,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "Created Workspace witwave")
}

func TestCreate_FromFile_KindMismatch(t *testing.T) {
	tmp := t.TempDir()
	manifest := filepath.Join(tmp, "wrong.yaml")
	if err := os.WriteFile(manifest, []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: nope
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := Create(context.Background(), smokeTarget(), nil, CreateOptions{
		Namespace: "default",
		FromFile:  manifest,
		AssumeYes: true,
		Out:       captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("expected apiVersion-mismatch error; got %v", err)
	}
}

func TestCreate_RejectsMixedFileAndFlags(t *testing.T) {
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	err := Create(context.Background(), smokeTarget(), nil, CreateOptions{
		Name:      "witwave",
		Namespace: "default",
		FromFile:  "/dev/null",
		Volumes:   []VolumeSpec{{Name: "x", Size: "1Gi"}},
		Out:       captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error; got %v", err)
	}
}

func TestCreate_VolumeMissingSize(t *testing.T) {
	dyn := makeFakeDynamic()
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))
	// Bypass ParseVolumeSpecs (which catches this) to verify the
	// downstream Build also rejects it. Defense-in-depth.
	err := Create(context.Background(), smokeTarget(), nil, CreateOptions{
		Name:      "witwave",
		Namespace: "default",
		Volumes:   []VolumeSpec{{Name: "source"}},
		AssumeYes: true,
		Out:       captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "size is required") {
		t.Errorf("expected size-required error; got %v", err)
	}
}
