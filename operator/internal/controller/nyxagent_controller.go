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
	"strings"
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

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
	"github.com/nyx-ai/nyx-operator/internal/tracing"
)

// DefaultImageTag is used when an ImageSpec omits Tag. The release pipeline
// overrides this at link time via:
//
//	-ldflags "-X github.com/nyx-ai/nyx-operator/internal/controller.DefaultImageTag=<version>"
//
// The "unset" sentinel makes uninjected builds detectable so cmd/main.go can
// warn at startup; users can always pin tags explicitly per-NyxAgent (#440).
var DefaultImageTag = "unset"

// DefaultImageTagSentinel is the value that indicates the build did not
// inject a real version via ldflags.
const DefaultImageTagSentinel = "unset"

// nyxAgentFinalizer guarantees the operator observes NyxAgent deletion so
// per-CR metric series and owned cluster resources are cleaned up even when
// the operator was offline at delete time (#569). Future per-CR metrics or
// external-state teardown should piggyback on this single finalizer rather
// than adding per-concern finalizers.
const nyxAgentFinalizer = "nyxagent.nyx.ai/finalizer"

// NyxAgentReconciler reconciles a NyxAgent object.
type NyxAgentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=nyx.ai,resources=nyxagents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nyx.ai,resources=nyxagents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nyx.ai,resources=nyxagents/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services;configmaps;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// Secret verbs (#749, #761): controller-gen union-merges multi-line
// markers back to one rule, so the split of read vs write verbs lives
// in the chart (see charts/nyx-operator/templates/clusterrole.yaml and
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

