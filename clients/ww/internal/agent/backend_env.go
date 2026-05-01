package agent

import (
	"fmt"
)

// ParseBackendEnvs converts repeatable `--backend-env <backend>:<KEY>=<VALUE>`
// flag values into a map keyed by backend name → map of KEY → VALUE.
// Parallel to ParseBackendAuth's --auth-set handling but routes the
// pairs to spec.backends[].env[] (plain env vars) rather than to a
// minted Secret.
//
// Form per entry: `<backend>:<KEY>=<VALUE>`. Empty raw → empty map.
// Duplicate `(backend, KEY)` pairs are a hard error; silent
// last-write-wins is a future debug session waiting to happen and the
// flag is repeatable, so the user gets to fix the typo at parse time.
//
// Leading/trailing whitespace around the backend name and KEY is
// trimmed; whitespace inside the VALUE is preserved verbatim (matches
// SplitInlineKV's posture — env values can be syntactically meaningful).
func ParseBackendEnvs(raw []string) (map[string]map[string]string, error) {
	out := make(map[string]map[string]string)
	for _, entry := range raw {
		backend, key, value, err := splitBackendInline(entry, "--backend-env")
		if err != nil {
			return nil, err
		}
		bucket, ok := out[backend]
		if !ok {
			bucket = make(map[string]string)
			out[backend] = bucket
		}
		if existing, dup := bucket[key]; dup {
			return nil, fmt.Errorf(
				"--backend-env %s: key %q given twice (first=%q, second=%q) — pick one",
				backend, key, existing, value,
			)
		}
		bucket[key] = value
	}
	return out, nil
}

// ApplyBackendEnvs merges the parsed env map onto the per-backend
// BackendSpec entries. Each (backend, kvs) pair finds its matching
// spec by name and stamps spec.Env. References to backend names that
// don't exist in `backends` are rejected so a typo can't silently
// drop env vars on the floor.
//
// Returns the modified backend slice (same pointer semantics as
// ApplyBackendPersist) so callers can chain Apply* steps.
func ApplyBackendEnvs(backends []BackendSpec, envs map[string]map[string]string) ([]BackendSpec, error) {
	if len(envs) == 0 {
		return backends, nil
	}
	byName := make(map[string]int, len(backends))
	for i, b := range backends {
		byName[b.Name] = i
	}
	for backend, kvs := range envs {
		idx, ok := byName[backend]
		if !ok {
			return nil, fmt.Errorf(
				"--backend-env %s: no backend named %q on this agent (declared: %v)",
				backend, backend, backendNames(backends),
			)
		}
		// Merge rather than replace so future composition (e.g. a
		// future profile flag stamping a default + --backend-env
		// adding overrides) Just Works without each call site
		// having to coordinate.
		if backends[idx].Env == nil {
			backends[idx].Env = make(map[string]string, len(kvs))
		}
		for k, v := range kvs {
			backends[idx].Env[k] = v
		}
	}
	return backends, nil
}

func backendNames(backends []BackendSpec) []string {
	out := make([]string, len(backends))
	for i, b := range backends {
		out[i] = b.Name
	}
	return out
}
