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
//   Tier 1 — named profile:     --auth claude=oauth          (MVP: claude only)
//   Tier 2 — arbitrary env:     --backend-secret-from-env claude=FOO (escape hatch)
//   Tier 3 — existing Secret:   --auth-secret claude=name    (pre-created)
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

	// BackendAuthInline — user supplies one or more KEY=VALUE pairs
	// on the command line (`--auth-set BACKEND:KEY=VALUE` or, on
	// `backend add` where the backend is positional, `--auth-set
	// KEY=VALUE`). The CLI mints a Secret with each KEY as a data
	// key and VALUE as the corresponding bytes.
	//
	// Caveats: command-line values land in shell history + ps output
	// + journal scrapers. For production tokens use --auth-secret
	// (pre-create with `kubectl create secret --from-env-file`) or
	// --backend-secret-from-env (lift from the shell environment). --auth-set is
	// for dev-loop ergonomics + custom KEY=VALUE shapes the named
	// profile catalog doesn't cover.
	BackendAuthInline
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

	// EnvVars (BackendAuthFromEnv) — env var specs in the user's shell.
	// Each entry is one of:
	//
	//   <NAME>          — read $NAME, store under Secret key NAME
	//                     (Secret key matches the source var name).
	//   <SRC>:<DEST>    — read $SRC, store under Secret key DEST.
	//                     Lets the same in-container env-var name (DEST)
	//                     pull from a per-backend-prefixed shell var
	//                     (SRC) so two backends can take different
	//                     values for the same in-container key.
	EnvVars []string

	// ExistingSecret (BackendAuthExistingSecret) — name of a Secret
	// that MUST already exist in the agent's namespace.
	ExistingSecret string

	// Inline (BackendAuthInline) — KEY=VALUE pairs the user supplied
	// on the command line. Each pair becomes a key in the minted
	// Secret with the corresponding value. Map shape (vs. parallel
	// slices) gives us free dup-detection: ParseBackendAuth refuses
	// when the same KEY shows up twice for the same backend rather
	// than letting the second silently overwrite the first.
	Inline map[string]string
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
	return agentName + "-" + backendName
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
			return "", fmt.Errorf("--backend-secret-from-env %s=: env var list is required", r.Backend)
		}
		data, err := readEnvVars(r.EnvVars)
		if err != nil {
			return "", fmt.Errorf("--backend-secret-from-env %s=%s: %w",
				r.Backend, strings.Join(r.EnvVars, ","), err)
		}
		secretName := backendCredentialSecretName(agentName, r.Backend)
		return secretName, upsertBackendCredentialSecret(
			ctx, k8s, namespace, secretName, agentName, r.Backend, data,
			fmt.Sprintf("ww agent create --backend-secret-from-env %s=%s",
				r.Backend, strings.Join(r.EnvVars, ",")),
		)

	case BackendAuthInline:
		if len(r.Inline) == 0 {
			return "", fmt.Errorf("--auth-set %s: at least one KEY=VALUE pair is required", r.Backend)
		}
		// Validate: keys non-empty (parser already rejects empty),
		// values non-empty (we don't mint empty-string credentials —
		// almost certainly a user typo, and an empty value tends to
		// surface as a confusing 401 from the backend later).
		for k, v := range r.Inline {
			if v == "" {
				return "", fmt.Errorf("--auth-set %s: %q has an empty value (use --backend-secret-from-env if you want to lift from the shell instead)", r.Backend, k)
			}
			_ = k // shape symmetry; v is the validated piece
		}
		secretName := backendCredentialSecretName(agentName, r.Backend)
		// `created-by` deliberately doesn't echo the values — they'd
		// land in the Secret's metadata and any operator running
		// `kubectl get secret -o yaml` would see them. Just record
		// the keys; the values live in the data block where they
		// belong.
		keys := make([]string, 0, len(r.Inline))
		for k := range r.Inline {
			keys = append(keys, k)
		}
		return secretName, upsertBackendCredentialSecret(
			ctx, k8s, namespace, secretName, agentName, r.Backend, r.Inline,
			fmt.Sprintf("ww agent create --auth-set %s [keys=%s]",
				r.Backend, strings.Join(keys, ",")),
		)
	}
	return "", fmt.Errorf("unknown BackendAuthMode %d", r.Mode)
}

