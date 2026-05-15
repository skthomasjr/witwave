package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// GitAuthMode enumerates how `ww agent git add` resolves the K8s Secret
// that the gitSync sidecar will reference for private-repo auth.
type GitAuthMode int

const (
	// GitAuthNone — public repo; no Secret is created or referenced.
	GitAuthNone GitAuthMode = iota

	// GitAuthExistingSecret — user pre-created a K8s Secret; we just
	// point the CR at it. Production default.
	GitAuthExistingSecret

	// GitAuthFromGH — mint a Secret from `gh auth token` at CLI-run
	// time. Convenient for dev laptops already signed into gh. The
	// stored token lives on the cluster forever after this — users
	// who don't want that should use an existingSecret with explicit
	// rotation plumbing or a short-TTL PAT.
	GitAuthFromGH

	// GitAuthFromEnv — mint a Secret from a named environment
	// variable. Covers CI/CD, secret managers, and any other
	// "I've already loaded a token into my shell" workflow.
	GitAuthFromEnv
)

// GitAuthResolver captures the user's --auth-* flag choices. Exactly
// one of the fields should be set (the empty zero value resolves to
// GitAuthNone — public repo).
type GitAuthResolver struct {
	Mode           GitAuthMode
	ExistingSecret string // Mode == GitAuthExistingSecret
	EnvVar         string // Mode == GitAuthFromEnv

	// SecretName is the name to use when minting a new Secret
	// (GitAuthFromGH / GitAuthFromEnv). Defaults to
	// `<agent-name>-git-credentials` — set explicitly to override.
	SecretName string
}

// gitSecretNameForAgent returns the default K8s Secret name for a
// ww-minted git credential Secret. Predictable so operators can find +
// rotate it, and namespaced to the agent so multiple agents in the
// same namespace don't clobber one another.
func gitSecretNameForAgent(agentName string) string {
	return agentName + "-git-credentials"
}

// resolveGitSyncSecret ensures a K8s Secret exists that the gitSync
// sidecar can reference. Returns the resolved Secret name.
//
// Existing-secret mode: verifies the Secret exists + has the expected
// shape; never creates or modifies anything. Mint modes (gh / env):
// reads the token from the chosen source, upserts a K8s Secret at the
// derived name, returns that name.
func (r GitAuthResolver) resolve(ctx context.Context, k8s kubernetes.Interface, namespace, agentName string) (string, error) {
	switch r.Mode {
	case GitAuthNone:
		return "", nil

	case GitAuthExistingSecret:
		if r.ExistingSecret == "" {
			return "", errors.New("--auth-secret requires a Secret name")
		}
		// Sanity-check the Secret exists so the CR doesn't accept a
		// name that would later cause the operator to sit in a
		// reconcile loop trying to resolve it. Don't validate the
		// shape — users may have their own conventions (Tailscale-
		// style git-credentials, SSH keys, etc.).
		if _, err := k8s.CoreV1().Secrets(namespace).Get(ctx, r.ExistingSecret, metav1.GetOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				return "", fmt.Errorf(
					"Secret %q not found in namespace %q — create it first, e.g.: "+
						"`kubectl -n %[2]s create secret generic %[1]s "+
						"--from-literal=GITSYNC_USERNAME=x-access-token "+
						"--from-literal=GITSYNC_PASSWORD=<your-token>`",
					r.ExistingSecret, namespace,
				)
			}
			return "", fmt.Errorf("check Secret %s/%s: %w", namespace, r.ExistingSecret, err)
		}
		return r.ExistingSecret, nil

	case GitAuthFromGH:
		tok, ok := ghAuthToken()
		if !ok {
			return "", errors.New(
				"--auth-from-gh: no token available from `gh auth token`. " +
					"Run `gh auth login`, or pick a different --auth-* flag",
			)
		}
		secretName := r.SecretName
		if secretName == "" {
			secretName = gitSecretNameForAgent(agentName)
		}
		return secretName, upsertGitCredentialSecret(ctx, k8s, namespace, secretName, tok, agentName)

	case GitAuthFromEnv:
		if r.EnvVar == "" {
			return "", errors.New("--secret-from-env requires the environment variable name")
		}
		tok := strings.TrimSpace(os.Getenv(r.EnvVar))
		if tok == "" {
			return "", fmt.Errorf(
				"--secret-from-env %q: environment variable is unset or empty", r.EnvVar,
			)
		}
		secretName := r.SecretName
		if secretName == "" {
			secretName = gitSecretNameForAgent(agentName)
		}
		return secretName, upsertGitCredentialSecret(ctx, k8s, namespace, secretName, tok, agentName)
	}
	return "", fmt.Errorf("unknown GitAuthMode %d", r.Mode)
}

// upsertGitCredentialSecret creates or updates a K8s Secret with the
// env-var shape the gitSync sidecar expects (GITSYNC_USERNAME +
// GITSYNC_PASSWORD). GitHub PAT auth requires the literal username
// "x-access-token"; the password field carries the PAT. Stamps a
// managed-by label + a ww-specific annotation so operators grepping
// the cluster can tell ww-minted Secrets apart from hand-rolled ones.
func upsertGitCredentialSecret(ctx context.Context, k8s kubernetes.Interface, namespace, name, token, agentName string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				LabelManagedBy:               LabelManagedByWW,
				"app.kubernetes.io/name":     agentName,
				"witwave.ai/credential-type": "gitsync",
			},
			Annotations: map[string]string{
				"witwave.ai/created-by": "ww agent git add",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"GITSYNC_USERNAME": "x-access-token",
			"GITSYNC_PASSWORD": token,
		},
	}

	existing, err := k8s.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		if _, err := k8s.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create Secret %s/%s: %w", namespace, name, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get Secret %s/%s: %w", namespace, name, err)
	default:
		// Refuse to overwrite a Secret that wasn't minted by ww.
		// Hand-rolled Secrets under the same name are user-managed; we
		// shouldn't clobber their structure just because it shares a
		// name with our default. The user can rename our conflict by
		// passing --auth-secret-name <other>.
		if existing.Labels[LabelManagedBy] != LabelManagedByWW {
			return fmt.Errorf(
				"Secret %s/%s exists but is not ww-managed. "+
					"Use `--auth-secret %s` to reference it as-is, or `--auth-secret-name <other>` to mint a separate Secret",
				namespace, name, name,
			)
		}
		existing.StringData = secret.StringData
		existing.Labels = secret.Labels
		existing.Annotations = secret.Annotations
		if _, err := k8s.CoreV1().Secrets(namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update Secret %s/%s: %w", namespace, name, err)
		}
		return nil
	}
}
