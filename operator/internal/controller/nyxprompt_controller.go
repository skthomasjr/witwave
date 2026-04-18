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
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	yaml "sigs.k8s.io/yaml"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// Labels and annotations stamped on NyxPrompt-owned ConfigMaps so the
// cleanup pass and the agent-side mount builder can find them without
// re-querying the CRs.
const (
	// labelNyxPromptName identifies the NyxPrompt a ConfigMap was rendered
	// for. Unique per ConfigMap (one CM per (NyxPrompt, agent) pair).
	labelNyxPromptName = "nyx.ai/nyxprompt"

	// labelNyxPromptTargetAgent identifies which NyxAgent the ConfigMap is
	// rendered for. Lets the NyxAgent reconciler list matching CMs with a
	// single label selector instead of joining across NyxPrompt specs.
	labelNyxPromptTargetAgent = "nyx.ai/nyxprompt-target-agent"

	// labelNyxPromptKind is set to the spec.kind value. The agent-side
	// mount builder uses this to pick the right container directory
	// (.nyx/jobs, .nyx/tasks, …, or HEARTBEAT.md for kind=heartbeat).
	labelNyxPromptKind = "nyx.ai/nyxprompt-kind"

	// componentNyxPrompt distinguishes NyxPrompt-owned ConfigMaps from
	// the inline-config + manifest ConfigMaps the NyxAgent reconciler
	// owns. Prevents the NyxAgent CM cleanup pass from treating a
	// NyxPrompt CM as stale.
	componentNyxPrompt = "nyxprompt"

	// annotationNyxPromptFilename records the filename the ConfigMap
	// materialises into inside the pod. The agent reconciler reads this
	// when building the per-file subPath mount, so filename drift is
	// authoritative from the CM (the NyxPrompt spec can change).
	annotationNyxPromptFilename = "nyx.ai/nyxprompt-filename"
)

// NyxPromptReconciler reconciles a NyxPrompt object.
//
// One reconcile produces one ConfigMap per (NyxPrompt, target agent) pair.
// The NyxAgent reconciler watches these ConfigMaps via a label selector
// and renders them into the pod's .nyx/ tree so the harness scheduler
// treats NyxPrompt-sourced prompts indistinguishably from gitSync-
// materialised ones.
type NyxPromptReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=nyx.ai,resources=nyxprompts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nyx.ai,resources=nyxprompts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nyx.ai,resources=nyxprompts/finalizers,verbs=update

