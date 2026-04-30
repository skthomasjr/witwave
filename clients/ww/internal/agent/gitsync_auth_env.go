package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ---------------------------------------------------------------------------
// gitSync credential wiring for `ww agent create --gitsync-from-env`.
//
// Sibling of --secret-from-env (which targets backend containers via envFrom).
// This flag lifts two shell vars (the user's PAT or password + username) into
// a single per-agent Secret with the standard git-sync env keys, and stamps
// the Secret reference onto every gitSyncs[] entry on the agent. One Secret
// per agent is the common case — the rare cross-org "different creds per
// repo" path is still served by the long-form --gitsync-secret <name>=<k8s>.
// ---------------------------------------------------------------------------

// GitSyncFromEnvSpec captures one parsed --gitsync-from-env flag value.
// Form: <USER_VAR>:<PASS_VAR>. The CLI reads $USER_VAR / $PASS_VAR from
// the shell and stores them in the minted Secret under the upstream
// kubernetes/git-sync env-var keys (GITSYNC_USERNAME / GITSYNC_PASSWORD).
type GitSyncFromEnvSpec struct {
	UserVar string
	PassVar string
}

// ParseGitSyncFromEnv splits a `<USER_VAR>:<PASS_VAR>` flag value. Both
// halves are required and must be non-empty — there's no bare-name form
// because the operator-side wire contract demands both keys to drive a
// successful HTTPS clone.
func ParseGitSyncFromEnv(raw string) (GitSyncFromEnvSpec, error) {
	entry := strings.TrimSpace(raw)
	if entry == "" {
		return GitSyncFromEnvSpec{}, fmt.Errorf("--gitsync-from-env: empty value")
	}
	i := strings.IndexByte(entry, ':')
	if i < 0 {
		return GitSyncFromEnvSpec{}, fmt.Errorf("--gitsync-from-env %q: form is <USER_VAR>:<PASS_VAR>", entry)
	}
	user := strings.TrimSpace(entry[:i])
	pass := strings.TrimSpace(entry[i+1:])
	if user == "" || pass == "" {
		return GitSyncFromEnvSpec{}, fmt.Errorf("--gitsync-from-env %q: both <USER_VAR> and <PASS_VAR> are required", entry)
	}
	return GitSyncFromEnvSpec{UserVar: user, PassVar: pass}, nil
}

// gitsyncFromEnvSecretName is the per-agent gitSync credential Secret
// minted by --gitsync-from-env. Predictable name (one per agent) so
// operators can find + rotate it; mirrors the bare backend-Secret
// convention (<agent>-<backend>) by appending the gitsync target.
//
// NOTE: deliberately distinct from the operator's
// gitsyncCredentialsSecretName (`<agent>-<entry>-gitsync-credentials`),
// which is reserved for the inline-CR path the operator owns. This name
// is for the CLI-minted, agent-wide Secret; ownership stays with the
// CLI's existing-secret-reference path.
func gitsyncFromEnvSecretName(agentName string) string {
	return agentName + "-gitsync"
}

// ResolveGitSyncFromEnv reads the named env vars, mints (or updates) the
// per-agent gitSync Secret, and returns its name. Errors with a clear
// "set $X in your shell" diagnostic when either env var is missing.
func ResolveGitSyncFromEnv(
	ctx context.Context,
	k8s kubernetes.Interface,
	namespace, agentName string,
	spec GitSyncFromEnvSpec,
) (string, error) {
	user := strings.TrimSpace(os.Getenv(spec.UserVar))
	pass := strings.TrimSpace(os.Getenv(spec.PassVar))
	var missing []string
	if user == "" {
		missing = append(missing, spec.UserVar)
	}
	if pass == "" {
		missing = append(missing, spec.PassVar)
	}
	if len(missing) > 0 {
		return "", fmt.Errorf(
			"--gitsync-from-env: required environment variable(s) unset or empty: %s",
			strings.Join(missing, ", "),
		)
	}
	data := map[string]string{
		"GITSYNC_USERNAME": user,
		"GITSYNC_PASSWORD": pass,
	}
	name := gitsyncFromEnvSecretName(agentName)
	createdBy := fmt.Sprintf("ww agent create --gitsync-from-env %s:%s", spec.UserVar, spec.PassVar)
	if err := upsertGitSyncFromEnvSecret(ctx, k8s, namespace, name, agentName, data, createdBy); err != nil {
		return "", err
	}
	return name, nil
}

// upsertGitSyncFromEnvSecret creates-or-updates the per-agent gitSync
// credential Secret. Refuses to clobber a hand-rolled Secret at the
// same name (managed-by label gates the update path).
func upsertGitSyncFromEnvSecret(
	ctx context.Context,
	k8s kubernetes.Interface,
	namespace, secretName, agentName string,
	data map[string]string,
	createdBy string,
) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				LabelManagedBy:               LabelManagedByWW,
				"app.kubernetes.io/name":     agentName,
				"witwave.ai/credential-type": "gitsync",
			},
			Annotations: map[string]string{
				"witwave.ai/created-by": createdBy,
			},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}

	existing, err := k8s.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		if _, err := k8s.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create Secret %s/%s: %w", namespace, secretName, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get Secret %s/%s: %w", namespace, secretName, err)
	default:
		if existing.Labels[LabelManagedBy] != LabelManagedByWW {
			return fmt.Errorf(
				"Secret %s/%s exists but is not ww-managed. "+
					"Use `--gitsync-secret <gitsync-name>=%s` to reference it as-is, or delete it and retry",
				namespace, secretName, secretName,
			)
		}
		existing.StringData = secret.StringData
		existing.Labels = secret.Labels
		existing.Annotations = secret.Annotations
		if _, err := k8s.CoreV1().Secrets(namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update Secret %s/%s: %w", namespace, secretName, err)
		}
		return nil
	}
}

// StampGitSyncSecretOnAll sets ExistingSecret on every GitSyncFlagSpec
// that doesn't already have one. Per-entry --gitsync-secret values win
// (precedence: explicit per-entry > agent-wide --gitsync-from-env).
// Returns the syncs slice unchanged when secretName is empty.
func StampGitSyncSecretOnAll(syncs []GitSyncFlagSpec, secretName string) []GitSyncFlagSpec {
	if secretName == "" {
		return syncs
	}
	for i := range syncs {
		if syncs[i].ExistingSecret == "" {
			syncs[i].ExistingSecret = secretName
		}
	}
	return syncs
}
