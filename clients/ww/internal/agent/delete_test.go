package agent

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
)

// smokeTarget returns a minimal *k8s.Target suitable for preflight
// rendering in unit tests. AssumeYes=true skips the prompt, so these
// fields are cosmetic — but `k8s.Confirm` still dereferences the
// pointer, so non-nil matters.
func smokeTarget() *k8s.Target {
	return &k8s.Target{
		Context:   "fake-context",
		Cluster:   "fake-cluster",
		Server:    "https://fake.invalid",
		Namespace: "default",
	}
}

func TestDelete_HappyPath_NoFlags(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	k8s := makeFakeK8s()
	t.Cleanup(withFakeClients(t, dyn, k8s))

	out := captureOut()
	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:      "hello",
		Namespace: "default",
		AssumeYes: true,
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "Deleted WitwaveAgent hello")

	// CR should no longer exist.
	if _, err := dyn.Resource(GVR()).Namespace("default").Get(
		context.Background(), "hello", metav1.GetOptions{},
	); err == nil {
		t.Error("CR should have been deleted")
	}
}

func TestDelete_DeleteGitSecret_RemovesWWManaged(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["gitSyncs"] = []interface{}{
			map[string]interface{}{
				"name":   "my-sync",
				"repo":   "https://github.com/owner/repo.git",
				"period": "60s",
				"credentials": map[string]interface{}{
					"existingSecret": "hello-git-credentials",
				},
			},
		}
	})
	// Pre-seed the ww-managed credential Secret.
	managed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hello-git-credentials",
			Namespace: "default",
			Labels:    map[string]string{LabelManagedBy: LabelManagedByWW},
		},
	}
	dyn := makeFakeDynamic(cr)
	k8sClient := makeFakeK8s(managed)
	t.Cleanup(withFakeClients(t, dyn, k8sClient))

	out := captureOut()
	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:            "hello",
		Namespace:       "default",
		DeleteGitSecret: true,
		AssumeYes:       true,
		Out:             out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := k8sClient.CoreV1().Secrets("default").Get(
		context.Background(), "hello-git-credentials", metav1.GetOptions{},
	); err == nil {
		t.Error("ww-managed Secret should have been deleted")
	}
	mustContain(t, out.String(), "Deleted ww-managed Secret")
}

func TestDelete_DeleteGitSecret_PreservesUserManaged(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["gitSyncs"] = []interface{}{
			map[string]interface{}{
				"name":   "my-sync",
				"repo":   "https://github.com/owner/repo.git",
				"period": "60s",
				"credentials": map[string]interface{}{
					"existingSecret": "my-pat",
				},
			},
		}
	})
	// User-created Secret: no managed-by label.
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pat",
			Namespace: "default",
		},
	}
	dyn := makeFakeDynamic(cr)
	k8sClient := makeFakeK8s(userSecret)
	t.Cleanup(withFakeClients(t, dyn, k8sClient))

	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:            "hello",
		Namespace:       "default",
		DeleteGitSecret: true,
		AssumeYes:       true,
		Out:             captureOut(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// User Secret preserved — ww's label gate blocked the delete.
	got, err := k8sClient.CoreV1().Secrets("default").Get(
		context.Background(), "my-pat", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("user Secret should have been preserved: %v", err)
	}
	if _, labelled := got.Labels[LabelManagedBy]; labelled {
		t.Error("user-managed Secret should not have gained a managed-by label")
	}
}

func TestDelete_RemoveRepoFolder_NoGitSync_SoftSkips(t *testing.T) {
	// No gitSyncs on the CR → the wipe is a no-op, NOT an error. The
	// benign case ("nothing to wipe") is the one scenario where soft-
	// skipping is the right answer.
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	k8sClient := makeFakeK8s()
	t.Cleanup(withFakeClients(t, dyn, k8sClient))

	out := captureOut()
	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:             "hello",
		Namespace:        "default",
		RemoveRepoFolder: true,
		AssumeYes:        true,
		Out:              out,
	})
	if err != nil {
		t.Fatalf("expected no error when there's no gitSync to wipe; got: %v", err)
	}
	mustContain(t, out.String(), "no gitSync configured")
	mustContain(t, out.String(), "Deleted WitwaveAgent hello")
}

func TestDelete_RemoveRepoFolder_MultipleSyncs_HardFails(t *testing.T) {
	// Two gitSyncs → genuinely ambiguous which to wipe. Hard-fail so
	// the user picks one via --git-remove first.
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["gitSyncs"] = []interface{}{
			map[string]interface{}{
				"name":   "primary",
				"repo":   "https://github.com/owner/primary.git",
				"period": "60s",
			},
			map[string]interface{}{
				"name":   "secondary",
				"repo":   "https://github.com/owner/secondary.git",
				"period": "60s",
			},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:             "hello",
		Namespace:        "default",
		RemoveRepoFolder: true,
		AssumeYes:        true,
		Out:              captureOut(),
	})
	if err == nil {
		t.Fatal("expected an error with multiple gitSyncs + --remove-repo-folder")
	}
	if !strings.Contains(err.Error(), "ambiguous") && !strings.Contains(err.Error(), "gitSyncs configured") {
		t.Errorf("error = %q; want ambiguity message", err)
	}

	// CR should NOT have been deleted — repo-side failed pre-CR.
	if _, err := dyn.Resource(GVR()).Namespace("default").Get(
		context.Background(), "hello", metav1.GetOptions{},
	); err != nil {
		t.Errorf("CR should be preserved on repo-side failure; got: %v", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	dyn := makeFakeDynamic() // no CRs seeded
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := Delete(context.Background(), smokeTarget(), nil, DeleteOptions{
		Name:      "ghost",
		Namespace: "default",
		AssumeYes: true,
		Out:       captureOut(),
	})
	if err == nil {
		t.Fatal("expected an error for missing agent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q; want 'not found' substring", err)
	}
}
