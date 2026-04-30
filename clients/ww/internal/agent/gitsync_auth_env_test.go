package agent

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseGitSyncFromEnv_HappyPath(t *testing.T) {
	t.Parallel()
	got, err := ParseGitSyncFromEnv("GITSYNC_USERNAME_IRIS:GITSYNC_PASSWORD_IRIS")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UserVar != "GITSYNC_USERNAME_IRIS" || got.PassVar != "GITSYNC_PASSWORD_IRIS" {
		t.Errorf("got = %+v; want UserVar=GITSYNC_USERNAME_IRIS PassVar=GITSYNC_PASSWORD_IRIS", got)
	}
}

func TestParseGitSyncFromEnv_RejectsMissingHalves(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",           // empty value
		"USER_ONLY",  // no colon
		":PASS_ONLY", // empty user half
		"USER_ONLY:", // empty pass half
		":",          // both empty
		"  :  ",      // whitespace-only halves
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			_, err := ParseGitSyncFromEnv(c)
			if err == nil {
				t.Errorf("ParseGitSyncFromEnv(%q): expected error, got nil", c)
			}
		})
	}
}

func TestResolveGitSyncFromEnv_MintsSecret(t *testing.T) {
	t.Setenv("MY_USER", "iris-bot")
	t.Setenv("MY_PASS", "ghp_secret_123")
	k8s := makeFakeK8s()

	name, err := ResolveGitSyncFromEnv(context.Background(), k8s, "witwave", "iris", GitSyncFromEnvSpec{
		UserVar: "MY_USER",
		PassVar: "MY_PASS",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "iris-gitsync" {
		t.Errorf("name = %q; want iris-gitsync", name)
	}
	sec, err := k8s.CoreV1().Secrets("witwave").Get(context.Background(), "iris-gitsync", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Secret not minted: %v", err)
	}
	if sec.StringData["GITSYNC_USERNAME"] != "iris-bot" {
		t.Errorf("GITSYNC_USERNAME = %q; want iris-bot", sec.StringData["GITSYNC_USERNAME"])
	}
	if sec.StringData["GITSYNC_PASSWORD"] != "ghp_secret_123" {
		t.Errorf("GITSYNC_PASSWORD value didn't land")
	}
	if sec.Labels[LabelManagedBy] != LabelManagedByWW {
		t.Errorf("missing managed-by label")
	}
	if sec.Labels["witwave.ai/credential-type"] != "gitsync" {
		t.Errorf("credential-type label = %q; want gitsync", sec.Labels["witwave.ai/credential-type"])
	}
	createdBy := sec.Annotations["witwave.ai/created-by"]
	if strings.Contains(createdBy, "iris-bot") || strings.Contains(createdBy, "ghp_secret_123") {
		t.Errorf("created-by annotation leaks values: %q", createdBy)
	}
}

func TestResolveGitSyncFromEnv_MissingEnvVar(t *testing.T) {
	t.Setenv("ONLY_USER", "iris-bot")
	// Deliberately don't set MISSING_PASS.
	k8s := makeFakeK8s()
	_, err := ResolveGitSyncFromEnv(context.Background(), k8s, "witwave", "iris", GitSyncFromEnvSpec{
		UserVar: "ONLY_USER",
		PassVar: "MISSING_PASS",
	})
	if err == nil {
		t.Fatal("expected error when PassVar is unset")
	}
	if !strings.Contains(err.Error(), "MISSING_PASS") {
		t.Errorf("error = %q; want it to name MISSING_PASS", err)
	}
}

func TestStampGitSyncSecretOnAll(t *testing.T) {
	t.Parallel()
	syncs := []GitSyncFlagSpec{
		{Name: "config", URL: "https://github.com/org/config.git"},
		{Name: "private-override", URL: "https://github.com/org/other.git", ExistingSecret: "preexisting"},
		{Name: "second", URL: "https://github.com/org/foo.git"},
	}
	out := StampGitSyncSecretOnAll(syncs, "iris-gitsync")
	if out[0].ExistingSecret != "iris-gitsync" {
		t.Errorf("entry[0].ExistingSecret = %q; want iris-gitsync", out[0].ExistingSecret)
	}
	// Per-entry value wins; agent-wide stamp must not overwrite.
	if out[1].ExistingSecret != "preexisting" {
		t.Errorf("entry[1].ExistingSecret = %q; want preexisting (per-entry wins)", out[1].ExistingSecret)
	}
	if out[2].ExistingSecret != "iris-gitsync" {
		t.Errorf("entry[2].ExistingSecret = %q; want iris-gitsync", out[2].ExistingSecret)
	}
}

func TestStampGitSyncSecretOnAll_EmptyName(t *testing.T) {
	t.Parallel()
	syncs := []GitSyncFlagSpec{{Name: "config"}}
	out := StampGitSyncSecretOnAll(syncs, "")
	if out[0].ExistingSecret != "" {
		t.Errorf("empty secretName should be a no-op; got %q", out[0].ExistingSecret)
	}
}