// readEnvVars pulls the named env vars out of the process environment.
// Each entry is either a bare `<NAME>` (read $NAME, store under Secret
// key NAME) or a `<SRC>:<DEST>` rename pair (read $SRC, store under
// Secret key DEST). The rename form lets the same in-container env-var
// name pull from a per-backend-prefixed shell var so two backends can
// take different values for the same in-container key (e.g.
// ECHO1_GITHUB_TOKEN:GITHUB_TOKEN + ECHO2_GITHUB_TOKEN:GITHUB_TOKEN
// against shell vars ECHO1_GITHUB_TOKEN and ECHO2_GITHUB_TOKEN).
//
// All sources must be set + non-empty, or the whole resolution fails.
// Errors name the missing source variable so the user can fix their
// shell and retry.
func readEnvVars(vars []string) (map[string]string, error) {
	out := make(map[string]string, len(vars))
	var missing []string
	for _, v := range vars {
		src, dest, err := splitEnvVarRename(v)
		if err != nil {
			return nil, err
		}
		val := strings.TrimSpace(os.Getenv(src))
		if val == "" {
			missing = append(missing, src)
			continue
		}
		out[dest] = val
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf(
			"required environment variable(s) unset or empty: %s",
			strings.Join(missing, ", "),
		)
	}
	return out, nil
}

