package agent

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// BuildOptions collects everything the CR builder needs beyond the name
// and namespace. Zero values are sensible for hello-world usage.
type BuildOptions struct {
	// Name of the WitwaveAgent CR.
	Name string
	// Namespace the CR will live in.
	Namespace string

	// Backends is the list of declared backends. Empty → a single
	// default-echo backend (the hello-world shortcut). Multiple entries
	// produce a multi-sidecar agent with distinct names + ports.
	// Populate via ParseBackendSpecs for cobra-parsed CLI flags, or
	// construct directly for tests / programmatic use.
	Backends []BackendSpec

	// HarnessImage is the full image reference for the harness container.
	// Empty string → HarnessImage(cliVersion).
	HarnessImage string

	// CLIVersion from cmd.Version, used to resolve image tags when
	// HarnessImage / per-backend images aren't supplied explicitly.
	// Safe to pass the "dev" sentinel — imageRef handles it.
	CLIVersion string

	// CreatedBy captures the command line that produced the CR. Landed
	// as an annotation so operators can trace surprise CRs back to the
	// invocation that created them.
	CreatedBy string
}

// Build constructs the unstructured.Unstructured representation of a
// minimum-viable WitwaveAgent. The CRD requires only `image` and a
// non-empty `backends[]` with each entry carrying `name` and `image` —
// every other field falls through to operator defaults.
//
// The resulting object is ready to pass to dynamic client Create.
func Build(opts BuildOptions) (*unstructured.Unstructured, error) {
	if err := ValidateName(opts.Name); err != nil {
		return nil, fmt.Errorf("validate name: %w", err)
	}
	if opts.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}

	backends := opts.Backends
	if len(backends) == 0 {
		// Hello-world shortcut: empty → one default-echo backend.
		// Keeps the single-backend code path unchanged for callers
		// that don't care about multi-backend (tests, the simpler
		// `ww agent create` invocations, …).
		backends = []BackendSpec{{Name: DefaultBackend, Type: DefaultBackend, Port: BackendPort(0)}}
	}
	// Defense-in-depth: every backend's type must still be known, and
	// names still unique. ParseBackendSpecs enforces this at flag-parse
	// time but callers constructing BuildOptions directly (tests,
	// programmatic use) may not have gone through it.
	seen := make(map[string]bool, len(backends))
	for i, b := range backends {
		if err := ValidateName(b.Name); err != nil {
			return nil, fmt.Errorf("backends[%d] name: %w", i, err)
		}
		if !IsKnownBackend(b.Type) {
			return nil, fmt.Errorf(
				"backends[%d] unknown type %q; valid: %v",
				i, b.Type, KnownBackends(),
			)
		}
		if seen[b.Name] {
			return nil, fmt.Errorf("backends[%d]: duplicate name %q", i, b.Name)
		}
		seen[b.Name] = true
	}

	harnessImage := opts.HarnessImage
	if harnessImage == "" {
		harnessImage = HarnessImage(opts.CLIVersion)
	}

	annotations := map[string]string{}
	if opts.CreatedBy != "" {
		annotations[AnnotationCreatedBy] = opts.CreatedBy
	}

	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(APIVersionString())
	obj.SetKind(Kind)
	obj.SetName(opts.Name)
	obj.SetNamespace(opts.Namespace)
	obj.SetLabels(map[string]string{LabelManagedBy: LabelManagedByWW})
	if len(annotations) > 0 {
		obj.SetAnnotations(annotations)
	}

	// Build spec.backends[] — one entry per declared backend. Image is
	// derived from the TYPE (not the name), so `echo-1` and `echo-2`
	// both pull the echo image.
	specBackends := make([]interface{}, 0, len(backends))
	for _, b := range backends {
		img := BackendImage(b.Type, opts.CLIVersion)
		specBackends = append(specBackends, map[string]interface{}{
			"name": b.Name,
			"port": int64(b.Port),
			"image": map[string]interface{}{
				"repository": splitRepo(img),
				"tag":        splitTag(img),
			},
		})
	}

	spec := map[string]interface{}{
		"port": int64(DefaultHarnessPort),
		"image": map[string]interface{}{
			"repository": splitRepo(harnessImage),
			"tag":        splitTag(harnessImage),
		},
		// Inline backend.yaml so the harness can route A2A requests to
		// at least one sidecar without waiting on a gitSync pull.
		// Without this, harness /health/ready stays 503 with
		// reason=no-backends-configured (harness/main.py:524-534) and
		// the pod never flips Ready. See harness/backends/config.py
		// for the shape. For multi-backend agents every declared
		// backend is listed, but every concern routes to the FIRST —
		// users redistribute routing by editing the file.
		"config": []interface{}{
			map[string]interface{}{
				"name":      "backend.yaml",
				"mountPath": "/home/agent/.witwave/backend.yaml",
				"content":   renderBackendYAML(backends),
			},
		},
		"backends": specBackends,
	}
	if err := unstructured.SetNestedField(obj.Object, spec, "spec"); err != nil {
		return nil, fmt.Errorf("set spec: %w", err)
	}
	return obj, nil
}

// splitRepo returns the repository half of an image reference.
// `ghcr.io/org/name:tag` → `ghcr.io/org/name`. A reference without a
// colon is returned verbatim.
func splitRepo(ref string) string {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == ':' {
			// Guard against port-in-registry edge cases like
			// `localhost:5000/foo` (no tag) — a colon followed by a '/'
			// isn't a tag separator. Walk the suffix for a slash.
			for j := i + 1; j < len(ref); j++ {
				if ref[j] == '/' {
					return ref
				}
			}
			return ref[:i]
		}
		if ref[i] == '/' {
			break
		}
	}
	return ref
}

// splitTag returns the tag half of an image reference. `repo:tag` → `tag`.
// A reference without a recognised tag separator returns "" so the chart
// default (.Chart.AppVersion) kicks in downstream.
func splitTag(ref string) string {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == ':' {
			for j := i + 1; j < len(ref); j++ {
				if ref[j] == '/' {
					return ""
				}
			}
			return ref[i+1:]
		}
		if ref[i] == '/' {
			break
		}
	}
	return ""
}

// Enforce package compile-time that metav1 is actually used — we export
// the TypeMeta shape implicitly via APIVersionString / Kind.
var _ = metav1.TypeMeta{}

// renderBackendYAML generates the harness routing config consumed at
// BACKEND_CONFIG_PATH. Every declared backend is enumerated in
// `agents:`; every concern (a2a, heartbeat, job, task, trigger,
// continuation) initially routes to the FIRST backend. Users with
// multi-backend agents redistribute routing by editing the file —
// picking defaults that put some on a "foreground" backend and others
// on "background" is a product decision we don't want to guess. See
// harness/backends/config.py for the authoritative shape.
//
// Single-backend callers get the same output as the pre-multi-backend
// implementation: one entry under agents:, every concern pointed at
// it. Backward-compatible.
func renderBackendYAML(backends []BackendSpec) string {
	var agents strings.Builder
	for _, b := range backends {
		fmt.Fprintf(&agents, "    - id: %s\n      url: http://localhost:%d\n", b.Name, b.Port)
	}
	primary := PrimaryBackend(backends).Name
	return fmt.Sprintf(`backend:
  agents:
%[1]s
  routing:
    default:
      agent: %[2]s
    a2a:
      agent: %[2]s
    heartbeat:
      agent: %[2]s
    job:
      agent: %[2]s
    task:
      agent: %[2]s
    trigger:
      agent: %[2]s
    continuation:
      agent: %[2]s
`, strings.TrimRight(agents.String(), "\n"), primary)
}
