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

package controller

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
	"github.com/witwave-ai/witwave-operator/internal/tracing"
)

// DefaultImageTag is used when an ImageSpec omits Tag. The release pipeline
// overrides this at link time via:
//
//	-ldflags "-X github.com/witwave-ai/witwave-operator/internal/controller.DefaultImageTag=<version>"
//
// The "unset" sentinel makes uninjected builds detectable so cmd/main.go can
// warn at startup; users can always pin tags explicitly per-WitwaveAgent (#440).
var DefaultImageTag = "unset"

// DefaultImageTagSentinel is the value that indicates the build did not
// inject a real version via ldflags.
const DefaultImageTagSentinel = "unset"

// witwaveAgentFinalizer guarantees the operator observes WitwaveAgent deletion so
// per-CR metric series and owned cluster resources are cleaned up even when
// the operator was offline at delete time (#569). Future per-CR metrics or
// external-state teardown should piggyback on this single finalizer rather
// than adding per-concern finalizers.
//
// #1373 KNOWN RESIDUAL RISK (SSA vs MergeFrom on metadata):
// The finalizer add/remove + teardown-complete annotation write go
// through client.Patch(ctx, agent, client.MergeFrom(before)), while
// the rest of the apply chain uses SSA with WitwaveOperatorFieldManager.
// Under high GitOps churn (spec update every few seconds + user
// delete) the metadata Patch can race SSA Apply, producing
// "Forbidden: finalizer added to terminating object" on rare windows.
// Full mitigation is to SSA-patch the metadata too under a dedicated
// FieldManager name — tracked as follow-up. Current behaviour:
// observable via controller-runtime retries; does not leak state but
// does add noise in reconcile-error rate.
const witwaveAgentFinalizer = "witwaveagent.witwave.ai/finalizer"

// WitwaveOperatorFieldManager is the FieldManager name the operator uses for
// Server-Side Apply writes (#751). Isolating the field-owner in one place
// means external field managers (HPA, VPA, GitOps) can coexist with the
// operator without per-reconcile write thrash: SSA only updates the fields
// the operator actually owns, leaving others untouched on the apiserver.
const WitwaveOperatorFieldManager = "witwave-operator"

// WitwaveAgentReconciler reconciles a WitwaveAgent object.
type WitwaveAgentReconciler struct {
	client.Client
	// APIReader is a cache-bypassing reader wired from mgr.GetAPIReader()
	// (#900). The manifest reconciler uses it to List team members with
	// read-your-writes consistency — the default cached Client can miss a
	// just-created peer during rapid create bursts, dropping its ownerRef
	// on the very next manifest Update. Optional: unit tests that skip
	// the manager bootstrap leave this nil and fall back to the cached
	// Client path.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
}

// +kubebuilder:rbac:groups=witwave.ai,resources=witwaveagents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=witwave.ai,resources=witwaveagents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=witwave.ai,resources=witwaveagents/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services;configmaps;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// Secret verbs (#749, #761): controller-gen union-merges multi-line
// markers back to one rule, so the split of read vs write verbs lives
// in the chart (see charts/witwave-operator/templates/clusterrole.yaml and
// role.yaml, gated by rbac.secretsWrite). The marker below documents
// the *full* set the operator needs when write is enabled; operators
// running with inline-credentials disabled can drop the write half via
// the chart value and the controller-runtime client will only use
// get/list/watch on the reconcile path.
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies;ingresses,verbs=get;list;watch;create;update;patch;delete

