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

// ---------------------------------------------------------------------------
// Backend credential wiring for `ww agent create`.
//
// Mirrors the three-tier pattern `ww agent git add` uses for gitSync auth:
//
//   Tier 1 — named profile:     --auth claude=oauth        (MVP: claude only)
//   Tier 2 — arbitrary env:     --auth-from-env claude=FOO  (escape hatch)
//   Tier 3 — existing Secret:   --auth-secret claude=name   (pre-created)
//
// Each choice resolves to a K8s Secret that the CR's
// spec.backends[].credentials.existingSecret field points at; the operator
// mounts it into the backend container as envFrom at reconcile time.
// ---------------------------------------------------------------------------

// BackendAuthMode enumerates how the caller wants the CLI to resolve a
// per-backend credential Secret.
type BackendAuthMode int

const (
	// BackendAuthNone — no Secret is referenced (the default when no
	// --auth-* flag is passed for a backend). For backends like echo
	// that don't need credentials, this is the correct state.
	BackendAuthNone BackendAuthMode = iota

	// BackendAuthProfile — named profile (e.g. claude=oauth). The CLI
	// looks up the profile in the catalog, reads the corresponding env
	// var(s) from the user's shell, and mints a Secret with those keys.
	BackendAuthProfile

	// BackendAuthFromEnv — user names the env var(s) directly. Escape
	// hatch for custom setups not covered by a named profile. The
	// minted Secret keys match the env var names verbatim.
	BackendAuthFromEnv

	// BackendAuthExistingSecret — user pre-created a Secret; the CLI
	// just verifies it exists and points the CR at it. Production
	// default for ops who manage credential rotation out of band.
	BackendAuthExistingSecret
)

// BackendAuthResolver captures the user's credential choice for a single
// backend on a WitwaveAgent. Populated per --auth-* flag entry by the
// cobra parser.
type BackendAuthResolver struct {
	// Backend is the spec.backends[].name this resolver targets.
	// Validated against the agent's backend list before resolution.
	Backend string

	Mode BackendAuthMode

	// Profile name (BackendAuthProfile) — e.g. "oauth", "api-key".
	// Resolved against the catalog at run-time.
	Profile string

	// EnvVars (BackendAuthFromEnv) — env var names in the user's shell
	// whose values become Secret keys of the same name.
	EnvVars []string

	// ExistingSecret (BackendAuthExistingSecret) — name of a Secret
	// that MUST already exist in the agent's namespace.
	ExistingSecret string
}

// credentialProfile describes one named way to authenticate a backend.
// The CLI ships a fixed catalog indexed by (backend type, profile name).
// Keeping this as code (not config) lets `ww agent create --auth <x>=<y>`
// fail fast with a full list of valid profiles when the pair is unknown.
type credentialProfile struct {
	// EnvVars must ALL be present in the user's shell for this profile
	// to resolve successfully. Order is cosmetic (used for deterministic
	// error messages on missing vars).
	EnvVars []string
}

// credentialProfiles is the MVP profile catalog. Extend here (not in the
// resolver) when a new backend or auth shape lands. Each entry is
// { backend-type: { profile-name: profile } }.
//
// MVP scope (per user's explicit call): claude { api-key, oauth }. Other
// backends fall through to BackendAuthProfile's "unknown profile" error
// until they're added here.
var credentialProfiles = map[string]map[string]credentialProfile{
	"claude": {
		"api-key": {EnvVars: []string{"ANTHROPIC_API_KEY"}},
		"oauth":   {EnvVars: []string{"CLAUDE_CODE_OAUTH_TOKEN"}},
	},
}

// KnownCredentialProfiles returns a sorted, human-readable listing of
// every backend-type → profile-name pair the CLI knows. Used by the
// `--auth --help` copy and by "unknown profile" error messages so users
// can see what's available without reading the code.
func KnownCredentialProfiles() string {
	var parts []string
	for backend, profiles := range credentialProfiles {
		names := make([]string, 0, len(profiles))
		for name := range profiles {
			names = append(names, name)
		}
		parts = append(parts, fmt.Sprintf("%s: %s", backend, strings.Join(names, " | ")))
	}
	return strings.Join(parts, "; ")
}

// backendCredentialSecretName returns the default Secret name for a
// ww-minted backend credential Secret. Predictable (one per agent per
// backend) so operators can find + rotate it; namespaced to the
// <agent>-<backend> pair so a multi-backend agent's creds don't
// collide.
func backendCredentialSecretName(agentName, backendName string) string {
	return agentName + "-" + backendName + "-credentials"
}

