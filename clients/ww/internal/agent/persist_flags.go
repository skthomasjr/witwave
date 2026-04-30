package agent

import (
	"fmt"
	"strings"
)

// BackendStorageMount is the CLI-side representation of one backend
// PVC subPath → mountPath pair. Mirrors the chart's
// `agents[i].backends[j].storage.mounts[k]` entry shape and the
// operator CRD's BackendStorageMount, so a CR built by ww renders
// byte-for-byte identical to a Helm-rendered pod with the same
// values.
type BackendStorageMount struct {
	// SubPath is the path inside the PVC (no leading "/").
	SubPath string

	// MountPath is the absolute path inside the backend container.
	MountPath string
}

// BackendStorageSpec describes the per-backend PVC declaration parsed
// from one `--persist` flag entry. One PVC per backend (named
// `<agent>-<backend>-data` by the operator), subPath-fanned into the
// container at one or more mount paths.
type BackendStorageSpec struct {
	// Size is the PVC storage request (e.g. "10Gi"). The CRD's
	// validation pattern is the resource.Quantity syntax — we let the
	// operator's admission webhook reject malformed values rather than
	// duplicating the regex on the CLI side.
	Size string

	// StorageClassName, when non-empty, overrides the cluster's
	// default storage class for the operator-created PVC.
	StorageClassName string

	// Mounts is the subPath → mountPath fan-out. Populated by the
	// flag parser using a per-backend-type default convention; future
	// `--persist-mount` flags would append/override here.
	Mounts []BackendStorageMount
}

// BackendStoragePresets enumerates the default subPath → mountPath
// fan-out for each known backend type. Mirrors the chart's example
// values (charts/witwave/values.yaml ~line 1488) and the dirs each
// backend's image expects to find under `/home/agent/.<type>/`.
//
// Backend types not listed here have no defaults — the CLI rejects
// `--persist <name>=<size>` for them with an actionable error rather
// than rendering an unused PVC.
var BackendStoragePresets = map[string][]BackendStorageMount{
	"claude": {
		{SubPath: "projects", MountPath: "/home/agent/.claude/projects"},
		{SubPath: "sessions", MountPath: "/home/agent/.claude/sessions"},
		{SubPath: "backups", MountPath: "/home/agent/.claude/backups"},
		{SubPath: "memory", MountPath: "/home/agent/.claude/memory"},
	},
	"codex": {
		{SubPath: "memory", MountPath: "/home/agent/.codex/memory"},
		{SubPath: "sessions", MountPath: "/home/agent/.codex/sessions"},
	},
	"gemini": {
		// gemini stores conversation JSON under memory/sessions/ per
		// SESSION_STORE_DIR's default, and persisted memory files at
		// memory/. One PVC, two subPaths.
		{SubPath: "memory", MountPath: "/home/agent/.gemini/memory"},
	},
	"echo": {
		// Echo's intentional non-scope (backends/echo/README.md) means
		// it has no real session state. The single `memory/` subPath
		// here is a symbolic convention so `--persist` exercises the
		// mechanic uniformly across backend types — useful for
		// bootstrap walkthroughs that want to verify the per-backend
		// PVC story without dragging in claude/codex/gemini API keys.
		// Path is type-keyed (`/home/agent/.echo/memory`) so two
		// echo backends in the same agent (echo-1 + echo-2) each get
		// their own PVC mounted at the same in-container path,
		// distinct from any name-based gitMapping at .echo-1/ /
		// .echo-2/.
		{SubPath: "memory", MountPath: "/home/agent/.echo/memory"},
	},
}

