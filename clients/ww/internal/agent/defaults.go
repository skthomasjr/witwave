package agent

import (
	"slices"
	"strings"
)

// Default image registry + repo prefix. Mirrors every other image this
// project publishes.
const imageRepoPrefix = "ghcr.io/skthomasjr/images/"

// Default listen port for the harness + every backend sidecar. The chart
// uses the same default so there's nothing special to coordinate.
const DefaultPort int32 = 8000

// Backend-type identifiers the CLI knows about today. Keep this list
// aligned with `backends/` subdirectories and the dashboard's
// BackendType union — adding a backend type without updating this list
// would leave `ww agent create --backend <new>` silently unresolvable.
const (
	BackendEcho   = "echo"
	BackendClaude = "claude"
	BackendCodex  = "codex"
	BackendGemini = "gemini"
)

// DefaultBackend is the backend name used when `ww agent create` is
// invoked without `--backend`. Echo is chosen because it requires no API
// keys and no external services, which is the "access to a Kubernetes
// cluster and the CLI is all you need" promise (see
// backends/echo/README.md).
const DefaultBackend = BackendEcho

// KnownBackends returns the backends `ww agent create --backend` accepts.
// Order is display-order (echo first for onboarding visibility).
func KnownBackends() []string {
	return []string{BackendEcho, BackendClaude, BackendCodex, BackendGemini}
}

// IsKnownBackend reports whether name matches a backend this CLI knows
// about. Echo + the three LLM backends only; future backends must be
// added both here and in the dashboard's BackendType union.
func IsKnownBackend(name string) bool {
	return slices.Contains(KnownBackends(), name)
}

// HarnessImage returns the default harness image reference for the given
// CLI version. An empty or "dev" version falls back to :latest with an
// explicit marker so a dev build never silently points at a stale
// production tag.
func HarnessImage(cliVersion string) string {
	return imageRef("harness", cliVersion)
}

// BackendImage returns the default image reference for a named backend
// at the given CLI version.
func BackendImage(backend, cliVersion string) string {
	return imageRef(backend, cliVersion)
}

// imageRef assembles `ghcr.io/skthomasjr/images/<name>:<tag>`. A blank,
// "dev", or "unknown" cliVersion falls back to :latest — this happens
// only on unreleased builds and is loud by virtue of the tag itself.
func imageRef(name, cliVersion string) string {
	tag := strings.TrimSpace(cliVersion)
	switch tag {
	case "", "dev", "unknown":
		tag = "latest"
	}
	// Strip any leading `v` on release tags (v0.6.0 → 0.6.0) to match the
	// GHCR publishing pattern used by .github/workflows/release.yaml.
	tag = strings.TrimPrefix(tag, "v")
	return imageRepoPrefix + name + ":" + tag
}

// IsDevVersion reports whether the CLI was built without a concrete
// release version (ldflags not set). Callers surface this as a warning
// in the preflight banner so operators know they're about to deploy
// floating-tag images.
func IsDevVersion(cliVersion string) bool {
	t := strings.TrimSpace(cliVersion)
	return t == "" || t == "dev" || t == "unknown"
}

// ResolveNamespace picks the namespace for an `ww agent` operation.
// Precedence matches kubectl: explicit flag → context's configured
// namespace → "default". Callers MUST log the resolved value so the
// user sees where an unnamed invocation landed (DESIGN.md NS-2).
func ResolveNamespace(flagValue, contextNS string) string {
	if flagValue != "" {
		return flagValue
	}
	if contextNS != "" {
		return contextNS
	}
	return "default"
}
