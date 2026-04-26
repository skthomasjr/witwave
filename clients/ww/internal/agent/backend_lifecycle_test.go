package agent

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ---------------------------------------------------------------------------
// GitList
// ---------------------------------------------------------------------------

func TestGitList_NoSyncs(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	err := GitList(context.Background(), nil, GitListOptions{
		Agent: "hello", Namespace: "default", Out: out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain(t, out.String(), "no gitSyncs configured")
	mustContain(t, out.String(), "ww agent git add hello")
}

func TestGitList_WithSync(t *testing.T) {
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
		spec["gitMappings"] = []interface{}{
			map[string]interface{}{
				"gitSync": "my-sync",
				"src":     ".agents/hello/.witwave/",
				"dest":    "/home/agent/.witwave/",
			},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	out := captureOut()
	if err := GitList(context.Background(), nil, GitListOptions{
		Agent: "hello", Namespace: "default", Out: out,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"my-sync",
		"https://github.com/owner/repo.git",
		"60s",
		"hello-git-credentials",
		"/home/agent/.witwave/",
	} {
		mustContain(t, out.String(), want)
	}
}

// ---------------------------------------------------------------------------
// GitRemove
// ---------------------------------------------------------------------------

func TestGitRemove_AutoSelectsSoleSync(t *testing.T) {
	cr := seedAgent("hello", "default", withSingleGitSync("only-sync"))
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	// SyncName unset → should auto-select the agent's only gitSync.
	out := captureOut()
	if err := GitRemove(context.Background(), nil, GitRemoveOptions{
		Agent: "hello", Namespace: "default", AssumeYes: true, Out: out,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	updated := readAgent(t, dyn, "default", "hello")
	syncs, found, _ := unstructured.NestedSlice(updated.Object, "spec", "gitSyncs")
	if found && len(syncs) != 0 {
		t.Fatalf("expected gitSyncs to be empty after remove; got %d entries", len(syncs))
	}
}

func TestGitRemove_AmbiguousWithoutSyncName(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["gitSyncs"] = []interface{}{
			map[string]interface{}{"name": "first", "repo": "x"},
			map[string]interface{}{"name": "second", "repo": "y"},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := GitRemove(context.Background(), nil, GitRemoveOptions{
		Agent: "hello", Namespace: "default", AssumeYes: true, Out: captureOut(),
	})
	if err == nil {
		t.Fatal("expected error on ambiguous remove without --sync-name")
	}
	if !strings.Contains(err.Error(), "pick one") {
		t.Errorf("error = %q; want 'pick one' substring with the name list", err)
	}
}

func TestGitRemove_NoSyncs(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := GitRemove(context.Background(), nil, GitRemoveOptions{
		Agent: "hello", Namespace: "default", AssumeYes: true, Out: captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "nothing to remove") {
		t.Errorf("expected 'nothing to remove' error, got: %v", err)
	}
}

func TestGitRemove_DeleteSecretPreservesUserManaged(t *testing.T) {
	cr := seedAgent("hello", "default", withSingleGitSync("only-sync"))
	// Pre-existing Secret without the ww managed-by label — user-owned.
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hello-git-credentials", Namespace: "default",
		},
	}
	dyn := makeFakeDynamic(cr)
	k8s := makeFakeK8s(userSecret)
	t.Cleanup(withFakeClients(t, dyn, k8s))

	out := captureOut()
	if err := GitRemove(context.Background(), nil, GitRemoveOptions{
		Agent:        "hello",
		Namespace:    "default",
		DeleteSecret: true,
		AssumeYes:    true,
		Out:          out,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// User-managed Secret must still exist.
	if _, err := k8s.CoreV1().Secrets("default").Get(
		context.Background(), "hello-git-credentials", metav1.GetOptions{},
	); err != nil {
		t.Errorf("user-managed Secret was deleted: %v", err)
	}
	// Output should explicitly note the preservation.
	mustContain(t, out.String(), "not ww-managed")
}

func TestGitRemove_DeleteSecretDropsWWManaged(t *testing.T) {
	cr := seedAgent("hello", "default", withSingleGitSync("only-sync"))
	wwSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hello-git-credentials",
			Namespace: "default",
			Labels:    map[string]string{LabelManagedBy: LabelManagedByWW},
		},
	}
	dyn := makeFakeDynamic(cr)
	k8s := makeFakeK8s(wwSecret)
	t.Cleanup(withFakeClients(t, dyn, k8s))

	if err := GitRemove(context.Background(), nil, GitRemoveOptions{
		Agent:        "hello",
		Namespace:    "default",
		DeleteSecret: true,
		AssumeYes:    true,
		Out:          captureOut(),
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := k8s.CoreV1().Secrets("default").Get(
		context.Background(), "hello-git-credentials", metav1.GetOptions{},
	)
	if err == nil {
		t.Error("ww-managed Secret should have been deleted with --delete-secret")
	}
}

// ---------------------------------------------------------------------------
// BackendRemove
// ---------------------------------------------------------------------------

func TestBackendRemove_HappyPath_RewritesInlineBackendYAML(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["backends"] = []interface{}{
			echoBackend("echo-1", BackendPort(0)),
			echoBackend("echo-2", BackendPort(1)),
		}
		spec["config"] = []interface{}{
			map[string]interface{}{
				"name":      "backend.yaml",
				"mountPath": "/home/agent/.witwave/backend.yaml",
				"content": renderBackendYAML([]BackendSpec{
					{Name: "echo-1", Port: BackendPort(0)},
					{Name: "echo-2", Port: BackendPort(1)},
				}),
			},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	if err := BackendRemove(context.Background(), nil, BackendRemoveOptions{
		Agent: "hello", Namespace: "default", BackendName: "echo-2",
		AssumeYes: true, Out: captureOut(),
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := readAgent(t, dyn, "default", "hello")
	backends, _, _ := unstructured.NestedSlice(updated.Object, "spec", "backends")
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend remaining, got %d", len(backends))
	}
	if name, _ := backends[0].(map[string]interface{})["name"].(string); name != "echo-1" {
		t.Errorf("remaining backend = %q; want echo-1", name)
	}

	// Inline backend.yaml should have been regenerated without echo-2.
	cfg, _, _ := unstructured.NestedSlice(updated.Object, "spec", "config")
	content, _ := cfg[0].(map[string]interface{})["content"].(string)
	mustNotContain(t, content, "id: echo-2")
	mustContain(t, content, "id: echo-1")
	mustContain(t, content, "agent: echo-1")
}

func TestBackendRemove_RefusesLast(t *testing.T) {
	cr := seedAgent("hello", "default", nil) // default has one echo backend
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendRemove(context.Background(), nil, BackendRemoveOptions{
		Agent: "hello", Namespace: "default", BackendName: "echo",
		AssumeYes: true, Out: captureOut(),
	})
	if err == nil {
		t.Fatal("expected refusal when removing the last backend")
	}
	for _, want := range []string{"refusing", "last backend", "ww agent delete hello"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestBackendRemove_NotFound(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["backends"] = []interface{}{
			echoBackend("echo-1", BackendPort(0)),
			echoBackend("echo-2", BackendPort(1)),
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendRemove(context.Background(), nil, BackendRemoveOptions{
		Agent: "hello", Namespace: "default", BackendName: "echo-99",
		AssumeYes: true, Out: captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error; got: %v", err)
	}
	// Error message should list the available names so the user can
	// immediately see what they should have typed.
	if !strings.Contains(err.Error(), "echo-1") || !strings.Contains(err.Error(), "echo-2") {
		t.Errorf("error should enumerate current backends: %v", err)
	}
}

// ---------------------------------------------------------------------------
// BackendRename
// ---------------------------------------------------------------------------

func TestBackendRename_HappyPath_CROnly(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["backends"] = []interface{}{
			echoBackendWithMappings("echo-1", BackendPort(0)),
			echoBackendWithMappings("echo-2", BackendPort(1)),
		}
		spec["config"] = []interface{}{
			map[string]interface{}{
				"name":      "backend.yaml",
				"mountPath": "/home/agent/.witwave/backend.yaml",
				"content": renderBackendYAML([]BackendSpec{
					{Name: "echo-1", Port: BackendPort(0)},
					{Name: "echo-2", Port: BackendPort(1)},
				}),
			},
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	if err := BackendRename(context.Background(), nil, BackendRenameOptions{
		Agent:      "hello",
		Namespace:  "default",
		OldName:    "echo-2",
		NewName:    "echo-backup",
		RepoRename: false, // CR-only path — no git touched
		AssumeYes:  true,
		Out:        captureOut(),
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := readAgent(t, dyn, "default", "hello")

	// Backend name changed.
	backends, _, _ := unstructured.NestedSlice(updated.Object, "spec", "backends")
	names := collectBackendNames(backends)
	if names[0] != "echo-1" || names[1] != "echo-backup" {
		t.Errorf("backends = %v; want [echo-1 echo-backup]", names)
	}

	// Per-backend gitMappings dest should reflect new name.
	echoBackup := backends[1].(map[string]interface{})
	if bMaps, ok := echoBackup["gitMappings"].([]interface{}); ok {
		for _, raw := range bMaps {
			m := raw.(map[string]interface{})
			dest, _ := m["dest"].(string)
			if strings.Contains(dest, "echo-2") {
				t.Errorf("dest still references old name: %q", dest)
			}
		}
	}

	// Inline backend.yaml regenerated.
	cfg, _, _ := unstructured.NestedSlice(updated.Object, "spec", "config")
	content, _ := cfg[0].(map[string]interface{})["content"].(string)
	mustNotContain(t, content, "id: echo-2")
	mustContain(t, content, "id: echo-backup")
}

func TestBackendRename_RefusesSameName(t *testing.T) {
	dyn := makeFakeDynamic(seedAgent("hello", "default", nil))
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendRename(context.Background(), nil, BackendRenameOptions{
		Agent: "hello", Namespace: "default", OldName: "echo", NewName: "echo",
		RepoRename: false, Out: captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "matches old name") {
		t.Errorf("expected 'matches old name' error; got: %v", err)
	}
}

func TestBackendRename_RefusesCollision(t *testing.T) {
	cr := seedAgent("hello", "default", func(spec map[string]interface{}) {
		spec["backends"] = []interface{}{
			echoBackend("echo-1", BackendPort(0)),
			echoBackend("echo-2", BackendPort(1)),
		}
	})
	dyn := makeFakeDynamic(cr)
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendRename(context.Background(), nil, BackendRenameOptions{
		Agent: "hello", Namespace: "default",
		OldName: "echo-1", NewName: "echo-2", // already exists
		RepoRename: false, Out: captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error; got: %v", err)
	}
}

func TestBackendRename_OldNotFound(t *testing.T) {
	dyn := makeFakeDynamic(seedAgent("hello", "default", nil))
	t.Cleanup(withFakeClients(t, dyn, makeFakeK8s()))

	err := BackendRename(context.Background(), nil, BackendRenameOptions{
		Agent: "hello", Namespace: "default",
		OldName: "missing", NewName: "new-name",
		RepoRename: false, Out: captureOut(),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error; got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// withSingleGitSync is a seedAgent mutator that adds one gitSync plus
// the default harness mapping + echo-backend mapping. Used across
// remove tests that need a wired agent to start from.
func withSingleGitSync(name string) func(spec map[string]interface{}) {
	return func(spec map[string]interface{}) {
		spec["gitSyncs"] = []interface{}{
			map[string]interface{}{
				"name":   name,
				"repo":   "https://github.com/owner/repo.git",
				"period": "60s",
				"credentials": map[string]interface{}{
					"existingSecret": "hello-git-credentials",
				},
			},
		}
		spec["gitMappings"] = []interface{}{
			map[string]interface{}{
				"gitSync": name,
				"src":     ".agents/hello/.witwave/",
				"dest":    "/home/agent/.witwave/",
			},
		}
	}
}

// echoBackend returns a bare spec.backends[] entry for an echo backend.
func echoBackend(name string, port int32) map[string]interface{} {
	return map[string]interface{}{
		"name": name,
		"port": int64(port),
		"image": map[string]interface{}{
			"repository": "ghcr.io/witwave-ai/images/echo",
			"tag":        "test",
		},
	}
}

// echoBackendWithMappings is echoBackend plus a per-backend gitMapping
// that references the backend's folder, so rename tests can verify
// dest paths get rewritten.
func echoBackendWithMappings(name string, port int32) map[string]interface{} {
	entry := echoBackend(name, port)
	entry["gitMappings"] = []interface{}{
		map[string]interface{}{
			"gitSync": "witwave-test",
			"src":     ".agents/hello/." + name + "/",
			"dest":    "/home/agent/." + name + "/",
		},
	}
	return entry
}

// collectBackendNames extracts names from a []interface{} of backend
// map entries. Terse helper for assertions.
func collectBackendNames(backends []interface{}) []string {
	out := make([]string, 0, len(backends))
	for _, b := range backends {
		if m, ok := b.(map[string]interface{}); ok {
			if name, _ := m["name"].(string); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}
