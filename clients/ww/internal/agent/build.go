package agent

import (
	"fmt"

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
	// Backend name — one of the known types (echo, claude, codex, gemini).
	// Empty string defaults to DefaultBackend.
	Backend string
	// HarnessImage is the full image reference for the harness container.
	// Empty string → HarnessImage(cliVersion).
	HarnessImage string
	// BackendImage is the full image reference for the backend sidecar.
	// Empty string → BackendImage(backend, cliVersion).
	BackendImage string
	// CLIVersion from cmd.Version, used to resolve image tags when
	// HarnessImage / BackendImage aren't supplied explicitly. Safe to
	// pass the "dev" sentinel — imageRef handles it.
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

	backend := opts.Backend
	if backend == "" {
		backend = DefaultBackend
	}
	if !IsKnownBackend(backend) {
		return nil, fmt.Errorf(
			"unknown backend %q; valid backends: %v",
			backend, KnownBackends(),
		)
	}

	harnessImage := opts.HarnessImage
	if harnessImage == "" {
		harnessImage = HarnessImage(opts.CLIVersion)
	}
	backendImage := opts.BackendImage
	if backendImage == "" {
		backendImage = BackendImage(backend, opts.CLIVersion)
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

	spec := map[string]interface{}{
		"port": int64(DefaultPort),
		"image": map[string]interface{}{
			"repository": splitRepo(harnessImage),
			"tag":        splitTag(harnessImage),
		},
		"backends": []interface{}{
			map[string]interface{}{
				"name": backend,
				"port": int64(DefaultPort),
				"image": map[string]interface{}{
					"repository": splitRepo(backendImage),
					"tag":        splitTag(backendImage),
				},
			},
		},
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
