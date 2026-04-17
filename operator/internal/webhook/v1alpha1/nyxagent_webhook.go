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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
// to 8080. Additional rules land as follow-up gaps once the scaffold is
// live in production.
func (d *NyxAgentCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	log := logf.FromContext(ctx)
	agent, ok := obj.(*nyxv1alpha1.NyxAgent)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected *NyxAgent, got %T", obj))
	}
	if agent.Spec.Port == 0 {
		agent.Spec.Port = 8080
		log.V(1).Info("defaulted spec.port", "namespace", agent.Namespace, "name", agent.Name, "port", 8080)
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
	return nil, validateBackendNamesUnique(agent)
}

func (v *NyxAgentCustomValidator) ValidateUpdate(ctx context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	agent, ok := newObj.(*nyxv1alpha1.NyxAgent)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *NyxAgent, got %T", newObj))
	}
	return nil, validateBackendNamesUnique(agent)
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
