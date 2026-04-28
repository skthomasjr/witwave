package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"

	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
)

// VolumeSpec is the parsed shape of a single --volume flag entry. Captures
// only the knobs the CLI exposes; per-volume reclaimPolicy / mountPath
// overrides are CR-only fields and lift via the -f path.
type VolumeSpec struct {
	// Name is the workspace-local volume identifier. Required.
	Name string
	// Size is the PVC storage request (e.g. "50Gi"). Required.
	Size string
	// StorageClassName is the PVC storage class. Empty → cluster default.
	StorageClassName string
}

// SecretSpec is the parsed shape of a single --secret flag entry.
type SecretSpec struct {
	// Name references an existing Secret in the workspace's namespace.
	Name string
	// MountPath, when non-empty, mounts the Secret at the absolute path.
	// Mutually exclusive with EnvFrom.
	MountPath string
	// EnvFrom, when true, projects the Secret's keys as env vars on
	// every backend container. Mutually exclusive with MountPath.
	EnvFrom bool
}

// CreateOptions collects the runtime inputs for `ww workspace create`.
//
// Two construction modes:
//
//   - File mode (FromFile non-empty): the manifest is read from disk and
//     applied verbatim. metadata.namespace, when missing, defaults to
//     Namespace; metadata.name, when missing, defaults to Name (so users
//     can leave both off the file and let CLI flags supply them).
//   - Flags mode (FromFile empty): Volumes / Secrets supply the spec.
//
// Mixing the two is rejected up-front so a typo doesn't silently skip
// half the input.
type CreateOptions struct {
	Name      string
	Namespace string

	// FromFile is the path to a YAML/JSON WitwaveWorkspace manifest. When set,
	// Volumes / Secrets MUST be empty.
	FromFile string

	// Volumes lists the volumes declared via repeatable --volume flags
	// (parsed via ParseVolumeSpecs). Empty when --from-file is used or
	// when the user wants a no-volume workspace.
	Volumes []VolumeSpec

	// Secrets lists the existing-Secret references declared via
	// repeatable --secret flags. Empty when --from-file is used.
	Secrets []SecretSpec

	// CLIVersion from cmd.Version; stamped into the created-by
	// annotation for forensics.
	CLIVersion string

	// CreatedBy captures the invoking command (e.g. "ww workspace
	// create witwave") for the CR's created-by annotation.
	CreatedBy string

	// AssumeYes skips the preflight confirmation. --yes / WW_ASSUME_YES.
	AssumeYes bool
	// DryRun renders the banner and exits without calling the API server.
	DryRun bool

	// CreateNamespace, when true, ensures the target namespace exists
	// before creating the CR (creates it if missing, no-op if it already
	// exists). Mirrors `helm install --create-namespace` and `ww agent
	// create --create-namespace`.
	CreateNamespace bool

	// Out + In route UI to/from the caller. Usually os.Stdout / os.Stdin.
	Out io.Writer
	In  io.Reader
}