// Reconcile is the control loop's entry point. It brings owned resources into
// alignment with the WitwaveAgent spec and writes status.
func (r *WitwaveAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Capture reconcile start wall-clock for the ReconcileHistory ring
	// (#1112). Using time.Now() here rather than the OTel span's start
	// keeps the duration readable in `kubectl describe` regardless of
	// whether tracing is enabled.
	reconcileStart := time.Now()

	// OTel server span around the full reconcile (#471 part B). When OTel
	// isn't enabled the tracer is a no-op so the overhead is a single
	// branch + interface dispatch — safe to leave on always.
	ctx, span := tracing.Tracer().Start(ctx, "witwaveagent.reconcile",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("witwave.namespace", req.Namespace),
			attribute.String("witwave.name", req.Name),
		),
	)
	defer span.End()

	log := logf.FromContext(ctx)

	agent := &witwavev1alpha1.WitwaveAgent{}
	if err := r.Get(ctx, req.NamespacedName, agent); err != nil {
		if apierrors.IsNotFound(err) {
			// Belt-and-suspenders: with the finalizer (#569) the
			// delete-branch below is the primary cleanup path, but
			// this NotFound branch still runs if a CR is ever
			// orphaned (e.g. operator upgrade where a user removed
			// the finalizer externally) and drops the gauge series
			// so it doesn't linger until process restart (#471).
			witwaveagentDashboardEnabled.DeleteLabelValues(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Reconcile-pause short-circuit (#1113). Distinct from spec.enabled=false
	// (which tears the Deployment and Service down), the pause annotation
	// freezes reconciliation in place: running pods stay up, the controller
	// stops writing, and operators can investigate / roll back via other
	// tooling without racing against the operator reapplying the spec.
	// We deliberately still service the deletion path (DeletionTimestamp
	// set) so a ``kubectl delete`` of a paused agent still runs the
	// finalizer — otherwise a paused agent could not be removed without
	// manually clearing the annotation first.
	const reconcilePausedAnnotation = "witwave.ai/reconcile-paused"
	if agent.DeletionTimestamp.IsZero() {
		if v := agent.Annotations[reconcilePausedAnnotation]; v == "true" {
			log.V(1).Info("WitwaveAgent reconciliation paused via annotation — skipping",
				"annotation", reconcilePausedAnnotation, "name", agent.Name)
			return ctrl.Result{}, nil
		}
	}

	// Finalizer lifecycle (#569). Two cases:
	//
	//   1. CR is being deleted (DeletionTimestamp set): run teardown so
	//      the dashboard metric series, owned ConfigMaps, PVCs, and
	//      other resources are cleaned up explicitly — then drop the
	//      finalizer so the apiserver can remove the object. Without
	//      this, a delete that happens while the operator is down
	//      relies on OwnerReferences GC alone, which leaves the
	//      per-CR gauge series stuck at its last value until the
	//      operator process restarts.
	//
	//   2. CR is live: ensure the finalizer is attached before any
	//      further work so a subsequent delete is guaranteed to be
	//      observed. Requeue immediately after the patch lands — the
	//      update triggers its own reconcile anyway, but returning
	//      here keeps the control flow linear.
	// Finalizer / teardown-annotation mutations use client.Patch with a
	// MergeFrom base rather than r.Update (#1068). The full-object PUT
	// path clobbers concurrent spec writers (GitOps sync, kubectl apply,
	// admission-webhook defaults) between the Get at the top of
	// Reconcile and the write here; a strategic/merge patch over just
	// the metadata.finalizers list (or metadata.annotations key) leaves
	// spec / status / labels untouched on the apiserver.
	if !agent.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(agent, witwaveAgentFinalizer) {
			if err := r.finalizeWitwaveAgent(ctx, agent); err != nil {
				return ctrl.Result{}, fmt.Errorf("finalize WitwaveAgent: %w", err)
			}
			before := agent.DeepCopy()
			controllerutil.RemoveFinalizer(agent, witwaveAgentFinalizer)
			if err := r.Patch(ctx, agent, client.MergeFrom(before)); err != nil {
				return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(agent, witwaveAgentFinalizer) {
		before := agent.DeepCopy()
		if controllerutil.AddFinalizer(agent, witwaveAgentFinalizer) {
			if err := r.Patch(ctx, agent, client.MergeFrom(before)); err != nil {
				return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}
	}

	// Per-agent enabled flag (default true). When explicitly false, tear
	// down owned resources and skip reconciliation entirely. This mirrors
	// the chart's per-agent toggle (#chart beta.32) and lets operators
	// pause an agent without deleting the CR.
	//
	// Teardown is idempotent but every cache resync (default 10h, but
	// configurable and often much shorter) re-ran every step — spinning
	// reconciles, wasted List calls, and transient errors attributed to
	// a disabled agent (#903). Stamp a teardown-complete annotation on
	// success so subsequent reconciles short-circuit with no apiserver
	// calls. Re-enabling the agent removes the annotation via the
	// spec.enabled branch below (Generation changes clear state anyway,
	// and the annotation is cleared explicitly on the enabled path).
	const teardownAnnotation = "witwave.ai/teardown-complete-generation"
	if agent.Spec.Enabled != nil && !*agent.Spec.Enabled {
		if v := agent.Annotations[teardownAnnotation]; v == fmt.Sprintf("%d", agent.Generation) {
			// Teardown already ran for this spec generation — nothing new
			// to do. Controller-runtime's watch predicates ensure we'll
			// be woken on the next spec change.
			return ctrl.Result{}, nil
		}
		log.Info("WitwaveAgent disabled — tearing down owned resources", "name", agent.Name)
		if err := r.teardownDisabledAgent(ctx, agent); err != nil {
			// Return the error alone so controller-runtime's rate
			// limiter applies exponential backoff rather than our
			// defeating it with a fixed 30s interval (#548).
			return ctrl.Result{}, err
		}
		// Stamp the generation on success so we short-circuit next time.
		// Patch (#1068) over the annotation-map delta so a concurrent
		// spec writer's fields survive this metadata-only update.
		before := agent.DeepCopy()
		if agent.Annotations == nil {
			agent.Annotations = map[string]string{}
		}
		agent.Annotations[teardownAnnotation] = fmt.Sprintf("%d", agent.Generation)
		if err := r.Patch(ctx, agent, client.MergeFrom(before)); err != nil {
			// Non-fatal — the next reconcile will retry the stamp.
			log.Info("teardown complete but annotation stamp failed; will retry",
				"name", agent.Name, "err", err.Error())
		}
		return ctrl.Result{}, nil
	}

	// Re-enabled path: clear a prior teardown-complete annotation so a
	// future disable picks up the then-current generation fresh. Patch
	// (#1068) over the annotation-map delta; a full-object Update here
	// would clobber concurrent spec edits.
	//
	// #1017: the Patch here can race with a concurrent delete — either
	// the Patch succeeds on an object that just gained DeletionTimestamp
	// (Kubernetes allows metadata patches during termination) or it
	// fails with a conflict/not-found. Before falling through to the
	// apply chain, re-Get via APIReader so a DeletionTimestamp set
	// between the original Get at the top of Reconcile and here is
	// visible. If the CR is terminating we let the delete branch handle
	// it on the next reconcile rather than running the apply chain and
	// producing "Forbidden: being deleted" errors from Create calls with
	// owner refs pointed at a terminating parent.
	if v, ok := agent.Annotations[teardownAnnotation]; ok && v != "" {
		before := agent.DeepCopy()
		delete(agent.Annotations, teardownAnnotation)
		if err := r.Patch(ctx, agent, client.MergeFrom(before)); err != nil {
			log.Info("failed to clear teardown-complete annotation on re-enable; non-fatal",
				"name", agent.Name, "err", err.Error())
		}
	}
	// Re-check DeletionTimestamp after any metadata Patch on the
	// re-enable path so a concurrent delete that landed during
	// reconcile is honoured before the apply chain runs (#1017).
	// APIReader is preferred because it bypasses the informer cache,
	// but some unit tests instantiate the reconciler without wiring
	// APIReader — fall back to the cached client Get in that case so
	// tests don't nil-deref (#1168, same guard as line 859).
	{
		fresh := &witwavev1alpha1.WitwaveAgent{}
		var gErr error
		if r.APIReader != nil {
			gErr = r.APIReader.Get(ctx, req.NamespacedName, fresh)
		} else {
			gErr = r.Get(ctx, req.NamespacedName, fresh)
		}
		if gErr != nil {
			if apierrors.IsNotFound(gErr) {
				// CR was deleted out from under us — nothing to do.
				return ctrl.Result{}, nil
			}
			// Transient read error — surface for rate-limited retry
			// rather than racing into the apply chain.
			return ctrl.Result{}, fmt.Errorf("re-check DeletionTimestamp: %w", gErr)
		}
		if !fresh.DeletionTimestamp.IsZero() {
			log.Info("WitwaveAgent entered deletion during reconcile; deferring apply chain to delete branch",
				"name", agent.Name)
			return ctrl.Result{}, nil
		}
	}

	// Apply all desired resources, joining any errors so status and logs
	// surface every failure rather than only the first (#497).
	var reconcileErrs []error

	// Credentials Secrets (#witwave.resolveCredentials parity). Runs FIRST
	// so the inline-mode path's freshly-created Secret exists by the
	// time the kubelet resolves envFrom references for the pod spec
	// applyDeployment is about to write.
	if err := r.reconcileCredentialsSecrets(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	// #1219: when autoscaling has flipped off, delete the existing HPA
	// BEFORE the Deployment SSA so ownership of spec.replicas transfers
	// back to the operator cleanly. Otherwise the SSA would reclaim
	// spec.replicas while the HPA is still alive, scaling pods down to
	// the default desiredReplicas before the later reconcileHPA step
	// removes the HPA. Ordering the delete-if-present first avoids the
	// transient under-provisioning window.
	if err := r.preflightDeleteHPAIfDisabled(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	if err := r.applyDeployment(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	if err := r.applyService(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}

	// Optional resources. Failure to apply an optional does not block the
	// whole reconcile — it is captured into the error chain.
	if err := r.reconcileConfigMaps(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	if err := r.reconcileManifestConfigMap(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	if err := r.applyBackendPVCs(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	// Shared-storage PVC (#481) runs after backend PVC reconciliation so
	// it can create or delete the distinct `component=shared-storage`
	// claim independently. hostPath mode (#611) is a pod-spec concern
	// and does not produce a PVC — the reconciler still runs so any
	// previously-created operator-managed PVC gets cleaned up on a flip
	// from pvc→hostPath.
	if err := r.reconcileSharedStoragePVC(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	if err := r.reconcileHPA(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	if err := r.reconcilePDB(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	// Dashboard is opt-in per agent (#470). reconcileDashboard handles
	// both the create/update path when enabled and the delete path when
	// the field is removed or toggled off, so the cluster converges
	// cleanly in either direction.
	if err := r.reconcileDashboard(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	// ServiceMonitor is opt-in per agent (#476). Reconciliation no-ops
	// when the monitoring.coreos.com CRD is absent so the operator
	// runs safely on clusters without the Prometheus Operator.
	if err := r.reconcileServiceMonitor(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	// PodMonitor is opt-in per agent (#582). Same gating as ServiceMonitor:
	// spec.podMonitor.enabled + spec.metrics.enabled + CRD present.
	if err := r.reconcilePodMonitor(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	// MCP tools (#830). Scaffold: render enabled tool Deployment + Service
	// pairs so operator-only installs have the mcp-kubernetes / mcp-helm
	// endpoints the backends' mcp.json entries point at. Full chart-values
	// parity (RBAC, resources, scheduling) is follow-up work.
	if err := r.reconcileMCPTools(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	// Dashboard Ingress + auth guard (#831). Scaffold: the fail-closed
	// auth gate is wired end-to-end here; full Ingress + Secret rendering
	// is a follow-up so the CRD schema can settle before the controller
	// grows per-ingress-class adapters.
	if err := r.reconcileDashboardIngress(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	// NetworkPolicy (#971). Scaffold: renders a per-agent NetworkPolicy
	// when spec.networkPolicy.enabled=true — MCP-tool NetworkPolicies and
	// explicit egress rules are follow-up work.
	if err := r.reconcileNetworkPolicy(ctx, agent); err != nil {
		reconcileErrs = append(reconcileErrs, err)
	}
	reconcileErr := errors.Join(reconcileErrs...)

	// Observe Deployment status and update our own status subresource.
	if err := r.updateStatus(ctx, agent, reconcileErr, reconcileStart); err != nil {
		log.Error(err, "failed to update WitwaveAgent status")
		// Don't mask the primary reconcile error.
		if reconcileErr == nil {
			reconcileErr = err
		}
	}

	// Stamp the resulting phase onto the span so traces show outcome at a
	// glance, plus mark errors so collectors flag them red.
	span.SetAttributes(attribute.String("witwave.phase", string(agent.Status.Phase)))
	if reconcileErr != nil {
		span.RecordError(reconcileErr)
		span.SetStatus(codes.Error, reconcileErr.Error())
		// Let controller-runtime's rate limiter drive retry spacing via
		// exponential backoff (5ms → 1000s). A hardcoded 30s interval
		// defeats that and can thundering-herd an unhealthy apiserver
		// when many WitwaveAgents fail in lockstep (#548).
		return ctrl.Result{}, reconcileErr
	}

	// The Deployment watch registered via Owns(&appsv1.Deployment{})
	// delivers the real Ready transition, so no primary polling loop is
	// needed. Keep a long safety-net requeue as a floor in case the
	// informer ever misses an event (cache resync, informer reset) so
	// the agent doesn't hang in a non-Ready phase forever (#548).
	if agent.Status.Phase != witwavev1alpha1.WitwaveAgentPhaseReady {
		return ctrl.Result{RequeueAfter: 10 * time.Minute}, nil
	}
	return ctrl.Result{}, nil
}

// ── Apply helpers ─────────────────────────────────────────────────────────────

// startStepSpan opens a child span for a reconcile sub-step (#629) and
// returns the (ctx, span, finish) triple. `finish` MUST be deferred by
// the caller with the final error so errors are recorded on the
// specific sub-span (not only the joined top-level error). The
// tracer returned by tracing.Tracer() is a no-op when OTel is
// disabled, so this is free in that mode.
func startStepSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span, func(*error)) {
	var span trace.Span
	ctx, span = tracing.Tracer().Start(ctx, name, trace.WithAttributes(attrs...))
	finish := func(errPtr *error) {
		if errPtr != nil && *errPtr != nil {
			span.RecordError(*errPtr)
			span.SetStatus(codes.Error, (*errPtr).Error())
		}
		span.End()
	}
	return ctx, span, finish
}

func (r *WitwaveAgentReconciler) applyDeployment(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "witwaveagent.applyDeployment",
		attribute.Int("witwave.backends.count", len(agent.Spec.Backends)),
	)
	defer finish(&err)

	prompts, err := r.listWitwavePromptsForAgent(ctx, agent)
	if err != nil {
		return fmt.Errorf("list WitwavePrompts: %w", err)
	}
	// #1222: refuse to render a pod whose harness + backend containers
	// would collide on a computed metrics port. Failing fast here is
	// safer than letting the Deployment land and crash-loop on bind.
	// TODO(#1222): tighten appPort range in the validating webhook so
	// this guard becomes a defence-in-depth rather than a front-line check.
	clampedContainers, clampErr := metricsPortClampStatus(agent)
	if clampErr != nil {
		return fmt.Errorf("metrics port validation: %w", clampErr)
	}
	// #1250: single-container clamp is not a collision but is still a
	// misconfiguration (silent clamp from appPort+1000 to 65535 means
	// the operator picked a port the user did not ask for). Surface a
	// Warning Event so the misconfig is visible in `kubectl describe`.
	if len(clampedContainers) > 0 && r.Recorder != nil {
		r.Recorder.Eventf(agent, corev1.EventTypeWarning, "MetricsPortClamped",
			"metrics port for containers %v was clamped to 65535 because appPort > 64535; set spec.metricsPort explicitly or lower the app ports",
			clampedContainers,
		)
	}
	desired := buildDeployment(agent, DefaultImageTag, prompts)
	if err = controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on Deployment: %w", err)
	}

	// Credential-Secret checksum (#1114). Stamp a hash of the referenced
	// Secrets' ResourceVersions onto the pod template so a rotated token
	// triggers a rolling restart. Errors surface through the reconcile
	// chain so a transient apiserver blip doesn't silently leave the
	// agent on the old token.
	checksum, ccErr := r.computeCredentialsChecksum(ctx, agent)
	if ccErr != nil {
		return ccErr
	}
	if checksum != "" {
		if desired.Spec.Template.ObjectMeta.Annotations == nil {
			desired.Spec.Template.ObjectMeta.Annotations = map[string]string{}
		}
		desired.Spec.Template.ObjectMeta.Annotations[credentialsChecksumAnnotation] = checksum
	}

	// Detect a credential rotation by looking at the existing Deployment's
	// previously-stamped checksum. Also honour the HPA's authority over
	// spec.replicas (#486): when autoscaling is on, leave replicas unset in
	// the desired object so SSA does not claim ownership of that field.
	existing := &appsv1.Deployment{}
	getErr := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(getErr):
		// First-install seed: when autoscaling is enabled, buildDeployment
		// leaves Replicas=nil so the HPA can own the field after the
		// initial apply. But reconcileHPA runs later in the reconcile
		// chain — between this create and HPA-create, Kubernetes fills
		// Replicas with its default of 1, producing a transient
		// under-provisioning window relative to the user's declared
		// autoscaling floor. Seed the initial Replicas to the declared
		// minReplicas so the pod count starts at the floor; the very
		// next reconcile (after HPA exists) takes the update branch below
		// and drops Replicas=nil again so SSA relinquishes ownership to
		// the HPA for all subsequent scaling.
		if agent.Spec.Autoscaling != nil && agent.Spec.Autoscaling.Enabled {
			minR := agent.Spec.Autoscaling.MinReplicas
			if minR < 1 {
				minR = 1
			}
			desired.Spec.Replicas = int32Ptr(minR)
		}
		span.SetAttributes(attribute.String("witwave.resource.action", "create"))
	case getErr != nil:
		err = getErr
		return err
	default:
		span.SetAttributes(attribute.String("witwave.resource.action", "apply"))
		if existing.Spec.Template.Annotations != nil {
			prev := existing.Spec.Template.Annotations[credentialsChecksumAnnotation]
			if prev != "" && prev != checksum {
				WitwaveAgentCredentialRotationsTotal.WithLabelValues(agent.Namespace, agent.Name).Inc()
			}
		}
		if agent.Spec.Autoscaling != nil && agent.Spec.Autoscaling.Enabled {
			// Drop replicas from the desired object so SSA relinquishes
			// ownership of the field to the HPA — otherwise every reconcile
			// would reassert replicas=<nil|default> and fight the HPA.
			desired.Spec.Replicas = nil
		}
	}

	// Server-Side Apply (#751). Tagging every operator-managed write with
	// FieldManager="witwave-operator" lets apiserver track field ownership so
	// external managers (HPA for replicas, GitOps for labels, a human
	// running ``kubectl edit``) can coexist without per-reconcile write
	// thrash. ForceOwnership claims fields we consider operator-owned on
	// upgrades that previously used bare Update.
	if err = applySSA(ctx, r.Client, desired); err != nil {
		return err
	}
	return nil
}

// applySSA issues a Server-Side Apply patch against “obj“ using the
// operator's FieldManager. The caller must set GVK on the object (SSA
// requires TypeMeta); the helper strips ResourceVersion and ManagedFields
// which would otherwise conflict with Apply semantics.
func applySSA(ctx context.Context, c client.Client, obj client.Object) error {
	// SSA requires TypeMeta populated; the object's GVK must resolve
	// through the scheme. Clear server-managed bookkeeping so we don't
	// trip apiserver conflict checks.
	obj.SetResourceVersion("")
	obj.SetManagedFields(nil)
	if obj.GetObjectKind().GroupVersionKind().Empty() {
		// Best-effort: look up the object's kind on the scheme when the
		// caller forgot to set TypeMeta. Falls through to the Patch call
		// which will surface a clear error if GVK is still missing.
		gvks, _, err := c.Scheme().ObjectKinds(obj)
		if err == nil && len(gvks) > 0 {
			obj.GetObjectKind().SetGroupVersionKind(gvks[0])
		}
	}
	return c.Patch(ctx, obj, client.Apply,
		client.ForceOwnership,
		client.FieldOwner(WitwaveOperatorFieldManager),
	)
}

// WitwavePromptAgentRefIndex is the field-indexer key that maps every
// WitwavePrompt to its spec.agentRefs[].name values (#753). Indexing here
// lets “listWitwavePromptsForAgent“ issue a single scoped List instead of
// the full-namespace List + in-memory O(N*R) filter it used to run on
// every reconcile.
const WitwavePromptAgentRefIndex = "spec.agentRefs.name"

// WitwavePromptAgentRefExtractor returns the agent-ref names of one
// WitwavePrompt. Empty ref names are dropped so missing fields don't
// pollute the index.
func WitwavePromptAgentRefExtractor(obj client.Object) []string {
	p, ok := obj.(*witwavev1alpha1.WitwavePrompt)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(p.Spec.AgentRefs))
	for _, ref := range p.Spec.AgentRefs {
		if ref.Name != "" {
			out = append(out, ref.Name)
		}
	}
	return out
}

// IsFieldIndexMissing returns true for the specific error controller-
// runtime / client-go return when a List requests a MatchingFields
// value for an index that isn't registered — i.e. the operator never
// called IndexField (common in unit tests that skip manager bootstrap).
//
// Without this precise check the fallback branch at the call sites
// swallowed EVERY List error — context cancellations, RBAC denials,
// apiserver 500s — and silently degraded to a full-namespace List on
// each reconcile (#901). Real errors must propagate so they are
// logged/retried; only index-missing is legitimately recoverable with
// the fallback path.
// fieldIndexMissingRe matches the upstream error formats produced when
// a scoped List references an index that isn't registered. Known
// variants across controller-runtime + client-go + envtest/apiserver
// versions (#1014, #1179):
//
//	controller-runtime cache_reader: `index with name %s does not exist`
//	client-go thread_safe_store:     `Index with name %s does not exist`
//	                                 `indexer "%s" does not exist`
//	newer upstream phrasing:         `index with name %s is not registered`
//	                                 `indexer "%s" not registered`
//	envtest apiserver (no index):    `field label not supported: %s`
//
// The previous substring-pair check (`"index"` AND `"does not exist"`)
// was loose enough to classify wrapped/joined errors like
// `context deadline exceeded; index X does not exist` as recoverable.
// The anchored alternation below requires the exact upstream phrase
// shape so unrelated errors that happen to mention both words no longer
// trip the fallback path, while still covering the "not registered" and
// envtest "field label not supported" phrasings.
var fieldIndexMissingRe = regexp.MustCompile(
	`(?i)\b(?:(?:index with name \S+|indexer "\S+")\s+(?:does not exist|is not registered|not registered)|field label not supported:\s*\S+)\b`,
)

func IsFieldIndexMissing(err error) bool {
	if err == nil {
		return false
	}
	// Walk the wrap chain first so a legitimately-wrapped upstream
	// sentinel still matches even if surrounding context contains noise.
	for e := err; e != nil; e = errors.Unwrap(e) {
		if fieldIndexMissingRe.MatchString(e.Error()) {
			return true
		}
	}
	return false
}

// ownerRefsEqual reports whether two OwnerReference slices represent
// the same desired set (order-insensitive over (UID, Controller)). We
// compare (UID, Controller) rather than the whole struct because
// BlockOwnerDeletion and APIVersion drift are not signals of
// ownership drift — only UID set and the Controller flag matter for
// the GC semantics the manifest CM depends on.
func ownerRefsEqual(a, b []metav1.OwnerReference) bool {
	if len(a) != len(b) {
		return false
	}
	type key struct {
		UID        string
		Controller bool
	}
	toSet := func(in []metav1.OwnerReference) map[key]struct{} {
		out := make(map[key]struct{}, len(in))
		for _, r := range in {
			ctrl := false
			if r.Controller != nil {
				ctrl = *r.Controller
			}
			out[key{UID: string(r.UID), Controller: ctrl}] = struct{}{}
		}
		return out
	}
	aSet := toSet(a)
	bSet := toSet(b)
	for k := range aSet {
		if _, ok := bSet[k]; !ok {
			return false
		}
	}
	return true
}

// listContainsSelf reports whether the given WitwaveAgentList includes the
// agent identified by agent.UID (or agent.Name when UID is empty, e.g.
// in unit tests that don't set UIDs). Used by reconcileManifestConfigMap
// to detect the narrow informer-cache lag where the cached, scoped List
// has not yet observed this agent's own add-event and an APIReader
// escalation is required (#1066, #900).
func listContainsSelf(list *witwavev1alpha1.WitwaveAgentList, agent *witwavev1alpha1.WitwaveAgent) bool {
	if list == nil || agent == nil {
		return false
	}
	for i := range list.Items {
		a := &list.Items[i]
		if agent.UID != "" && a.UID == agent.UID {
			return true
		}
		if agent.UID == "" && a.Name == agent.Name && a.Namespace == agent.Namespace {
			return true
		}
	}
	return false
}

// WitwaveAgentTeamIndex is the field-indexer key that maps every WitwaveAgent to
// its team label value (#753). Agents without the label land under the
// empty-string key — the same grouping teamKey() uses in-memory.
const WitwaveAgentTeamIndex = "metadata.labels.witwave.ai/team"

// WitwaveAgentTeamExtractor returns the single-element team key for a
// WitwaveAgent, including the empty-string case. Returning a single value
// means the scoped List “client.MatchingFields{WitwaveAgentTeamIndex: t}“
// yields exactly the peers that share the team group.
func WitwaveAgentTeamExtractor(obj client.Object) []string {
	a, ok := obj.(*witwavev1alpha1.WitwaveAgent)
	if !ok {
		return nil
	}
	return []string{teamKey(a)}
}

// listWitwavePromptsForAgent returns every WitwavePrompt in the agent's namespace
// whose spec.agentRefs contains this agent. The result is sorted by CR
// name so buildDeployment renders pod volumes in a deterministic order.
//
// Performance (#753): uses “client.MatchingFields“ against
// “WitwavePromptAgentRefIndex“ so each call is O(k) in the number of
// prompts bound to this agent rather than O(N) in the namespace prompt
// count. Falls back to the legacy full-List path when the index is
// missing (unit tests that skip the manager bootstrap).
func (r *WitwaveAgentReconciler) listWitwavePromptsForAgent(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) ([]witwavev1alpha1.WitwavePrompt, error) {
	scoped := &witwavev1alpha1.WitwavePromptList{}
	err := r.List(ctx, scoped,
		client.InNamespace(agent.Namespace),
		client.MatchingFields{WitwavePromptAgentRefIndex: agent.Name},
	)
	if err == nil {
		return append([]witwavev1alpha1.WitwavePrompt(nil), scoped.Items...), nil
	}
	// Distinguish "index not registered" from every other List error
	// (#901 twin): fall back to the full-list path only for the
	// specific case, propagate all others so RBAC denials and context
	// cancellations aren't masked as "index missing".
	if !IsFieldIndexMissing(err) {
		return nil, err
	}
	// Index missing — fall back to the legacy full-namespace scan.
	all := &witwavev1alpha1.WitwavePromptList{}
	if lErr := r.List(ctx, all, client.InNamespace(agent.Namespace)); lErr != nil {
		return nil, lErr
	}
	matched := make([]witwavev1alpha1.WitwavePrompt, 0, len(all.Items))
	for i := range all.Items {
		p := &all.Items[i]
		for _, ref := range p.Spec.AgentRefs {
			if ref.Name == agent.Name {
				matched = append(matched, *p)
				break
			}
		}
	}
	return matched, nil
}

func (r *WitwaveAgentReconciler) applyService(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "witwaveagent.applyService")
	defer finish(&err)

	desired := buildService(agent)
	if err = controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on Service: %w", err)
	}
	// Preserve ClusterIP across updates — the apiserver rejects attempts to
	// change it. Read the live object only to carry that immutable field
	// forward; everything else is expressed via SSA (#751).
	existing := &corev1.Service{}
	getErr := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(getErr):
		span.SetAttributes(attribute.String("witwave.resource.action", "create"))
	case getErr != nil:
		err = getErr
		return err
	default:
		span.SetAttributes(attribute.String("witwave.resource.action", "apply"))
		desired.Spec.ClusterIP = existing.Spec.ClusterIP
	}
	if err = applySSA(ctx, r.Client, desired); err != nil {
		return err
	}
	return nil
}

// reconcileConfigMaps applies every ConfigMap the spec currently calls for
// AND garbage-collects ConfigMaps owned by this WitwaveAgent that the spec no
// longer asks for (#443). Replaces the previous applyAgentConfigMap +
// applyBackendConfigMaps split, which had a known TODO about stale cleanup.
func (r *WitwaveAgentReconciler) reconcileConfigMaps(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "witwaveagent.reconcileConfigMaps")
	defer finish(&err)

	// Compute the desired set first — we apply by iterating this set, and we
	// reuse it for the cleanup pass to decide what to delete.
	desired := map[string]*corev1.ConfigMap{}
	if cm := buildConfigMap(agent, agentConfigMapName(agent, ""), agent.Spec.Config); cm != nil {
		desired[cm.Name] = cm
	}
	for _, b := range agent.Spec.Backends {
		if !backendEnabled(b) {
			continue
		}
		if cm := buildConfigMap(agent, agentConfigMapName(agent, b.Name), b.Config); cm != nil {
			desired[cm.Name] = cm
		}
	}

	// Git-sync script + mappings ConfigMaps (#475). The script CM is the
	// chart's byte-identical rsync helper; mapping CMs carry per-context
	// TSV tables (one line per mapping). Reconciled via the same desired-
	// set + cleanup flow as the inline-config CMs, so a mapping removed
	// from spec is GC'd by the label-matching cleanup pass below.
	if cm := buildGitSyncScriptConfigMap(agent); cm != nil {
		desired[cm.Name] = cm
	}
	for _, cm := range buildGitMappingsConfigMaps(agent) {
		desired[cm.Name] = cm
	}
	span.SetAttributes(attribute.Int("witwave.configmaps.desired", len(desired)))

	// Apply each desired ConfigMap.
	for _, cm := range desired {
		if err := r.applyConfigMap(ctx, agent, cm); err != nil {
			return err
		}
	}

	// Cleanup: list ConfigMaps in this namespace that carry our agent labels,
	// then delete any owned by THIS WitwaveAgent that are not in the desired set.
	// Dual-check both labels and IsControlledBy before deleting to never touch
	// foreign or shared ConfigMaps.
	existing := &corev1.ConfigMapList{}
	if err := r.List(ctx, existing,
		client.InNamespace(agent.Namespace),
		client.MatchingLabels{
			labelName:      agent.Name,
			labelManagedBy: managedBy,
		},
	); err != nil {
		return fmt.Errorf("list owned ConfigMaps for cleanup: %w", err)
	}
	for i := range existing.Items {
		cm := &existing.Items[i]
		if _, wanted := desired[cm.Name]; wanted {
			continue
		}
		if !metav1.IsControlledBy(cm, agent) {
			// Defensive: another controller owns this ConfigMap despite the
			// labels matching. Leave it alone.
			continue
		}
		if err := r.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale ConfigMap %s: %w", cm.Name, err)
		}
	}
	return nil
}

// reconcileManifestConfigMap maintains the per-team manifest ConfigMap the
// harness mounts at /home/agent/manifest.json (#474). Each reconcile lists
// every WitwaveAgent that shares the current agent's team label (or every
// enabled, non-deleting WitwaveAgent in the namespace when no team label is
// set) and writes a CM enumerating the members by name + service URL.
//
// A content-hash annotation on the CM lets us short-circuit writes when
// membership is unchanged — this matters because option (a) in the
// gap-approve comment warns every CR write triggers manifest churn, and
// without the hash every pod would see a rolling configmap update on
// every reconcile.
func (r *WitwaveAgentReconciler) reconcileManifestConfigMap(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "witwaveagent.reconcileManifestConfigMap",
		attribute.String("witwave.team", teamKey(agent)),
	)
	defer finish(&err)

	// Scoped List via WitwaveAgentTeamIndex (#753): the field indexer
	// returns only WitwaveAgents that share this agent's team label
	// (including the empty-string grouping when the label is absent),
	// so a namespace of hundreds of unrelated agents no longer lands a
	// full-namespace List on every manifest reconcile.
	//
	// Cache-first (#1066): the prior #900 implementation unconditionally
	// used APIReader (direct apiserver) for every manifest reconcile,
	// scaling O(agents × reconciles) in namespace LIST QPS and
	// regressing the #753 index. We now default to the cached, scoped
	// List and escalate to APIReader only when the cached snapshot
	// omits *self* — the narrow race the #900 cache-bypass was added
	// to cover (rapid create-burst where the informer cache has not
	// yet delivered this agent's own add-event).
	wantTeam := teamKey(agent)
	allAgents := &witwavev1alpha1.WitwaveAgentList{}
	listedViaCache := false
	if err := r.List(ctx, allAgents,
		client.InNamespace(agent.Namespace),
		client.MatchingFields{WitwaveAgentTeamIndex: wantTeam},
	); err != nil {
		// Distinguish "index not registered" from every other List
		// error (#901). Previously any error fell through to the
		// full-namespace List path, silently masking RBAC denials,
		// context cancellations, and apiserver 500s as "index
		// missing" and degrading every reconcile.
		if !IsFieldIndexMissing(err) {
			return fmt.Errorf("list WitwaveAgents for manifest (scoped): %w", err)
		}
		// Index missing — full-namespace List keeps prior behaviour.
		if err := r.List(ctx, allAgents, client.InNamespace(agent.Namespace)); err != nil {
			return fmt.Errorf("list WitwaveAgents for manifest: %w", err)
		}
		listedViaCache = true
	} else {
		listedViaCache = true
	}

	// Cache-miss-self escalation (#1066): if the cached snapshot does
	// not include this agent's own UID, the informer is lagging the
	// authoritative apiserver state and writing the CM now would drop
	// self's ownerRef. Fall through to an APIReader-backed List
	// exactly once. APIReader does not support field selectors over
	// custom indices, so we List by namespace and filter by teamKey()
	// in the existing in-memory loop below.
	if listedViaCache && r.APIReader != nil && !listContainsSelf(allAgents, agent) {
		live := &witwavev1alpha1.WitwaveAgentList{}
		if err := r.APIReader.List(ctx, live, client.InNamespace(agent.Namespace)); err != nil {
			return fmt.Errorf("list WitwaveAgents for manifest (live escalation): %w", err)
		}
		allAgents = live
	}

	var members []manifestMember
	var memberAgents []*witwavev1alpha1.WitwaveAgent
	for i := range allAgents.Items {
		a := &allAgents.Items[i]
		// Skip agents being deleted and agents explicitly disabled —
		// both would produce a stale entry in the manifest that
		// resolves to a Service with no endpoints.
		if !a.DeletionTimestamp.IsZero() {
			continue
		}
		if a.Spec.Enabled != nil && !*a.Spec.Enabled {
			continue
		}
		if teamKey(a) != wantTeam {
			// Defensive — when the indexer path succeeded every entry
			// already matches; the fallback path still needs this filter.
			continue
		}
		port := a.Spec.Port
		if port == 0 {
			port = 8000
		}
		members = append(members, manifestMember{Name: a.Name, Port: port})
		memberAgents = append(memberAgents, a)
	}

	// Shared-CM ownership (#684): the manifest CM carries one
	// non-controller OwnerReference per live team member, not a single
	// controller ref on `agent`. This way K8s garbage collection only
	// removes the CM when the LAST team member is deleted — surviving
	// pods keep their mount through any intermediate membership churn.
	//
	// Upgrade path: existing clusters whose CM still carries the old
	// single controller ref get converged to the new shape on the next
	// reconcile write, because we overwrite `existing.OwnerReferences`
	// from `desired.OwnerReferences` below.
	desired, desiredHash := buildManifestConfigMap(agent, memberAgents, members)
	span.SetAttributes(attribute.Int("witwave.members.count", len(members)))

	// Empty-membership finalize (#1010): when the last team member is
	// being removed, buildManifestOwnerRefs returns zero OwnerReferences
	// and rewriting the CM would produce an orphaned object that K8s
	// garbage collection can never reclaim. Delete it explicitly so a new
	// agent joining the same team later doesn't collide with a stale
	// manifestHashAnnotation.
	if len(memberAgents) == 0 {
		existing := &corev1.ConfigMap{}
		getErr := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
		if apierrors.IsNotFound(getErr) {
			return nil
		}
		if getErr != nil {
			return getErr
		}
		if delErr := r.Delete(ctx, existing); delErr != nil && !apierrors.IsNotFound(delErr) {
			return delErr
		}
		return nil
	}

	existing := &corev1.ConfigMap{}
	err = r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(err):
		err = r.Create(ctx, desired)
		return err
	case err != nil:
		return err
	}

	// Hash short-circuit: if the already-stored CM carries the same
	// content hash AND the on-object OwnerReferences match the
	// desired shape, do nothing. The hash input already covers both
	// body and sorted owner UIDs, but a bystander (operator upgrade,
	// ArgoCD drift, manual kubectl edit) can mutate OwnerReferences
	// without touching the annotation — in that case the annotation
	// alone would falsely report "in sync" and we'd skip the write
	// the upgrade path (#684) depends on. Compare both.
	if existing.Annotations[manifestHashAnnotation] == desiredHash &&
		ownerRefsEqual(existing.OwnerReferences, desired.OwnerReferences) {
		return nil
	}
	// #1215: merge labels/annotations rather than full-overwrite so a
	// bystander (ArgoCD tracker, cost labeller, CSI snapshotter) writing
	// between our Get and Update does not get clobbered. Only the keys
	// this controller owns are (re)written; foreign keys pass through.
	existing.Labels = mergeOwnedStringMap(existing.Labels, desired.Labels, witwaveAgentOwnedLabelKeys)
	desiredAnnotations := map[string]string{}
	for k, v := range desired.Annotations {
		desiredAnnotations[k] = v
	}
	desiredAnnotations[manifestHashAnnotation] = desiredHash
	existing.Annotations = mergeOwnedStringMap(existing.Annotations, desiredAnnotations, witwaveAgentOwnedAnnotationKeys)
	// Converge ownerRefs to the desired multi-owner shape. This is the
	// critical write for migrating off the legacy single-controller
	// shape — we replace the whole slice rather than appending so any
	// stale ref (e.g. pointing at a deleted agent's UID) is dropped.
	existing.OwnerReferences = desired.OwnerReferences
	existing.Data = desired.Data
	return r.Update(ctx, existing)
}

// witwaveAgentOwnedLabelKeys enumerates the metadata.labels keys the
// WitwaveAgent controller stamps onto owned ConfigMaps (#1215, #1216).
// Only these keys are (re)written on Update; foreign labels pass
// through so bystander controllers' writes are preserved.
var witwaveAgentOwnedLabelKeys = []string{
	labelName,
	labelComponent,
	labelPartOf,
	labelManagedBy,
}

// witwaveAgentOwnedAnnotationKeys enumerates the metadata.annotations keys
// the WitwaveAgent controller writes on owned ConfigMaps. Foreign
// annotations pass through on Update (#1215, #1216).
var witwaveAgentOwnedAnnotationKeys = []string{
	manifestHashAnnotation,
}

func (r *WitwaveAgentReconciler) applyConfigMap(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent, desired *corev1.ConfigMap) error {
	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on ConfigMap %s: %w", desired.Name, err)
	}
	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return err
	}
	existing.Data = desired.Data
	// #1216: merge labels/annotations rather than full-overwrite. See
	// applyConfigMap sibling (manifest CM path) and the WitwavePrompt CM
	// path at witwaveprompt_controller.go for the same pattern.
	existing.Labels = mergeOwnedStringMap(existing.Labels, desired.Labels, witwaveAgentOwnedLabelKeys)
	existing.Annotations = mergeOwnedStringMap(existing.Annotations, desired.Annotations, witwaveAgentOwnedAnnotationKeys)
	return r.Update(ctx, existing)
}

func (r *WitwaveAgentReconciler) applyBackendPVCs(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "witwaveagent.applyBackendPVCs",
		attribute.Int("witwave.backends.count", len(agent.Spec.Backends)),
	)
	defer finish(&err)

	pvcs, buildErrs := buildBackendPVCs(agent)
	span.SetAttributes(
		attribute.Int("witwave.pvcs.desired", len(pvcs)),
		attribute.Int("witwave.pvcs.build_errors", len(buildErrs)),
	)
	// Surface size-parse failures so they don't get silently dropped (#454).
	// We log via the reconciler's logger and emit a Warning Event per bad
	// entry. This is non-fatal — other backends still get their PVCs.
	if len(buildErrs) > 0 {
		log := logf.FromContext(ctx)
		for _, be := range buildErrs {
			log.Error(be.Err, "skipping backend PVC", "backend", be.BackendName, "size", be.Size)
			witwaveagentPVCBuildErrorsTotal.WithLabelValues(be.BackendName).Inc()
			if r.Recorder != nil {
				r.Recorder.Eventf(agent, corev1.EventTypeWarning, "InvalidStorageSize",
					"backend %q: invalid storage.size %q (%v)", be.BackendName, be.Size, be.Err)
			}
		}
	}
	// Build the desired-set index up front so the cleanup pass can look up
	// names in O(1). Mirrors the reconcileConfigMaps pattern (#491).
	desired := make(map[string]*corev1.PersistentVolumeClaim, len(pvcs))
	for _, pvc := range pvcs {
		desired[pvc.Name] = pvc
	}
	for _, d := range desired {
		// Annotate the per-backend apply on the parent span as an event
		// so per-PVC outcomes are searchable in Jaeger/Tempo without the
		// cardinality cost of a child span per PVC (#629).
		span.AddEvent("pvc.apply.begin", trace.WithAttributes(attribute.String("witwave.pvc.name", d.Name)))
		if err := controllerutil.SetControllerReference(agent, d, r.Scheme); err != nil {
			return fmt.Errorf("set owner on PVC %s: %w", d.Name, err)
		}
		existing := &corev1.PersistentVolumeClaim{}
		err := r.Get(ctx, client.ObjectKeyFromObject(d), existing)
		switch {
		case apierrors.IsNotFound(err):
			if err := r.Create(ctx, d); err != nil {
				return err
			}
			span.AddEvent("pvc.created", trace.WithAttributes(attribute.String("witwave.pvc.name", d.Name)))
			continue
		case err != nil:
			return err
		}
		// #1218: guard the in-place Update behind an IsControlledBy check
		// so we never clobber a PVC whose name collides with an agent
		// backend's generated name but whose controllerRef points at a
		// different agent (or a user-created claim that matched our
		// naming). Mirrors the HPA/PDB guard; emit a Warning Event so
		// operators notice the collision.
		if !metav1.IsControlledBy(existing, agent) {
			if r.Recorder != nil {
				r.Recorder.Eventf(agent, corev1.EventTypeWarning, "BackendStorageCollision",
					"backend PVC %q exists but is not controlled by this agent — skipping update", d.Name)
			}
			span.AddEvent("pvc.skip.not-controlled", trace.WithAttributes(attribute.String("witwave.pvc.name", d.Name)))
			continue
		}
		// PVC specs are largely immutable after creation; only labels are
		// reconciled in-place. Size changes would need an expand-volume flow.
		existing.Labels = d.Labels
		if err := r.Update(ctx, existing); err != nil {
			return err
		}
		span.AddEvent("pvc.updated", trace.WithAttributes(attribute.String("witwave.pvc.name", d.Name)))
	}
	// Cleanup: list PVCs in this namespace that carry our agent labels, then
	// delete any owned by THIS WitwaveAgent that are not in the desired set.
	// This catches backends that have been disabled, removed from spec, or
	// transitioned to `existingClaim` (#491). Dual-check both labels and
	// IsControlledBy before deleting to never touch foreign or shared PVCs.
	// Distinct from #490's teardown path, which runs only when the whole
	// agent is disabled.
	existing := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, existing,
		client.InNamespace(agent.Namespace),
		client.MatchingLabels{
			labelName:      agent.Name,
			labelManagedBy: managedBy,
		},
	); err != nil {
		return fmt.Errorf("list owned PVCs for cleanup: %w", err)
	}
	for i := range existing.Items {
		pvc := &existing.Items[i]
		if _, wanted := desired[pvc.Name]; wanted {
			continue
		}
		// Skip the operator-managed shared-storage PVC — it is
		// reconciled by reconcileSharedStoragePVC (#481) and carries a
		// distinct `component=shared-storage` label. Without this guard
		// the two reconcilers would reciprocally delete each other's
		// PVCs since both match name+managed-by.
		if pvc.Labels[labelComponent] == componentSharedStorage {
			continue
		}
		if !metav1.IsControlledBy(pvc, agent) {
			// Defensive: another controller owns this PVC despite the
			// labels matching. Leave it alone.
			continue
		}
		if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale PVC %s: %w", pvc.Name, err)
		}
	}
	return nil
}

// reconcileSharedStoragePVC creates, updates, or deletes the agent-wide
// shared-storage PVC (#481). The PVC is only produced when the WitwaveAgent
// sets sharedStorage.enabled=true with storageType=pvc (default) and no
// existingClaim — mirroring the chart's `pvc.yaml` branch gated on the
// same three conditions. When any of those flip, or when storageType is
// hostPath (#611), the reconciler deletes any PVC it previously created
// (tracked by the `component=shared-storage` label). The backend PVC
// reconciler skips PVCs bearing this component label so the two paths
// never step on each other.
func (r *WitwaveAgentReconciler) reconcileSharedStoragePVC(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) error {
	desired, buildErr := buildSharedStoragePVC(agent)
	if buildErr != nil {
		// Size-parse failure — surface as event + log, reconcile as if
		// the PVC is not desired so a previously-created shared PVC is
		// left in place rather than recreated with a bad size.
		logf.FromContext(ctx).Error(buildErr, "skipping shared-storage PVC")
		if r.Recorder != nil {
			r.Recorder.Eventf(agent, corev1.EventTypeWarning, "InvalidSharedStorageSize",
				"sharedStorage: %v", buildErr)
		}
		return nil
	}

	// Delete path: no PVC desired — sweep any operator-managed shared
	// PVC labelled for this agent. Dual-check labels + IsControlledBy to
	// never touch a user-supplied claim.
	if desired == nil {
		owned := &corev1.PersistentVolumeClaimList{}
		if err := r.List(ctx, owned,
			client.InNamespace(agent.Namespace),
			client.MatchingLabels{
				labelName:      agent.Name,
				labelManagedBy: managedBy,
				labelComponent: componentSharedStorage,
			},
		); err != nil {
			return fmt.Errorf("list shared-storage PVC for cleanup: %w", err)
		}
		for i := range owned.Items {
			pvc := &owned.Items[i]
			if !metav1.IsControlledBy(pvc, agent) {
				continue
			}
			if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete stale shared-storage PVC %s: %w", pvc.Name, err)
			}
		}
		return nil
	}

	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on shared-storage PVC: %w", err)
	}
	existing := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("create shared-storage PVC: %w", err)
		}
		return nil
	case err != nil:
		return err
	}
	// #1217: guard the in-place Update behind an IsControlledBy check.
	// When the existing shared PVC is not controlled by this agent
	// (e.g. a user-created claim or one owned by a sibling agent in a
	// renamed/cloned deployment) the controller must not silently
	// mutate it. Emit a Warning Event and skip — mirrors HPA/PDB.
	if !metav1.IsControlledBy(existing, agent) {
		if r.Recorder != nil {
			r.Recorder.Eventf(agent, corev1.EventTypeWarning, "SharedStorageCollision",
				"shared-storage PVC %q exists but is not controlled by this agent — skipping update", desired.Name)
		}
		return nil
	}
	// PVC specs are largely immutable post-creation; reconcile labels
	// only so cleanup selectors stay accurate across spec edits.
	existing.Labels = desired.Labels
	if err := r.Update(ctx, existing); err != nil {
		return fmt.Errorf("update shared-storage PVC: %w", err)
	}
	return nil
}

