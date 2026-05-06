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
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// witwaveWorkspaceFinalizer guarantees the operator observes WitwaveWorkspace deletion so
// PVCs scheduled for reclaim, owned ConfigMaps, and the per-CR metric series
// can be drained before the apiserver removes the object. The same finalizer
// also implements the refuse-delete invariant for workspaces with bound
// agents (see tmp/workspace-crd.md "WitwaveWorkspace deletion: refuse-delete
// finalizer while any agent references the workspace").
const witwaveWorkspaceFinalizer = "witwaveworkspace.witwave.ai/finalizer"

// Labels stamped on WitwaveWorkspace-owned resources so the dual-check
// IsControlledBy + label pattern can find them without re-querying the CRs.
const (
	// componentWitwaveWorkspaceVolume identifies operator-owned PVCs reconciled
	// for a WitwaveWorkspace.Spec.Volumes entry.
	componentWitwaveWorkspaceVolume = "witwaveworkspace-volume"

	// componentWitwaveWorkspaceConfigFile identifies operator-owned ConfigMaps
	// rendered for a WitwaveWorkspace.Spec.ConfigFiles[].Inline entry.
	componentWitwaveWorkspaceConfigFile = "witwaveworkspace-configfile"

	// labelWitwaveWorkspaceName identifies the parent WitwaveWorkspace on every owned
	// PVC / ConfigMap. Used by cleanup paths to scope List calls to the
	// CR's resources without scanning the namespace.
	labelWitwaveWorkspaceName = "witwave.ai/witwaveworkspace"

	// labelWitwaveWorkspaceVolumeName identifies which Spec.Volumes entry an
	// owned PVC was reconciled for.
	labelWitwaveWorkspaceVolumeName = "witwave.ai/witwaveworkspace-volume"

	// labelWitwaveWorkspaceConfigFileName identifies which inline ConfigFile
	// entry an owned ConfigMap was reconciled for.
	labelWitwaveWorkspaceConfigFileName = "witwave.ai/witwaveworkspace-configfile"
)

// WitwaveWorkspaceReconciler reconciles a WitwaveWorkspace object.
//
// Three concerns per reconcile:
//
//  1. Provision one PVC per Spec.Volumes[] (IsControlledBy guarded). PVCs
//     whose ReclaimPolicy is Retain are kept on WitwaveWorkspace deletion;
//     Delete-mode PVCs are removed alongside the parent.
//  2. Render one ConfigMap per Spec.ConfigFiles[].Inline (IsControlledBy
//     guarded with the labelWitwaveWorkspaceConfigFileName dual-check pattern).
//  3. Maintain Status.BoundAgents as the inverted index over WitwaveAgents
//     whose Spec.WorkspaceRefs reference this WitwaveWorkspace.
//
// On deletion the controller refuses to clear its finalizer while
// Status.BoundAgents is non-empty so a WitwaveWorkspace cannot disappear out from
// under a still-attached WitwaveAgent. Operators clear the block by
// dropping the WitwaveWorkspaceRefs entry on each affected agent.
type WitwaveWorkspaceReconciler struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
}

// +kubebuilder:rbac:groups=witwave.ai,resources=witwaveworkspaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=witwave.ai,resources=witwaveworkspaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=witwave.ai,resources=witwaveworkspaces/finalizers,verbs=update

