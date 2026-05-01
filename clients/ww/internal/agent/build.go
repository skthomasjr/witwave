package agent

import (
	"fmt"
	"sort"
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

	// Team, when non-empty, stamps the `witwave.ai/team=<team>` label on
	// the CR so the operator lists it in the per-team manifest ConfigMap
	// at first reconcile. Equivalent to running `ww agent team join`
	// after `ww agent create`, but avoids the two-call race where the
	// agent flashes into the namespace-wide manifest for a moment
	// before the label lands.
	Team string

	// WorkspaceRefs lists WitwaveWorkspace names this agent should bind
	// to at creation time, populating spec.workspaceRefs[]. v1alpha1
	// only supports same-namespace binding so each name resolves to a
	// workspace in BuildOptions.Namespace. Empty → no refs (the user
	// can attach later with `ww workspace bind`). Duplicate names are
	// rejected — the CRD's listMapKey=name forbids them.
	WorkspaceRefs []string

	// GitSyncs declares the gitSync entries the operator should
	// reconcile onto this agent's pod. Each entry produces one
	// gitSyncs[] CR field plus init / sidecar containers that clone
	// the named repo into /git/<name>. Cross-flag validation
	// (referenced by GitMappings) is the caller's responsibility —
	// see ValidateGitFlags.
	GitSyncs []GitSyncFlagSpec

	// GitMappings declares the per-(container, dest) copy operations
	// the gitSync init script applies after each clone. Entries with
	// Container == HarnessContainer land in spec.gitMappings[]; entries
	// targeting a backend name land in that backend's
	// spec.backends[].gitMappings[]. Cross-flag validation is the
	// caller's responsibility — see ValidateGitFlags.
	GitMappings []GitMappingFlagSpec

	// NoMetrics, when true, stamps spec.metrics.enabled=false on the CR
	// to opt out of the operator's default-on metrics posture. When false
	// (the default), the field is omitted from the CR so the CRD's
	// kubebuilder default fires and metrics land enabled.
	NoMetrics bool
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
	if opts.Team != "" {
		if err := ValidateName(opts.Team); err != nil {
			return nil, fmt.Errorf("team name %q: %w", opts.Team, err)
		}
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
	labels := map[string]string{LabelManagedBy: LabelManagedByWW}
	if opts.Team != "" {
		labels[TeamLabel] = opts.Team
	}
	obj.SetLabels(labels)
	if len(annotations) > 0 {
		obj.SetAnnotations(annotations)
	}

	// Pre-bucket per-backend gitMappings so each backend entry below
	// only stamps its own slice. Harness-targeted mappings emit at the
	// agent level (see further down) and are excluded here.
	perBackendMappings := map[string][]interface{}{}
	for _, m := range opts.GitMappings {
		if m.Container == HarnessContainer {
			continue
		}
		perBackendMappings[m.Container] = append(perBackendMappings[m.Container],
			map[string]interface{}{
				"gitSync": m.GitSync,
				"src":     m.Src,
				"dest":    m.Dest,
			})
	}

	// Build spec.backends[] — one entry per declared backend. Image is
	// derived from the TYPE (not the name), so `echo-1` and `echo-2`
	// both pull the echo image.
	specBackends := make([]interface{}, 0, len(backends))
	for _, b := range backends {
		img := BackendImage(b.Type, opts.CLIVersion)
		entry := map[string]interface{}{
			"name": b.Name,
			"port": int64(b.Port),
			"image": map[string]interface{}{
				"repository": splitRepo(img),
				"tag":        splitTag(img),
			},
		}
		if b.CredentialSecret != "" {
			entry["credentials"] = map[string]interface{}{
				"existingSecret": b.CredentialSecret,
			}
		}
		if b.Storage != nil {
			storage := map[string]interface{}{
				"enabled": true,
				"size":    b.Storage.Size,
			}
			if b.Storage.StorageClassName != "" {
				storage["storageClassName"] = b.Storage.StorageClassName
			}
			if len(b.Storage.Mounts) > 0 {
				mounts := make([]interface{}, 0, len(b.Storage.Mounts))
				for _, m := range b.Storage.Mounts {
					mounts = append(mounts, map[string]interface{}{
						"subPath":   m.SubPath,
						"mountPath": m.MountPath,
					})
				}
				storage["mounts"] = mounts
			}
			entry["storage"] = storage
		}
		if maps := perBackendMappings[b.Name]; len(maps) > 0 {
			entry["gitMappings"] = maps
		}
		if len(b.Env) > 0 {
			// Sort keys for deterministic output — `kubectl get -o yaml`
			// diffs and CR equality checks rely on stable ordering.
			keys := make([]string, 0, len(b.Env))
			for k := range b.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			envList := make([]interface{}, 0, len(keys))
			for _, k := range keys {
				envList = append(envList, map[string]interface{}{
					"name":  k,
					"value": b.Env[k],
				})
			}
			entry["env"] = envList
		}
		specBackends = append(specBackends, entry)
	}

	// spec.workspaceRefs[] — one {name: <ws>} entry per requested
	// binding. Defense-in-depth: duplicates rejected here too so a
	// caller bypassing the CLI flag layer (tests, programmatic) still
	// gets the same shape the CRD's listMapKey=name expects.
	var workspaceRefs []interface{}
	if len(opts.WorkspaceRefs) > 0 {
		seenWS := make(map[string]bool, len(opts.WorkspaceRefs))
		workspaceRefs = make([]interface{}, 0, len(opts.WorkspaceRefs))
		for i, name := range opts.WorkspaceRefs {
			if err := ValidateName(name); err != nil {
				return nil, fmt.Errorf("workspaceRefs[%d] %q: %w", i, name, err)
			}
			if seenWS[name] {
				return nil, fmt.Errorf("workspaceRefs[%d]: duplicate name %q", i, name)
			}
			seenWS[name] = true
			workspaceRefs = append(workspaceRefs, map[string]interface{}{"name": name})
		}
	}

	// Decide whether to emit the inline backend.yaml. Skip it when a
	// harness-level gitMapping already covers /home/agent/.witwave/ —
	// in that case the gitSync sidecar delivers the file from the repo
	// and the operator's overlay would race the rsync exechook (the
	// `--delete` flag deletes any file in dest not present in source,
	// including operator-overlaid configmap subPath files). The harness
	// tolerates a brief no-backends window at startup until gitSync's
	// first pull lands; the existing /health/ready degraded state is
	// the right signal during that window.
	harnessOwnsWitwaveDir := false
	for _, m := range opts.GitMappings {
		if m.Container != HarnessContainer {
			continue
		}
		if m.Dest == "/home/agent/.witwave/" ||
			m.Dest == "/home/agent/.witwave" ||
			m.Dest == "/home/agent/.witwave/backend.yaml" {
			harnessOwnsWitwaveDir = true
			break
		}
	}

	spec := map[string]interface{}{
		"port": int64(DefaultHarnessPort),
		"image": map[string]interface{}{
			"repository": splitRepo(harnessImage),
			"tag":        splitTag(harnessImage),
		},
		"backends": specBackends,
	}
	if !harnessOwnsWitwaveDir {
		// Inline backend.yaml so the harness can route A2A requests to
		// at least one sidecar without waiting on a gitSync pull.
		// Without this AND without a covering gitMapping, harness
		// /health/ready stays 503 with reason=no-backends-configured
		// (harness/main.py:524-534) and the pod never flips Ready. See
		// harness/backends/config.py for the shape. For multi-backend
		// agents every declared backend is listed, but every concern
		// routes to the FIRST — users redistribute routing by editing
		// the file.
		spec["config"] = []interface{}{
			map[string]interface{}{
				"name":      "backend.yaml",
				"mountPath": "/home/agent/.witwave/backend.yaml",
				"content":   renderBackendYAML(backends),
			},
		}
	}
	if workspaceRefs != nil {
		spec["workspaceRefs"] = workspaceRefs
	}

	// spec.gitSyncs[] — one entry per declared --gitsync. The buildGitSyncEntry
	// helper handles the public (no creds) and existing-Secret cases; inline
	// credentials are intentionally not part of the CLI flag surface.
	if len(opts.GitSyncs) > 0 {
		gitSyncs := make([]interface{}, 0, len(opts.GitSyncs))
		for _, gs := range opts.GitSyncs {
			gitSyncs = append(gitSyncs,
				buildGitSyncEntry(gs.Name, gs.URL, gs.Branch, DefaultGitPeriod, gs.ExistingSecret))
		}
		spec["gitSyncs"] = gitSyncs
	}

	// spec.gitMappings[] — harness-targeted mappings only. Per-backend
	// mappings landed inside the backend entries above.
	var harnessMappings []interface{}
	for _, m := range opts.GitMappings {
		if m.Container != HarnessContainer {
			continue
		}
		harnessMappings = append(harnessMappings, map[string]interface{}{
			"gitSync": m.GitSync,
			"src":     m.Src,
			"dest":    m.Dest,
		})
	}
	if len(harnessMappings) > 0 {
		spec["gitMappings"] = harnessMappings
	}

	// Only stamp spec.metrics when the user explicitly opts out. Leaving
	// the field unset lets the CRD's kubebuilder default (true) fire at
	// admission, which is the default-on posture every operator-managed
	// agent gets.
	if opts.NoMetrics {
		spec["metrics"] = map[string]interface{}{"enabled": false}
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