// preflightDeleteHPAIfDisabled deletes a lingering controller-owned HPA
// when autoscaling has flipped to disabled (#1219). Running this BEFORE
// the Deployment SSA prevents the Deployment from briefly running at its
// default replica count between SSA reasserting ownership of
// spec.replicas and the subsequent reconcileHPA removing the HPA.
// Non-controller-owned HPAs are left alone (mirrors reconcileHPA).
func (r *WitwaveAgentReconciler) preflightDeleteHPAIfDisabled(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) error {
	// Only act when autoscaling is disabled in the current spec. When
	// autoscaling is enabled the regular reconcileHPA path handles
	// create/update and applyDeployment already drops replicas from the
	// SSA so the HPA keeps ownership of spec.replicas.
	if agent.Spec.Autoscaling != nil && agent.Spec.Autoscaling.Enabled {
		return nil
	}
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}
	existing := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := r.Get(ctx, key, existing); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !metav1.IsControlledBy(existing, agent) {
		return nil
	}
	if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// reconcileHPA creates, updates, or deletes the HPA to match spec.
func (r *WitwaveAgentReconciler) reconcileHPA(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "witwaveagent.reconcileHPA")
	defer finish(&err)

	desired := buildHPA(agent)
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}
	if desired == nil {
		span.SetAttributes(attribute.String("witwave.resource.action", "delete-if-present"))
	}

	if desired == nil {
		// Ensure no HPA lingers from a previous spec.
		existing := &autoscalingv2.HorizontalPodAutoscaler{}
		if err := r.Get(ctx, key, existing); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if metav1.IsControlledBy(existing, agent) {
			return r.Delete(ctx, existing)
		}
		return nil
	}

	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on HPA: %w", err)
	}
	existing := &autoscalingv2.HorizontalPodAutoscaler{}
	err = r.Get(ctx, key, existing)
	switch {
	case apierrors.IsNotFound(err):
		err = r.Create(ctx, desired)
		return err
	case err != nil:
		return err
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	err = r.Update(ctx, existing)
	return err
}

