package operator

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
)

// HelmClient wraps the Helm action.Configuration and the parameters ww
// cares about for managing the operator release. One HelmClient per
// command invocation — don't reuse across namespaces.
type HelmClient struct {
	cfg         *action.Configuration
	settings    *cli.EnvSettings
	namespace   string
	releaseName string
}

// NewHelmClient builds an action.Configuration against the caller-
// supplied kubeconfig + namespace. The Helm SDK insists on taking its
// own genericclioptions.ConfigFlags; we pass the one the k8s.Resolver
// produced so kubeconfig path + context + namespace are consistent
// across ww's read-only probes and the Helm actions here.
//
// logFn receives Helm's verbose progress messages. Pass a closure that
// writes to ww's output writer, or Noop to discard.
func NewHelmClient(flags *genericclioptions.ConfigFlags, namespace, releaseName string, logFn func(string, ...interface{})) (*HelmClient, error) {
	if logFn == nil {
		logFn = Noop
	}
	cfg := new(action.Configuration)
	// Driver "secret" matches Helm 3's default release storage — one
	// Secret per release revision. "configmap" and "sql" exist but
	// nobody should use them for a first-party install.
	if err := cfg.Init(flags, namespace, "secret", func(format string, v ...interface{}) {
		logFn(format, v...)
	}); err != nil {
		return nil, fmt.Errorf("init helm config: %w", err)
	}
	return &HelmClient{
		cfg:         cfg,
		settings:    cli.New(),
		namespace:   namespace,
		releaseName: releaseName,
	}, nil
}

// Noop is a no-op log sink suitable for callers that want quiet Helm
// actions (e.g. status probes).
func Noop(string, ...interface{}) {}

// StderrLog returns a log function that writes Helm's progress output
// to the given writer. Each message gets a "helm: " prefix so users
// can separate it from ww's own output.
func StderrLog(w io.Writer) func(string, ...interface{}) {
	logger := log.New(w, "helm: ", 0)
	return logger.Printf
}

// Install runs `helm install <release> <chart>` using the embedded
// chart. Values is the rendered user-supplied values map (typically
// empty for ww operator install — the chart defaults are sensible).
// Returns the deployed release on success.
//
// The caller is responsible for having run the preflight checks
// (singleton detection, RBAC preflight, namespace creation).
func (c *HelmClient) Install(ctx context.Context, ch *chart.Chart, values map[string]interface{}) (*release.Release, error) {
	act := action.NewInstall(c.cfg)
	act.ReleaseName = c.releaseName
	act.Namespace = c.namespace
	act.CreateNamespace = true
	act.Wait = false
	// We install the CRDs explicitly via SSA in the upgrade path and
	// via helm.DisableCRDInstall=false here so first-install carries
	// the CRDs as part of the chart. Operators running ww twice in a
	// row will hit the SSA path on the second invocation.
	act.SkipCRDs = false
	// RunWithContext honours context cancellation mid-apply.
	rel, err := act.RunWithContext(ctx, ch, values)
	if err != nil {
		return nil, fmt.Errorf("helm install: %w", err)
	}
	return rel, nil
}

// Upgrade runs `helm upgrade <release> <chart>` against an existing
// release. Caller should have applied any CRD changes via a separate
// server-side-apply step first so Helm's upgrade just touches the
// regular Kubernetes objects.
func (c *HelmClient) Upgrade(ctx context.Context, ch *chart.Chart, values map[string]interface{}) (*release.Release, error) {
	act := action.NewUpgrade(c.cfg)
	act.Namespace = c.namespace
	act.Wait = false
	act.SkipCRDs = true // CRDs are applied separately (see upgrade.go)
	act.MaxHistory = 10 // keep recent history for rollback; drop ancient
	rel, err := act.RunWithContext(ctx, c.releaseName, ch, values)
	if err != nil {
		return nil, fmt.Errorf("helm upgrade: %w", err)
	}
	return rel, nil
}

// Uninstall removes the Helm release. CRD deletion is NOT part of
// Helm's uninstall and MUST be handled separately — callers decide
// whether to delete CRDs based on --delete-crds + CR-existence safety.
func (c *HelmClient) Uninstall(ctx context.Context) (*release.UninstallReleaseResponse, error) {
	act := action.NewUninstall(c.cfg)
	act.Timeout = 0 // no explicit timeout; context cancellation is authoritative
	// Keep history off so a re-install lands cleanly.
	act.KeepHistory = false
	resp, err := act.Run(c.releaseName)
	if err != nil {
		return nil, fmt.Errorf("helm uninstall: %w", err)
	}
	return resp, nil
}

// Get returns the current release info or nil if not installed.
func (c *HelmClient) Get() (*release.Release, error) {
	act := action.NewGet(c.cfg)
	rel, err := act.Run(c.releaseName)
	if err != nil {
		// Helm returns a specific error for "release not found"; string-
		// match is gross but the SDK doesn't export a typed sentinel.
		if strings.Contains(err.Error(), "release: not found") {
			return nil, nil
		}
		return nil, fmt.Errorf("helm get: %w", err)
	}
	return rel, nil
}

// EnsureNamespace creates the target namespace if it doesn't exist.
// Idempotent — calling against an existing namespace is a no-op.
// Helm's own CreateNamespace flag covers install, but uninstall and
// manual namespace seeding (e.g. for dry-run output) need their own
// path.
func EnsureNamespace(ctx context.Context, k8s kubernetes.Interface, ns string) error {
	_, err := k8s.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("probe namespace %s: %w", ns, err)
	}
	_, err = k8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace %s: %w", ns, err)
	}
	return nil
}