// resolve is called once per --auth-* flag entry. Returns the resolved
// Secret name (to stamp onto spec.backends[].credentials.existingSecret)
// or an empty string + nil error when the mode is BackendAuthNone. Mint
// modes upsert a Secret; existing-secret mode only verifies presence.
func (r BackendAuthResolver) resolve(ctx context.Context, k8s kubernetes.Interface, namespace, agentName, backendType string) (string, error) {
	switch r.Mode {
	case BackendAuthNone:
		return "", nil

	case BackendAuthExistingSecret:
		if r.ExistingSecret == "" {
			return "", fmt.Errorf("--auth-secret %s=: Secret name is required", r.Backend)
		}
		// Sanity-check the Secret exists. We deliberately don't inspect
		// its keys — users may have conventions the CLI doesn't know
		// about (multi-key bundles, re-used infra-wide creds, etc.).
		if _, err := k8s.CoreV1().Secrets(namespace).Get(ctx, r.ExistingSecret, metav1.GetOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				return "", fmt.Errorf(
					"--auth-secret %s=%s: Secret not found in namespace %q — create it first, "+
						"e.g.: `kubectl -n %s create secret generic %s --from-literal=<KEY>=<value>`",
					r.Backend, r.ExistingSecret, namespace, namespace, r.ExistingSecret,
				)
			}
			return "", fmt.Errorf("check Secret %s/%s: %w", namespace, r.ExistingSecret, err)
		}
		return r.ExistingSecret, nil

	case BackendAuthProfile:
		profile, ok := credentialProfiles[backendType][r.Profile]
		if !ok {
			return "", fmt.Errorf(
				"--auth %s=%s: unknown profile for backend type %q. Known profiles: %s",
				r.Backend, r.Profile, backendType, KnownCredentialProfiles(),
			)
		}
		data, err := readEnvVars(profile.EnvVars)
		if err != nil {
			return "", fmt.Errorf("--auth %s=%s: %w", r.Backend, r.Profile, err)
		}
		secretName := backendCredentialSecretName(agentName, r.Backend)
		return secretName, upsertBackendCredentialSecret(
			ctx, k8s, namespace, secretName, agentName, r.Backend, data,
			fmt.Sprintf("ww agent create --auth %s=%s", r.Backend, r.Profile),
		)

	case BackendAuthFromEnv:
		if len(r.EnvVars) == 0 {
			return "", fmt.Errorf("--auth-from-env %s=: env var list is required", r.Backend)
		}
		data, err := readEnvVars(r.EnvVars)
		if err != nil {
			return "", fmt.Errorf("--auth-from-env %s=%s: %w",
				r.Backend, strings.Join(r.EnvVars, ","), err)
		}
		secretName := backendCredentialSecretName(agentName, r.Backend)
		return secretName, upsertBackendCredentialSecret(
			ctx, k8s, namespace, secretName, agentName, r.Backend, data,
			fmt.Sprintf("ww agent create --auth-from-env %s=%s",
				r.Backend, strings.Join(r.EnvVars, ",")),
		)
	}
	return "", fmt.Errorf("unknown BackendAuthMode %d", r.Mode)
}

// readEnvVars pulls the named env vars out of the process environment.
// All must be set + non-empty, or the whole resolution fails. Returns a
// map suitable for passing as Secret StringData. Errors name the
// missing variable so the user can fix their shell and retry.
func readEnvVars(vars []string) (map[string]string, error) {
	out := make(map[string]string, len(vars))
	var missing []string
	for _, v := range vars {
		val := strings.TrimSpace(os.Getenv(v))
		if val == "" {
			missing = append(missing, v)
			continue
		}
		out[v] = val
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf(
			"required environment variable(s) unset or empty: %s",
			strings.Join(missing, ", "),
		)
	}
	return out, nil
}