// reconcilePDB creates, updates, or deletes the PDB to match spec.
func (r *WitwaveAgentReconciler) reconcilePDB(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "witwaveagent.reconcilePDB")
	defer finish(&err)

	desired := buildPDB(agent)
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}
	if desired == nil {
		span.SetAttributes(attribute.String("witwave.resource.action", "delete-if-present"))
	}

	if desired == nil {
		existing := &policyv1.PodDisruptionBudget{}
		if err := r.Get(ctx, key, existing); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if metav1.IsControlledBy(existing, agent) {
			return r.Delete(ctx, existing)
		}
		return nil
	}

	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on PDB: %w", err)
	}
	existing := &policyv1.PodDisruptionBudget{}
	err = r.Get(ctx, key, existing)
	switch {
	case apierrors.IsNotFound(err):
		err = r.Create(ctx, desired)
		return err
	case err != nil:
		return err
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	err = r.Update(ctx, existing)
	return err
}

// teardownDisabledAgent deletes every owned resource for an agent whose
// spec.enabled has been flipped to false, and is also invoked from the
// finalizer path. The delete is gated on IsControlledBy so we never
// touch resources we didn't create. Status is left untouched — the
// next reconcile after re-enabling will rewrite it from observed
// Deployment state.
//
// Error handling (#754): every step is best-effort idempotent.  A
// transient apiserver failure on, say, the PodMonitor delete must not
// prevent the next step (the dashboard, or removing the finalizer)
// from running.  All non-nil errors are accumulated via “errors.Join“
// and returned together.  Each failure also increments
// “witwaveagent_teardown_step_errors_total{kind,reason}“ so operators can
// alert on stuck-delete patterns without grepping reconcile logs.
func (r *WitwaveAgentReconciler) teardownDisabledAgent(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) error {
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}

	var teardownErrs []error
	recordErr := func(kind, reason string, err error) {
		if err == nil {
			return
		}
		witwaveagentTeardownStepErrorsTotal.WithLabelValues(kind, reason).Inc()
		teardownErrs = append(teardownErrs, fmt.Errorf("%s %s: %w", reason, kind, err))
	}

	// Helper closure: fetch the resource at `key`, delete only if owned
	// by this WitwaveAgent. Missing-object is not an error.  Returns (getErr,
	// delErr) so the caller can label the right metric reason.
	tryDelete := func(obj client.Object) (getErr, delErr error) {
		if err := r.Get(ctx, key, obj); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, nil
			}
			return err, nil
		}
		if !metav1.IsControlledBy(obj, agent) {
			return nil, nil
		}
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, nil
	}

	// Order: Deployment → Service → optional resources. Doesn't matter
	// for correctness (k8s GC handles dependents) but makes log streams
	// easier to read.  Each step records its own metric + accumulates
	// the error rather than short-circuiting (#754).
	if gErr, dErr := tryDelete(&appsv1.Deployment{}); gErr != nil || dErr != nil {
		recordErr("Deployment", "get", gErr)
		recordErr("Deployment", "delete", dErr)
	}
	if gErr, dErr := tryDelete(&corev1.Service{}); gErr != nil || dErr != nil {
		recordErr("Service", "get", gErr)
		recordErr("Service", "delete", dErr)
	}
	if gErr, dErr := tryDelete(&autoscalingv2.HorizontalPodAutoscaler{}); gErr != nil || dErr != nil {
		recordErr("HorizontalPodAutoscaler", "get", gErr)
		recordErr("HorizontalPodAutoscaler", "delete", dErr)
	}
	if gErr, dErr := tryDelete(&policyv1.PodDisruptionBudget{}); gErr != nil || dErr != nil {
		recordErr("PodDisruptionBudget", "get", gErr)
		recordErr("PodDisruptionBudget", "delete", dErr)
	}
	// ConfigMaps and PVCs use per-backend naming, so they can't be
	// addressed by the single `key` above. List every object carrying our
	// agent labels and delete the ones we actually own, mirroring the
	// cleanup pattern in reconcileConfigMaps (#490).
	labelSel := client.MatchingLabels{
		labelName:      agent.Name,
		labelManagedBy: managedBy,
	}
	cms := &corev1.ConfigMapList{}
	if err := r.List(ctx, cms, client.InNamespace(agent.Namespace), labelSel); err != nil {
		recordErr("ConfigMap", "list", err)
	} else {
		for i := range cms.Items {
			cm := &cms.Items[i]
			if !metav1.IsControlledBy(cm, agent) {
				continue
			}
			if err := r.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
				recordErr("ConfigMap", "delete", fmt.Errorf("%s: %w", cm.Name, err))
			}
		}
	}
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcs, client.InNamespace(agent.Namespace), labelSel); err != nil {
		recordErr("PersistentVolumeClaim", "list", err)
	} else {
		for i := range pvcs.Items {
			pvc := &pvcs.Items[i]
			if !metav1.IsControlledBy(pvc, agent) {
				continue
			}
			if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
				recordErr("PersistentVolumeClaim", "delete", fmt.Errorf("%s: %w", pvc.Name, err))
			}
		}
	}
	// Force the dashboard teardown regardless of spec.dashboard.enabled
	// (#682). Without the forceDelete flag, reconcileDashboard's apply
	// path would keep (or even create) the dashboard stack pointing at
	// the harness Service we just removed, and on the finalize path
	// Create would return Forbidden on an object with a
	// DeletionTimestamp and leak the finalizer.
	if err := r.reconcileDashboardInternal(ctx, agent, true); err != nil {
		recordErr("Dashboard", "delete", err)
	}
	// ServiceMonitor follows the same pattern — its reconciler treats
	// spec.metrics.enabled=false (which is implied when the agent is
	// disabled and we still hold spec.serviceMonitor.enabled=true) as a
	// delete, and gracefully no-ops when the Prometheus Operator CRD
	// is absent (#476). Run it here so a disabled agent never leaves a
	// stale ServiceMonitor pointing at a Service with no endpoints.
	if present, err := r.serviceMonitorCRDPresent(ctx); err != nil {
		recordErr("ServiceMonitor", "probe", err)
	} else if present {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(serviceMonitorGVK)
		if err := r.Get(ctx, key, existing); err == nil {
			if metav1.IsControlledBy(existing, agent) {
				if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
					recordErr("ServiceMonitor", "delete", err)
				}
			}
		} else if !apierrors.IsNotFound(err) {
			recordErr("ServiceMonitor", "get", err)
		}
	}
	// PodMonitor mirrors the ServiceMonitor block above (#683). Without
	// this, toggling spec.enabled=false left an orphaned PodMonitor in
	// the cluster — OwnerReferences GC only fires on full CR deletion.
	if pmPresent, err := r.podMonitorCRDPresent(ctx); err != nil {
		recordErr("PodMonitor", "probe", err)
	} else if pmPresent {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(podMonitorGVK)
		if err := r.Get(ctx, key, existing); err == nil {
			if metav1.IsControlledBy(existing, agent) {
				if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
					recordErr("PodMonitor", "delete", err)
				}
			}
		} else if !apierrors.IsNotFound(err) {
			recordErr("PodMonitor", "get", err)
		}
	}
	// Rewrite the shared team manifest CM with the terminating (or
	// disabled) agent excluded (#902). The manifest reconciler already
	// filters out agents with a non-zero DeletionTimestamp and those
	// whose spec.enabled is false, so simply invoking it from the
	// teardown path converges peer manifests in the same reconcile
	// cycle that finalizes this agent — peer pods no longer see a stale
	// entry pointing at a Service with no endpoints until K8s GC or a
	// peer reconcile catches up. Errors are accumulated so any
	// apiserver failure lands in the teardown-step metric set.
	if err := r.reconcileManifestConfigMap(ctx, agent); err != nil {
		recordErr("ManifestConfigMap", "rewrite", err)
	}
	// Drop the per-CR dashboard gauge so the metric series doesn't
	// linger across enable/disable cycles.
	witwaveagentDashboardEnabled.DeleteLabelValues(agent.Namespace, agent.Name)
	return errors.Join(teardownErrs...)
}