// Reconcile is the control loop entry point. See the type doc for what it does.
func (r *NyxPromptReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	prompt := &nyxv1alpha1.NyxPrompt{}
	if err := r.Get(ctx, req.NamespacedName, prompt); err != nil {
		if apierrors.IsNotFound(err) {
			// ConfigMap GC is handled by OwnerReferences. No work.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	// Snapshot for status Patch(MergeFrom) (#757) — captured before any
	// in-memory mutation so the computed patch contains only the status
	// delta, not unrelated server-side fields.
	promptBeforeStatus := prompt.DeepCopy()

	// Compute the desired ConfigMap set.
	desired := map[string]*corev1.ConfigMap{}
	bindings := make([]nyxv1alpha1.NyxPromptBinding, 0, len(prompt.Spec.AgentRefs))
	var reconcileErrs []error

	for _, ref := range prompt.Spec.AgentRefs {
		binding := nyxv1alpha1.NyxPromptBinding{AgentName: ref.Name}

		// Resolve the target agent. When absent we keep going — the
		// watch on NyxAgent re-enqueues this NyxPrompt once the agent
		// shows up, so there is no need to return an error.
		agent := &nyxv1alpha1.NyxAgent{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: prompt.Namespace, Name: ref.Name}, agent); err != nil {
			if apierrors.IsNotFound(err) {
				binding.Message = "target NyxAgent not found (will retry when it appears)"
				bindings = append(bindings, binding)
				nyxpromptBindingOutcomesTotal.WithLabelValues(ref.Name, "agent_missing").Inc()
				continue
			}
			reconcileErrs = append(reconcileErrs, fmt.Errorf("get NyxAgent %q: %w", ref.Name, err))
			binding.Message = err.Error()
			bindings = append(bindings, binding)
			nyxpromptBindingOutcomesTotal.WithLabelValues(ref.Name, "agent_missing").Inc()
			continue
		}

		cm, err := buildNyxPromptConfigMap(prompt, ref)
		if err != nil {
			reconcileErrs = append(reconcileErrs, fmt.Errorf("build ConfigMap for %q: %w", ref.Name, err))
			binding.Message = err.Error()
			bindings = append(bindings, binding)
			nyxpromptBindingOutcomesTotal.WithLabelValues(ref.Name, "build_error").Inc()
			continue
		}
		if err := controllerutil.SetControllerReference(prompt, cm, r.Scheme); err != nil {
			reconcileErrs = append(reconcileErrs, fmt.Errorf("set owner on ConfigMap %s: %w", cm.Name, err))
			binding.Message = err.Error()
			bindings = append(bindings, binding)
			nyxpromptBindingOutcomesTotal.WithLabelValues(ref.Name, "owner_error").Inc()
			continue
		}

		desired[cm.Name] = cm
		binding.ConfigMapName = cm.Name
		binding.Filename = cm.Annotations[annotationNyxPromptFilename]
		binding.Ready = true
		bindings = append(bindings, binding)
		nyxpromptBindingOutcomesTotal.WithLabelValues(ref.Name, "ready").Inc()
	}

	// Apply each desired ConfigMap.
	for _, cm := range desired {
		if err := r.applyNyxPromptConfigMap(ctx, cm); err != nil {
			reconcileErrs = append(reconcileErrs, err)
			// Flip the matching binding to Ready=false so status reflects
			// the write failure instead of the build success.
			for i := range bindings {
				if bindings[i].ConfigMapName == cm.Name {
					bindings[i].Ready = false
					bindings[i].Message = err.Error()
					// Re-classify — the earlier "ready" outcome counted
					// the build; the apply failure now drives the binding
					// back into an apply_error state for the dashboard.
					nyxpromptBindingOutcomesTotal.WithLabelValues(bindings[i].AgentName, "apply_error").Inc()
				}
			}
		}
	}

	// GC any NyxPrompt-owned ConfigMaps whose (prompt, agent) pair is no
	// longer in the spec. Controller-runtime's OwnerReferences GC covers
	// the delete-whole-prompt case; this pass handles the remove-agent-
	// from-spec case.
	existing := &corev1.ConfigMapList{}
	if err := r.List(ctx, existing,
		client.InNamespace(prompt.Namespace),
		client.MatchingLabels{
			labelManagedBy:     managedBy,
			labelNyxPromptName: prompt.Name,
		},
	); err != nil {
		reconcileErrs = append(reconcileErrs, fmt.Errorf("list owned ConfigMaps: %w", err))
	} else {
		for i := range existing.Items {
			cm := &existing.Items[i]
			if _, ok := desired[cm.Name]; ok {
				continue
			}
			if !metav1.IsControlledBy(cm, prompt) {
				continue
			}
			if err := r.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
				reconcileErrs = append(reconcileErrs, fmt.Errorf("delete stale ConfigMap %s: %w", cm.Name, err))
			}
		}
	}

	// Update status.
	var readyCount int32
	for _, b := range bindings {
		if b.Ready {
			readyCount++
		}
	}
	prompt.Status.ObservedGeneration = prompt.Generation
	prompt.Status.Bindings = bindings
	prompt.Status.ReadyCount = readyCount
	// Publish ready/desired counts as gauges so dashboards can alert on
	// partial-binding without scraping the CR status subresource (#837).
	nyxpromptReadyCount.WithLabelValues(prompt.Namespace, prompt.Name).Set(float64(readyCount))
	nyxpromptDesiredCount.WithLabelValues(prompt.Namespace, prompt.Name).Set(float64(len(prompt.Spec.AgentRefs)))

	readyCond := metav1.Condition{
		Type:               nyxv1alpha1.NyxPromptConditionReady,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: prompt.Generation,
	}
	if len(reconcileErrs) == 0 && readyCount == int32(len(prompt.Spec.AgentRefs)) {
		readyCond.Status = metav1.ConditionTrue
		readyCond.Reason = "AllBound"
		readyCond.Message = fmt.Sprintf("%d/%d agents bound.", readyCount, len(prompt.Spec.AgentRefs))
	} else {
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = "PartialBinding"
		readyCond.Message = fmt.Sprintf("%d/%d agents bound.", readyCount, len(prompt.Spec.AgentRefs))
	}
	setCondition(&prompt.Status.Conditions, readyCond)

	// Status().Patch with MergeFrom (#757) so concurrent writers on the
	// status subresource do not contend over the full object version;
	// only contested fields raise a conflict. A 409 here still requeues
	// via the returned error but no duplicate status-write is attempted
	// inside this reconcile.
	if err := r.Status().Patch(ctx, prompt, client.MergeFrom(promptBeforeStatus)); err != nil {
		reconcileErrs = append(reconcileErrs, fmt.Errorf("update status: %w", err))
	}

	if len(reconcileErrs) > 0 {
		// Join all errors so controller-runtime backoff applies.
		msg := make([]string, 0, len(reconcileErrs))
		for _, e := range reconcileErrs {
			msg = append(msg, e.Error())
		}
		log.Error(fmt.Errorf("%s", strings.Join(msg, "; ")), "NyxPrompt reconcile encountered errors")
		return ctrl.Result{}, reconcileErrs[0]
	}

	return ctrl.Result{}, nil
}