// upsertBackendCredentialSecret creates or updates a Secret carrying the
// resolved credential data. Label/annotation surface mirrors the
// gitSync credential Secret's so `ww agent delete --purge` (and its
// label-gated deletion) treat both the same way. The `witwave.ai/
// credential-type=backend` label disambiguates from `credential-type=
// gitsync` for operators grepping the cluster.
func upsertBackendCredentialSecret(
	ctx context.Context,
	k8s kubernetes.Interface,
	namespace, secretName, agentName, backendName string,
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
				"witwave.ai/credential-type": "backend",
				"witwave.ai/backend":         backendName,
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
		// Never clobber a hand-rolled Secret at this name. User's
		// escape: pre-create their own and use --auth-secret.
		if existing.Labels[LabelManagedBy] != LabelManagedByWW {
			return fmt.Errorf(
				"Secret %s/%s exists but is not ww-managed. "+
					"Use `--auth-secret %s=%s` to reference it as-is, or delete it and retry",
				namespace, secretName, backendName, secretName,
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

// ParseBackendAuth turns the three repeatable cobra flags
// (--auth / --auth-from-env / --auth-secret) into a resolver list
// keyed by backend name. Each raw flag value is `<backend>=<rest>`.
// Validates that a backend isn't double-specified across the three
// flags — the user has to pick one auth shape per backend.
func ParseBackendAuth(profiles, fromEnv, fromSecret []string) ([]BackendAuthResolver, error) {
	seen := make(map[string]string) // backend -> flag that claimed it
	out := make([]BackendAuthResolver, 0,
		len(profiles)+len(fromEnv)+len(fromSecret))

	claim := func(backend, flag string) error {
		if prev, ok := seen[backend]; ok {
			return fmt.Errorf(
				"backend %q already has credentials from %s; can't also set via %s",
				backend, prev, flag,
			)
		}
		seen[backend] = flag
		return nil
	}

	for _, raw := range profiles {
		backend, profile, err := splitBackendKV(raw, "--auth")
		if err != nil {
			return nil, err
		}
		if err := claim(backend, "--auth"); err != nil {
			return nil, err
		}
		out = append(out, BackendAuthResolver{
			Backend: backend,
			Mode:    BackendAuthProfile,
			Profile: profile,
		})
	}

	for _, raw := range fromEnv {
		backend, vars, err := splitBackendKV(raw, "--auth-from-env")
		if err != nil {
			return nil, err
		}
		if err := claim(backend, "--auth-from-env"); err != nil {
			return nil, err
		}
		out = append(out, BackendAuthResolver{
			Backend: backend,
			Mode:    BackendAuthFromEnv,
			EnvVars: splitCommaList(vars),
		})
	}

	for _, raw := range fromSecret {
		backend, secretName, err := splitBackendKV(raw, "--auth-secret")
		if err != nil {
			return nil, err
		}
		if err := claim(backend, "--auth-secret"); err != nil {
			return nil, err
		}
		out = append(out, BackendAuthResolver{
			Backend:        backend,
			Mode:           BackendAuthExistingSecret,
			ExistingSecret: secretName,
		})
	}
	return out, nil
}

// splitBackendKV parses the `<backend>=<value>` shape used by all three
// auth flags. Empty backend / value rejected with a clear message so
// typos like `--auth =oauth` or `--auth claude=` fail fast.
func splitBackendKV(raw, flag string) (backend, value string, err error) {
	idx := strings.Index(raw, "=")
	if idx < 0 {
		return "", "", fmt.Errorf(
			"%s %q: expected <backend>=<value> form", flag, raw,
		)
	}
	backend = strings.TrimSpace(raw[:idx])
	value = strings.TrimSpace(raw[idx+1:])
	if backend == "" {
		return "", "", fmt.Errorf("%s %q: backend name is empty", flag, raw)
	}
	if value == "" {
		return "", "", fmt.Errorf("%s %q: value is empty", flag, raw)
	}
	return backend, value, nil
}

// splitCommaList splits `FOO,BAR,BAZ` into {"FOO", "BAR", "BAZ"},
// trimming whitespace and dropping empties so `FOO, ,BAR` still works.
func splitCommaList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveBackendAuthFor scans the resolver list for a backend by name.
// Returns nil when the backend has no auth flag — a legitimate state
// for backends like echo that need no credentials.
func resolveBackendAuthFor(resolvers []BackendAuthResolver, backendName string) *BackendAuthResolver {
	for i := range resolvers {
		if resolvers[i].Backend == backendName {
			return &resolvers[i]
		}
	}
	return nil
}

// validateBackendAuthTargets checks that every resolver's Backend
// matches a declared backend on the CR. Catches typos like
// `--auth clade=oauth` before we mint any Secrets. Returns early on
// the first mismatch so the user fixes one at a time.
func validateBackendAuthTargets(resolvers []BackendAuthResolver, backends []BackendSpec) error {
	declared := make(map[string]struct{}, len(backends))
	for _, b := range backends {
		declared[b.Name] = struct{}{}
	}
	for _, r := range resolvers {
		if _, ok := declared[r.Backend]; !ok {
			names := make([]string, 0, len(backends))
			for _, b := range backends {
				names = append(names, b.Name)
			}
			return fmt.Errorf(
				"--auth* references unknown backend %q; declared backends: [%s]",
				r.Backend, strings.Join(names, ", "),
			)
		}
	}
	return nil
}

// sentinel error so unit tests can assert on the "nothing to do" branch
// without string-matching.
var errNoBackendAuth = errors.New("no backend auth flags provided")
