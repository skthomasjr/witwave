// Package k8s resolves kubeconfig, context, and preflight target confirmation
// for ww's operator / stack management commands.
//
// The package is deliberately thin: it wraps client-go's standard loader
// chain so callers see one Target struct with every field they need to
// print a preflight banner, classify the cluster for the prompt
// heuristic, and build a REST config.
package k8s

import (
	"errors"
	"fmt"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Options selects which kubeconfig + context ww uses. Zero-value means
// "follow client-go defaults" (KUBECONFIG env → ~/.kube/config, current-context).
type Options struct {
	// KubeconfigPath — absolute or tilde-expanded path. Empty defers to
	// the standard loader (KUBECONFIG env var, then ~/.kube/config).
	KubeconfigPath string
	// Context — name of the context to use. Empty means current-context
	// as recorded in the kubeconfig.
	Context string
	// Namespace — when non-empty, overrides the namespace the context
	// would otherwise select. Used by `ww operator --namespace`.
	Namespace string
}

// Target is the resolved view of where ww is about to act. Populated by
// Resolve and safe to print in a preflight banner. All fields are display-
// oriented strings; callers use REST() or ClientConfig() to get a typed
// config for actual API calls.
type Target struct {
	// Context is the kubeconfig context name that was selected
	// (either current-context or the caller-supplied override).
	Context string
	// Cluster is the cluster nickname the context points at.
	Cluster string
	// Server is the apiserver URL. Used by the local-cluster heuristic
	// and surfaced verbatim in the preflight banner so operators can
	// sanity-check which apiserver they're about to mutate.
	Server string
	// User is the kubeconfig user nickname the context points at.
	User string
	// Namespace is the effective namespace (Options.Namespace override,
	// else the context's namespace, else "default").
	Namespace string
}

// Resolver loads a kubeconfig and produces a Target + REST config.
// Wraps clientcmd.NewNonInteractiveDeferredLoadingClientConfig so that
// KUBECONFIG, --kubeconfig, and ~/.kube/config are all honoured in the
// standard client-go precedence.
type Resolver struct {
	opts        Options
	raw         clientcmdapi.Config
	clientCfg   clientcmd.ClientConfig
	resolvedCtx string
}

// NewResolver runs the client-go loader chain and captures the parsed
// kubeconfig so Resolve() can return a Target without re-reading. Errors
// out of NewResolver are surfaced to the caller with the missing-file /
// missing-context diagnostics embedded — so `ww` prints a readable
// "no kubeconfig found" instead of a raw stack trace.
func NewResolver(opts Options) (*Resolver, error) {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.KubeconfigPath != "" {
		loading.ExplicitPath = opts.KubeconfigPath
	}

	overrides := &clientcmd.ConfigOverrides{}
	if opts.Context != "" {
		overrides.CurrentContext = opts.Context
	}
	if opts.Namespace != "" {
		overrides.Context.Namespace = opts.Namespace
	}

	clientCfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, overrides)

	raw, err := clientCfg.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	// Resolve the effective context name. When the user did not supply
	// --context we fall back to current-context in the file; fail fast
	// if neither is set.
	ctxName := opts.Context
	if ctxName == "" {
		ctxName = raw.CurrentContext
	}
	if ctxName == "" {
		return nil, errors.New(
			"kubeconfig has no current-context set and none was supplied via --context",
		)
	}
	if _, ok := raw.Contexts[ctxName]; !ok {
		return nil, fmt.Errorf("kubeconfig context %q not found", ctxName)
	}

	return &Resolver{
		opts:        opts,
		raw:         raw,
		clientCfg:   clientCfg,
		resolvedCtx: ctxName,
	}, nil
}

// Target returns a display-oriented snapshot of where ww is about to act.
func (r *Resolver) Target() *Target {
	ctx := r.raw.Contexts[r.resolvedCtx]
	// NewResolver verifies the context key exists in raw.Contexts but not
	// that the value itself is non-nil. A kubeconfig with a nil context
	// entry is malformed but should not panic Target().
	if ctx == nil {
		return &Target{
			Context:   r.resolvedCtx,
			Namespace: "default",
		}
	}
	cluster := r.raw.Clusters[ctx.Cluster]

	ns := r.opts.Namespace
	if ns == "" {
		ns = ctx.Namespace
	}
	if ns == "" {
		ns = "default"
	}

	server := ""
	if cluster != nil {
		server = cluster.Server
	}

	return &Target{
		Context:   r.resolvedCtx,
		Cluster:   ctx.Cluster,
		Server:    server,
		User:      ctx.AuthInfo,
		Namespace: ns,
	}
}

// REST returns a ready-to-use *rest.Config built from the resolved
// kubeconfig + context. Callers pass this to kubernetes.NewForConfig.
func (r *Resolver) REST() (*rest.Config, error) {
	cfg, err := r.clientCfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build REST config: %w", err)
	}
	return cfg, nil
}

// ConfigFlags returns cli-runtime ConfigFlags pre-populated from the
// resolved options. Useful when a downstream library (future Helm SDK
// integration, kubectl-style factories) expects the standard
// genericclioptions plumbing.
func (r *Resolver) ConfigFlags() *genericclioptions.ConfigFlags {
	f := genericclioptions.NewConfigFlags(true)
	if r.opts.KubeconfigPath != "" {
		p := r.opts.KubeconfigPath
		f.KubeConfig = &p
	}
	ctx := r.resolvedCtx
	f.Context = &ctx
	if r.opts.Namespace != "" {
		ns := r.opts.Namespace
		f.Namespace = &ns
	}
	return f
}
