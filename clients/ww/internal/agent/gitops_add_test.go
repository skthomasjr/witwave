package agent

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGitAdd_HappyPath_PublicRepo(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	k8s := makeFakeK8s()
	t.Cleanup(withFakeClients(t, dyn, k8s))

	out := captureOut()
	err := GitAdd(context.Background(), nil, GitAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Repo:      "owner/public-repo",
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify CR state: gitSyncs[0] + harness mapping + per-backend mapping.
	updated := readAgent(t, dyn, "default", "hello")
	syncs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "gitSyncs")
	if len(syncs) != 1 {
		t.Fatalf("expected 1 gitSync, got %d", len(syncs))
	}
	sync := syncs[0].(map[string]interface{})
	if name, _ := sync["name"].(string); name != "public-repo" {
		t.Errorf("sync.name = %q; want %q (derived from repo basename)", name, "public-repo")
	}
	// Public repo → no credentials field.
	if _, hasCreds := sync["credentials"]; hasCreds {
		t.Errorf("public repo should not have credentials: %+v", sync)
	}

	harnessMaps, _, _ := unstructured.NestedSlice(updated.Object, "spec", "gitMappings")
	if len(harnessMaps) != 1 {
		t.Fatalf("expected 1 harness mapping, got %d", len(harnessMaps))
	}
	hm := harnessMaps[0].(map[string]interface{})
	if dest, _ := hm["dest"].(string); dest != "/home/agent/.witwave/" {
		t.Errorf("harness mapping dest = %q; want /home/agent/.witwave/", dest)
	}
	if src, _ := hm["src"].(string); src != ".agents/hello/.witwave/" {
		t.Errorf("harness mapping src = %q; want .agents/hello/.witwave/", src)
	}

	// Per-backend mapping on the echo backend.
	backends, _, _ := unstructured.NestedSlice(updated.Object, "spec", "backends")
	echo := backends[0].(map[string]interface{})
	bMaps, _ := echo["gitMappings"].([]interface{})
	if len(bMaps) != 1 {
		t.Fatalf("expected 1 backend mapping, got %d", len(bMaps))
	}
	bm := bMaps[0].(map[string]interface{})
	if dest, _ := bm["dest"].(string); dest != "/home/agent/.echo/" {
		t.Errorf("backend mapping dest = %q; want /home/agent/.echo/", dest)
	}

	// No Secret created for public repo.
	_, err = k8s.CoreV1().Secrets("default").Get(context.Background(), "hello-git-credentials", metav1.GetOptions{})
	if err == nil {
		t.Error("public repo should not create a Secret")
	}

	mustContain(t, out.String(), "Attached gitSync")
}

func TestGitAdd_MintsSecretFromEnv(t *testing.T) {
	// Can't be t.Parallel — t.Setenv mutates process env, which is a
	// shared resource across the package's parallel tests.
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	k8s := makeFakeK8s()
	t.Cleanup(withFakeClients(t, dyn, k8s))
	t.Setenv("WW_TEST_GIT_TOKEN", "ghp_fake_token_12345")

	out := captureOut()
	err := GitAdd(context.Background(), nil, GitAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Repo:      "owner/private-repo",
		Auth:      GitAuthResolver{Mode: GitAuthFromEnv, EnvVar: "WW_TEST_GIT_TOKEN"},
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Secret should be minted with the standard name + labels.
	sec, err := k8s.CoreV1().Secrets("default").Get(context.Background(), "hello-git-credentials", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Secret not minted: %v", err)
	}
	if sec.Labels[LabelManagedBy] != LabelManagedByWW {
		t.Errorf("Secret missing managed-by label: %+v", sec.Labels)
	}
	if string(sec.StringData["GITSYNC_USERNAME"]) != "x-access-token" {
		t.Errorf("GITSYNC_USERNAME = %q; want x-access-token", sec.StringData["GITSYNC_USERNAME"])
	}
	if string(sec.StringData["GITSYNC_PASSWORD"]) != "ghp_fake_token_12345" {
		t.Errorf("GITSYNC_PASSWORD not the token from env")
	}

	// CR should reference the Secret.
	updated := readAgent(t, dyn, "default", "hello")
	syncs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "gitSyncs")
	sync := syncs[0].(map[string]interface{})
	creds, _ := sync["credentials"].(map[string]interface{})
	if sec, _ := creds["existingSecret"].(string); sec != "hello-git-credentials" {
		t.Errorf("sync.credentials.existingSecret = %q; want hello-git-credentials", sec)
	}
}

