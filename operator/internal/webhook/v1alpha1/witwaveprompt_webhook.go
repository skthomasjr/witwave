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

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
	"github.com/witwave-ai/witwave-operator/internal/controller"
)

// +kubebuilder:webhook:path=/validate-witwave-ai-v1alpha1-witwaveprompt,mutating=false,failurePolicy=fail,sideEffects=None,groups=witwave.ai,resources=witwaveprompts,verbs=create;update,versions=v1alpha1,name=vwitwaveprompt.kb.io,admissionReviewVersions=v1

// WitwavePromptCustomValidator enforces kind-specific invariants on WitwavePrompt
// objects: frontmatter shape, required keys, and the heartbeat singleton-
// per-agent rule. CRD-level validation already covers `kind` enum, agentRefs
// MinItems, and DNS-1123 name patterns — this webhook adds the checks the
// structural schema can't express.
type WitwavePromptCustomValidator struct {
	Client client.Client

	// indexRegistered is set true by SetupWitwavePromptWebhookWithManager only
	// after the heartbeat field-indexer registration returns success. When
	// false, the fast-path MatchingFields List is skipped and the validator
	// falls straight through to the full-namespace scan (#1247). This is
	// more reliable than sniffing error text from controller-runtime on
	// every admission request.
	indexRegistered bool
}

var _ webhook.CustomValidator = &WitwavePromptCustomValidator{}

var witwavepromptGR = schema.GroupResource{Group: "witwave.ai", Resource: "witwaveprompts"}

// ValidateCreate checks frontmatter invariants and the heartbeat singleton
// rule across every referenced agent.
func (v *WitwavePromptCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	p, ok := obj.(*witwavev1alpha1.WitwavePrompt)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *WitwavePrompt, got %T", obj))
	}
	if err := validateWitwavePromptSpec(p); err != nil {
		return nil, err
	}
	if err := v.validateHeartbeatSingleton(ctx, p); err != nil {
		return nil, err
	}
	return nil, nil
}

// ValidateUpdate runs the same checks as create; kind changes are allowed
// (the WitwavePrompt reconciler cleans up the old ConfigMap via its desired-
// set + GC pass) provided the new kind's invariants hold.
func (v *WitwavePromptCustomValidator) ValidateUpdate(ctx context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	p, ok := newObj.(*witwavev1alpha1.WitwavePrompt)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected *WitwavePrompt, got %T", newObj))
	}
	if err := validateWitwavePromptSpec(p); err != nil {
		return nil, err
	}
	if err := v.validateHeartbeatSingleton(ctx, p); err != nil {
		return nil, err
	}
	return nil, nil
}