// Reconcile is the control loop's entry point. It brings owned resources into
// alignment with the NyxAgent spec and writes status.
func (r *NyxAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// OTel server span around the full reconcile (#471 part B). When OTel
	// isn't enabled the tracer is a no-op so the overhead is a single
	// branch + interface dispatch — safe to leave on always.
	ctx, span := tracing.Tracer().Start(ctx, "nyxagent.reconcile",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("nyx.namespace", req.Namespace),
			attribute.String("nyx.name", req.Name),
		),
	)
	defer span.End()

	log := logf.FromContext(ctx)

	agent := &nyxv1alpha1.NyxAgent{}
	if err := r.Get(ctx, req.NamespacedName, agent); err != nil {
		if apierrors.IsNotFound(err) {
			// Belt-and-suspenders: with the finalizer (#569) the
			// delete-branch below is the primary cleanup path, but
			// this NotFound branch still runs if a CR is ever
			// orphaned (e.g. operator upgrade where a user removed
			// the finalizer externally) and drops the gauge series
			// so it doesn't linger until process restart (#471).
			nyxagentDashboardEnabled.DeleteLabelValues(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
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
	if !agent.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(agent, nyxAgentFinalizer) {
			if err := r.finalizeNyxAgent(ctx, agent); err != nil {
				return ctrl.Result{}, fmt.Errorf("finalize NyxAgent: %w", err)
			}
			controllerutil.RemoveFinalizer(agent, nyxAgentFinalizer)
			if err := r.Update(ctx, agent); err != nil {
				return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}
	if controllerutil.AddFinalizer(agent, nyxAgentFinalizer) {
		if err := r.Update(ctx, agent); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		return ctrl.Result{}, nil
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
	const teardownAnnotation = "nyx.ai/teardown-complete-generation"
	if agent.Spec.Enabled != nil && !*agent.Spec.Enabled {
		if v := agent.Annotations[teardownAnnotation]; v == fmt.Sprintf("%d", agent.Generation) {
			// Teardown already ran for this spec generation — nothing new
			// to do. Controller-runtime's watch predicates ensure we'll
			// be woken on the next spec change.
			return ctrl.Result{}, nil
		}
		log.Info("NyxAgent disabled — tearing down owned resources", "name", agent.Name)
		if err := r.teardownDisabledAgent(ctx, agent); err != nil {
			// Return the error alone so controller-runtime's rate
			// limiter applies exponential backoff rather than our
			// defeating it with a fixed 30s interval (#548).
			return ctrl.Result{}, err
		}
		// Stamp the generation on success so we short-circuit next time.
		if agent.Annotations == nil {
			agent.Annotations = map[string]string{}
		}
		agent.Annotations[teardownAnnotation] = fmt.Sprintf("%d", agent.Generation)
		if err := r.Update(ctx, agent); err != nil {
			// Non-fatal — the next reconcile will retry the stamp.
			log.Info("teardown complete but annotation stamp failed; will retry",
				"name", agent.Name, "err", err.Error())
		}
		return ctrl.Result{}, nil
	}

	// Re-enabled path: clear a prior teardown-complete annotation so a
	// future disable picks up the then-current generation fresh.
	if v, ok := agent.Annotations[teardownAnnotation]; ok && v != "" {
		delete(agent.Annotations, teardownAnnotation)
		if err := r.Update(ctx, agent); err != nil {
			log.Info("failed to clear teardown-complete annotation on re-enable; non-fatal",
				"name", agent.Name, "err", err.Error())
		}
	}

	// Apply all desired resources, joining any errors so status and logs
	// surface every failure rather than only the first (#497).
	var reconcileErrs []error

	// Credentials Secrets (#nyx.resolveCredentials parity). Runs FIRST
	// so the inline-mode path's freshly-created Secret exists by the
	// time the kubelet resolves envFrom references for the pod spec
	// applyDeployment is about to write.
	if err := r.reconcileCredentialsSecrets(ctx, agent); err != nil {
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
	reconcileErr := errors.Join(reconcileErrs...)

	// Observe Deployment status and update our own status subresource.
	if err := r.updateStatus(ctx, agent, reconcileErr); err != nil {
		log.Error(err, "failed to update NyxAgent status")
		// Don't mask the primary reconcile error.
		if reconcileErr == nil {
			reconcileErr = err
		}
	}

	// Stamp the resulting phase onto the span so traces show outcome at a
	// glance, plus mark errors so collectors flag them red.
	span.SetAttributes(attribute.String("nyx.phase", string(agent.Status.Phase)))
	if reconcileErr != nil {
		span.RecordError(reconcileErr)
		span.SetStatus(codes.Error, reconcileErr.Error())
		// Let controller-runtime's rate limiter drive retry spacing via
		// exponential backoff (5ms → 1000s). A hardcoded 30s interval
		// defeats that and can thundering-herd an unhealthy apiserver
		// when many NyxAgents fail in lockstep (#548).
		return ctrl.Result{}, reconcileErr
	}

	// The Deployment watch registered via Owns(&appsv1.Deployment{})
	// delivers the real Ready transition, so no primary polling loop is
	// needed. Keep a long safety-net requeue as a floor in case the
	// informer ever misses an event (cache resync, informer reset) so
	// the agent doesn't hang in a non-Ready phase forever (#548).
	if agent.Status.Phase != nyxv1alpha1.NyxAgentPhaseReady {
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

func (r *NyxAgentReconciler) applyDeployment(ctx context.Context, agent *nyxv1alpha1.NyxAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "nyxagent.applyDeployment",
		attribute.Int("nyx.backends.count", len(agent.Spec.Backends)),
	)
	defer finish(&err)

	prompts, err := r.listNyxPromptsForAgent(ctx, agent)
	if err != nil {
		return fmt.Errorf("list NyxPrompts: %w", err)
	}
	desired := buildDeployment(agent, DefaultImageTag, prompts)
	if err = controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on Deployment: %w", err)
	}
	existing := &appsv1.Deployment{}
	getErr := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(getErr):
		span.SetAttributes(attribute.String("nyx.resource.action", "create"))
		err = r.Create(ctx, desired)
		return err
	case getErr != nil:
		err = getErr
		return err
	}
	// When autoscaling is enabled, the HPA owns spec.replicas. Preserve the
	// existing value so that `existing.Spec = desired.Spec` (where desired has
	// replicas=nil) doesn't defeat the HPA on every reconcile. See #486.
	if agent.Spec.Autoscaling != nil && agent.Spec.Autoscaling.Enabled {
		desired.Spec.Replicas = existing.Spec.Replicas
	}
	// Patch spec + labels; keep existing status and server-filled fields.
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	span.SetAttributes(attribute.String("nyx.resource.action", "update"))
	err = r.Update(ctx, existing)
	return err
}

// NyxPromptAgentRefIndex is the field-indexer key that maps every
// NyxPrompt to its spec.agentRefs[].name values (#753). Indexing here
// lets ``listNyxPromptsForAgent`` issue a single scoped List instead of
// the full-namespace List + in-memory O(N*R) filter it used to run on
// every reconcile.
const NyxPromptAgentRefIndex = "spec.agentRefs.name"

// NyxPromptAgentRefExtractor returns the agent-ref names of one
// NyxPrompt. Empty ref names are dropped so missing fields don't
// pollute the index.
func NyxPromptAgentRefExtractor(obj client.Object) []string {
	p, ok := obj.(*nyxv1alpha1.NyxPrompt)
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

// isFieldIndexMissing returns true for the specific error controller-
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
func isFieldIndexMissing(err error) bool {
	if err == nil {
		return false
	}
	// controller-runtime cache (byIndexes) uses:
	//   "index with name %s does not exist"
	// client-go thread_safe_store uses:
	//   "indexer %q does not exist"
	// Match on the lowercased substring pair so minor upstream edits
	// (wrapping, trailing punctuation) don't silently reintroduce the
	// error-swallowing bug.
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "index") && strings.Contains(m, "does not exist")
}

// NyxAgentTeamIndex is the field-indexer key that maps every NyxAgent to
// its team label value (#753). Agents without the label land under the
// empty-string key — the same grouping teamKey() uses in-memory.
const NyxAgentTeamIndex = "metadata.labels.nyx.ai/team"

// NyxAgentTeamExtractor returns the single-element team key for a
// NyxAgent, including the empty-string case. Returning a single value
// means the scoped List ``client.MatchingFields{NyxAgentTeamIndex: t}``
// yields exactly the peers that share the team group.
func NyxAgentTeamExtractor(obj client.Object) []string {
	a, ok := obj.(*nyxv1alpha1.NyxAgent)
	if !ok {
		return nil
	}
	return []string{teamKey(a)}
}

// listNyxPromptsForAgent returns every NyxPrompt in the agent's namespace
// whose spec.agentRefs contains this agent. The result is sorted by CR
// name so buildDeployment renders pod volumes in a deterministic order.
//
// Performance (#753): uses ``client.MatchingFields`` against
// ``NyxPromptAgentRefIndex`` so each call is O(k) in the number of
// prompts bound to this agent rather than O(N) in the namespace prompt
// count. Falls back to the legacy full-List path when the index is
// missing (unit tests that skip the manager bootstrap).
func (r *NyxAgentReconciler) listNyxPromptsForAgent(ctx context.Context, agent *nyxv1alpha1.NyxAgent) ([]nyxv1alpha1.NyxPrompt, error) {
	scoped := &nyxv1alpha1.NyxPromptList{}
	err := r.List(ctx, scoped,
		client.InNamespace(agent.Namespace),
		client.MatchingFields{NyxPromptAgentRefIndex: agent.Name},
	)
	if err == nil {
		return append([]nyxv1alpha1.NyxPrompt(nil), scoped.Items...), nil
	}
	// Distinguish "index not registered" from every other List error
	// (#901 twin): fall back to the full-list path only for the
	// specific case, propagate all others so RBAC denials and context
	// cancellations aren't masked as "index missing".
	if !isFieldIndexMissing(err) {
		return nil, err
	}
	// Index missing — fall back to the legacy full-namespace scan.
	all := &nyxv1alpha1.NyxPromptList{}
	if lErr := r.List(ctx, all, client.InNamespace(agent.Namespace)); lErr != nil {
		return nil, lErr
	}
	matched := make([]nyxv1alpha1.NyxPrompt, 0, len(all.Items))
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

func (r *NyxAgentReconciler) applyService(ctx context.Context, agent *nyxv1alpha1.NyxAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "nyxagent.applyService")
	defer finish(&err)

	desired := buildService(agent)
	if err = controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on Service: %w", err)
	}
	existing := &corev1.Service{}
	getErr := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(getErr):
		span.SetAttributes(attribute.String("nyx.resource.action", "create"))
		err = r.Create(ctx, desired)
		return err
	case getErr != nil:
		err = getErr
		return err
	}
	// Preserve ClusterIP across updates — the API server rejects attempts to
	// change it.
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	existing.Annotations = desired.Annotations
	span.SetAttributes(attribute.String("nyx.resource.action", "update"))
	err = r.Update(ctx, existing)
	return err
}

// reconcileConfigMaps applies every ConfigMap the spec currently calls for
// AND garbage-collects ConfigMaps owned by this NyxAgent that the spec no
// longer asks for (#443). Replaces the previous applyAgentConfigMap +
// applyBackendConfigMaps split, which had a known TODO about stale cleanup.
func (r *NyxAgentReconciler) reconcileConfigMaps(ctx context.Context, agent *nyxv1alpha1.NyxAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "nyxagent.reconcileConfigMaps")
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
	span.SetAttributes(attribute.Int("nyx.configmaps.desired", len(desired)))

	// Apply each desired ConfigMap.
	for _, cm := range desired {
		if err := r.applyConfigMap(ctx, agent, cm); err != nil {
			return err
		}
	}

	// Cleanup: list ConfigMaps in this namespace that carry our agent labels,
	// then delete any owned by THIS NyxAgent that are not in the desired set.
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
// every NyxAgent that shares the current agent's team label (or every
// enabled, non-deleting NyxAgent in the namespace when no team label is
// set) and writes a CM enumerating the members by name + service URL.
//
// A content-hash annotation on the CM lets us short-circuit writes when
// membership is unchanged — this matters because option (a) in the
// gap-approve comment warns every CR write triggers manifest churn, and
// without the hash every pod would see a rolling configmap update on
// every reconcile.
func (r *NyxAgentReconciler) reconcileManifestConfigMap(ctx context.Context, agent *nyxv1alpha1.NyxAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "nyxagent.reconcileManifestConfigMap",
		attribute.String("nyx.team", teamKey(agent)),
	)
	defer finish(&err)

	// Scoped List via NyxAgentTeamIndex (#753): the field indexer
	// returns only NyxAgents that share this agent's team label
	// (including the empty-string grouping when the label is absent),
	// so a namespace of hundreds of unrelated agents no longer lands a
	// full-namespace List on every manifest reconcile.  Falls back to
	// the legacy full-List path when the index is missing.
	wantTeam := teamKey(agent)
	allAgents := &nyxv1alpha1.NyxAgentList{}
	if err := r.List(ctx, allAgents,
		client.InNamespace(agent.Namespace),
		client.MatchingFields{NyxAgentTeamIndex: wantTeam},
	); err != nil {
		// Distinguish "index not registered" from every other List
		// error (#901). Previously any error fell through to the
		// full-namespace List path, silently masking RBAC denials,
		// context cancellations, and apiserver 500s as "index
		// missing" and degrading every reconcile.
		if !isFieldIndexMissing(err) {
			return fmt.Errorf("list NyxAgents for manifest (scoped): %w", err)
		}
		// Index missing — full-namespace List keeps prior behaviour.
		if err := r.List(ctx, allAgents, client.InNamespace(agent.Namespace)); err != nil {
			return fmt.Errorf("list NyxAgents for manifest: %w", err)
		}
	}

	var members []manifestMember
	var memberAgents []*nyxv1alpha1.NyxAgent
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
	span.SetAttributes(attribute.Int("nyx.members.count", len(members)))

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
	// content hash, do nothing. The hash input now includes the sorted
	// set of owner UIDs, so a membership change that coincidentally
	// leaves the rendered JSON unchanged still forces a refresh of the
	// owner refs.
	if existing.Annotations[manifestHashAnnotation] == desiredHash {
		return nil
	}
	if existing.Annotations == nil {
		existing.Annotations = map[string]string{}
	}
	existing.Annotations[manifestHashAnnotation] = desiredHash
	existing.Labels = desired.Labels
	// Converge ownerRefs to the desired multi-owner shape. This is the
	// critical write for migrating off the legacy single-controller
	// shape — we replace the whole slice rather than appending so any
	// stale ref (e.g. pointing at a deleted agent's UID) is dropped.
	existing.OwnerReferences = desired.OwnerReferences
	existing.Data = desired.Data
	return r.Update(ctx, existing)
}

func (r *NyxAgentReconciler) applyConfigMap(ctx context.Context, agent *nyxv1alpha1.NyxAgent, desired *corev1.ConfigMap) error {
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
	existing.Labels = desired.Labels
	return r.Update(ctx, existing)
}

func (r *NyxAgentReconciler) applyBackendPVCs(ctx context.Context, agent *nyxv1alpha1.NyxAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "nyxagent.applyBackendPVCs",
		attribute.Int("nyx.backends.count", len(agent.Spec.Backends)),
	)
	defer finish(&err)

	pvcs, buildErrs := buildBackendPVCs(agent)
	span.SetAttributes(
		attribute.Int("nyx.pvcs.desired", len(pvcs)),
		attribute.Int("nyx.pvcs.build_errors", len(buildErrs)),
	)
	// Surface size-parse failures so they don't get silently dropped (#454).
	// We log via the reconciler's logger and emit a Warning Event per bad
	// entry. This is non-fatal — other backends still get their PVCs.
	if len(buildErrs) > 0 {
		log := logf.FromContext(ctx)
		for _, be := range buildErrs {
			log.Error(be.Err, "skipping backend PVC", "backend", be.BackendName, "size", be.Size)
			nyxagentPVCBuildErrorsTotal.WithLabelValues(be.BackendName).Inc()
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
		span.AddEvent("pvc.apply.begin", trace.WithAttributes(attribute.String("nyx.pvc.name", d.Name)))
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
			span.AddEvent("pvc.created", trace.WithAttributes(attribute.String("nyx.pvc.name", d.Name)))
			continue
		case err != nil:
			return err
		}
		// PVC specs are largely immutable after creation; only labels are
		// reconciled in-place. Size changes would need an expand-volume flow.
		existing.Labels = d.Labels
		if err := r.Update(ctx, existing); err != nil {
			return err
		}
		span.AddEvent("pvc.updated", trace.WithAttributes(attribute.String("nyx.pvc.name", d.Name)))
	}
	// Cleanup: list PVCs in this namespace that carry our agent labels, then
	// delete any owned by THIS NyxAgent that are not in the desired set.
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
// shared-storage PVC (#481). The PVC is only produced when the NyxAgent
// sets sharedStorage.enabled=true with storageType=pvc (default) and no
// existingClaim — mirroring the chart's `pvc.yaml` branch gated on the
// same three conditions. When any of those flip, or when storageType is
// hostPath (#611), the reconciler deletes any PVC it previously created
// (tracked by the `component=shared-storage` label). The backend PVC
// reconciler skips PVCs bearing this component label so the two paths
// never step on each other.
func (r *NyxAgentReconciler) reconcileSharedStoragePVC(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
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
	// PVC specs are largely immutable post-creation; reconcile labels
	// only so cleanup selectors stay accurate across spec edits.
	existing.Labels = desired.Labels
	if err := r.Update(ctx, existing); err != nil {
		return fmt.Errorf("update shared-storage PVC: %w", err)
	}
	return nil
}

// reconcileHPA creates, updates, or deletes the HPA to match spec.
func (r *NyxAgentReconciler) reconcileHPA(ctx context.Context, agent *nyxv1alpha1.NyxAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "nyxagent.reconcileHPA")
	defer finish(&err)

	desired := buildHPA(agent)
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}
	if desired == nil {
		span.SetAttributes(attribute.String("nyx.resource.action", "delete-if-present"))
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
func (r *NyxAgentReconciler) reconcilePDB(ctx context.Context, agent *nyxv1alpha1.NyxAgent) (err error) {
	ctx, span, finish := startStepSpan(ctx, "nyxagent.reconcilePDB")
	defer finish(&err)

	desired := buildPDB(agent)
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}
	if desired == nil {
		span.SetAttributes(attribute.String("nyx.resource.action", "delete-if-present"))
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
// from running.  All non-nil errors are accumulated via ``errors.Join``
// and returned together.  Each failure also increments
// ``nyxagent_teardown_step_errors_total{kind,reason}`` so operators can
// alert on stuck-delete patterns without grepping reconcile logs.
func (r *NyxAgentReconciler) teardownDisabledAgent(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}

	var teardownErrs []error
	recordErr := func(kind, reason string, err error) {
		if err == nil {
			return
		}
		nyxagentTeardownStepErrorsTotal.WithLabelValues(kind, reason).Inc()
		teardownErrs = append(teardownErrs, fmt.Errorf("%s %s: %w", reason, kind, err))
	}

	// Helper closure: fetch the resource at `key`, delete only if owned
	// by this NyxAgent. Missing-object is not an error.  Returns (getErr,
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
	// Drop the per-CR dashboard gauge so the metric series doesn't
	// linger across enable/disable cycles.
	nyxagentDashboardEnabled.DeleteLabelValues(agent.Namespace, agent.Name)
	return errors.Join(teardownErrs...)
}

// finalizeNyxAgent runs the explicit cleanup path invoked when a NyxAgent is
// being deleted (#569). OwnerReferences already cascade to owned cluster
// resources, but the operator also needs to drop the per-CR Prometheus
// gauge series and proactively delete resources the controller manages —
// both are achieved by reusing the teardown path used for spec.enabled=false,
// which covers Deployment, Service, HPA, PDB, ConfigMaps, PVCs, the
// dashboard stack, and the `nyxagent_dashboard_enabled` gauge.
func (r *NyxAgentReconciler) finalizeNyxAgent(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	return r.teardownDisabledAgent(ctx, agent)
}

// reconcileDashboard creates, updates, or deletes the per-agent dashboard
// ConfigMap + Deployment + Service to match NyxAgent.spec.dashboard (#470).
// The ConfigMap holds the nginx template that routes /api/agents/<name>/...
// directly to the owned agent's service, matching the direct-routing
// architecture the Helm chart uses cluster-wide.
func (r *NyxAgentReconciler) reconcileDashboard(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	return r.reconcileDashboardInternal(ctx, agent, false)
}

// reconcileDashboardInternal implements the dashboard reconcile with an
// explicit forceDelete flag used by teardownDisabledAgent/finalizeNyxAgent
// (#682). When forceDelete is true, the function skips the apply path and
// runs the existing Delete block unconditionally, even when
// spec.dashboard.enabled=true, so dashboard pods do not linger pointing at
// an already-removed harness Service.
func (r *NyxAgentReconciler) reconcileDashboardInternal(ctx context.Context, agent *nyxv1alpha1.NyxAgent, forceDelete bool) (err error) {
	dashboardEnabled := agent.Spec.Dashboard != nil && agent.Spec.Dashboard.Enabled
	ctx, _, finish := startStepSpan(ctx, "nyxagent.reconcileDashboard",
		attribute.Bool("nyx.dashboard.enabled", dashboardEnabled),
		attribute.Bool("nyx.dashboard.force_delete", forceDelete),
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
	// can sum() across all NyxAgents to count adoption (#471).
	{
		val := 0.0
		if agent.Spec.Dashboard != nil && agent.Spec.Dashboard.Enabled {
			val = 1.0
		}
		nyxagentDashboardEnabled.WithLabelValues(agent.Namespace, agent.Name).Set(val)
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
// long enough that a high-churn NyxAgent workload stops hammering the
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

// serviceMonitorCRDPresent reports whether the cluster has the
// monitoring.coreos.com/v1 ServiceMonitor REST mapping registered. Uses
// the RESTMapper on the shared client so the probe is a cache lookup on
// steady state rather than an apiserver round trip per reconcile. A
// NoKindMatchError (or any IsNoMatchError) means the CRD is not
// installed — the reconciler treats that as a clean no-op. Other errors
// propagate so they surface in status + the retry loop.
//
// Results are cached for ``crdProbeTTL`` (#756) so a high-churn
// NyxAgent workload does not re-probe the RESTMapper on every
// reconcile. A fresh install of the Prometheus Operator CRDs is
// picked up within that TTL without operator restart.
func (r *NyxAgentReconciler) serviceMonitorCRDPresent(ctx context.Context) (bool, error) {
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
//  1. The NyxAgent opted in via spec.serviceMonitor.enabled=true.
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
func (r *NyxAgentReconciler) reconcileServiceMonitor(ctx context.Context, agent *nyxv1alpha1.NyxAgent) (err error) {
	ctx, _, finish := startStepSpan(ctx, "nyxagent.reconcileServiceMonitor",
		attribute.Bool("nyx.servicemonitor.enabled", serviceMonitorEnabled(agent)),
		attribute.Bool("nyx.metrics.enabled", agent.Spec.Metrics.Enabled),
	)
	defer finish(&err)

	log := logf.FromContext(ctx)

	// Probe CRD presence. An absent CRD means we can't read or write
	// the object at all; short-circuit both the apply and delete paths.
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
			return fmt.Errorf("get ServiceMonitor for delete: %w", err)
		}
		if !metav1.IsControlledBy(existing, agent) {
			return nil
		}
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
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
			return fmt.Errorf("create ServiceMonitor: %w", err)
		}
		return nil
	case err != nil:
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
		return fmt.Errorf("update ServiceMonitor: %w", err)
	}
	return nil
}

// podMonitorCRDPresent reports whether the monitoring.coreos.com/v1
// PodMonitor CRD is known to the cluster. Mirrors serviceMonitorCRDPresent
// and shares its short-TTL result cache (#756).
func (r *NyxAgentReconciler) podMonitorCRDPresent(ctx context.Context) (bool, error) {
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
func (r *NyxAgentReconciler) reconcilePodMonitor(ctx context.Context, agent *nyxv1alpha1.NyxAgent) (err error) {
	ctx, _, finish := startStepSpan(ctx, "nyxagent.reconcilePodMonitor",
		attribute.Bool("nyx.podmonitor.enabled", podMonitorEnabled(agent)),
		attribute.Bool("nyx.metrics.enabled", agent.Spec.Metrics.Enabled),
	)
	defer finish(&err)

	log := logf.FromContext(ctx)

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
			return fmt.Errorf("get PodMonitor for delete: %w", err)
		}
		if !metav1.IsControlledBy(existing, agent) {
			return nil
		}
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
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
			return fmt.Errorf("create PodMonitor: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get PodMonitor: %w", err)
	}

	if !metav1.IsControlledBy(existing, agent) {
		return nil
	}
	existing.Object["spec"] = desired.Object["spec"]
	existing.SetLabels(desired.GetLabels())
	if err := r.Update(ctx, existing); err != nil {
		return fmt.Errorf("update PodMonitor: %w", err)
	}
	return nil
}

// ── Status ────────────────────────────────────────────────────────────────────

func (r *NyxAgentReconciler) updateStatus(ctx context.Context, agent *nyxv1alpha1.NyxAgent, reconcileErr error) (err error) {
	ctx, _, finish := startStepSpan(ctx, "nyxagent.updateStatus",
		attribute.Bool("nyx.reconcile.had_error", reconcileErr != nil),
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
		Type:               nyxv1alpha1.ConditionReconcileSuccess,
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
		Type:               nyxv1alpha1.ConditionAvailable,
		LastTransitionTime: now,
		ObservedGeneration: agent.Generation,
	}
	progCond := metav1.Condition{
		Type:               nyxv1alpha1.ConditionProgressing,
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
		newStatus.Phase = nyxv1alpha1.NyxAgentPhasePending
		availCond.Status = metav1.ConditionFalse
		availCond.Reason = "DeploymentMissing"
		availCond.Message = "Agent Deployment does not yet exist."
		progCond.Status = metav1.ConditionTrue
		progCond.Reason = "Creating"
		progCond.Message = "Creating the agent Deployment."
	case depErr != nil:
		newStatus.Phase = nyxv1alpha1.NyxAgentPhaseError
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
			newStatus.Phase = nyxv1alpha1.NyxAgentPhaseError
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
			newStatus.Phase = nyxv1alpha1.NyxAgentPhaseReady
			availCond.Status = metav1.ConditionTrue
			availCond.Reason = "AllReplicasReady"
			availCond.Message = fmt.Sprintf("%d/%d replicas ready.", dep.Status.ReadyReplicas, desired)
			progCond.Status = metav1.ConditionFalse
			progCond.Reason = "Deployed"
			progCond.Message = "Rollout complete."
		default:
			newStatus.Phase = nyxv1alpha1.NyxAgentPhaseDegraded
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

	// Skip the write if nothing changed (avoids status churn).
	if statusEqual(agent.Status, *newStatus) {
		return nil
	}
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
		nyxagentPhaseTransitionsTotal.WithLabelValues(string(previousPhase), string(newStatus.Phase)).Inc()
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
func (r *NyxAgentReconciler) recordPhaseTransitionEvent(
	agent *nyxv1alpha1.NyxAgent,
	from, to nyxv1alpha1.NyxAgentPhase,
	desiredReplicas int32,
	reconcileErr error,
) {
	switch to {
	case nyxv1alpha1.NyxAgentPhaseReady:
		r.Recorder.Eventf(agent, corev1.EventTypeNormal, "Ready",
			"NyxAgent transitioned %s → Ready (%d/%d replicas)",
			from, agent.Status.ReadyReplicas, desiredReplicas)
	case nyxv1alpha1.NyxAgentPhaseDegraded:
		r.Recorder.Eventf(agent, corev1.EventTypeWarning, "Degraded",
			"NyxAgent transitioned %s → Degraded (%d/%d replicas ready)",
			from, agent.Status.ReadyReplicas, desiredReplicas)
	case nyxv1alpha1.NyxAgentPhaseError:
		msg := "reconcile failed"
		if reconcileErr != nil {
			msg = reconcileErr.Error()
		}
		r.Recorder.Eventf(agent, corev1.EventTypeWarning, "ReconcileError",
			"NyxAgent transitioned %s → Error: %s", from, msg)
	case nyxv1alpha1.NyxAgentPhasePending:
		r.Recorder.Eventf(agent, corev1.EventTypeNormal, "Pending",
			"NyxAgent transitioned %s → Pending", from)
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

// statusEqual is a shallow equality check sufficient for deciding whether to
// write the status subresource. Conditions compared by (type, status, reason,
// message, observedGeneration) only.
func statusEqual(a, b nyxv1alpha1.NyxAgentStatus) bool {
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
func (r *NyxAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// enqueueTeammates requeues every NyxAgent that shares a team
	// (or namespace group) with the object that just changed, so
	// team membership edits propagate into the manifest within one
	// reconcile cycle regardless of which CR was mutated (#474).
	enqueueTeammates := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		trigger, ok := obj.(*nyxv1alpha1.NyxAgent)
		if !ok {
			return nil
		}
		triggerTeam := ""
		if trigger.Labels != nil {
			triggerTeam = trigger.Labels[teamLabel]
		}
		// Scoped List via NyxAgentTeamIndex (#753) so every create/
		// delete/label edit no longer fans out an O(namespace) List
		// against the cache. The index is registered in
		// cmd/main.go; when absent we fall back to a full-namespace
		// List so the existing behaviour is preserved.
		peers := &nyxv1alpha1.NyxAgentList{}
		err := mgr.GetClient().List(ctx, peers,
			client.InNamespace(trigger.Namespace),
			client.MatchingFields{NyxAgentTeamIndex: triggerTeam},
		)
		if err != nil {
			if fbErr := mgr.GetClient().List(ctx, peers, client.InNamespace(trigger.Namespace)); fbErr != nil {
				return nil
			}
		}
		reqs := make([]reconcile.Request, 0, len(peers.Items))
		for i := range peers.Items {
			p := &peers.Items[i]
			peerTeam := ""
			if p.Labels != nil {
				peerTeam = p.Labels[teamLabel]
			}
			if peerTeam != triggerTeam {
				continue
			}
			if p.Namespace == trigger.Namespace && p.Name == trigger.Name {
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
	})

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
			if a, ok := e.ObjectOld.(*nyxv1alpha1.NyxAgent); ok {
				oldPort = a.Spec.Port
			}
			if a, ok := e.ObjectNew.(*nyxv1alpha1.NyxAgent); ok {
				newPort = a.Spec.Port
			}
			return oldTeam != newTeam || oldPort != newPort
		},
	}

	// enqueueAgentsBoundByPrompt re-enqueues every NyxAgent listed in a
	// NyxPrompt's spec.agentRefs when the NyxPrompt changes. This keeps
	// the agent Deployment pod-spec in sync with prompt adds/removes
	// without waiting for a future unrelated reconcile to pick up the
	// new prompt list.
	enqueueAgentsBoundByPrompt := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		p, ok := obj.(*nyxv1alpha1.NyxPrompt)
		if !ok {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(p.Spec.AgentRefs))
		for _, ref := range p.Spec.AgentRefs {
			reqs = append(reqs, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: p.Namespace, Name: ref.Name},
			})
		}
		return reqs
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&nyxv1alpha1.NyxAgent{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Watches(&nyxv1alpha1.NyxAgent{}, enqueueTeammates, builder.WithPredicates(teamPredicate)).
		Watches(&nyxv1alpha1.NyxPrompt{}, enqueueAgentsBoundByPrompt).
		Named("nyxagent").
		Complete(r)
}
