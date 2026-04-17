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

package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	nyxv1alpha1 "github.com/nyx-ai/nyx-operator/api/v1alpha1"
)

// +kubebuilder:webhook:path=/validate-nyx-ai-v1alpha1-nyxprompt,mutating=false,failurePolicy=fail,sideEffects=None,groups=nyx.ai,resources=nyxprompts,verbs=create;update,versions=v1alpha1,name=vnyxprompt.kb.io,admissionReviewVersions=v1

// NyxPromptCustomValidator enforces kind-specific invariants on NyxPrompt
// objects: frontmatter shape, required keys, and the heartbeat singleton-
// per-agent rule. CRD-level validation already covers `kind` enum, agentRefs
// MinItems, and DNS-1123 name patterns — this webhook adds the checks the
// structural schema can't express.
type NyxPromptCustomValidator struct {
	Client client.Client
}

var _ webhook.CustomValidator = &NyxPromptCustomValidator{}

var nyxpromptGR = schema.GroupResource{Group: "nyx.ai", Resource: "nyxprompts"}

// ValidateCreate checks frontmatter invariants and the heartbeat singleton
// rule across every referenced agent.
func (v *NyxPromptCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	p, ok := obj.(*nyxv1alpha1.NyxPrompt)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *NyxPrompt, got %T", obj))
	}
	if err := validateNyxPromptSpec(p); err != nil {
		return nil, err
	}
	if err := v.validateHeartbeatSingleton(ctx, p); err != nil {
		return nil, err
	}
	return nil, nil
}

// ValidateUpdate runs the same checks as create; kind changes are allowed
// (the NyxPrompt reconciler cleans up the old ConfigMap via its desired-
// set + GC pass) provided the new kind's invariants hold.
func (v *NyxPromptCustomValidator) ValidateUpdate(ctx context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	p, ok := newObj.(*nyxv1alpha1.NyxPrompt)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *NyxPrompt, got %T", newObj))
	}
	if err := validateNyxPromptSpec(p); err != nil {
		return nil, err
	}
	if err := v.validateHeartbeatSingleton(ctx, p); err != nil {
		return nil, err
	}
	return nil, nil
}