// Create applies a WitwaveWorkspace CR to the target cluster. Flow:
//
//  1. Build the unstructured CR from opts (or read it from FromFile).
//  2. Render preflight banner via k8s.Confirm (honours --yes / --dry-run /
//     local-cluster heuristic per DESIGN.md KC-4).
//  3. Create via dynamic client. AlreadyExists is surfaced cleanly so a
//     user re-running with the same name gets a clear error, not a panic.
func Create(
	ctx context.Context,
	target *k8s.Target,
	cfg *rest.Config,
	opts CreateOptions,
) error {
	if opts.Out == nil {
		return fmt.Errorf("CreateOptions.Out is required")
	}
	if opts.FromFile != "" && (len(opts.Volumes) > 0 || len(opts.Secrets) > 0) {
		return fmt.Errorf("--from-file is mutually exclusive with --volume / --secret; pick one shape")
	}

	var (
		obj *unstructured.Unstructured
		err error
	)
	if opts.FromFile != "" {
		obj, err = buildFromFile(opts)
	} else {
		obj, err = buildFromFlags(opts)
	}
	if err != nil {
		return err
	}

	plan := []k8s.PlanLine{
		{Key: "Action", Value: fmt.Sprintf("create WitwaveWorkspace %q", obj.GetName())},
	}
	if vols := volumesSummary(obj); vols != "" {
		plan = append(plan, k8s.PlanLine{Key: "Volumes", Value: vols})
	}
	if secrets := secretsSummary(obj); secrets != "" {
		plan = append(plan, k8s.PlanLine{Key: "Secrets", Value: secrets})
	}
	if cfgs := configFilesSummary(obj); cfgs != "" {
		plan = append(plan, k8s.PlanLine{Key: "ConfigFiles", Value: cfgs})
	}

	proceed, err := k8s.Confirm(opts.Out, opts.In, target, plan, k8s.PromptOptions{
		AssumeYes: opts.AssumeYes,
		DryRun:    opts.DryRun,
	})
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	if opts.CreateNamespace {
		if err := ensureNamespace(ctx, cfg, opts.Namespace, opts.Out); err != nil {
			return err
		}
	}

	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}
	created, err := dyn.Resource(GVR()).Namespace(obj.GetNamespace()).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf(
				"WitwaveWorkspace %q already exists in namespace %q; delete it with `ww workspace delete %s -n %s` or choose a different name",
				obj.GetName(), obj.GetNamespace(), obj.GetName(), obj.GetNamespace(),
			)
		}
		return fmt.Errorf("create workspace: %w", err)
	}
	fmt.Fprintf(opts.Out, "Created WitwaveWorkspace %s in namespace %s (uid=%s).\n",
		created.GetName(), created.GetNamespace(), created.GetUID(),
	)
	fmt.Fprintln(opts.Out, "Next steps:")
	fmt.Fprintln(opts.Out, "  ww workspace status "+created.GetName()+"  # see provisioning state")
	fmt.Fprintln(opts.Out, "  ww workspace bind <agent> "+created.GetName()+"  # attach an agent")
	return nil
}

// buildFromFlags constructs the WitwaveWorkspace CR from the CLI's
// --volume / --secret flags. Names + sizes go through the same
// validation the CRD enforces so the user sees a usable error before
// the apiserver round-trip.
func buildFromFlags(opts CreateOptions) (*unstructured.Unstructured, error) {
	if err := ValidateName(opts.Name); err != nil {
		return nil, fmt.Errorf("validate name: %w", err)
	}
	if opts.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
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

	spec := map[string]interface{}{}

	if len(opts.Volumes) > 0 {
		volumes := make([]interface{}, 0, len(opts.Volumes))
		for i, v := range opts.Volumes {
			if err := ValidateVolumeName(v.Name); err != nil {
				return nil, fmt.Errorf("volume[%d]: %w", i, err)
			}
			if v.Size == "" {
				return nil, fmt.Errorf("volume[%d] %q: size is required", i, v.Name)
			}
			entry := map[string]interface{}{
				"name":       v.Name,
				"size":       v.Size,
				"accessMode": "ReadWriteMany",
			}
			if v.StorageClassName != "" {
				entry["storageClassName"] = v.StorageClassName
			}
			volumes = append(volumes, entry)
		}
		spec["volumes"] = volumes
	}

	if len(opts.Secrets) > 0 {
		secrets := make([]interface{}, 0, len(opts.Secrets))
		for i, s := range opts.Secrets {
			if s.Name == "" {
				return nil, fmt.Errorf("secret[%d]: name is required", i)
			}
			if s.MountPath != "" && s.EnvFrom {
				return nil, fmt.Errorf("secret[%d] %q: --secret mountPath and envFrom are mutually exclusive", i, s.Name)
			}
			entry := map[string]interface{}{"name": s.Name}
			if s.MountPath != "" {
				entry["mountPath"] = s.MountPath
			}
			if s.EnvFrom {
				entry["envFrom"] = true
			}
			secrets = append(secrets, entry)
		}
		spec["secrets"] = secrets
	}

	if err := unstructured.SetNestedField(obj.Object, spec, "spec"); err != nil {
		return nil, fmt.Errorf("set spec: %w", err)
	}
	return obj, nil
}