// splitEnvVarRename parses one `--backend-secret-from-env` entry. Bare `<NAME>`
// means src=dest=NAME. `<SRC>:<DEST>` reads from $SRC and stores under
// Secret key DEST. Empty SRC or DEST is an error — both halves of a
// rename must be non-empty.
func splitEnvVarRename(spec string) (src, dest string, err error) {
	if i := strings.IndexByte(spec, ':'); i >= 0 {
		src = strings.TrimSpace(spec[:i])
		dest = strings.TrimSpace(spec[i+1:])
		if src == "" || dest == "" {
			return "", "", fmt.Errorf("env-var rename %q: form is <SRC>:<DEST>, both halves required", spec)
		}
		return src, dest, nil
	}
	src = strings.TrimSpace(spec)
	if src == "" {
		return "", "", fmt.Errorf("env-var spec is empty")
	}
	return src, src, nil
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

// ParseBackendAuth turns the four repeatable cobra flags
// (--auth / --backend-secret-from-env / --auth-secret / --auth-set) into a
// resolver list keyed by backend name. Each raw flag value is
// `<backend>=<rest>` (or `<backend>:<KEY>=<VALUE>` for --auth-set's
// shape — see splitBackendInline). Validates that a backend isn't
// double-specified across mutually-exclusive auth flags; --auth-set
// is allowed to repeat for the same backend (multiple KEY=VALUE
// pairs accumulate into one Secret).
func ParseBackendAuth(profiles, fromEnv, fromSecret, fromSet []string) ([]BackendAuthResolver, error) {
	// seen[backend] = flag-name that claimed it. --auth-set is
	// special: it accumulates into one resolver but conflicts with
	// the other three.
	seen := make(map[string]string)
	// inlineByBackend collects KEY=VALUE pairs across multiple
	// --auth-set flags for the same backend before we materialise
	// a single BackendAuthInline resolver per backend. Order is
	// per-flag-arrival; dup-key detection happens at insert time.
	inlineByBackend := make(map[string]map[string]string)
	out := make([]BackendAuthResolver, 0,
		len(profiles)+len(fromEnv)+len(fromSecret)+len(fromSet))

	claim := func(backend, flag string) error {
		if prev, ok := seen[backend]; ok && prev != flag {
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

	// Multiple --backend-secret-from-env entries for the same backend are
	// additive — they accumulate into ONE resolver whose EnvVars list
	// is the concatenation. Lets callers spread a long list across
	// multiple flag invocations rather than packing everything into a
	// single comma-delimited value:
	//
	//   --backend-secret-from-env echo-1=A:X,B:Y          # combined
	//   --backend-secret-from-env echo-1=A:X --backend-secret-from-env echo-1=B:Y
	//                                              # equivalent, line per pair
	fromEnvIdx := make(map[string]int) // backend → index in `out`
	for _, raw := range fromEnv {
		backend, vars, err := splitBackendKV(raw, "--backend-secret-from-env")
		if err != nil {
			return nil, err
		}
		if err := claim(backend, "--backend-secret-from-env"); err != nil {
			return nil, err
		}
		newVars := splitCommaList(vars)
		if i, ok := fromEnvIdx[backend]; ok {
			out[i].EnvVars = append(out[i].EnvVars, newVars...)
			continue
		}
		fromEnvIdx[backend] = len(out)
		out = append(out, BackendAuthResolver{
			Backend: backend,
			Mode:    BackendAuthFromEnv,
			EnvVars: newVars,
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

	// --auth-set BACKEND:KEY=VALUE — repeatable, accumulates per
	// backend. Dup-keys within one backend are a hard error rather
	// than last-write-wins (silent overwrites are a future debug-
	// session waiting to happen).
	for _, raw := range fromSet {
		backend, key, value, err := splitBackendInline(raw, "--auth-set")
		if err != nil {
			return nil, err
		}
		if err := claim(backend, "--auth-set"); err != nil {
			return nil, err
		}
		bucket, ok := inlineByBackend[backend]
		if !ok {
			bucket = make(map[string]string)
			inlineByBackend[backend] = bucket
		}
		if existing, dup := bucket[key]; dup {
			return nil, fmt.Errorf(
				"--auth-set %s: key %q given twice (first=%q, second=%q) — pick one",
				backend, key, existing, value,
			)
		}
		bucket[key] = value
	}
	for backend, pairs := range inlineByBackend {
		out = append(out, BackendAuthResolver{
			Backend: backend,
			Mode:    BackendAuthInline,
			Inline:  pairs,
		})
	}

	return out, nil
}

// splitBackendInline parses the `<backend>:<KEY>=<VALUE>` shape used
// by --auth-set on the create command. The colon-then-equals split
// avoids the visual ambiguity of `<backend>=<KEY>=<VALUE>` (two
// equals signs would be confusing to read, and the parser would
// have to guess at which one's the backend separator).
//
// Empty backend / KEY / VALUE all rejected with named diagnostics
// so typos surface fast. The single-backend variant of the parser
// (no `<backend>:` prefix, used by `ww agent backend add` where the
// backend's already positional) is SplitInlineKV below.
func splitBackendInline(raw, flag string) (backend, key, value string, err error) {
	colonIdx := strings.Index(raw, ":")
	if colonIdx < 0 {
		return "", "", "", fmt.Errorf(
			"%s %q: expected <backend>:<KEY>=<VALUE> form (e.g. claude:ANTHROPIC_API_KEY=sk-...)", flag, raw,
		)
	}
	backend = strings.TrimSpace(raw[:colonIdx])
	if backend == "" {
		return "", "", "", fmt.Errorf("%s %q: backend name is empty", flag, raw)
	}
	key, value, err = SplitInlineKV(raw[colonIdx+1:], flag)
	if err != nil {
		return "", "", "", err
	}
	return backend, key, value, nil
}

// SplitInlineKV parses a `<KEY>=<VALUE>` shape. Used by
// --auth-set on `ww agent backend add` where the backend's already
// the positional arg, and as the inner half of splitBackendInline
// for `ww agent create`'s --auth-set. Trims whitespace around the
// KEY but PRESERVES it inside the VALUE (tokens often have leading
// or trailing chars that are syntactically meaningful — better to
// take the raw bytes than to second-guess).
func SplitInlineKV(raw, flag string) (key, value string, err error) {
	eqIdx := strings.Index(raw, "=")
	if eqIdx < 0 {
		return "", "", fmt.Errorf(
			"%s %q: expected KEY=VALUE form", flag, raw,
		)
	}
	key = strings.TrimSpace(raw[:eqIdx])
	value = raw[eqIdx+1:]
	if key == "" {
		return "", "", fmt.Errorf("%s %q: KEY is empty", flag, raw)
	}
	if value == "" {
		return "", "", fmt.Errorf("%s %q: VALUE is empty (use --backend-secret-from-env if you want a placeholder lifted at runtime)", flag, raw)
	}
	return key, value, nil
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