func (v *NyxPromptCustomValidator) ValidateDelete(ctx context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateNyxPromptSpec enforces kind-specific frontmatter invariants:
//   - job / task: `schedule` (non-empty string)
//   - trigger: `endpoint` (non-empty string)
//   - continuation: `continues-after` (non-empty string or list)
//   - webhook: `url` (non-empty string)
//   - heartbeat: frontmatter optional, but duplicate agentRefs are rejected
//     so two NyxPrompts cannot quietly race to populate HEARTBEAT.md.
//
// These mirror the parsers in harness/jobs.py, harness/tasks.py, etc., so a
// CR that passes admission lands on a pod whose harness actually accepts it.
func validateNyxPromptSpec(p *nyxv1alpha1.NyxPrompt) error {
	seen := make(map[string]int, len(p.Spec.AgentRefs))
	for i, ref := range p.Spec.AgentRefs {
		if prev, dup := seen[ref.Name]; dup {
			return apierrors.NewForbidden(nyxpromptGR, p.Name, fmt.Errorf(
				"spec.agentRefs[%d].name %q duplicates spec.agentRefs[%d].name; list each target agent once",
				i, ref.Name, prev,
			))
		}
		seen[ref.Name] = i
	}

	fm, err := decodePromptFrontmatter(p)
	if err != nil {
		return apierrors.NewForbidden(nyxpromptGR, p.Name, err)
	}
	switch p.Spec.Kind {
	case nyxv1alpha1.NyxPromptKindJob, nyxv1alpha1.NyxPromptKindTask:
		if err := requireNonEmptyString(fm, "schedule"); err != nil {
			return apierrors.NewForbidden(nyxpromptGR, p.Name, fmt.Errorf(
				"spec.frontmatter for kind=%s: %w", p.Spec.Kind, err,
			))
		}
	case nyxv1alpha1.NyxPromptKindTrigger:
		if err := requireNonEmptyString(fm, "endpoint"); err != nil {
			return apierrors.NewForbidden(nyxpromptGR, p.Name, fmt.Errorf(
				"spec.frontmatter for kind=trigger: %w", err,
			))
		}
	case nyxv1alpha1.NyxPromptKindContinuation:
		if err := requireNonEmptyStringOrList(fm, "continues-after"); err != nil {
			return apierrors.NewForbidden(nyxpromptGR, p.Name, fmt.Errorf(
				"spec.frontmatter for kind=continuation: %w", err,
			))
		}
	case nyxv1alpha1.NyxPromptKindWebhook:
		if err := requireNonEmptyString(fm, "url"); err != nil {
			return apierrors.NewForbidden(nyxpromptGR, p.Name, fmt.Errorf(
				"spec.frontmatter for kind=webhook: %w", err,
			))
		}
	case nyxv1alpha1.NyxPromptKindHeartbeat:
		// No required keys. The harness treats an empty HEARTBEAT.md as
		// "heartbeat disabled" which is a valid deployment state.
	}
	return nil
}

// validateHeartbeatSingleton rejects a kind=heartbeat NyxPrompt that
// targets an agent that already has another heartbeat prompt bound to it.
// The harness reads a single `HEARTBEAT.md` file so overlapping prompts
// would race to overwrite the same mount.
func (v *NyxPromptCustomValidator) validateHeartbeatSingleton(ctx context.Context, p *nyxv1alpha1.NyxPrompt) error {
	if p.Spec.Kind != nyxv1alpha1.NyxPromptKindHeartbeat {
		return nil
	}
	if v.Client == nil {
		// No client wired in a unit-test harness — skip. Production
		// SetupNyxPromptWebhookWithManager always passes the manager's
		// client, so this branch only fires in unit tests.
		return nil
	}
	all := &nyxv1alpha1.NyxPromptList{}
	if err := v.Client.List(ctx, all, client.InNamespace(p.Namespace)); err != nil {
		return apierrors.NewInternalError(fmt.Errorf("list NyxPrompts: %w", err))
	}
	for i := range all.Items {
		other := &all.Items[i]
		if other.Name == p.Name {
			continue
		}
		if other.Spec.Kind != nyxv1alpha1.NyxPromptKindHeartbeat {
			continue
		}
		for _, myRef := range p.Spec.AgentRefs {
			for _, theirRef := range other.Spec.AgentRefs {
				if myRef.Name != theirRef.Name {
					continue
				}
				return apierrors.NewForbidden(nyxpromptGR, p.Name, fmt.Errorf(
					"agent %q already has a heartbeat NyxPrompt (%q); heartbeat is singleton-per-agent — consolidate into one CR or bind separate agents",
					myRef.Name, other.Name,
				))
			}
		}
	}
	return nil
}

// decodePromptFrontmatter unmarshals the raw JSON frontmatter into a
// map. An absent frontmatter is returned as an empty map so callers can
// probe for keys uniformly.
func decodePromptFrontmatter(p *nyxv1alpha1.NyxPrompt) (map[string]interface{}, error) {
	if p.Spec.Frontmatter == nil || len(p.Spec.Frontmatter.Raw) == 0 {
		return map[string]interface{}{}, nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(p.Spec.Frontmatter.Raw, &m); err != nil {
		return nil, fmt.Errorf("spec.frontmatter must be a JSON object: %w", err)
	}
	return m, nil
}

func requireNonEmptyString(fm map[string]interface{}, key string) error {
	v, ok := fm[key]
	if !ok {
		return fmt.Errorf("missing required key %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("key %q must be a string (got %T)", key, v)
	}
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("key %q must be a non-empty string", key)
	}
	return nil
}

func requireNonEmptyStringOrList(fm map[string]interface{}, key string) error {
	v, ok := fm[key]
	if !ok {
		return fmt.Errorf("missing required key %q", key)
	}
	switch tv := v.(type) {
	case string:
		if strings.TrimSpace(tv) == "" {
			return fmt.Errorf("key %q must be a non-empty string", key)
		}
	case []interface{}:
		if len(tv) == 0 {
			return fmt.Errorf("key %q must be a non-empty list", key)
		}
		for i, e := range tv {
			s, ok := e.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return fmt.Errorf("key %q[%d] must be a non-empty string", key, i)
			}
		}
	default:
		return fmt.Errorf("key %q must be a string or list of strings (got %T)", key, v)
	}
	return nil
}

// SetupNyxPromptWebhookWithManager registers the validator with the
// controller-runtime manager. Call this from main.go alongside the NyxAgent
// webhook setup.
func SetupNyxPromptWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&nyxv1alpha1.NyxPrompt{}).
		WithValidator(&NyxPromptCustomValidator{Client: mgr.GetClient()}).
		Complete()
}