func TestGitAdd_VerifiesExistingSecret(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	// Pre-create a Secret the user supposedly manages themselves.
	preExisting := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pat", Namespace: "default"},
	}
	dyn := makeFakeDynamic(cr)
	k8s := makeFakeK8s(preExisting)
	t.Cleanup(withFakeClients(t, dyn, k8s))

	out := captureOut()
	err := GitAdd(context.Background(), nil, GitAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Repo:      "owner/private-repo",
		Auth:      GitAuthResolver{Mode: GitAuthExistingSecret, ExistingSecret: "my-pat"},
		Out:       out,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// User Secret is preserved — no managed-by label added on update.
	got, _ := k8s.CoreV1().Secrets("default").Get(context.Background(), "my-pat", metav1.GetOptions{})
	if _, labelled := got.Labels[LabelManagedBy]; labelled {
		t.Error("user-managed Secret should not be touched by GitAdd")
	}
}

func TestGitAdd_ExistingSecretMissing_ReturnsActionableError(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	k8s := makeFakeK8s() // no pre-existing Secret
	t.Cleanup(withFakeClients(t, dyn, k8s))

	err := GitAdd(context.Background(), nil, GitAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Repo:      "owner/private-repo",
		Auth:      GitAuthResolver{Mode: GitAuthExistingSecret, ExistingSecret: "missing-pat"},
		Out:       captureOut(),
	})
	if err == nil {
		t.Fatal("expected error when referenced Secret doesn't exist")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q; want 'not found' substring", err)
	}
	if !strings.Contains(err.Error(), "kubectl -n default create secret") {
		t.Errorf("error should include a copy-pasteable recovery recipe: %q", err)
	}
}

func TestGitAdd_Idempotent_SameSyncNameReplaces(t *testing.T) {
	cr := seedAgent("hello", "default", nil)
	dyn := makeFakeDynamic(cr)
	k8s := makeFakeK8s()
	t.Cleanup(withFakeClients(t, dyn, k8s))

	opts := GitAddOptions{
		Agent:     "hello",
		Namespace: "default",
		Repo:      "owner/repo-a",
		SyncName:  "primary",
		Out:       captureOut(),
	}
	if err := GitAdd(context.Background(), nil, opts); err != nil {
		t.Fatalf("first GitAdd: %v", err)
	}
	// Re-run pointing the SAME sync-name at a different repo. The
	// existing entry should be replaced rather than duplicated.
	opts.Repo = "owner/repo-b"
	opts.Out = captureOut()
	if err := GitAdd(context.Background(), nil, opts); err != nil {
		t.Fatalf("second GitAdd: %v", err)
	}
	updated := readAgent(t, dyn, "default", "hello")
	syncs, _, _ := unstructured.NestedSlice(updated.Object, "spec", "gitSyncs")
	if len(syncs) != 1 {
		t.Fatalf("expected 1 gitSync (replaced, not duplicated), got %d", len(syncs))
	}
	sync := syncs[0].(map[string]interface{})
	if repo, _ := sync["repo"].(string); !strings.Contains(repo, "repo-b") {
		t.Errorf("sync.repo = %q; want the second repo URL (replace semantics)", repo)
	}
}

func TestGitAdd_AgentNotFound(t *testing.T) {
	dyn := makeFakeDynamic() // no agents seeded
	k8s := makeFakeK8s()
	t.Cleanup(withFakeClients(t, dyn, k8s))

	err := GitAdd(context.Background(), nil, GitAddOptions{
		Agent:     "nonexistent",
		Namespace: "default",
		Repo:      "owner/repo",
		Out:       captureOut(),
	})
	if err == nil {
		t.Fatal("expected error when agent CR doesn't exist")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q; want 'not found' substring", err)
	}
}
