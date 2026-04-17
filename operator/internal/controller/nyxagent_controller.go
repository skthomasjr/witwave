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
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

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
			// OwnerReferences take care of cascading deletion. Drop the
			// per-CR dashboard gauge series so a deleted agent doesn't
			// linger in the metrics output until process restart (#471).
			nyxagentDashboardEnabled.DeleteLabelValues(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Per-agent enabled flag (default true). When explicitly false, tear
	// down owned resources and skip reconciliation entirely. This mirrors
	// the chart's per-agent toggle (#chart beta.32) and lets operators
	// pause an agent without deleting the CR.
	if agent.Spec.Enabled != nil && !*agent.Spec.Enabled {
		log.Info("NyxAgent disabled — tearing down owned resources", "name", agent.Name)
		if err := r.teardownDisabledAgent(ctx, agent); err != nil {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}
		return ctrl.Result{}, nil
	}

	// Apply all desired resources, accumulating any error so status can
	// reflect the first failure.
	var reconcileErr error
	applied := map[string]bool{
		"deployment": false,
		"service":    false,
	}

	if err := r.applyDeployment(ctx, agent); err != nil {
		reconcileErr = err
	} else {
		applied["deployment"] = true
	}

	if err := r.applyService(ctx, agent); err != nil && reconcileErr == nil {
		reconcileErr = err
	} else if err == nil {
		applied["service"] = true
	}

	// Optional resources. Failure to apply an optional does not block the
	// whole reconcile — it is captured into the error chain.
	if err := r.reconcileConfigMaps(ctx, agent); err != nil && reconcileErr == nil {
		reconcileErr = err
	}
	if err := r.applyBackendPVCs(ctx, agent); err != nil && reconcileErr == nil {
		reconcileErr = err
	}
	if err := r.reconcileHPA(ctx, agent); err != nil && reconcileErr == nil {
		reconcileErr = err
	}
	if err := r.reconcilePDB(ctx, agent); err != nil && reconcileErr == nil {
		reconcileErr = err
	}
	// Dashboard is opt-in per agent (#470). reconcileDashboard handles
	// both the create/update path when enabled and the delete path when
	// the field is removed or toggled off, so the cluster converges
	// cleanly in either direction.
	if err := r.reconcileDashboard(ctx, agent); err != nil && reconcileErr == nil {
		reconcileErr = err
	}

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
		return ctrl.Result{RequeueAfter: 30 * time.Second}, reconcileErr
	}

	// Requeue while the Deployment is still rolling out; watches handle the
	// steady state.
	if agent.Status.Phase != nyxv1alpha1.NyxAgentPhaseReady {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// ── Apply helpers ─────────────────────────────────────────────────────────────

func (r *NyxAgentReconciler) applyDeployment(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	desired := buildDeployment(agent, DefaultImageTag)
	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on Deployment: %w", err)
	}
	existing := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return err
	}
	// Patch spec + labels; keep existing status and server-filled fields.
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	return r.Update(ctx, existing)
}

func (r *NyxAgentReconciler) applyService(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	desired := buildService(agent)
	if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on Service: %w", err)
	}
	existing := &corev1.Service{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return err
	}
	// Preserve ClusterIP across updates — the API server rejects attempts to
	// change it.
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	existing.Annotations = desired.Annotations
	return r.Update(ctx, existing)
}

