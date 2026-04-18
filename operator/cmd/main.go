/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
	"github.com/nyx-ai/nyx-operator/internal/controller"
	"github.com/nyx-ai/nyx-operator/internal/tracing"
	webhookv1alpha1 "github.com/nyx-ai/nyx-operator/internal/webhook/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(nyxv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var watchNamespacesRaw string
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	// Default leader-elect=true (#752). An operator Deployment running
	// replicas>1 without leader election produces two active reconcilers
	// racing on the same CRs, yielding double-writes, OwnerReferences
	// flapping, and duplicate Kubernetes Events. Users who explicitly
	// want single-replica local runs can still pass --leader-elect=false.
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager. "+
			"Default is true to make replicas>1 deployments safe out of the box (#752); "+
			"pass --leader-elect=false for single-replica local runs.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&watchNamespacesRaw, "watch-namespaces", "",
		"Comma-separated list of namespaces to watch. When empty, the operator "+
			"watches all namespaces (requires cluster-scoped RBAC). Set this when "+
			"running with per-namespace Role/RoleBinding RBAC (#532) so "+
			"controller-runtime restricts its cache to the same namespaces.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Initialise OpenTelemetry tracing (#471 part B). No-op when
	// OTEL_ENABLED is unset/false. Shutdown is deferred so a clean exit
	// flushes the in-flight batch span exporter; the timeout matches
	// the standard envoy-style 5s drain budget.
	otelShutdown, err := tracing.InitOTel(context.Background())
	if err != nil {
		setupLog.Error(err, "OTel init failed — continuing without tracing")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(shutdownCtx); err != nil {
			setupLog.Error(err, "OTel shutdown returned an error")
		}
	}()

	// Surface uninjected DefaultImageTag at boot so misconfigured release
	// builds are loud rather than silently pulling whatever "latest" resolves
	// to on each pod restart (#440). Local development with `go run` or
	// `go build` (no ldflags) intentionally trips this warning.
	if controller.DefaultImageTag == controller.DefaultImageTagSentinel {
		setupLog.Info(
			"WARNING: DefaultImageTag was not injected at build time — "+
				"NyxAgent specs that omit spec.image.tag (and per-backend image.tag) "+
				"will fail to render valid image references; "+
				"set explicit image tags per NyxAgent or rebuild with "+
				"-ldflags \"-X github.com/nyx-ai/nyx-operator/internal/controller.DefaultImageTag=<version>\"",
			"defaultImageTag", controller.DefaultImageTag,
		)
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create watchers for metrics and webhooks certificates
	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		var err error
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(webhookCertPath, webhookCertName),
			filepath.Join(webhookCertPath, webhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize webhook certificate watcher")
			os.Exit(1)
		}

		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	// When --watch-namespaces is set, restrict the manager's cache to the
	// listed namespaces so controller-runtime does not attempt cluster-wide
	// LIST/WATCH calls the namespaced RBAC does not permit (#532).
	cacheOpts := cache.Options{}
	if strings.TrimSpace(watchNamespacesRaw) != "" {
		nsSet := map[string]cache.Config{}
		for _, ns := range strings.Split(watchNamespacesRaw, ",") {
			ns = strings.TrimSpace(ns)
			if ns == "" {
				continue
			}
			nsSet[ns] = cache.Config{}
		}
		if len(nsSet) > 0 {
			cacheOpts.DefaultNamespaces = nsSet
			setupLog.Info("restricting controller cache to namespaces",
				"namespaces", watchNamespacesRaw)
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		Cache:                  cacheOpts,
		LeaderElection:   enableLeaderElection,
		LeaderElectionID: "2658b259.nyx.ai",
		// LeaderElectionReleaseOnCancel=true (#752): the binary exits
		// immediately after mgr.Start returns (no external cleanup runs
		// past that point), so it is safe to release the lease on
		// cancel. Dropping the lease voluntarily lets a second replica
		// promote in ~LeaseDuration/2 instead of the full LeaseDuration
		// a crashed leader would take, which reduces the duplicate-
		// reconcile blast window during a rolling restart.
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Field indexers for the primary reconciler hot paths (#753). Register
	// them *before* the reconciler is created so the first reconcile sees a
	// populated index. Each indexer failure aborts startup — silently
	// falling back to the legacy full-List path would mask the perf
	// regression in production.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&nyxv1alpha1.NyxPrompt{},
		controller.NyxPromptAgentRefIndex,
		controller.NyxPromptAgentRefExtractor,
	); err != nil {
		setupLog.Error(err, "unable to register field indexer", "field", controller.NyxPromptAgentRefIndex)
		os.Exit(1)
	}
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&nyxv1alpha1.NyxAgent{},
		controller.NyxAgentTeamIndex,
		controller.NyxAgentTeamExtractor,
	); err != nil {
		setupLog.Error(err, "unable to register field indexer", "field", controller.NyxAgentTeamIndex)
		os.Exit(1)
	}

	if err := (&controller.NyxAgentReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("nyxagent-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NyxAgent")
		os.Exit(1)
	}

	if err := (&controller.NyxPromptReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("nyxprompt-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NyxPrompt")
		os.Exit(1)
	}

	// Admission webhook (#624). Registered only when a cert path is supplied
	// so non-webhook installs (local dev, clusters without cert-manager)
	// still boot cleanly without failing Complete() on the missing TLS pair.
	if len(webhookCertPath) > 0 {
		if err := webhookv1alpha1.SetupNyxAgentWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "NyxAgent")
			os.Exit(1)
		}
		// Field indexer for heartbeat NyxPrompt singleton validation
		// (#755). Registered before the webhook setup so the validator's
		// fast-path ``MatchingFields`` lookup works on the first
		// admission request instead of silently falling back to the
		// full-namespace List.
		if err := mgr.GetFieldIndexer().IndexField(
			context.Background(),
			&nyxv1alpha1.NyxPrompt{},
			webhookv1alpha1.NyxPromptHeartbeatAgentIndex,
			webhookv1alpha1.NyxPromptHeartbeatAgentExtractor,
		); err != nil {
			setupLog.Error(err, "unable to register field indexer", "field", webhookv1alpha1.NyxPromptHeartbeatAgentIndex)
			os.Exit(1)
		}
		if err := webhookv1alpha1.SetupNyxPromptWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "NyxPrompt")
			os.Exit(1)
		}
	} else {
		setupLog.Info("webhook disabled: --webhook-cert-path is empty (install cert-manager and enable webhooks.certManager in the chart to turn on admission validation)")
	}
	// +kubebuilder:scaffold:builder

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

	if webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			setupLog.Error(err, "unable to add webhook certificate watcher to manager")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// When webhooks are enabled, gate readyz on the cert pair actually
	// being on disk (#750). Chart rollouts that land
	// Mutating/ValidatingWebhookConfiguration with failurePolicy=Fail
	// before the operator pod has a mounted cert would otherwise cause
	// cluster-wide NyxAgent CRUD rejections: the Service endpoint is
	// healthy (readyz returns 200 via healthz.Ping) so the apiserver
	// routes admission calls to the pod, which then TLS-errors because
	// the serving cert is missing. Holding readyz at 503 until both
	// tls.crt and tls.key exist keeps the Service endpoint out of the
	// kube-proxy rotation until the webhook server can actually answer.
	if len(webhookCertPath) > 0 {
		certFile := filepath.Join(webhookCertPath, webhookCertName)
		keyFile := filepath.Join(webhookCertPath, webhookCertKey)
		if err := mgr.AddReadyzCheck("webhook-cert", func(_ *http.Request) error {
			if _, err := os.Stat(certFile); err != nil {
				return err
			}
			if _, err := os.Stat(keyFile); err != nil {
				return err
			}
			return nil
		}); err != nil {
			setupLog.Error(err, "unable to set up webhook cert ready check")
			os.Exit(1)
		}
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