// ParseBackendPersistMounts converts repeatable `--persist-mount`
// flag values to (backend-name → []BackendStorageMount) entries.
// Form:
//
//	<backend-name>=<subpath>:<mountpath>
//
// Each backend can be referenced multiple times to declare more
// than one mount. Cross-flag validation (does the backend-name
// have a matching --persist entry? duplicate within a backend?)
// happens in ApplyBackendPersist — the parser stays a pure
// string→struct conversion.
func ParseBackendPersistMounts(raw []string) (map[string][]BackendStorageMount, error) {
	out := map[string][]BackendStorageMount{}
	for i, r := range raw {
		entry := strings.TrimSpace(r)
		if entry == "" {
			return nil, fmt.Errorf("--persist-mount[%d]: empty value", i)
		}
		eq := strings.IndexByte(entry, '=')
		if eq < 1 {
			return nil, fmt.Errorf("--persist-mount[%d] %q: form is <backend-name>=<subpath>:<mountpath>", i, entry)
		}
		name := strings.TrimSpace(entry[:eq])
		body := strings.TrimSpace(entry[eq+1:])
		if err := ValidateName(name); err != nil {
			return nil, fmt.Errorf("--persist-mount[%d] backend %q: %w", i, name, err)
		}
		// Split body into <subpath>:<mountpath>. mountPath must be
		// absolute (starts with `/`) so the colon split is unambiguous —
		// any `:` before the first `/` is the subPath separator.
		colon := strings.IndexByte(body, ':')
		if colon < 1 {
			return nil, fmt.Errorf("--persist-mount[%d] %q: form is <backend-name>=<subpath>:<mountpath>", i, entry)
		}
		subPath := strings.TrimSpace(body[:colon])
		mountPath := strings.TrimSpace(body[colon+1:])
		if subPath == "" || mountPath == "" {
			return nil, fmt.Errorf("--persist-mount[%d] %q: subpath and mountpath are both required", i, entry)
		}
		if !strings.HasPrefix(mountPath, "/") {
			return nil, fmt.Errorf("--persist-mount[%d] %q: mountpath %q must be absolute (start with '/')", i, entry, mountPath)
		}
		// kubelet rejects subPath with `..`, leading `/`, or absolute
		// segments — catch it client-side for a clearer error.
		if strings.HasPrefix(subPath, "/") {
			return nil, fmt.Errorf("--persist-mount[%d] %q: subpath %q must be relative (no leading '/')", i, entry, subPath)
		}
		if strings.Contains(subPath, "..") {
			return nil, fmt.Errorf("--persist-mount[%d] %q: subpath %q must not contain '..'", i, entry, subPath)
		}
		out[name] = append(out[name], BackendStorageMount{
			SubPath:   subPath,
			MountPath: mountPath,
		})
	}
	return out, nil
}

// ParseBackendPersist converts repeatable `--persist` flag values to
// (backend-name → BackendStorageSpec) entries. Form:
//
//	<backend-name>=<size>[@<storage-class>]
//
// Cross-flag validation (does the backend-name reference an actual
// --backend? does the backend's type have a default mount preset?)
// happens in ApplyBackendPersist — the parser stays a pure
// string→struct conversion.
func ParseBackendPersist(raw []string) (map[string]BackendStorageSpec, error) {
	out := map[string]BackendStorageSpec{}
	for i, r := range raw {
		entry := strings.TrimSpace(r)
		if entry == "" {
			return nil, fmt.Errorf("--persist[%d]: empty value", i)
		}
		eq := strings.IndexByte(entry, '=')
		if eq < 1 {
			return nil, fmt.Errorf("--persist[%d] %q: form is <backend-name>=<size>[@<storage-class>]", i, entry)
		}
		name := strings.TrimSpace(entry[:eq])
		rest := strings.TrimSpace(entry[eq+1:])
		if rest == "" {
			return nil, fmt.Errorf("--persist[%d] %q: empty size", i, entry)
		}
		if err := ValidateName(name); err != nil {
			return nil, fmt.Errorf("--persist[%d] backend %q: %w", i, name, err)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("--persist[%d]: duplicate backend %q", i, name)
		}

		var size, class string
		if at := strings.IndexByte(rest, '@'); at >= 0 {
			size = strings.TrimSpace(rest[:at])
			class = strings.TrimSpace(rest[at+1:])
			if size == "" {
				return nil, fmt.Errorf("--persist[%d] %q: empty size before '@'", i, entry)
			}
			if class == "" {
				return nil, fmt.Errorf("--persist[%d] %q: empty storage class after '@'", i, entry)
			}
		} else {
			size = rest
		}

		out[name] = BackendStorageSpec{
			Size:             size,
			StorageClassName: class,
		}
	}
	return out, nil
}