// applyNyxPromptConfigMap is the create-or-update helper for a NyxPrompt-
// owned ConfigMap. Mirrors the simpler of the two patterns in the NyxAgent
// reconciler (no content-hash short-circuit yet — prompts change infrequently
// and are small).
func (r *NyxPromptReconciler) applyNyxPromptConfigMap(ctx context.Context, desired *corev1.ConfigMap) error {
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
	existing.Annotations = desired.Annotations
	existing.OwnerReferences = desired.OwnerReferences
	return r.Update(ctx, existing)
}

// nyxPromptConfigMapName is the per-(prompt, agent) ConfigMap name. Keeping
// the prompt name first makes the CMs sort together under
// `kubectl get cm -l nyx.ai/nyxprompt=<name>`.
func nyxPromptConfigMapName(promptName, agentName string) string {
	return fmt.Sprintf("nyxprompt-%s-%s", promptName, agentName)
}

// nyxPromptFilename is the filename materialised inside the pod under the
// kind's directory. Heartbeat is a special case: the harness watches for a
// file named exactly HEARTBEAT.md.
func nyxPromptFilename(prompt *nyxv1alpha1.NyxPrompt, ref nyxv1alpha1.NyxPromptAgentRef) string {
	if prompt.Spec.Kind == nyxv1alpha1.NyxPromptKindHeartbeat {
		return "HEARTBEAT.md"
	}
	base := fmt.Sprintf("nyxprompt-%s", prompt.Name)
	if ref.FilenameSuffix != "" {
		base = fmt.Sprintf("%s-%s", base, ref.FilenameSuffix)
	}
	return base + ".md"
}

// nyxPromptMountDir is the absolute path inside the pod the prompt file
// materialises at. For heartbeat this is the file path itself; for
// directory-watched kinds it is the parent directory.
func nyxPromptMountDir(kind nyxv1alpha1.NyxPromptKind) string {
	switch kind {
	case nyxv1alpha1.NyxPromptKindJob:
		return "/home/agent/.nyx/jobs"
	case nyxv1alpha1.NyxPromptKindTask:
		return "/home/agent/.nyx/tasks"
	case nyxv1alpha1.NyxPromptKindTrigger:
		return "/home/agent/.nyx/triggers"
	case nyxv1alpha1.NyxPromptKindContinuation:
		return "/home/agent/.nyx/continuations"
	case nyxv1alpha1.NyxPromptKindWebhook:
		return "/home/agent/.nyx/webhooks"
	case nyxv1alpha1.NyxPromptKindHeartbeat:
		return "/home/agent/.nyx"
	}
	return ""
}