// finalizeWitwaveAgent runs the explicit cleanup path invoked when a WitwaveAgent is
// being deleted (#569). OwnerReferences already cascade to owned cluster
// resources, but the operator also needs to drop the per-CR Prometheus
// gauge series and proactively delete resources the controller manages —
// both are achieved by reusing the teardown path used for spec.enabled=false,
// which covers Deployment, Service, HPA, PDB, ConfigMaps, PVCs, the
// dashboard stack, and the `witwaveagent_dashboard_enabled` gauge.
func (r *WitwaveAgentReconciler) finalizeWitwaveAgent(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) error {
	return r.teardownDisabledAgent(ctx, agent)
}

// reconcileDashboard creates, updates, or deletes the per-agent dashboard
// ConfigMap + Deployment + Service to match WitwaveAgent.spec.dashboard (#470).
// The ConfigMap holds the nginx template that routes /api/agents/<name>/...
// directly to the owned agent's service, matching the direct-routing
// architecture the Helm chart uses cluster-wide.
func (r *WitwaveAgentReconciler) reconcileDashboard(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) error {
	return r.reconcileDashboardInternal(ctx, agent, false)
}

// reconcileDashboardInternal implements the dashboard reconcile with an
// explicit forceDelete flag used by teardownDisabledAgent/finalizeWitwaveAgent
// (#682). When forceDelete is true, the function skips the apply path and
// runs the existing Delete block unconditionally, even when
// spec.dashboard.enabled=true, so dashboard pods do not linger pointing at
// an already-removed harness Service.
func (r *WitwaveAgentReconciler) reconcileDashboardInternal(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent, forceDelete bool) (err error) {
	dashboardEnabled := agent.Spec.Dashboard != nil && agent.Spec.Dashboard.Enabled
	ctx, _, finish := startStepSpan(ctx, "witwaveagent.reconcileDashboard",
		attribute.Bool("witwave.dashboard.enabled", dashboardEnabled),
		attribute.Bool("witwave.dashboard.force_delete", forceDelete),
	)
	defer finish(&err)

	desiredCM := buildDashboardConfigMap(agent)
	desiredDep := buildDashboardDeployment(agent, DefaultImageTag)
	desiredSvc := buildDashboardService(agent)
	if forceDelete {
		// Collapse the apply path so the existing desiredDep==nil
		// delete branch below tears down every dashboard resource we
		// own, regardless of spec.dashboard.enabled.
		desiredCM = nil
		desiredDep = nil
		desiredSvc = nil
	}
	// Mirror spec.dashboard.enabled into the per-CR gauge so dashboards
	// can sum() across all WitwaveAgents to count adoption (#471).
	{
		val := 0.0
		if agent.Spec.Dashboard != nil && agent.Spec.Dashboard.Enabled {
			val = 1.0
		}
		witwaveagentDashboardEnabled.WithLabelValues(agent.Namespace, agent.Name).Set(val)
	}

	// Key used for Deployment and Service — <agent>-dashboard.
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name + "-dashboard"}
	// ConfigMap uses its own suffix so it doesn't collide with the
	// Deployment/Service objects sharing the short key.
	cmKey := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name + "-dashboard-nginx"}

	// Delete path: the spec was toggled off or removed. Only remove
	// resources we actually own to avoid clobbering something the
	// operator didn't create.
	if desiredDep == nil {
		existingDep := &appsv1.Deployment{}
		if err := r.Get(ctx, key, existingDep); err == nil {
			if metav1.IsControlledBy(existingDep, agent) {
				if err := r.Delete(ctx, existingDep); err != nil && !apierrors.IsNotFound(err) {
					return err
				}
			}
		} else if !apierrors.IsNotFound(err) {
			return err
		}
		existingSvc := &corev1.Service{}
		if err := r.Get(ctx, key, existingSvc); err == nil {
			if metav1.IsControlledBy(existingSvc, agent) {
				if err := r.Delete(ctx, existingSvc); err != nil && !apierrors.IsNotFound(err) {
					return err
				}
			}
		} else if !apierrors.IsNotFound(err) {
			return err
		}
		existingCM := &corev1.ConfigMap{}
		if err := r.Get(ctx, cmKey, existingCM); err == nil {
			if metav1.IsControlledBy(existingCM, agent) {
				if err := r.Delete(ctx, existingCM); err != nil && !apierrors.IsNotFound(err) {
					return err
				}
			}
		} else if !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}

	// Apply ConfigMap first — the Deployment mounts it, so creation order
	// matters for fresh installs.
	if desiredCM != nil {
		if err := controllerutil.SetControllerReference(agent, desiredCM, r.Scheme); err != nil {
			return fmt.Errorf("set owner on dashboard ConfigMap: %w", err)
		}
		existingCM := &corev1.ConfigMap{}
		if err := r.Get(ctx, cmKey, existingCM); err != nil {
			if apierrors.IsNotFound(err) {
				if err := r.Create(ctx, desiredCM); err != nil {
					return fmt.Errorf("create dashboard ConfigMap: %w", err)
				}
			} else {
				return err
			}
		} else {
			existingCM.Data = desiredCM.Data
			existingCM.Labels = desiredCM.Labels
			if err := r.Update(ctx, existingCM); err != nil {
				return fmt.Errorf("update dashboard ConfigMap: %w", err)
			}
		}
	}

	// Apply Deployment.
	if err := controllerutil.SetControllerReference(agent, desiredDep, r.Scheme); err != nil {
		return fmt.Errorf("set owner on dashboard Deployment: %w", err)
	}
	existingDep := &appsv1.Deployment{}
	if err := r.Get(ctx, key, existingDep); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, desiredDep); err != nil {
				return fmt.Errorf("create dashboard Deployment: %w", err)
			}
		} else {
			return err
		}
	} else {
		existingDep.Spec = desiredDep.Spec
		existingDep.Labels = desiredDep.Labels
		if err := r.Update(ctx, existingDep); err != nil {
			return fmt.Errorf("update dashboard Deployment: %w", err)
		}
	}

	// Apply Service.
	if desiredSvc != nil {
		if err := controllerutil.SetControllerReference(agent, desiredSvc, r.Scheme); err != nil {
			return fmt.Errorf("set owner on dashboard Service: %w", err)
		}
		existingSvc := &corev1.Service{}
		if err := r.Get(ctx, key, existingSvc); err != nil {
			if apierrors.IsNotFound(err) {
				if err := r.Create(ctx, desiredSvc); err != nil {
					return fmt.Errorf("create dashboard Service: %w", err)
				}
			} else {
				return err
			}
		} else {
			// Preserve ClusterIP (immutable) while patching the rest.
			desiredSvc.Spec.ClusterIP = existingSvc.Spec.ClusterIP
			existingSvc.Spec = desiredSvc.Spec
			existingSvc.Labels = desiredSvc.Labels
			if err := r.Update(ctx, existingSvc); err != nil {
				return fmt.Errorf("update dashboard Service: %w", err)
			}
		}
	}
	return nil
}