func (v *WitwavePromptCustomValidator) ValidateDelete(ctx context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateWitwavePromptSpec enforces kind-specific frontmatter invariants:
//   - job / task: `schedule` (non-empty string)
//   - trigger: `endpoint` (non-empty string)
//   - continuation: `continues-after` (non-empty string or list)
//   - webhook: `url` (non-empty string)
//   - heartbeat: frontmatter optional, but duplicate agentRefs are rejected
//     so two WitwavePrompts cannot quietly race to populate HEARTBEAT.md.
//
// These mirror the parsers in harness/jobs.py, harness/tasks.py, etc., so a
// CR that passes admission lands on a pod whose harness actually accepts it.
func validateWitwavePromptSpec(p *witwavev1alpha1.WitwavePrompt) error {
	seen := make(map[string]int, len(p.Spec.AgentRefs))
	for i, ref := range p.Spec.AgentRefs {
		// #1564: reject empty agentRef names at admission instead of
		// letting them reach the reconciler, where they'd trigger a
		// noisy NotFound loop on Get("") for every reconcile.
		if ref.Name == "" {
			return apierrors.NewForbidden(witwavepromptGR, p.Name, fmt.Errorf(
				"spec.agentRefs[%d].name must be non-empty", i,
			))
		}
		if prev, dup := seen[ref.Name]; dup {
			return apierrors.NewForbidden(witwavepromptGR, p.Name, fmt.Errorf(
				"spec.agentRefs[%d].name %q duplicates spec.agentRefs[%d].name; list each target agent once",
				i, ref.Name, prev,
			))
		}
		seen[ref.Name] = i
	}

	fm, err := decodePromptFrontmatter(p)
	if err != nil {
		return apierrors.NewForbidden(witwavepromptGR, p.Name, err)
	}
	switch p.Spec.Kind {
	case witwavev1alpha1.WitwavePromptKindJob, witwavev1alpha1.WitwavePromptKindTask:
		if err := requireNonEmptyString(fm, "schedule"); err != nil {
			return apierrors.NewForbidden(witwavepromptGR, p.Name, fmt.Errorf(
				"spec.frontmatter for kind=%s: %w", p.Spec.Kind, err,
			))
		}
	case witwavev1alpha1.WitwavePromptKindTrigger:
		if err := requireNonEmptyString(fm, "endpoint"); err != nil {
			return apierrors.NewForbidden(witwavepromptGR, p.Name, fmt.Errorf(
				"spec.frontmatter for kind=trigger: %w", err,
			))
		}
	case witwavev1alpha1.WitwavePromptKindContinuation:
		if err := requireNonEmptyStringOrList(fm, "continues-after"); err != nil {
			return apierrors.NewForbidden(witwavepromptGR, p.Name, fmt.Errorf(
				"spec.frontmatter for kind=continuation: %w", err,
			))
		}
	case witwavev1alpha1.WitwavePromptKindWebhook:
		if err := requireNonEmptyString(fm, "url"); err != nil {
			return apierrors.NewForbidden(witwavepromptGR, p.Name, fmt.Errorf(
				"spec.frontmatter for kind=webhook: %w", err,
			))
		}
	case witwavev1alpha1.WitwavePromptKindHeartbeat:
		// No required keys. The harness treats an empty HEARTBEAT.md as
		// "heartbeat disabled" which is a valid deployment state.
	}
	return nil
}

// WitwavePromptHeartbeatAgentIndex is the field-indexer key under which the
// controller-runtime cache pre-computes "heartbeat agent ref names" for
// every WitwavePrompt (#755).  Keying the index by agent name means the
// webhook can issue one short scoped List per AgentRef on the incoming
// object instead of a full-namespace List that the admission handler
// has to filter in-process on every Create/Update.  The indexer is
// registered from “cmd/main.go“ alongside the other field indexers;
// the webhook falls back to the legacy full-List path when the index
// is not present (for example in unit tests that skip the manager
// bootstrap).
const WitwavePromptHeartbeatAgentIndex = "spec.agentRefs.name.heartbeat"

// WitwavePromptHeartbeatAgentExtractor returns the per-object values stored
// under “WitwavePromptHeartbeatAgentIndex“.  Non-heartbeat prompts
// produce nil so they drop out of the index entirely — keeps the
// index small when most WitwavePrompts are jobs/tasks/triggers.
func WitwavePromptHeartbeatAgentExtractor(obj client.Object) []string {
	p, ok := obj.(*witwavev1alpha1.WitwavePrompt)
	if !ok {
		return nil
	}
	if p.Spec.Kind != witwavev1alpha1.WitwavePromptKindHeartbeat {
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

// validateHeartbeatSingleton rejects a kind=heartbeat WitwavePrompt that
// targets an agent that already has another heartbeat prompt bound to it.
// The harness reads a single `HEARTBEAT.md` file so overlapping prompts
// would race to overwrite the same mount.
//
// Performance (#755): prefers “client.MatchingFields“ against the
// “WitwavePromptHeartbeatAgentIndex“ field indexer so each agent-ref
// contributes one O(k) scoped List rather than a full-namespace O(N)
// scan.  Exits on the first collision rather than continuing to scan.
// When the indexer is not wired (unit tests) the code path falls back
// to the legacy full List so behaviour is preserved.
func (v *WitwavePromptCustomValidator) validateHeartbeatSingleton(ctx context.Context, p *witwavev1alpha1.WitwavePrompt) error {
	if p.Spec.Kind != witwavev1alpha1.WitwavePromptKindHeartbeat {
		return nil
	}
	if v.Client == nil {
		// No client wired in a unit-test harness — skip. Production
		// SetupWitwavePromptWebhookWithManager always passes the manager's
		// client, so this branch only fires in unit tests.
		return nil
	}
	// Skip the MatchingFields fast path entirely when the indexer was not
	// registered successfully (#1247). Sniffing error text on every call
	// was brittle and could wedge admission if controller-runtime reworded
	// its errors again. The full-scan fallback is safe and correct — just
	// slower on large namespaces, which is acceptable for a fallback.
	if !v.indexRegistered {
		return v.validateHeartbeatSingletonFull(ctx, p)
	}
	// Fast path: one scoped List per agent-ref via the field indexer.
	// The index only contains heartbeat prompts, so every returned item
	// is already a collision candidate — the inner loop just filters out
	// ``p`` itself (the object currently being validated).
	for _, myRef := range p.Spec.AgentRefs {
		if myRef.Name == "" {
			continue
		}
		scoped := &witwavev1alpha1.WitwavePromptList{}
		err := v.Client.List(ctx, scoped,
			client.InNamespace(p.Namespace),
			client.MatchingFields{WitwavePromptHeartbeatAgentIndex: myRef.Name},
		)
		if err != nil {
			// An indexer lookup error may mean the index wasn't registered.
			// Previously we substring-matched the raw error text for the
			// field name, which is brittle against controller-runtime /
			// client-go error-message reformats — a failed match with
			// failurePolicy=Fail would wedge all WitwavePrompt CRUD
			// cluster-wide (#1069). Reuse the canonical, regex-anchored
			// IsFieldIndexMissing helper shared with the controller
			// package and emit a counter so operators see fallback fires
			// in their dashboards.
			if controller.IsFieldIndexMissing(err) {
				controller.WitwavePromptWebhookIndexFallbackTotal.Inc()
				return v.validateHeartbeatSingletonFull(ctx, p)
			}
			return apierrors.NewInternalError(fmt.Errorf("list heartbeat WitwavePrompts by index: %w", err))
		}
		for i := range scoped.Items {
			other := &scoped.Items[i]
			if other.Name == p.Name && other.Namespace == p.Namespace {
				continue
			}
			return apierrors.NewForbidden(witwavepromptGR, p.Name, fmt.Errorf(
				"agent %q already has a heartbeat WitwavePrompt (%q); heartbeat is singleton-per-agent — consolidate into one CR or bind separate agents",
				myRef.Name, other.Name,
			))
		}
	}
	return nil
}

// validateHeartbeatSingletonFull is the legacy O(N) full-namespace
// scan, kept as a fallback for unit-test call sites that run without
// the field indexer registered.
func (v *WitwavePromptCustomValidator) validateHeartbeatSingletonFull(ctx context.Context, p *witwavev1alpha1.WitwavePrompt) error {
	all := &witwavev1alpha1.WitwavePromptList{}
	if err := v.Client.List(ctx, all, client.InNamespace(p.Namespace)); err != nil {
		return apierrors.NewInternalError(fmt.Errorf("list WitwavePrompts: %w", err))
	}
	for i := range all.Items {
		other := &all.Items[i]
		// #1568: harmonize with the fast path — compare Name AND
		// Namespace so a future cluster-scope move (or a caller that
		// lists across namespaces) can't misclassify the object under
		// validation as its own collision.
		if other.Name == p.Name && other.Namespace == p.Namespace {
			continue
		}
		if other.Spec.Kind != witwavev1alpha1.WitwavePromptKindHeartbeat {
			continue
		}
		for _, myRef := range p.Spec.AgentRefs {
			for _, theirRef := range other.Spec.AgentRefs {
				if myRef.Name != theirRef.Name {
					continue
				}
				return apierrors.NewForbidden(witwavepromptGR, p.Name, fmt.Errorf(
					"agent %q already has a heartbeat WitwavePrompt (%q); heartbeat is singleton-per-agent — consolidate into one CR or bind separate agents",
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
func decodePromptFrontmatter(p *witwavev1alpha1.WitwavePrompt) (map[string]interface{}, error) {
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

// SetupWitwavePromptWebhookWithManager registers the validator with the
// controller-runtime manager. Call this from main.go alongside the WitwaveAgent
// webhook setup.
//
// Registers the heartbeat field indexer itself and flips the validator's
// indexRegistered flag only when registration succeeds (#1247). A failed
// indexer registration is logged and tolerated — the validator falls back
// to the full-namespace scan. That avoids wedging admission cluster-wide on
// an indexer hiccup.
func SetupWitwavePromptWebhookWithManager(mgr ctrl.Manager) error {
	v := &WitwavePromptCustomValidator{Client: mgr.GetClient()}
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&witwavev1alpha1.WitwavePrompt{},
		WitwavePromptHeartbeatAgentIndex,
		WitwavePromptHeartbeatAgentExtractor,
	); err != nil {
		ctrl.Log.WithName("witwaveprompt-webhook").Error(err,
			"failed to register heartbeat field indexer; validator will fall back to full-namespace scan",
			"field", WitwavePromptHeartbeatAgentIndex,
		)
	} else {
		v.indexRegistered = true
	}
	return ctrl.NewWebhookManagedBy(mgr).
		For(&witwavev1alpha1.WitwavePrompt{}).
		WithValidator(v).
		Complete()
}
