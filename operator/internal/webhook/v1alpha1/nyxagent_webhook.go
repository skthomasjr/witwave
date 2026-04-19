/*
Copyright 2025.

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

// Package v1alpha1 hosts the admission-webhook scaffolding for the
// NyxAgent CRD (#624).
//
// The scaffold is intentionally narrow: one defaulting rule and one
// validating rule, wired so cert-manager-issued certs (#639) and
// kubebuilder-shaped manifests are all that's needed to turn it on.
// Further invariants should land as separate narrow gaps on top of
// this skeleton.
package v1alpha1

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// +kubebuilder:webhook:path=/mutate-nyx-ai-v1alpha1-nyxagent,mutating=true,failurePolicy=fail,sideEffects=None,groups=nyx.ai,resources=nyxagents,verbs=create;update,versions=v1alpha1,name=mnyxagent.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-nyx-ai-v1alpha1-nyxagent,mutating=false,failurePolicy=fail,sideEffects=None,groups=nyx.ai,resources=nyxagents,verbs=create;update,versions=v1alpha1,name=vnyxagent.kb.io,admissionReviewVersions=v1

// NyxAgentCustomDefaulter applies defaults to NyxAgent objects on admission.
type NyxAgentCustomDefaulter struct{}

var _ webhook.CustomDefaulter = &NyxAgentCustomDefaulter{}

// Default sets fields that aren't worth defaulting via render-time helpers
// because they belong on the stored CR (visible in kubectl get, driftable
// against git-ops tools, etc.).
//
// Initial scope: exactly one rule — when Spec.Port is unset (0), default
// to 8000. Additional rules land as follow-up gaps once the scaffold is
// live in production.
func (d *NyxAgentCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	log := logf.FromContext(ctx)
	agent, ok := obj.(*nyxv1alpha1.NyxAgent)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected *NyxAgent, got %T", obj))
	}
	if agent.Spec.Port == 0 {
		agent.Spec.Port = 8000
		log.V(1).Info("defaulted spec.port", "namespace", agent.Namespace, "name", agent.Name, "port", 8000)
	}
	return nil
}

// NyxAgentCustomValidator validates NyxAgent objects on admission.
type NyxAgentCustomValidator struct{}

var _ webhook.CustomValidator = &NyxAgentCustomValidator{}

func (v *NyxAgentCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	agent, ok := obj.(*nyxv1alpha1.NyxAgent)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *NyxAgent, got %T", obj))
	}
	return nil, validateNyxAgent(agent)
}

func (v *NyxAgentCustomValidator) ValidateUpdate(ctx context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	agent, ok := newObj.(*nyxv1alpha1.NyxAgent)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *NyxAgent, got %T", newObj))
	}
	return nil, validateNyxAgent(agent)
}

// validateNyxAgent runs every NyxAgent admission check. Any single failure
// returns immediately so the first offending field is reported to the
// user, rather than piling every unrelated error into one message.
func validateNyxAgent(agent *nyxv1alpha1.NyxAgent) error {
	if err := validateBackendNamesUnique(agent); err != nil {
		return err
	}
	if err := validateInlineCredentialsAck(agent); err != nil {
		return err
	}
	// Chart-side invariants surfaced at admission (#832) so misconfigs
	// fail loudly on `kubectl apply` instead of silently leaving pods
	// Pending or reconciler-retry-looping. Each validator targets a
	// specific field so the error message points the operator at the
	// exact knob to fix.
	if err := validatePreStopGrace(agent); err != nil {
		return err
	}
	if err := validatePodDisruptionBudget(agent); err != nil {
		return err
	}
	if err := validateBackendStorageSize(agent); err != nil {
		return err
	}
	if err := validateGitMappingRefs(agent); err != nil {
		return err
	}
	if err := validateSharedStorageHostPath(agent); err != nil {
		return err
	}
	return nil
}

// nyxagentGR is the GroupResource used on every Forbidden error so the
// API server renders consistent error surfaces.
var nyxagentGR = schema.GroupResource{Group: "nyx.ai", Resource: "nyxagents"}

// validatePreStopGrace refuses a CR whose `preStop.enabled=true` with a
// DelaySeconds that is not strictly less than TerminationGracePeriodSeconds
// (or the K8s default of 30s when the CR omits the override). Without the
// strict-less relationship, SIGKILL arrives while the preStop sleep is
// still running and the drain window the feature was designed to provide
// collapses to zero — exactly the failure mode the #447 scaffold warns
// about in a comment but never enforced.
func validatePreStopGrace(agent *nyxv1alpha1.NyxAgent) error {
	ps := agent.Spec.PreStop
	if ps == nil || !ps.Enabled {
		return nil
	}
	graceSeconds := int64(30) // corev1 default when TerminationGracePeriodSeconds is nil
	if agent.Spec.TerminationGracePeriodSeconds != nil {
		graceSeconds = *agent.Spec.TerminationGracePeriodSeconds
	}
	// delay == 0 is a no-op preStop sleep — permit it unconditionally even
	// when grace == 0. Only enforce the strict-less relationship when the
	// user actually asked for a sleep window.
	if ps.DelaySeconds > 0 && int64(ps.DelaySeconds) >= graceSeconds {
		return apierrors.NewForbidden(nyxagentGR, agent.Name, fmt.Errorf(
			"spec.preStop.delaySeconds=%d must be strictly less than "+
				"spec.terminationGracePeriodSeconds=%d (K8s default 30 when unset); "+
				"otherwise SIGKILL fires while preStop sleep is still running and "+
				"the drain window is effectively zero",
			ps.DelaySeconds, graceSeconds,
		))
	}
	return nil
}

// validatePodDisruptionBudget enforces the exactly-one rule that the
// PodDisruptionBudgetSpec doc comment promises — Kubernetes's PDB API
// itself is permissive (accepts neither or both), and the reconciler
// would silently pick one at reconcile time, so admission-time enforcement
// surfaces the misconfig immediately.
func validatePodDisruptionBudget(agent *nyxv1alpha1.NyxAgent) error {
	pdb := agent.Spec.PodDisruptionBudget
	if pdb == nil || !pdb.Enabled {
		return nil
	}
	hasMin := pdb.MinAvailable != nil
	hasMax := pdb.MaxUnavailable != nil
	if hasMin == hasMax { // neither OR both
		return apierrors.NewForbidden(nyxagentGR, agent.Name, fmt.Errorf(
			"spec.podDisruptionBudget: exactly one of minAvailable or maxUnavailable "+
				"must be set when enabled=true (have minAvailable=%v, maxUnavailable=%v)",
			hasMin, hasMax,
		))
	}
	return nil
}

// validateBackendStorageSize parses every enabled backend's Size through
// resource.ParseQuantity so the operator doesn't silently skip a PVC at
// reconcile time with only a Warning Event and a counter bump.
func validateBackendStorageSize(agent *nyxv1alpha1.NyxAgent) error {
	for i, b := range agent.Spec.Backends {
		st := b.Storage
		if st == nil || !st.Enabled {
			continue
		}
		if st.ExistingClaim != "" {
			continue
		}
		if strings.TrimSpace(st.Size) == "" {
			return apierrors.NewForbidden(nyxagentGR, agent.Name, fmt.Errorf(
				"spec.backends[%d].storage.size (backend=%q): required when enabled=true "+
					"and existingClaim is empty — e.g. \"1Gi\"",
				i, b.Name,
			))
		}
		if _, err := resource.ParseQuantity(st.Size); err != nil {
			return apierrors.NewForbidden(nyxagentGR, agent.Name, fmt.Errorf(
				"spec.backends[%d].storage.size (backend=%q): invalid "+
					"resource.Quantity %q: %v — must parse as a Kubernetes "+
					"storage quantity (\"1Gi\", \"500Mi\", etc.)",
				i, b.Name, st.Size, err,
			))
		}
	}
	return nil
}

// validateGitMappingRefs refuses a CR whose GitMapping entries reference
// a GitSync name that isn't declared in Spec.GitSyncs. The reconciler
// would otherwise silently fail to populate the emptyDir volume,
// leaving the backend with a missing mount and no kubectl-visible signal.
func validateGitMappingRefs(agent *nyxv1alpha1.NyxAgent) error {
	declared := make(map[string]bool, len(agent.Spec.GitSyncs))
	for _, gs := range agent.Spec.GitSyncs {
		declared[gs.Name] = true
	}
	for bi, b := range agent.Spec.Backends {
		for mi, gm := range b.GitMappings {
			if !declared[gm.GitSync] {
				return apierrors.NewForbidden(nyxagentGR, agent.Name, fmt.Errorf(
					"spec.backends[%d].gitMappings[%d].gitSync=%q "+
						"(backend=%q) does not name any entry in "+
						"spec.gitSyncs — declared names are %v",
					bi, mi, gm.GitSync, b.Name, gitSyncNameList(agent),
				))
			}
		}
	}
	return nil
}

func gitSyncNameList(agent *nyxv1alpha1.NyxAgent) []string {
	names := make([]string, 0, len(agent.Spec.GitSyncs))
	for _, gs := range agent.Spec.GitSyncs {
		names = append(names, gs.Name)
	}
	return names
}

// validateSharedStorageHostPath refuses a CR whose sharedStorage uses
// storageType=hostPath without a non-empty absolute HostPath (and not
// ".."-escaping). The CRD schema allows HostPath to be empty (pvc mode
// doesn't use it), so we enforce the conditional requirement here.
func validateSharedStorageHostPath(agent *nyxv1alpha1.NyxAgent) error {
	ss := agent.Spec.SharedStorage
	if ss == nil || !ss.Enabled {
		return nil
	}
	if ss.StorageType != nyxv1alpha1.SharedStorageTypeHostPath {
		return nil
	}
	hp := strings.TrimSpace(ss.HostPath)
	switch {
	case hp == "":
		return apierrors.NewForbidden(nyxagentGR, agent.Name, fmt.Errorf(
			"spec.sharedStorage.hostPath: required when storageType=hostPath",
		))
	case !strings.HasPrefix(hp, "/"):
		return apierrors.NewForbidden(nyxagentGR, agent.Name, fmt.Errorf(
			"spec.sharedStorage.hostPath=%q: must be an absolute path (start with '/')",
			ss.HostPath,
		))
	case strings.Contains(hp, ".."):
		return apierrors.NewForbidden(nyxagentGR, agent.Name, fmt.Errorf(
			"spec.sharedStorage.hostPath=%q: must not contain '..' path elements "+
				"(hostPath volumes bypass cluster-level isolation)",
			ss.HostPath,
		))
	}
	return nil
}

// validateInlineCredentialsAck enforces the AcknowledgeInsecureInline
// gate on every credentials block in the spec. Any GitSync or Backend
// entry whose inline Username/Token/Secrets is populated must set
// AcknowledgeInsecureInline=true — inline values land in etcd and show
// up in `kubectl get nyxagent -o yaml`, so we refuse them unless the
// operator explicitly opts in. Mirrors the chart's `nyx.resolveCredentials`
// fail path.
func validateInlineCredentialsAck(agent *nyxv1alpha1.NyxAgent) error {
	for i, gs := range agent.Spec.GitSyncs {
		c := gs.Credentials
		if c == nil {
			continue
		}
		// ExistingSecret wins; inline values are ignored when it's set,
		// so there's no security risk to accept the CR even if
		// acknowledgeInsecureInline is false in that case.
		if c.ExistingSecret != "" {
			continue
		}
		if (c.Username != "" || c.Token != "") && !c.AcknowledgeInsecureInline {
			return apierrors.NewForbidden(
				schema.GroupResource{Group: "nyx.ai", Resource: "nyxagents"},
				agent.Name,
				fmt.Errorf(
					"spec.gitSyncs[%d].credentials (name=%q): inline username/token requires acknowledgeInsecureInline=true — inline credentials land in etcd + CR history and are readable via `kubectl get nyxagent -o yaml`; set the flag to confirm (dev only) OR use existingSecret to reference a pre-created Secret (production)",
					i, gs.Name,
				),
			)
		}
	}
	for i, b := range agent.Spec.Backends {
		c := b.Credentials
		if c == nil {
			continue
		}
		if c.ExistingSecret != "" {
			continue
		}
		if len(c.Secrets) > 0 && !c.AcknowledgeInsecureInline {
			return apierrors.NewForbidden(
				schema.GroupResource{Group: "nyx.ai", Resource: "nyxagents"},
				agent.Name,
				fmt.Errorf(
					"spec.backends[%d].credentials (name=%q): inline secrets map requires acknowledgeInsecureInline=true — inline credentials land in etcd + CR history and are readable via `kubectl get nyxagent -o yaml`; set the flag to confirm (dev only) OR use existingSecret to reference a pre-created Secret (production)",
					i, b.Name,
				),
			)
		}
	}
	return nil
}

func (v *NyxAgentCustomValidator) ValidateDelete(ctx context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateBackendNamesUnique returns an error when two or more entries in
// Spec.Backends share the same Name. The reconciler's resource naming
// already assumes uniqueness (PVC + Deployment names embed the backend
// name); silent duplicates have historically caused one backend's
// resources to shadow the other's without any user-facing signal.
func validateBackendNamesUnique(agent *nyxv1alpha1.NyxAgent) error {
	seen := make(map[string]int, len(agent.Spec.Backends))
	for i, b := range agent.Spec.Backends {
		if prev, ok := seen[b.Name]; ok {
			return apierrors.NewForbidden(
				schema.GroupResource{Group: "nyx.ai", Resource: "nyxagents"},
				agent.Name,
				fmt.Errorf(
					"spec.backends[%d].name %q duplicates spec.backends[%d].name; backend names must be unique — they are embedded in Deployment / Service / PVC names and duplicates silently cause one backend's resources to shadow the other's",
					i, b.Name, prev,
				),
			)
		}
		seen[b.Name] = i
	}
	return nil
}

// SetupNyxAgentWebhookWithManager registers the defaulter and validator
// with the controller-runtime manager. Call this from main.go after the
// reconciler is registered.
func SetupNyxAgentWebhookWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&nyxv1alpha1.NyxAgent{}).
		WithDefaulter(&NyxAgentCustomDefaulter{}).
		WithValidator(&NyxAgentCustomValidator{}).
		Complete(); err != nil {
		return err
	}
	return nil
}