// Reconcile is the control loop entry point.
func (r *WitwaveWorkspaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	ws := &witwavev1alpha1.WitwaveWorkspace{}
	if err := r.Get(ctx, req.NamespacedName, ws); err != nil {
		if apierrors.IsNotFound(err) {
			witwaveWorkspaceBoundAgents.DeleteLabelValues(req.Namespace, req.Name)
			witwaveWorkspaceVolumesProvisioned.DeleteLabelValues(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Always refresh Status.BoundAgents up front (cheap, used by both the
	// happy path and the refuse-delete check below).
	bound, err := r.indexBoundAgents(ctx, ws)
	if err != nil {
		witwaveWorkspaceReconcileTotal.WithLabelValues("error").Inc()
		return ctrl.Result{}, fmt.Errorf("index bound agents: %w", err)
	}

	// Deletion path. Drain owned resources whose ReclaimPolicy permits it,
	// then refuse to remove the finalizer while any agent still references
	// the workspace.
	if !ws.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(ws, witwaveWorkspaceFinalizer) {
			if len(bound) > 0 {
				_ = r.patchStatus(ctx, ws, bound, []metav1.Condition{
					{
						Type:    witwavev1alpha1.WitwaveWorkspaceConditionDeletionBlocked,
						Status:  metav1.ConditionTrue,
						Reason:  "BoundAgents",
						Message: fmt.Sprintf("%d agent(s) still reference this WitwaveWorkspace; remove the workspaceRefs entry on each before deletion completes", len(bound)),
					},
				})
				witwaveWorkspaceReconcileTotal.WithLabelValues("delete_blocked").Inc()
				// Don't requeue with an error — the watch on
				// WitwaveAgent will re-enqueue once the last ref drops.
				return ctrl.Result{}, nil
			}
			if err := r.deleteRetainEligibleResources(ctx, ws); err != nil {
				witwaveWorkspaceReconcileTotal.WithLabelValues("error").Inc()
				return ctrl.Result{}, fmt.Errorf("delete owned resources: %w", err)
			}
			before := ws.DeepCopy()
			controllerutil.RemoveFinalizer(ws, witwaveWorkspaceFinalizer)
			if err := r.Patch(ctx, ws, client.MergeFrom(before)); err != nil {
				return ctrl.Result{}, fmt.Errorf("remove workspace finalizer: %w", err)
			}
			witwaveWorkspaceBoundAgents.DeleteLabelValues(ws.Namespace, ws.Name)
			witwaveWorkspaceVolumesProvisioned.DeleteLabelValues(ws.Namespace, ws.Name)
		}
		witwaveWorkspaceReconcileTotal.WithLabelValues("deleted").Inc()
		return ctrl.Result{}, nil
	}

	// Add the finalizer on first observation.
	if !controllerutil.ContainsFinalizer(ws, witwaveWorkspaceFinalizer) {
		before := ws.DeepCopy()
		if controllerutil.AddFinalizer(ws, witwaveWorkspaceFinalizer) {
			if err := r.Patch(ctx, ws, client.MergeFrom(before)); err != nil {
				return ctrl.Result{}, fmt.Errorf("add workspace finalizer: %w", err)
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}

	conds := []metav1.Condition{}
	var reconcileErrs []error

	// Volumes.
	if err := r.reconcileVolumes(ctx, ws); err != nil {
		reconcileErrs = append(reconcileErrs, fmt.Errorf("reconcile volumes: %w", err))
		conds = append(conds, metav1.Condition{
			Type:    witwavev1alpha1.WitwaveWorkspaceConditionVolumesProvisioned,
			Status:  metav1.ConditionFalse,
			Reason:  "VolumeReconcileFailed",
			Message: truncateMessage(err.Error(), 256),
		})
	} else {
		conds = append(conds, metav1.Condition{
			Type:    witwavev1alpha1.WitwaveWorkspaceConditionVolumesProvisioned,
			Status:  metav1.ConditionTrue,
			Reason:  "AllProvisioned",
			Message: fmt.Sprintf("%d volume(s) provisioned", len(ws.Spec.Volumes)),
		})
	}

	// Inline ConfigMaps.
	if err := r.reconcileConfigFiles(ctx, ws); err != nil {
		reconcileErrs = append(reconcileErrs, fmt.Errorf("reconcile configFiles: %w", err))
		conds = append(conds, metav1.Condition{
			Type:    witwavev1alpha1.WitwaveWorkspaceConditionConfigMapsRendered,
			Status:  metav1.ConditionFalse,
			Reason:  "ConfigMapReconcileFailed",
			Message: truncateMessage(err.Error(), 256),
		})
	} else {
		inlineCount := 0
		for _, cf := range ws.Spec.ConfigFiles {
			if cf.Inline != nil {
				inlineCount++
			}
		}
		conds = append(conds, metav1.Condition{
			Type:    witwavev1alpha1.WitwaveWorkspaceConditionConfigMapsRendered,
			Status:  metav1.ConditionTrue,
			Reason:  "AllRendered",
			Message: fmt.Sprintf("%d inline ConfigMap(s) rendered", inlineCount),
		})
	}

	conds = append(conds, metav1.Condition{
		Type:    witwavev1alpha1.WitwaveWorkspaceConditionBoundAgentsTracked,
		Status:  metav1.ConditionTrue,
		Reason:  "Indexed",
		Message: fmt.Sprintf("%d bound agent(s)", len(bound)),
	})

	readyStatus := metav1.ConditionTrue
	readyReason := "Ready"
	readyMessage := "WitwaveWorkspace reconciled successfully"
	if len(reconcileErrs) > 0 {
		readyStatus = metav1.ConditionFalse
		readyReason = "ReconcileFailed"
		readyMessage = errors.Join(reconcileErrs...).Error()
	}
	conds = append(conds, metav1.Condition{
		Type:    witwavev1alpha1.WitwaveWorkspaceConditionReady,
		Status:  readyStatus,
		Reason:  readyReason,
		Message: truncateMessage(readyMessage, 256),
	})

	if err := r.patchStatus(ctx, ws, bound, conds); err != nil {
		reconcileErrs = append(reconcileErrs, fmt.Errorf("patch status: %w", err))
	}

	witwaveWorkspaceBoundAgents.WithLabelValues(ws.Namespace, ws.Name).Set(float64(len(bound)))
	witwaveWorkspaceVolumesProvisioned.WithLabelValues(ws.Namespace, ws.Name).Set(float64(len(ws.Spec.Volumes)))

	if len(reconcileErrs) > 0 {
		witwaveWorkspaceReconcileTotal.WithLabelValues("error").Inc()
		joined := errors.Join(reconcileErrs...)
		log.Error(joined, "WitwaveWorkspace reconcile encountered errors")
		return ctrl.Result{}, joined
	}
	witwaveWorkspaceReconcileTotal.WithLabelValues("success").Inc()
	return ctrl.Result{}, nil
}

// indexBoundAgents lists every WitwaveAgent in the workspace's namespace
// and returns the subset whose Spec.WorkspaceRefs references this CR. The
// list is sorted by name so the rendered Status.BoundAgents is byte-stable
// across reconciles (avoiding spurious status patches).
func (r *WitwaveWorkspaceReconciler) indexBoundAgents(ctx context.Context, ws *witwavev1alpha1.WitwaveWorkspace) ([]witwavev1alpha1.WitwaveWorkspaceBoundAgent, error) {
	agents := &witwavev1alpha1.WitwaveAgentList{}
	if err := r.List(ctx, agents, client.InNamespace(ws.Namespace)); err != nil {
		return nil, err
	}
	var bound []witwavev1alpha1.WitwaveWorkspaceBoundAgent
	for i := range agents.Items {
		a := &agents.Items[i]
		for _, ref := range a.Spec.WorkspaceRefs {
			if ref.Name == ws.Name {
				bound = append(bound, witwavev1alpha1.WitwaveWorkspaceBoundAgent{
					Name:      a.Name,
					Namespace: a.Namespace,
				})
				break
			}
		}
	}
	sort.Slice(bound, func(i, j int) bool { return bound[i].Name < bound[j].Name })
	return bound, nil
}

// reconcileVolumes creates/updates one PVC per Spec.Volumes entry. Owned
// PVCs that no longer correspond to a spec entry are deleted unconditionally
// (their ReclaimPolicy is consulted only at WitwaveWorkspace-deletion time, since
// dropping a volume from the spec is a deliberate operator action).
func (r *WitwaveWorkspaceReconciler) reconcileVolumes(ctx context.Context, ws *witwavev1alpha1.WitwaveWorkspace) error {
	desired := map[string]*corev1.PersistentVolumeClaim{}
	for i := range ws.Spec.Volumes {
		vol := ws.Spec.Volumes[i]
		pvc, err := r.buildVolumePVC(ws, &vol)
		if err != nil {
			return fmt.Errorf("build PVC for volume %q: %w", vol.Name, err)
		}
		desired[pvc.Name] = pvc
	}

	for _, pvc := range desired {
		if err := r.applyOwnedPVC(ctx, ws, pvc); err != nil {
			return err
		}
	}

	// GC pass: remove PVCs we own that aren't in the desired set.
	existing := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, existing,
		client.InNamespace(ws.Namespace),
		client.MatchingLabels{
			labelManagedBy:            managedBy,
			labelWitwaveWorkspaceName: ws.Name,
			labelComponent:            componentWitwaveWorkspaceVolume,
		},
	); err != nil {
		return fmt.Errorf("list owned PVCs: %w", err)
	}
	for i := range existing.Items {
		pvc := &existing.Items[i]
		if _, keep := desired[pvc.Name]; keep {
			continue
		}
		if !metav1.IsControlledBy(pvc, ws) {
			continue
		}
		if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale PVC %s: %w", pvc.Name, err)
		}
	}
	return nil
}

// buildVolumePVC renders the PVC for one Spec.Volumes entry. Volume name +
// PVC name are stable so re-reconciles converge on the same object.
func (r *WitwaveWorkspaceReconciler) buildVolumePVC(ws *witwavev1alpha1.WitwaveWorkspace, vol *witwavev1alpha1.WitwaveWorkspaceVolume) (*corev1.PersistentVolumeClaim, error) {
	accessMode := vol.AccessMode
	if accessMode == "" {
		accessMode = corev1.ReadWriteMany
	}
	resources := corev1.VolumeResourceRequirements{}
	if vol.Size != nil {
		resources.Requests = corev1.ResourceList{
			corev1.ResourceStorage: *vol.Size,
		}
	} else {
		// Provide a small default so the PVC is bindable even when the
		// CR omits sizing — admission rejects this in production but
		// unit tests that bypass the webhook still need a valid PVC.
		q, _ := resource.ParseQuantity("1Gi")
		resources.Requests = corev1.ResourceList{
			corev1.ResourceStorage: q,
		}
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      WitwaveWorkspaceVolumePVCName(ws.Name, vol.Name),
			Namespace: ws.Namespace,
			Labels: map[string]string{
				labelName:                       ws.Name,
				labelComponent:                  componentWitwaveWorkspaceVolume,
				labelPartOf:                     partOf,
				labelManagedBy:                  managedBy,
				labelWitwaveWorkspaceName:       ws.Name,
				labelWitwaveWorkspaceVolumeName: vol.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{accessMode},
			StorageClassName: vol.StorageClassName,
			Resources:        resources,
		},
	}
	if err := controllerutil.SetControllerReference(ws, pvc, r.Scheme); err != nil {
		return nil, err
	}
	return pvc, nil
}

// applyOwnedPVC creates the PVC when it doesn't exist and otherwise
// preserves it as-is — PVC.Spec is largely immutable post-creation, so the
// reconciler must not try to overwrite Resources/StorageClassName on
// Update. Labels are merged (operator-owned keys overwritten, foreign keys
// preserved) so adopting actors like ArgoCD don't see flap.
func (r *WitwaveWorkspaceReconciler) applyOwnedPVC(ctx context.Context, ws *witwavev1alpha1.WitwaveWorkspace, desired *corev1.PersistentVolumeClaim) error {
	existing := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	// Refuse to adopt a PVC we don't already own — the IsControlledBy
	// dual-check prevents the operator from touching a user-created PVC
	// that happens to share the rendered name.
	if !metav1.IsControlledBy(existing, ws) {
		return fmt.Errorf("PVC %s/%s exists but is not controlled by WitwaveWorkspace %s; refusing to adopt", existing.Namespace, existing.Name, ws.Name)
	}
	existing.Labels = mergeOwnedStringMap(existing.Labels, desired.Labels, witwaveWorkspaceOwnedLabelKeys)
	return r.Update(ctx, existing)
}

var witwaveWorkspaceOwnedLabelKeys = []string{
	labelName,
	labelComponent,
	labelPartOf,
	labelManagedBy,
	labelWitwaveWorkspaceName,
	labelWitwaveWorkspaceVolumeName,
	labelWitwaveWorkspaceConfigFileName,
}

// reconcileConfigFiles renders one operator-owned ConfigMap per Inline entry
// and GCs any owned ConfigMaps no longer in the desired set.
func (r *WitwaveWorkspaceReconciler) reconcileConfigFiles(ctx context.Context, ws *witwavev1alpha1.WitwaveWorkspace) error {
	desired := map[string]*corev1.ConfigMap{}
	for i := range ws.Spec.ConfigFiles {
		cf := ws.Spec.ConfigFiles[i]
		if cf.Inline == nil {
			continue
		}
		cm, err := r.buildInlineConfigMap(ws, &cf)
		if err != nil {
			return err
		}
		desired[cm.Name] = cm
	}

	for _, cm := range desired {
		if err := r.applyOwnedConfigMap(ctx, ws, cm); err != nil {
			return err
		}
	}

	existing := &corev1.ConfigMapList{}
	if err := r.List(ctx, existing,
		client.InNamespace(ws.Namespace),
		client.MatchingLabels{
			labelManagedBy:            managedBy,
			labelWitwaveWorkspaceName: ws.Name,
			labelComponent:            componentWitwaveWorkspaceConfigFile,
		},
	); err != nil {
		return fmt.Errorf("list owned ConfigMaps: %w", err)
	}
	for i := range existing.Items {
		cm := &existing.Items[i]
		if _, keep := desired[cm.Name]; keep {
			continue
		}
		if !metav1.IsControlledBy(cm, ws) {
			continue
		}
		if err := r.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale ConfigMap %s: %w", cm.Name, err)
		}
	}
	return nil
}

func (r *WitwaveWorkspaceReconciler) buildInlineConfigMap(ws *witwavev1alpha1.WitwaveWorkspace, cf *witwavev1alpha1.WitwaveWorkspaceConfigFile) (*corev1.ConfigMap, error) {
	if cf.Inline == nil {
		return nil, fmt.Errorf("buildInlineConfigMap called on non-inline configFile")
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      WitwaveWorkspaceInlineConfigMapName(ws.Name, cf.Inline.Name),
			Namespace: ws.Namespace,
			Labels: map[string]string{
				labelName:                           ws.Name,
				labelComponent:                      componentWitwaveWorkspaceConfigFile,
				labelPartOf:                         partOf,
				labelManagedBy:                      managedBy,
				labelWitwaveWorkspaceName:           ws.Name,
				labelWitwaveWorkspaceConfigFileName: cf.Inline.Name,
			},
		},
		Data: map[string]string{
			cf.Inline.Path: cf.Inline.Content,
		},
	}
	if err := controllerutil.SetControllerReference(ws, cm, r.Scheme); err != nil {
		return nil, err
	}
	return cm, nil
}

func (r *WitwaveWorkspaceReconciler) applyOwnedConfigMap(ctx context.Context, ws *witwavev1alpha1.WitwaveWorkspace, desired *corev1.ConfigMap) error {
	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if !metav1.IsControlledBy(existing, ws) {
		return fmt.Errorf("ConfigMap %s/%s is not controlled by WitwaveWorkspace %s; refusing to adopt", existing.Namespace, existing.Name, ws.Name)
	}
	existing.Data = desired.Data
	existing.Labels = mergeOwnedStringMap(existing.Labels, desired.Labels, witwaveWorkspaceOwnedLabelKeys)
	existing.OwnerReferences = desired.OwnerReferences
	return r.Update(ctx, existing)
}

// deleteRetainEligibleResources is invoked from the deletion path to clean
// up owned resources that the spec asked the controller to delete. PVCs
// whose ReclaimPolicy is Retain are skipped (the operator leaves them
// behind, decoupled from the workspace's lifecycle); ConfigMaps are GC'd
// via owner references automatically once the finalizer clears, so this
// path only handles PVCs.
func (r *WitwaveWorkspaceReconciler) deleteRetainEligibleResources(ctx context.Context, ws *witwavev1alpha1.WitwaveWorkspace) error {
	retain := map[string]bool{}
	for _, vol := range ws.Spec.Volumes {
		if vol.ReclaimPolicy == witwavev1alpha1.WitwaveWorkspaceVolumeReclaimPolicyRetain {
			retain[WitwaveWorkspaceVolumePVCName(ws.Name, vol.Name)] = true
		}
	}
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcs,
		client.InNamespace(ws.Namespace),
		client.MatchingLabels{
			labelManagedBy:            managedBy,
			labelWitwaveWorkspaceName: ws.Name,
			labelComponent:            componentWitwaveWorkspaceVolume,
		},
	); err != nil {
		return fmt.Errorf("list owned PVCs: %w", err)
	}
	for i := range pvcs.Items {
		pvc := &pvcs.Items[i]
		if retain[pvc.Name] {
			// Strip the owner reference so the apiserver's GC does
			// not delete the Retain'd PVC alongside the WitwaveWorkspace.
			before := pvc.DeepCopy()
			pvc.OwnerReferences = removeWitwaveWorkspaceOwnerRef(pvc.OwnerReferences, ws)
			if len(before.OwnerReferences) != len(pvc.OwnerReferences) {
				if err := r.Patch(ctx, pvc, client.MergeFrom(before)); err != nil {
					return fmt.Errorf("strip owner ref from retained PVC %s: %w", pvc.Name, err)
				}
			}
			continue
		}
		if !metav1.IsControlledBy(pvc, ws) {
			continue
		}
		if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete PVC %s: %w", pvc.Name, err)
		}
	}
	return nil
}