// crdProbeTTL is the maximum age of a cached CRD-presence probe result
// before the next reconcile re-queries the RESTMapper (#756). 30s is
// long enough that a high-churn WitwaveAgent workload stops hammering the
// apiserver's discovery path on every reconcile, and short enough that
// installing the Prometheus Operator CRDs mid-run is picked up in under
// a minute without an operator restart.
const crdProbeTTL = 30 * time.Second

// crdProbeCache caches CRD-presence results across reconciles (#756). The
// keys are the string form of a GVK; values are atomic.Pointer so the
// struct literal below can be updated lock-free on the happy path. A
// single sync.Map entry per tracked GVK is all that's needed — the
// operator knows its set at compile time.
var crdProbeCache sync.Map // map[string]*atomic.Pointer[crdProbeEntry]

type crdProbeEntry struct {
	present bool
	at      time.Time
}

func getCachedCRDProbe(key string) (crdProbeEntry, bool) {
	v, ok := crdProbeCache.Load(key)
	if !ok {
		return crdProbeEntry{}, false
	}
	p, ok := v.(*atomic.Pointer[crdProbeEntry])
	if !ok || p == nil {
		return crdProbeEntry{}, false
	}
	e := p.Load()
	if e == nil {
		return crdProbeEntry{}, false
	}
	if time.Since(e.at) > crdProbeTTL {
		return crdProbeEntry{}, false
	}
	return *e, true
}

func setCachedCRDProbe(key string, present bool) {
	entry := &crdProbeEntry{present: present, at: time.Now()}
	v, ok := crdProbeCache.Load(key)
	if !ok {
		p := &atomic.Pointer[crdProbeEntry]{}
		p.Store(entry)
		crdProbeCache.Store(key, p)
		return
	}
	if p, ok := v.(*atomic.Pointer[crdProbeEntry]); ok && p != nil {
		p.Store(entry)
	}
}

// invalidateCachedCRDProbe flips a cached present=true entry to
// present=false inline (#1071). When a downstream apiserver call for a
// GVK returns NoKindMatchError — meaning the CRD has been uninstalled
// since we cached the probe — the reconcile step stops treating the
// failure as a real error and converges to the absent-CRD no-op on the
// next iteration (and this one, via the caller returning nil). Without
// this the stale present=true could persist for up to crdProbeTTL,
// flipping phase to Error for every agent during Prometheus-Operator
// maintenance.
func invalidateCachedCRDProbe(key string) {
	entry := &crdProbeEntry{present: false, at: time.Now()}
	if v, ok := crdProbeCache.Load(key); ok {
		if p, ok := v.(*atomic.Pointer[crdProbeEntry]); ok && p != nil {
			p.Store(entry)
			return
		}
	}
	p := &atomic.Pointer[crdProbeEntry]{}
	p.Store(entry)
	crdProbeCache.Store(key, p)
}

// handleDownstreamNoMatch inspects err from a reconcile step that
// previously cleared the CRD-presence probe. When the error is a
// NoKindMatchError the CRD was removed between the cached probe and the
// apiserver call; invalidate the cache and return (nil, true) so the
// caller can short-circuit to the no-op path. (nil, false) means the
// caller should continue its normal error handling.
func handleDownstreamNoMatch(cacheKey string, err error) (error, bool) {
	if err == nil {
		return nil, false
	}
	if meta.IsNoMatchError(err) {
		invalidateCachedCRDProbe(cacheKey)
		return nil, true
	}
	return err, false
}

// serviceMonitorCRDPresent reports whether the cluster has the
// monitoring.coreos.com/v1 ServiceMonitor REST mapping registered. Uses
// the RESTMapper on the shared client so the probe is a cache lookup on
// steady state rather than an apiserver round trip per reconcile. A
// NoKindMatchError (or any IsNoMatchError) means the CRD is not
// installed — the reconciler treats that as a clean no-op. Other errors
// propagate so they surface in status + the retry loop.
//
// Results are cached for “crdProbeTTL“ (#756) so a high-churn
// WitwaveAgent workload does not re-probe the RESTMapper on every
// reconcile. A fresh install of the Prometheus Operator CRDs is
// picked up within that TTL without operator restart.
func (r *WitwaveAgentReconciler) serviceMonitorCRDPresent(ctx context.Context) (bool, error) {
	_ = ctx // reserved — RESTMapper lookups are synchronous and don't need ctx
	const cacheKey = "monitoring.coreos.com/v1/ServiceMonitor"
	if e, ok := getCachedCRDProbe(cacheKey); ok {
		return e.present, nil
	}
	mapper := r.Client.RESTMapper()
	if mapper == nil {
		return false, nil
	}
	_, err := mapper.RESTMapping(serviceMonitorGVK.GroupKind(), serviceMonitorGVK.Version)
	if err != nil {
		if meta.IsNoMatchError(err) {
			setCachedCRDProbe(cacheKey, false)
			return false, nil
		}
		return false, err
	}
	setCachedCRDProbe(cacheKey, true)
	return true, nil
}

// reconcileServiceMonitor creates, updates, or deletes a per-agent
// ServiceMonitor (monitoring.coreos.com/v1) to match spec.serviceMonitor
// (#476). The reconciler is gated on three independent conditions:
//
//  1. The WitwaveAgent opted in via spec.serviceMonitor.enabled=true.
//  2. spec.metrics.enabled=true (there is nothing to scrape otherwise).
//  3. The monitoring.coreos.com/v1 ServiceMonitor CRD is installed on
//     the cluster. When absent the reconciler logs once per reconcile
//     and no-ops so clusters without the Prometheus Operator are
//     unaffected.
//
// When any of the conditions above are false the reconciler also runs
// the delete path: a ServiceMonitor we previously created is removed,
// and IsControlledBy gates the delete so a user-managed ServiceMonitor
// in the same namespace is never touched. ServiceMonitors created
// outside this reconciler keep their owner reference untouched — the
// controller dual-checks label + IsControlledBy before mutating.
func (r *WitwaveAgentReconciler) reconcileServiceMonitor(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, _, finish := startStepSpan(ctx, "witwaveagent.reconcileServiceMonitor",
		attribute.Bool("witwave.servicemonitor.enabled", serviceMonitorEnabled(agent)),
		attribute.Bool("witwave.metrics.enabled", agent.Spec.Metrics.Enabled),
	)
	defer finish(&err)

	log := logf.FromContext(ctx)

	// Probe CRD presence. An absent CRD means we can't read or write
	// the object at all; short-circuit both the apply and delete paths.
	const smCacheKey = "monitoring.coreos.com/v1/ServiceMonitor"
	present, err := r.serviceMonitorCRDPresent(ctx)
	if err != nil {
		return fmt.Errorf("probe ServiceMonitor CRD: %w", err)
	}
	if !present {
		if serviceMonitorEnabled(agent) && agent.Spec.Metrics.Enabled {
			// Keep this at V(1) so it's visible during debugging but
			// doesn't spam on every reconcile on clusters that
			// intentionally omit prometheus-operator.
			log.V(1).Info("ServiceMonitor CRD not installed — skipping ServiceMonitor reconcile",
				"group", serviceMonitorGVK.Group, "version", serviceMonitorGVK.Version)
		}
		return nil
	}

	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}

	wantCreate := serviceMonitorEnabled(agent) && agent.Spec.Metrics.Enabled

	// Delete path: spec disabled or metrics disabled. Only touch the
	// object when we own it.
	if !wantCreate {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(serviceMonitorGVK)
		if err := r.Get(ctx, key, existing); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			if _, skip := handleDownstreamNoMatch(smCacheKey, err); skip {
				return nil
			}
			return fmt.Errorf("get ServiceMonitor for delete: %w", err)
		}
		if !metav1.IsControlledBy(existing, agent) {
			return nil
		}
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			if _, skip := handleDownstreamNoMatch(smCacheKey, err); skip {
				return nil
			}
			return fmt.Errorf("delete ServiceMonitor: %w", err)
		}
		return nil
	}

	desired := buildServiceMonitor(agent)
	if desired == nil {
		return nil
	}
	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on ServiceMonitor: %w", err)
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(serviceMonitorGVK)
	err = r.Get(ctx, key, existing)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, desired); err != nil {
			if _, skip := handleDownstreamNoMatch(smCacheKey, err); skip {
				return nil
			}
			return fmt.Errorf("create ServiceMonitor: %w", err)
		}
		return nil
	case err != nil:
		if _, skip := handleDownstreamNoMatch(smCacheKey, err); skip {
			return nil
		}
		return fmt.Errorf("get ServiceMonitor: %w", err)
	}

	// Defensive: refuse to mutate a ServiceMonitor we don't own. Users
	// may pre-create one with the same name; leaving it untouched is
	// safer than clobbering their scrape config.
	if !metav1.IsControlledBy(existing, agent) {
		return nil
	}

	// Patch spec + labels; preserve everything else (resourceVersion,
	// uid, etc.).
	existing.Object["spec"] = desired.Object["spec"]
	existing.SetLabels(desired.GetLabels())
	if err := r.Update(ctx, existing); err != nil {
		if _, skip := handleDownstreamNoMatch(smCacheKey, err); skip {
			return nil
		}
		return fmt.Errorf("update ServiceMonitor: %w", err)
	}
	return nil
}

// podMonitorCRDPresent reports whether the monitoring.coreos.com/v1
// PodMonitor CRD is known to the cluster. Mirrors serviceMonitorCRDPresent
// and shares its short-TTL result cache (#756).
func (r *WitwaveAgentReconciler) podMonitorCRDPresent(ctx context.Context) (bool, error) {
	_ = ctx
	const cacheKey = "monitoring.coreos.com/v1/PodMonitor"
	if e, ok := getCachedCRDProbe(cacheKey); ok {
		return e.present, nil
	}
	mapper := r.Client.RESTMapper()
	if mapper == nil {
		return false, nil
	}
	_, err := mapper.RESTMapping(podMonitorGVK.GroupKind(), podMonitorGVK.Version)
	if err != nil {
		if meta.IsNoMatchError(err) {
			setCachedCRDProbe(cacheKey, false)
			return false, nil
		}
		return false, err
	}
	setCachedCRDProbe(cacheKey, true)
	return true, nil
}