// reconcileConfigMaps applies every ConfigMap the spec currently calls for
// AND garbage-collects ConfigMaps owned by this NyxAgent that the spec no
// longer asks for (#443). Replaces the previous applyAgentConfigMap +
// applyBackendConfigMaps split, which had a known TODO about stale cleanup.
func (r *NyxAgentReconciler) reconcileConfigMaps(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
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

func (r *NyxAgentReconciler) applyBackendPVCs(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	pvcs, buildErrs := buildBackendPVCs(agent)
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
	for _, desired := range pvcs {
		if err := controllerutil.SetControllerReference(agent, desired, r.Scheme); err != nil {
			return fmt.Errorf("set owner on PVC %s: %w", desired.Name, err)
		}
		existing := &corev1.PersistentVolumeClaim{}
		err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
		switch {
		case apierrors.IsNotFound(err):
			if err := r.Create(ctx, desired); err != nil {
				return err
			}
			continue
		case err != nil:
			return err
		}
		// PVC specs are largely immutable after creation; only labels are
		// reconciled in-place. Size changes would need an expand-volume flow.
		existing.Labels = desired.Labels
		if err := r.Update(ctx, existing); err != nil {
			return err
		}
	}
	return nil
}

// reconcileHPA creates, updates, or deletes the HPA to match spec.
func (r *NyxAgentReconciler) reconcileHPA(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	desired := buildHPA(agent)
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}

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
	err := r.Get(ctx, key, existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return err
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	return r.Update(ctx, existing)
}

// reconcilePDB creates, updates, or deletes the PDB to match spec.
func (r *NyxAgentReconciler) reconcilePDB(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	desired := buildPDB(agent)
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}

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
	err := r.Get(ctx, key, existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, desired)
	case err != nil:
		return err
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	return r.Update(ctx, existing)
}

// teardownDisabledAgent deletes every owned resource for an agent whose
// spec.enabled has been flipped to false. The delete is gated on
// IsControlledBy so we never touch resources we didn't create. Status
// is left untouched — the next reconcile after re-enabling will rewrite
// it from observed Deployment state.
func (r *NyxAgentReconciler) teardownDisabledAgent(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	key := client.ObjectKey{Namespace: agent.Namespace, Name: agent.Name}

	// Helper closure: fetch the resource at `key`, delete only if owned
	// by this NyxAgent. Missing-object is not an error.
	tryDelete := func(obj client.Object) error {
		if err := r.Get(ctx, key, obj); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if !metav1.IsControlledBy(obj, agent) {
			return nil
		}
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}

	// Order: Deployment → Service → optional resources. Doesn't matter
	// for correctness (k8s GC handles dependents) but makes log streams
	// easier to read.
	if err := tryDelete(&appsv1.Deployment{}); err != nil {
		return fmt.Errorf("delete Deployment: %w", err)
	}
	if err := tryDelete(&corev1.Service{}); err != nil {
		return fmt.Errorf("delete Service: %w", err)
	}
	if err := tryDelete(&autoscalingv2.HorizontalPodAutoscaler{}); err != nil {
		return fmt.Errorf("delete HPA: %w", err)
	}
	if err := tryDelete(&policyv1.PodDisruptionBudget{}); err != nil {
		return fmt.Errorf("delete PDB: %w", err)
	}
	// reconcileDashboard already handles the dashboard delete path when
	// spec.dashboard.enabled is false — call it so the dashboard
	// resources get cleaned up as part of the agent disable.
	if err := r.reconcileDashboard(ctx, agent); err != nil {
		return fmt.Errorf("teardown dashboard: %w", err)
	}
	// Drop the per-CR dashboard gauge so the metric series doesn't
	// linger across enable/disable cycles.
	nyxagentDashboardEnabled.DeleteLabelValues(agent.Namespace, agent.Name)
	return nil
}

// reconcileDashboard creates, updates, or deletes the per-agent dashboard
// ConfigMap + Deployment + Service to match NyxAgent.spec.dashboard (#470).
// The ConfigMap holds the nginx template that routes /api/agents/<name>/...
// directly to the owned agent's service, matching the direct-routing
// architecture the Helm chart uses cluster-wide.
func (r *NyxAgentReconciler) reconcileDashboard(ctx context.Context, agent *nyxv1alpha1.NyxAgent) error {
	desiredCM := buildDashboardConfigMap(agent)
	desiredDep := buildDashboardDeployment(agent, DefaultImageTag)
	desiredSvc := buildDashboardService(agent)
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

// ── Status ────────────────────────────────────────────────────────────────────

func (r *NyxAgentReconciler) updateStatus(ctx context.Context, agent *nyxv1alpha1.NyxAgent, reconcileErr error) error {
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
		case dep.Status.ReadyReplicas >= desired && desired > 0:
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
	agent.Status = *newStatus
	if err := r.Status().Update(ctx, agent); err != nil {
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&nyxv1alpha1.NyxAgent{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Named("nyxagent").
		Complete(r)
}
