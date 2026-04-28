package workspace

import (
	"context"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// ParseVolumeSpecs converts the repeatable --volume flag values into
// VolumeSpec entries. Supported shapes:
//
//	name=size                       e.g. source=50Gi
//	name=size@class                 e.g. source=50Gi@efs-sc
//	name=size:mode                  e.g. source=20Gi:rwo
//	name=size@class:mode            e.g. source=20Gi@hostpath:rwo
//
// `mode` is one of `rwm` / `rwo` / `rwop` (case-insensitive) or the
// canonical `ReadWriteMany` / `ReadWriteOnce` / `ReadWriteOncePod`.
// Default when omitted: `rwm` (cross-node-safe).
//
// Names go through ValidateVolumeName before return so the user sees a
// clear error before the apiserver round-trip. Duplicates are rejected.
func ParseVolumeSpecs(raw []string) ([]VolumeSpec, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]VolumeSpec, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, r := range raw {
		v, err := parseVolumeSpec(r)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[v.Name]; dup {
			return nil, fmt.Errorf("--volume: duplicate name %q", v.Name)
		}
		seen[v.Name] = struct{}{}
		out = append(out, v)
	}
	return out, nil
}

func parseVolumeSpec(raw string) (VolumeSpec, error) {
	// Split trailing :<accessMode> first so it can't be confused with
	// any colon that might appear inside a size or class name (none in
	// practice, but the order keeps the grammar unambiguous).
	body := raw
	var mode string
	if i := strings.LastIndexByte(body, ':'); i >= 0 {
		mode = strings.TrimSpace(body[i+1:])
		body = body[:i]
		if mode == "" {
			return VolumeSpec{}, fmt.Errorf("--volume %q: access mode after ':' is empty", raw)
		}
		canon, err := canonicaliseAccessMode(mode)
		if err != nil {
			return VolumeSpec{}, fmt.Errorf("--volume %q: %w", raw, err)
		}
		mode = canon
	}
	// Then split off optional @class suffix.
	var class string
	if i := strings.IndexByte(body, '@'); i >= 0 {
		class = strings.TrimSpace(body[i+1:])
		body = body[:i]
		if class == "" {
			return VolumeSpec{}, fmt.Errorf("--volume %q: storage class after '@' is empty", raw)
		}
	}
	// Then split name=size.
	eq := strings.IndexByte(body, '=')
	if eq < 0 {
		return VolumeSpec{}, fmt.Errorf("--volume %q: expected `name=size[@class][:mode]`", raw)
	}
	name := strings.TrimSpace(body[:eq])
	size := strings.TrimSpace(body[eq+1:])
	if name == "" {
		return VolumeSpec{}, fmt.Errorf("--volume %q: name before '=' is empty", raw)
	}
	if size == "" {
		return VolumeSpec{}, fmt.Errorf("--volume %q: size after '=' is empty", raw)
	}
	if err := ValidateVolumeName(name); err != nil {
		return VolumeSpec{}, fmt.Errorf("--volume %q: %w", raw, err)
	}
	return VolumeSpec{Name: name, Size: size, StorageClassName: class, AccessMode: mode}, nil
}

// canonicaliseAccessMode maps the user-friendly short forms (rwm/rwo/rwop,
// case-insensitive) to the canonical Kubernetes access-mode strings the
// CRD expects. Canonical inputs pass through unchanged. Unknown values
// produce a clear error listing the valid options.
func canonicaliseAccessMode(s string) (string, error) {
	switch strings.ToLower(s) {
	case "rwm", "readwritemany":
		return "ReadWriteMany", nil
	case "rwo", "readwriteonce":
		return "ReadWriteOnce", nil
	case "rwop", "readwriteoncepod":
		return "ReadWriteOncePod", nil
	default:
		return "", fmt.Errorf("access mode %q: must be rwm/ReadWriteMany, rwo/ReadWriteOnce, or rwop/ReadWriteOncePod", s)
	}
}

// ParseSecretSpecs converts the repeatable --secret flag values into
// SecretSpec entries. Supported shapes:
//
//	name                          plain reference (envFrom defaults off)
//	name@/abs/path                mount the Secret at /abs/path
//	name=env                      project the Secret as env vars
//
// "env" is a sentinel — anything else after `=` is an error so a typo
// doesn't silently fall through to mountPath mode.
func ParseSecretSpecs(raw []string) ([]SecretSpec, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]SecretSpec, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, r := range raw {
		s, err := parseSecretSpec(r)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[s.Name]; dup {
			return nil, fmt.Errorf("--secret: duplicate name %q", s.Name)
		}
		seen[s.Name] = struct{}{}
		out = append(out, s)
	}
	return out, nil
}

func parseSecretSpec(raw string) (SecretSpec, error) {
	if raw == "" {
		return SecretSpec{}, fmt.Errorf("--secret: empty value")
	}
	// envFrom shape: `name=env`.
	if i := strings.IndexByte(raw, '='); i >= 0 {
		name := strings.TrimSpace(raw[:i])
		mode := strings.TrimSpace(raw[i+1:])
		if name == "" {
			return SecretSpec{}, fmt.Errorf("--secret %q: name before '=' is empty", raw)
		}
		if mode != "env" {
			return SecretSpec{}, fmt.Errorf("--secret %q: only `=env` is recognised after '='; for a mount path use '@/abs/path'", raw)
		}
		return SecretSpec{Name: name, EnvFrom: true}, nil
	}
	// mountPath shape: `name@/abs/path`.
	if i := strings.IndexByte(raw, '@'); i >= 0 {
		name := strings.TrimSpace(raw[:i])
		path := strings.TrimSpace(raw[i+1:])
		if name == "" {
			return SecretSpec{}, fmt.Errorf("--secret %q: name before '@' is empty", raw)
		}
		if path == "" || !strings.HasPrefix(path, "/") {
			return SecretSpec{}, fmt.Errorf("--secret %q: mount path after '@' must be absolute (start with /)", raw)
		}
		return SecretSpec{Name: name, MountPath: path}, nil
	}
	return SecretSpec{Name: strings.TrimSpace(raw)}, nil
}

// ensureNamespace creates the target namespace if it doesn't already
// exist. Idempotent: a pre-existing namespace is treated as success.
// Mirrors agent.ensureNamespace so the workspace subtree behaves the
// same way under --create-namespace.
func ensureNamespace(ctx context.Context, cfg *rest.Config, name string, out io.Writer) error {
	k8sClient, err := clientFactory.kube(cfg)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}
	_, err = k8sClient.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check namespace %q: %w", name, err)
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				LabelManagedBy: LabelManagedByWW,
			},
		},
	}
	if _, err := k8sClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create namespace %q: %w", name, err)
	}
	fmt.Fprintf(out, "Created namespace %s (labelled %s=%s).\n", name, LabelManagedBy, LabelManagedByWW)
	return nil
}