// reconcilePodMonitor creates, updates, or deletes a per-agent PodMonitor
// (#582). Same gating pattern as reconcileServiceMonitor:
//
//  1. spec.podMonitor.enabled=true.
//  2. spec.metrics.enabled=true (nothing to scrape otherwise).
//  3. monitoring.coreos.com/v1 PodMonitor CRD is installed.
//
// When any condition flips off the reconciler deletes a previously-created
// PodMonitor. User-created PodMonitors that collide by name are left alone
// via the IsControlledBy check.
func (r *WitwaveAgentReconciler) reconcilePodMonitor(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent) (err error) {
	ctx, _, finish := startStepSpan(ctx, "witwaveagent.reconcilePodMonitor",
		attribute.Bool("witwave.podmonitor.enabled", podMonitorEnabled(agent)),
		attribute.Bool("witwave.metrics.enabled", agent.Spec.Metrics.Enabled),
	)
	defer finish(&err)

	log := logf.FromContext(ctx)

	const pmCacheKey = "monitoring.coreos.com/v1/PodMonitor"
	present, err := r.podMonitorCRDPresent(ctx)
	if err != nil {
		return fmt.Errorf("probe PodMonitor CRD: %w", err)
	}
	if !present {
		if podMonitorEnabled(agent) && agent.Spec.Metrics.Enabled {
			log.V(1).Info("PodMonitor CRD not installed — skipping PodMonitor reconcile",
				"group", podMonitorGVK.Group, "version", podMonitorGVK.Version)
		}
		return nil
	}

	key := client.ObjectKey{Namespace: agent.Namespace, Name: fmt.Sprintf("%s-backends", agent.Name)}
	wantCreate := podMonitorEnabled(agent) && agent.Spec.Metrics.Enabled

	if !wantCreate {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(podMonitorGVK)
		if err := r.Get(ctx, key, existing); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			if _, skip := handleDownstreamNoMatch(pmCacheKey, err); skip {
				return nil
			}
			return fmt.Errorf("get PodMonitor for delete: %w", err)
		}
		if !metav1.IsControlledBy(existing, agent) {
			return nil
		}
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			if _, skip := handleDownstreamNoMatch(pmCacheKey, err); skip {
				return nil
			}
			return fmt.Errorf("delete PodMonitor: %w", err)
		}
		return nil
	}

	desired := buildPodMonitor(agent)
	if desired == nil {
		return nil
	}
	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on PodMonitor: %w", err)
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(podMonitorGVK)
	err = r.Get(ctx, key, existing)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, desired); err != nil {
			if _, skip := handleDownstreamNoMatch(pmCacheKey, err); skip {
				return nil
			}
			return fmt.Errorf("create PodMonitor: %w", err)
		}
		return nil
	case err != nil:
		if _, skip := handleDownstreamNoMatch(pmCacheKey, err); skip {
			return nil
		}
		return fmt.Errorf("get PodMonitor: %w", err)
	}

	if !metav1.IsControlledBy(existing, agent) {
		return nil
	}
	existing.Object["spec"] = desired.Object["spec"]
	existing.SetLabels(desired.GetLabels())
	if err := r.Update(ctx, existing); err != nil {
		if _, skip := handleDownstreamNoMatch(pmCacheKey, err); skip {
			return nil
		}
		return fmt.Errorf("update PodMonitor: %w", err)
	}
	return nil
}

// ── Status ────────────────────────────────────────────────────────────────────

func (r *WitwaveAgentReconciler) updateStatus(ctx context.Context, agent *witwavev1alpha1.WitwaveAgent, reconcileErr error, reconcileStart time.Time) (err error) {
	ctx, _, finish := startStepSpan(ctx, "witwaveagent.updateStatus",
		attribute.Bool("witwave.reconcile.had_error", reconcileErr != nil),
	)
	defer finish(&err)

	// Re-read the Deployment to derive ready replicas.
	dep := &appsv1.Deployment{}
	depKey := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}
	depErr := r.Get(ctx, depKey, dep)

	// Capture the previous phase so we only emit Events on actual transitions
	// rather than on every reconcile (#442).
	previousPhase := agent.Status.Phase

	newStatus := agent.Status.DeepCopy()
	newStatus.ObservedGeneration = agent.Generation

	now := metav1.Now()

	// ReconcileSuccess condition.
	recCond := metav1.Condition{
		Type:               witwavev1alpha1.ConditionReconcileSuccess,
		LastTransitionTime: now,
		ObservedGeneration: agent.Generation,
	}
	if reconcileErr != nil {
		recCond.Status = metav1.ConditionFalse
		recCond.Reason = "ReconcileFailed"
		recCond.Message = reconcileErr.Error()
	} else {
		recCond.Status = metav1.ConditionTrue
		recCond.Reason = "Reconciled"
		recCond.Message = "All owned resources are in sync."
	}
	setCondition(&newStatus.Conditions, recCond)

	// Available / Progressing driven by the Deployment.
	availCond := metav1.Condition{
		Type:               witwavev1alpha1.ConditionAvailable,
		LastTransitionTime: now,
		ObservedGeneration: agent.Generation,
	}
	progCond := metav1.Condition{
		Type:               witwavev1alpha1.ConditionProgressing,
		LastTransitionTime: now,
		ObservedGeneration: agent.Generation,
	}

	// desired is only meaningful when the Deployment exists; default to 0 in
	// the missing/error cases so phase-transition Event messages don't show
	// stale numbers (#451). Populated in the default branch below.
	var desired int32

	switch {
	case apierrors.IsNotFound(depErr):
		newStatus.ReadyReplicas = 0
		newStatus.Phase = witwavev1alpha1.WitwaveAgentPhasePending
		availCond.Status = metav1.ConditionFalse
		availCond.Reason = "DeploymentMissing"
		availCond.Message = "Agent Deployment does not yet exist."
		progCond.Status = metav1.ConditionTrue
		progCond.Reason = "Creating"
		progCond.Message = "Creating the agent Deployment."
	case depErr != nil:
		newStatus.Phase = witwavev1alpha1.WitwaveAgentPhaseError
		availCond.Status = metav1.ConditionUnknown
		availCond.Reason = "DeploymentFetchFailed"
		availCond.Message = depErr.Error()
		progCond.Status = metav1.ConditionUnknown
		progCond.Reason = "DeploymentFetchFailed"
		progCond.Message = depErr.Error()
	default:
		newStatus.ReadyReplicas = dep.Status.ReadyReplicas
		desired = int32(1)
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}
		switch {
		case reconcileErr != nil:
			newStatus.Phase = witwavev1alpha1.WitwaveAgentPhaseError
			availCond.Status = metav1.ConditionFalse
			availCond.Reason = "ReconcileError"
			availCond.Message = reconcileErr.Error()
			progCond.Status = metav1.ConditionFalse
			progCond.Reason = "ReconcileError"
			progCond.Message = reconcileErr.Error()
		case dep.Status.ObservedGeneration >= dep.Generation &&
			dep.Status.UpdatedReplicas >= desired &&
			dep.Status.ReadyReplicas >= desired &&
			desired > 0:
			// Mirror `kubectl rollout status`: require the Deployment
			// controller to have observed the current generation AND
			// the new ReplicaSet to have updated replicas in the Ready
			// count before declaring Ready. Without the generation
			// guard, stale ReadyReplicas from the previous ReplicaSet
			// can satisfy the old check immediately after an image
			// bump, flipping phase to Ready before the new pods
			// actually come up (#554).
			newStatus.Phase = witwavev1alpha1.WitwaveAgentPhaseReady
			availCond.Status = metav1.ConditionTrue
			availCond.Reason = "AllReplicasReady"
			availCond.Message = fmt.Sprintf("%d/%d replicas ready.", dep.Status.ReadyReplicas, desired)
			progCond.Status = metav1.ConditionFalse
			progCond.Reason = "Deployed"
			progCond.Message = "Rollout complete."
		default:
			newStatus.Phase = witwavev1alpha1.WitwaveAgentPhaseDegraded
			availCond.Status = metav1.ConditionFalse
			availCond.Reason = "NotAllReady"
			availCond.Message = fmt.Sprintf("%d/%d replicas ready.", dep.Status.ReadyReplicas, desired)
			progCond.Status = metav1.ConditionTrue
			progCond.Reason = "RolloutInProgress"
			progCond.Message = "Waiting for replicas to become ready."
		}
	}

	setCondition(&newStatus.Conditions, availCond)
	setCondition(&newStatus.Conditions, progCond)

	// Skip the write if nothing changed (avoids status churn). Compared
	// before appending ReconcileHistory so an idempotent no-op reconcile
	// doesn't rotate a fresh entry through the ring on every pass — the
	// ring would otherwise fill with identical "Reconciled/Reconciled/…"
	// entries and hide the flap the field was added to help diagnose.
	if statusEqual(agent.Status, *newStatus) {
		return nil
	}

	// Record this reconcile in the capped ReconcileHistory ring (#1112).
	// Runs only when we're going to write status anyway, so every entry
	// marks a meaningful change (phase flip, condition flip, reconcile
	// error, or the first observation after controller start).
	newStatus.ReconcileHistory = appendReconcileHistory(
		newStatus.ReconcileHistory,
		reconcileStart,
		time.Since(reconcileStart),
		reconcileErr,
	)
	// Use Status().Patch with MergeFrom(before) (#757) rather than
	// Status().Update: a server-side optimistic-concurrency conflict on
	// the subresource now only fails the single field that contended,
	// without requeueing the whole object or prompting a duplicate
	// status write. Crucially, we also defer the phase-transition
	// metric increment + Kubernetes Event emission until *after* the
	// patch succeeds so a retried Update-on-conflict cannot double-count
	// either (the previous Update call incremented the metric first
	// then retried on 409 -> duplicate Events in the audit log).
	before := agent.DeepCopy()
	agent.Status = *newStatus
	if err := r.Status().Patch(ctx, agent, client.MergeFrom(before)); err != nil {
		return err
	}

	// Emit a Kubernetes Event on actual phase transitions (#442). Skipped on
	// the empty→Pending bootstrap and on every no-op reconcile thanks to the
	// previousPhase comparison and the statusEqual short-circuit above.
	// The same condition gates the phase-transition metric (#471), so
	// counts agree with the events emitted.
	if newStatus.Phase != previousPhase && previousPhase != "" {
		witwaveagentPhaseTransitionsTotal.WithLabelValues(string(previousPhase), string(newStatus.Phase)).Inc()
		if r.Recorder != nil {
			r.recordPhaseTransitionEvent(agent, previousPhase, newStatus.Phase, desired, reconcileErr)
		}
	}
	return nil
}

// recordPhaseTransitionEvent maps a phase transition to a Kubernetes Event.
// Reasons follow the convention used by mainstream operators (PhaseChanged,
// Ready, Degraded, ReconcileError). Eventtype is Warning for Degraded/Error
// transitions so kube-state-metrics and event-driven alerting can surface
// them as actionable signals.
func (r *WitwaveAgentReconciler) recordPhaseTransitionEvent(
	agent *witwavev1alpha1.WitwaveAgent,
	from, to witwavev1alpha1.WitwaveAgentPhase,
	desiredReplicas int32,
	reconcileErr error,
) {
	switch to {
	case witwavev1alpha1.WitwaveAgentPhaseReady:
		r.Recorder.Eventf(agent, corev1.EventTypeNormal, "Ready",
			"WitwaveAgent transitioned %s → Ready (%d/%d replicas)",
			from, agent.Status.ReadyReplicas, desiredReplicas)
	case witwavev1alpha1.WitwaveAgentPhaseDegraded:
		r.Recorder.Eventf(agent, corev1.EventTypeWarning, "Degraded",
			"WitwaveAgent transitioned %s → Degraded (%d/%d replicas ready)",
			from, agent.Status.ReadyReplicas, desiredReplicas)
	case witwavev1alpha1.WitwaveAgentPhaseError:
		msg := "reconcile failed"
		if reconcileErr != nil {
			msg = reconcileErr.Error()
		}
		r.Recorder.Eventf(agent, corev1.EventTypeWarning, "ReconcileError",
			"WitwaveAgent transitioned %s → Error: %s", from, msg)
	case witwavev1alpha1.WitwaveAgentPhasePending:
		r.Recorder.Eventf(agent, corev1.EventTypeNormal, "Pending",
			"WitwaveAgent transitioned %s → Pending", from)
	}
}

// setCondition upserts a condition by type, preserving LastTransitionTime
// when the status is unchanged.
func setCondition(conds *[]metav1.Condition, newCond metav1.Condition) {
	for i, c := range *conds {
		if c.Type == newCond.Type {
			if c.Status == newCond.Status {
				newCond.LastTransitionTime = c.LastTransitionTime
			}
			(*conds)[i] = newCond
			return
		}
	}
	*conds = append(*conds, newCond)
}