// buildFromFile reads opts.FromFile and parses it into an unstructured
// WitwaveWorkspace CR. Falls through to opts.Name / opts.Namespace defaults
// when the file leaves either field unset, so users can `-f workspace.yaml`
// without duplicating identity in two places.
func buildFromFile(opts CreateOptions) (*unstructured.Unstructured, error) {
	data, err := os.ReadFile(opts.FromFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", opts.FromFile, err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", opts.FromFile, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s is empty", opts.FromFile)
	}

	obj := &unstructured.Unstructured{Object: raw}

	// Reject mismatched apiVersion / kind early — apply-by-hand mistakes
	// (typing the wrong CRD into the wrong file) are common; surface them
	// before the apiserver does.
	if av := obj.GetAPIVersion(); av != "" && av != APIVersionString() {
		return nil, fmt.Errorf("%s: apiVersion %q does not match expected %q", opts.FromFile, av, APIVersionString())
	}
	if k := obj.GetKind(); k != "" && k != Kind {
		return nil, fmt.Errorf("%s: kind %q does not match expected %q", opts.FromFile, k, Kind)
	}
	obj.SetAPIVersion(APIVersionString())
	obj.SetKind(Kind)

	// Apply CLI defaults when the file leaves identity unset.
	if obj.GetName() == "" {
		if opts.Name == "" {
			return nil, fmt.Errorf("%s: metadata.name unset and no <name> argument supplied", opts.FromFile)
		}
		obj.SetName(opts.Name)
	} else if opts.Name != "" && opts.Name != obj.GetName() {
		return nil, fmt.Errorf(
			"%s: metadata.name %q does not match positional argument %q — pick one",
			opts.FromFile, obj.GetName(), opts.Name,
		)
	}
	if obj.GetNamespace() == "" {
		obj.SetNamespace(opts.Namespace)
	}

	if err := ValidateName(obj.GetName()); err != nil {
		return nil, fmt.Errorf("%s: %w", opts.FromFile, err)
	}

	// Stamp managed-by + created-by so file-based creates are still
	// distinguishable from hand-applied manifests.
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	if _, ok := labels[LabelManagedBy]; !ok {
		labels[LabelManagedBy] = LabelManagedByWW
	}
	obj.SetLabels(labels)
	if opts.CreatedBy != "" {
		annotations := obj.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		if _, ok := annotations[AnnotationCreatedBy]; !ok {
			annotations[AnnotationCreatedBy] = opts.CreatedBy
		}
		obj.SetAnnotations(annotations)
	}

	return obj, nil
}

// volumesSummary returns a one-line "name=size@class, …" summary of the
// rendered volumes, suitable for the preflight banner.
func volumesSummary(obj *unstructured.Unstructured) string {
	vols, found, err := unstructured.NestedSlice(obj.Object, "spec", "volumes")
	if err != nil || !found || len(vols) == 0 {
		return ""
	}
	parts := make([]string, 0, len(vols))
	for _, v := range vols {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		size, _ := m["size"].(string)
		class, _ := m["storageClassName"].(string)
		entry := name
		if size != "" {
			entry += "=" + size
		}
		if class != "" {
			entry += "@" + class
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, ", ")
}

func secretsSummary(obj *unstructured.Unstructured) string {
	items, found, err := unstructured.NestedSlice(obj.Object, "spec", "secrets")
	if err != nil || !found || len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, s := range items {
		m, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		entry := name
		if mp, _ := m["mountPath"].(string); mp != "" {
			entry += " (mount " + mp + ")"
		} else if env, _ := m["envFrom"].(bool); env {
			entry += " (envFrom)"
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, ", ")
}

func configFilesSummary(obj *unstructured.Unstructured) string {
	items, found, err := unstructured.NestedSlice(obj.Object, "spec", "configFiles")
	if err != nil || !found || len(items) == 0 {
		return ""
	}
	return fmt.Sprintf("%d file(s)", len(items))
}