func removeWitwaveWorkspaceOwnerRef(refs []metav1.OwnerReference, ws *witwavev1alpha1.WitwaveWorkspace) []metav1.OwnerReference {
	out := refs[:0]
	for _, ref := range refs {
		if ref.UID == ws.UID {
			continue
		}
		out = append(out, ref)
	}
	return out
}

// patchStatus updates Status.BoundAgents + Conditions via Status().Patch
// with MergeFrom so concurrent writers on the spec don't trip 409s on
// every reconcile.
func (r *WitwaveWorkspaceReconciler) patchStatus(ctx context.Context, ws *witwavev1alpha1.WitwaveWorkspace, bound []witwavev1alpha1.WitwaveWorkspaceBoundAgent, conds []metav1.Condition) error {
	before := ws.DeepCopy()
	ws.Status.ObservedGeneration = ws.Generation
	ws.Status.BoundAgents = bound
	for _, c := range conds {
		c.LastTransitionTime = metav1.Now()
		c.ObservedGeneration = ws.Generation
		setCondition(&ws.Status.Conditions, c)
	}
	return r.Status().Patch(ctx, ws, client.MergeFrom(before))
}

// WitwaveWorkspaceVolumePVCName returns the PVC name stamped by the operator for
// a Spec.Volumes entry. Exposed so the WitwaveAgent reconciler can build
// pod volume references without re-deriving the convention.
func WitwaveWorkspaceVolumePVCName(workspaceName, volumeName string) string {
	return fmt.Sprintf("%s-vol-%s", workspaceName, volumeName)
}