// appendReconcileHistory returns the history ring with a new
// ReconcileHistoryEntry appended at the end, truncated so at most
// witwavev1alpha1.ReconcileHistoryMax entries remain. The caller passes the
// pre-existing ring so the function is safe to use against newStatus
// (which already carries a DeepCopy of the prior entries). Messages are
// truncated to witwavev1alpha1.ReconcileHistoryMessageMax bytes with a
// trailing "…" so a pathological downstream error never bloats the
// status subresource.
func appendReconcileHistory(
	ring []witwavev1alpha1.ReconcileHistoryEntry,
	reconcileStart time.Time,
	duration time.Duration,
	reconcileErr error,
) []witwavev1alpha1.ReconcileHistoryEntry {
	entry := witwavev1alpha1.ReconcileHistoryEntry{
		Time:     metav1.NewTime(reconcileStart),
		Duration: duration.Round(time.Millisecond).String(),
	}
	if reconcileErr != nil {
		entry.Phase = witwavev1alpha1.ReconcileHistoryPhaseError
		entry.Reason = "ReconcileFailed"
		entry.Message = truncateMessage(reconcileErr.Error(), witwavev1alpha1.ReconcileHistoryMessageMax)
	} else {
		entry.Phase = witwavev1alpha1.ReconcileHistoryPhaseSuccess
		entry.Reason = "Reconciled"
	}
	ring = append(ring, entry)
	if overflow := len(ring) - witwavev1alpha1.ReconcileHistoryMax; overflow > 0 {
		// Drop oldest entries. Copy rather than reslice so the underlying
		// array shrinks and we don't pin a 10-entry array of stale values
		// indefinitely through status DeepCopies.
		trimmed := make([]witwavev1alpha1.ReconcileHistoryEntry, witwavev1alpha1.ReconcileHistoryMax)
		copy(trimmed, ring[overflow:])
		ring = trimmed
	}
	return ring
}

// truncateMessage clips s to max bytes, appending "…" when truncated.
// Returns s unchanged when already within the cap.
func truncateMessage(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	// Reserve three bytes for the ellipsis "…" (UTF-8 encoded) so the
	// emitted message never exceeds max bytes on the wire.
	const ellipsis = "…"
	if max <= len(ellipsis) {
		return ellipsis[:max]
	}
	return s[:max-len(ellipsis)] + ellipsis
}

// statusEqual is a shallow equality check sufficient for deciding whether to
// write the status subresource. Conditions compared by (type, status, reason,
// message, observedGeneration) only.
func statusEqual(a, b witwavev1alpha1.WitwaveAgentStatus) bool {
	if a.Phase != b.Phase ||
		a.ObservedGeneration != b.ObservedGeneration ||
		a.ReadyReplicas != b.ReadyReplicas ||
		len(a.Conditions) != len(b.Conditions) {
		return false
	}
	idx := map[string]metav1.Condition{}
	for _, c := range a.Conditions {
		idx[c.Type] = c
	}
	for _, c := range b.Conditions {
		prev, ok := idx[c.Type]
		if !ok {
			return false
		}
		if prev.Status != c.Status ||
			prev.Reason != c.Reason ||
			prev.Message != c.Message ||
			prev.ObservedGeneration != c.ObservedGeneration {
			return false
		}
	}
	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *WitwaveAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// enqueuePeersForTeam enqueues every WitwaveAgent in `namespace` whose
	// team label matches `team`, excluding the self-reference at
	// (namespace, skipName). Used by the Create/Delete/Generic paths
	// below and — crucially — twice on an Update event that flipped
	// team labels so BOTH the old and new team CMs converge within a
	// single reconcile cycle (#899).
	enqueuePeersForTeam := func(ctx context.Context, namespace, team, skipName string) []reconcile.Request {
		peers := &witwavev1alpha1.WitwaveAgentList{}
		err := mgr.GetClient().List(ctx, peers,
			client.InNamespace(namespace),
			client.MatchingFields{WitwaveAgentTeamIndex: team},
		)
		if err != nil {
			// Discriminate "index not registered" from every other
			// List error (#1011). Previously any error — RBAC
			// denial, context cancellation, apiserver 500 — was
			// silently swallowed into the fallback full-namespace
			// List, so peers never got enqueued during an apiserver
			// blip and manifest CMs stayed stale. Fall back only
			// when the index genuinely isn't registered (unit-test
			// bootstrap); otherwise log and return nil so the
			// caller's workqueue retry semantics kick in.
			if !IsFieldIndexMissing(err) {
				logf.FromContext(ctx).Error(err, "enqueuePeersForTeam: scoped List failed",
					"namespace", namespace, "team", team)
				return nil
			}
			if fbErr := mgr.GetClient().List(ctx, peers, client.InNamespace(namespace)); fbErr != nil {
				logf.FromContext(ctx).Error(fbErr, "enqueuePeersForTeam: fallback List failed",
					"namespace", namespace, "team", team)
				return nil
			}
		}
		reqs := make([]reconcile.Request, 0, len(peers.Items))
		for i := range peers.Items {
			p := &peers.Items[i]
			// #1178: use teamKey() here (matches WitwaveAgentTeamExtractor
			// semantics, including the empty-string-for-missing-label
			// grouping) instead of a raw label lookup. The previous
			// `p.Labels[teamLabel]` read did not nil-guard Labels in a
			// way that matched the extractor's contract, producing
			// post-filter mismatches on the fallback namespace-List
			// path where the field-indexer wasn't registered.
			if teamKey(p) != team {
				continue
			}
			if p.Namespace == namespace && p.Name == skipName {
				// The mutated CR already has its own
				// reconcile queued via the primary For()
				// watch — skip to avoid a duplicate entry.
				continue
			}
			reqs = append(reqs, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: p.Namespace,
					Name:      p.Name,
				},
			})
		}
		return reqs
	}

	// enqueueTeammates fans out peer reconciles on Create/Delete/Generic
	// events and on Update events that affect manifest content. For a
	// team-label flip (#899) the handler must target BOTH the OLD and
	// NEW team's peers: the new-team fan-out rewrites beta's CM to add
	// the moved agent, and the old-team fan-out rewrites alpha's CM to
	// drop it (without it alpha stays stale until an unrelated event
	// happens to kick one of its members).
	enqueueTeammates := handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			trigger, ok := e.Object.(*witwavev1alpha1.WitwaveAgent)
			if !ok {
				return
			}
			team := ""
			if trigger.Labels != nil {
				team = trigger.Labels[teamLabel]
			}
			for _, r := range enqueuePeersForTeam(ctx, trigger.Namespace, team, trigger.Name) {
				q.Add(r)
			}
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			trigger, ok := e.Object.(*witwavev1alpha1.WitwaveAgent)
			if !ok {
				return
			}
			team := ""
			if trigger.Labels != nil {
				team = trigger.Labels[teamLabel]
			}
			for _, r := range enqueuePeersForTeam(ctx, trigger.Namespace, team, trigger.Name) {
				q.Add(r)
			}
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			newObj, okNew := e.ObjectNew.(*witwavev1alpha1.WitwaveAgent)
			oldObj, okOld := e.ObjectOld.(*witwavev1alpha1.WitwaveAgent)
			if !okNew || !okOld {
				return
			}
			newTeam, oldTeam := "", ""
			if newObj.Labels != nil {
				newTeam = newObj.Labels[teamLabel]
			}
			if oldObj.Labels != nil {
				oldTeam = oldObj.Labels[teamLabel]
			}
			seen := map[types.NamespacedName]struct{}{}
			emit := func(reqs []reconcile.Request) {
				for _, r := range reqs {
					if _, dup := seen[r.NamespacedName]; dup {
						continue
					}
					seen[r.NamespacedName] = struct{}{}
					q.Add(r)
				}
			}
			// Always fan out to the current team so a port edit
			// (or a non-team label tweak that slipped past the
			// predicate in the future) still propagates.
			emit(enqueuePeersForTeam(ctx, newObj.Namespace, newTeam, newObj.Name))
			// On a team flip also fan out to the previous team
			// so its manifest CM drops the moved agent.
			if oldTeam != newTeam {
				emit(enqueuePeersForTeam(ctx, newObj.Namespace, oldTeam, newObj.Name))
			}
		},
		GenericFunc: func(ctx context.Context, e event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			trigger, ok := e.Object.(*witwavev1alpha1.WitwaveAgent)
			if !ok {
				return
			}
			team := ""
			if trigger.Labels != nil {
				team = trigger.Labels[teamLabel]
			}
			for _, r := range enqueuePeersForTeam(ctx, trigger.Namespace, team, trigger.Name) {
				q.Add(r)
			}
		},
	}

	// Only fan out on create/delete and on label-changing updates —
	// spec churn on a teammate doesn't affect the manifest payload
	// (name + URL only) and would otherwise thrash every peer.
	teamPredicate := predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return true },
		DeleteFunc:  func(e event.DeleteEvent) bool { return true },
		GenericFunc: func(e event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldTeam, newTeam := "", ""
			if e.ObjectOld != nil && e.ObjectOld.GetLabels() != nil {
				oldTeam = e.ObjectOld.GetLabels()[teamLabel]
			}
			if e.ObjectNew != nil && e.ObjectNew.GetLabels() != nil {
				newTeam = e.ObjectNew.GetLabels()[teamLabel]
			}
			// Port changes also change manifest URLs; include
			// them so a spec.port edit propagates without a
			// restart-the-operator workaround.
			oldPort := int32(0)
			newPort := int32(0)
			oldEnabled := true
			newEnabled := true
			oldDeleting := false
			newDeleting := false
			if a, ok := e.ObjectOld.(*witwavev1alpha1.WitwaveAgent); ok {
				oldPort = a.Spec.Port
				if a.Spec.Enabled != nil {
					oldEnabled = *a.Spec.Enabled
				}
				oldDeleting = !a.DeletionTimestamp.IsZero()
			}
			if a, ok := e.ObjectNew.(*witwavev1alpha1.WitwaveAgent); ok {
				newPort = a.Spec.Port
				if a.Spec.Enabled != nil {
					newEnabled = *a.Spec.Enabled
				}
				newDeleting = !a.DeletionTimestamp.IsZero()
			}
			// Enabled flips and deletionTimestamp-just-set
			// (#1067) both alter manifest membership because
			// reconcileManifestConfigMap drops disabled /
			// deleting agents. Without these clauses the
			// pause (enabled true→false) and the graceful-
			// delete onset leave peer manifests pointing at
			// a Service whose endpoints have gone empty.
			enabledFlipped := oldEnabled != newEnabled
			deletionJustSet := !oldDeleting && newDeleting
			return oldTeam != newTeam || oldPort != newPort || enabledFlipped || deletionJustSet
		},
	}

	// enqueueAgentsBoundByPrompt re-enqueues every WitwaveAgent listed in a
	// WitwavePrompt's spec.agentRefs when the WitwavePrompt changes. This keeps
	// the agent Deployment pod-spec in sync with prompt adds/removes
	// without waiting for a future unrelated reconcile to pick up the
	// new prompt list.
	//
	// Update events must inspect BOTH ObjectOld and ObjectNew so that an
	// agent being REMOVED from spec.agentRefs is enqueued too — otherwise
	// the dropped agent keeps the stale prompt ConfigMap mounted until an
	// unrelated reconcile fires on it (#1013, mirrors the #899 pattern
	// used above for team-label flips on WitwaveAgent).
	promptAgentRequests := func(p *witwavev1alpha1.WitwavePrompt) []reconcile.Request {
		if p == nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(p.Spec.AgentRefs))
		for _, ref := range p.Spec.AgentRefs {
			reqs = append(reqs, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: p.Namespace, Name: ref.Name},
			})
		}
		return reqs
	}
	enqueueAgentsBoundByPrompt := handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			p, ok := e.Object.(*witwavev1alpha1.WitwavePrompt)
			if !ok {
				return
			}
			for _, r := range promptAgentRequests(p) {
				q.Add(r)
			}
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			p, ok := e.Object.(*witwavev1alpha1.WitwavePrompt)
			if !ok {
				return
			}
			for _, r := range promptAgentRequests(p) {
				q.Add(r)
			}
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			newObj, okNew := e.ObjectNew.(*witwavev1alpha1.WitwavePrompt)
			oldObj, okOld := e.ObjectOld.(*witwavev1alpha1.WitwavePrompt)
			if !okNew || !okOld {
				return
			}
			seen := map[types.NamespacedName]struct{}{}
			emit := func(reqs []reconcile.Request) {
				for _, r := range reqs {
					if _, dup := seen[r.NamespacedName]; dup {
						continue
					}
					seen[r.NamespacedName] = struct{}{}
					q.Add(r)
				}
			}
			// Enqueue the union of old and new agent refs so agents
			// dropped from spec.agentRefs also reconcile and unmount
			// the stale prompt ConfigMap.
			emit(promptAgentRequests(newObj))
			emit(promptAgentRequests(oldObj))
		},
		GenericFunc: func(ctx context.Context, e event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			p, ok := e.Object.(*witwavev1alpha1.WitwavePrompt)
			if !ok {
				return
			}
			for _, r := range promptAgentRequests(p) {
				q.Add(r)
			}
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&witwavev1alpha1.WitwaveAgent{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Watches(&witwavev1alpha1.WitwaveAgent{}, enqueueTeammates, builder.WithPredicates(teamPredicate)).
		Watches(&witwavev1alpha1.WitwavePrompt{}, enqueueAgentsBoundByPrompt).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.enqueueAgentsReferencingSecret)).
		Named("witwaveagent").
		Complete(r)
}
