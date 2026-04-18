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