// WitwaveWorkspaceInlineConfigMapName returns the ConfigMap name reconciled for an
// inline ConfigFile entry.
func WitwaveWorkspaceInlineConfigMapName(workspaceName, fileName string) string {
	// Lowercase + dash-replace any '_' or '.' the user might have typed
	// in cf.Inline.Name so the result is DNS-1123-safe. ConfigMap names
	// inherit the same constraint and admission already rejects bad
	// names on the parent CR; this is purely defensive.
	return strings.ToLower(fmt.Sprintf("%s-cf-%s", workspaceName, sanitizeForDNS(fileName)))
}

func sanitizeForDNS(s string) string {
	repl := strings.NewReplacer("_", "-", ".", "-", " ", "-")
	return repl.Replace(s)
}

// SetupWithManager wires the workspace reconciler. The reconciler watches:
//   - WitwaveWorkspace CRs (primary)
//   - PVCs and ConfigMaps owned by WitwaveWorkspace (Owns())
//   - WitwaveAgent CRs (any Spec.WorkspaceRefs change re-enqueues the
//     referenced workspaces so the inverted index stays current)
func (r *WitwaveWorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueueReferencedWitwaveWorkspaces := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		agent, ok := obj.(*witwavev1alpha1.WitwaveAgent)
		if !ok {
			return nil
		}
		seen := map[string]struct{}{}
		out := make([]reconcile.Request, 0, len(agent.Spec.WorkspaceRefs))
		for _, ref := range agent.Spec.WorkspaceRefs {
			if _, dup := seen[ref.Name]; dup {
				continue
			}
			seen[ref.Name] = struct{}{}
			out = append(out, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: agent.Namespace,
					Name:      ref.Name,
				},
			})
		}
		// Also enqueue every WitwaveWorkspace in the namespace so a transition
		// from "agent-bound" → "agent-no-longer-bound" clears
		// Status.BoundAgents promptly. Bounded by namespace so the
		// list cost stays cheap.
		all := &witwavev1alpha1.WitwaveWorkspaceList{}
		if err := mgr.GetClient().List(ctx, all, client.InNamespace(agent.Namespace)); err != nil {
			// Defensive widening — primary enqueue above already fired.
			// Log so an operator investigating a missed
			// "no-longer-bound" Status.BoundAgents update has a signal
			// instead of silently dropping the List error.
			logf.FromContext(ctx).Info(
				"witwaveworkspace agent-watch defensive list failed; primary enqueue still fired",
				"err", err.Error(), "namespace", agent.Namespace,
			)
		} else {
			for i := range all.Items {
				ws := &all.Items[i]
				if _, dup := seen[ws.Name]; dup {
					continue
				}
				seen[ws.Name] = struct{}{}
				out = append(out, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: ws.Namespace,
						Name:      ws.Name,
					},
				})
			}
		}
		return out
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&witwavev1alpha1.WitwaveWorkspace{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ConfigMap{}).
		Watches(&witwavev1alpha1.WitwaveAgent{}, enqueueReferencedWitwaveWorkspaces).
		Named("witwaveworkspace").
		Complete(r)
}