// buildNyxPromptConfigMap renders the ConfigMap for one (NyxPrompt, agent)
// binding. The single data key is the filename the pod will see; the value
// is the fully-assembled YAML-frontmatter-plus-body .md document.
func buildNyxPromptConfigMap(prompt *nyxv1alpha1.NyxPrompt, ref nyxv1alpha1.NyxPromptAgentRef) (*corev1.ConfigMap, error) {
	body, err := renderNyxPromptBody(prompt)
	if err != nil {
		return nil, err
	}
	filename := nyxPromptFilename(prompt, ref)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nyxPromptConfigMapName(prompt.Name, ref.Name),
			Namespace: prompt.Namespace,
			Labels: map[string]string{
				labelName:                 ref.Name,
				labelComponent:            componentNyxPrompt,
				labelPartOf:               partOf,
				labelManagedBy:            managedBy,
				labelNyxPromptName:        prompt.Name,
				labelNyxPromptTargetAgent: ref.Name,
				labelNyxPromptKind:        string(prompt.Spec.Kind),
			},
			Annotations: map[string]string{
				annotationNyxPromptFilename: filename,
			},
		},
		Data: map[string]string{
			filename: body,
		},
	}
	return cm, nil
}

// renderNyxPromptBody assembles the final .md document: optional YAML
// frontmatter between "---" fences, then the body. Frontmatter keys are
// sorted so the output is deterministic across reconciles (kubelet
// compares ConfigMap bytes, and unstable ordering would churn the mount).
func renderNyxPromptBody(prompt *nyxv1alpha1.NyxPrompt) (string, error) {
	var buf strings.Builder
	if prompt.Spec.Frontmatter != nil && len(prompt.Spec.Frontmatter.Raw) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(prompt.Spec.Frontmatter.Raw, &m); err != nil {
			return "", fmt.Errorf("frontmatter is not a JSON object: %w", err)
		}
		// sigs.k8s.io/yaml emits keys in sorted order via its internal
		// json→yaml path, so the rendered frontmatter is stable.
		fm, err := yaml.Marshal(sortMap(m))
		if err != nil {
			return "", fmt.Errorf("marshal frontmatter: %w", err)
		}
		buf.WriteString("---\n")
		buf.Write(fm)
		buf.WriteString("---\n\n")
	}
	buf.WriteString(prompt.Spec.Body)
	// Ensure a trailing newline so POSIX tools that stream the file line-
	// by-line don't drop the last line when Body omits one.
	if !strings.HasSuffix(prompt.Spec.Body, "\n") {
		buf.WriteString("\n")
	}
	return buf.String(), nil
}

// sortMap returns the input with its top-level keys in a deterministic
// order. Sub-maps are intentionally left as-is; nested order is handled
// by sigs.k8s.io/yaml's sorted-key emission for map[string]interface{}.
func sortMap(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return in
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]interface{}, len(in))
	for _, k := range keys {
		out[k] = in[k]
	}
	return out
}

// SetupWithManager wires the reconciler. NyxPrompt owns its ConfigMaps;
// changes to a NyxAgent that is referenced (or was previously referenced)
// by a NyxPrompt re-enqueue every NyxPrompt in the namespace so the
// binding list in status catches up.
func (r *NyxPromptReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueueAllPrompts := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		prompts := &nyxv1alpha1.NyxPromptList{}
		if err := mgr.GetClient().List(ctx, prompts, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(prompts.Items))
		for i := range prompts.Items {
			p := &prompts.Items[i]
			// Re-enqueue every prompt whose spec references this agent,
			// whether the agent exists or not — the "not found" case
			// above writes a status message we want to clear promptly.
			for _, ref := range p.Spec.AgentRefs {
				if ref.Name == obj.GetName() {
					reqs = append(reqs, reconcile.Request{
						NamespacedName: types.NamespacedName{Namespace: p.Namespace, Name: p.Name},
					})
					break
				}
			}
		}
		return reqs
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&nyxv1alpha1.NyxPrompt{}).
		Owns(&corev1.ConfigMap{}).
		Watches(&nyxv1alpha1.NyxAgent{}, enqueueAllPrompts).
		Named("nyxprompt").
		Complete(r)
}