// ApplyBackendPersist resolves --persist + --persist-mount entries
// against the declared --backend list and stamps the storage spec
// onto the matching BackendSpec.
//
// Mount-list resolution is replace-on-presence: when `mounts` carries
// any entries for a backend, those entries are the FULL mount list
// for that backend's PVC — type-derived presets are skipped entirely.
// This avoids "your custom mount got accidentally extended by a
// surprise preset" footguns. To extend the defaults, the caller
// must spell them out alongside the new entry.
//
// Errors when:
//   - a --persist entry references a backend name that doesn't appear
//     in --backend.
//   - a --persist-mount entry references a backend name with no
//     matching --persist (no PVC = nowhere to mount onto).
//   - in default-mount mode, the matched backend's type has no
//     preset in BackendStoragePresets.
//   - a backend's resolved mount list contains duplicate (subPath,
//     mountPath) or duplicate mountPath entries.
func ApplyBackendPersist(backends []BackendSpec, persist map[string]BackendStorageSpec, mounts map[string][]BackendStorageMount) ([]BackendSpec, error) {
	if len(persist) == 0 && len(mounts) == 0 {
		return backends, nil
	}
	idx := make(map[string]int, len(backends))
	for i, b := range backends {
		idx[b.Name] = i
	}

	// Validate every --persist-mount has a matching --persist before
	// touching backends — produces a single clean error rather than
	// stamping some backends and erroring midway.
	for name := range mounts {
		if _, ok := persist[name]; !ok {
			return nil, fmt.Errorf(
				"--persist-mount %q: no matching --persist entry with that name "+
					"(declare --persist %s=<size> first to provision the PVC)",
				name, name)
		}
	}

	for name, spec := range persist {
		bi, ok := idx[name]
		if !ok {
			return nil, fmt.Errorf("--persist %q: no matching --backend entry with that name", name)
		}

		// Mount-list resolution: explicit override wins; otherwise
		// fall back to the type-derived preset.
		if explicit, ok := mounts[name]; ok && len(explicit) > 0 {
			spec.Mounts = append(spec.Mounts, explicit...)
		} else {
			preset, ok := BackendStoragePresets[backends[bi].Type]
			if !ok {
				return nil, fmt.Errorf(
					"--persist %q: backend type %q has no default persistent mount paths "+
						"(known: %s) — pass --persist-mount to declare them explicitly",
					name, backends[bi].Type, knownPresetTypes())
			}
			spec.Mounts = append(spec.Mounts, preset...)
		}

		// Defense-in-depth: reject duplicate destinations within the
		// resolved list (kubelet would reject the pod silently
		// otherwise; this surfaces a clean CLI error instead).
		seenSub := map[string]bool{}
		seenPath := map[string]bool{}
		for _, m := range spec.Mounts {
			if seenSub[m.SubPath] {
				return nil, fmt.Errorf("--persist %q: duplicate subPath %q in resolved mount list", name, m.SubPath)
			}
			if seenPath[m.MountPath] {
				return nil, fmt.Errorf("--persist %q: duplicate mountPath %q in resolved mount list", name, m.MountPath)
			}
			seenSub[m.SubPath] = true
			seenPath[m.MountPath] = true
		}

		backends[bi].Storage = &spec
	}
	return backends, nil
}

// knownPresetTypes returns a comma-separated list of backend types
// that have default mount presets, for use in error messages.
func knownPresetTypes() string {
	names := make([]string, 0, len(BackendStoragePresets))
	for k := range BackendStoragePresets {
		names = append(names, k)
	}
	// Stable order for predictable error text.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	return strings.Join(names, ", ")
}
