/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
	"github.com/witwave-ai/witwave-operator/internal/controller"
)

func wpAgentRef(name string) witwavev1alpha1.WitwavePromptAgentRef {
	return witwavev1alpha1.WitwavePromptAgentRef{Name: name}
}

func wpFrontmatter(raw string) *apiextensionsv1.JSON {
	return &apiextensionsv1.JSON{Raw: []byte(raw)}
}

func newWP(name string, kind witwavev1alpha1.WitwavePromptKind, fm string, refs ...string) *witwavev1alpha1.WitwavePrompt {
	wp := &witwavev1alpha1.WitwavePrompt{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: witwavev1alpha1.WitwavePromptSpec{
			Kind: kind,
		},
	}
	for _, r := range refs {
		wp.Spec.AgentRefs = append(wp.Spec.AgentRefs, wpAgentRef(r))
	}
	if fm != "" {
		wp.Spec.Frontmatter = wpFrontmatter(fm)
	}
	return wp
}

// ----- validateWitwavePromptSpec -------------------------------------

func TestValidateWitwavePromptSpec_JobRequiresSchedule(t *testing.T) {
	cases := []struct {
		name    string
		fm      string
		wantErr bool
	}{
		{name: "happy", fm: `{"schedule":"*/5 * * * *"}`, wantErr: false},
		{name: "missing key", fm: `{}`, wantErr: true},
		{name: "empty value", fm: `{"schedule":""}`, wantErr: true},
		{name: "wrong type", fm: `{"schedule": 30}`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wp := newWP("p", witwavev1alpha1.WitwavePromptKindJob, tc.fm, "iris")
			err := validateWitwavePromptSpec(wp)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidateWitwavePromptSpec_TaskMirrorsJob(t *testing.T) {
	wp := newWP("p", witwavev1alpha1.WitwavePromptKindTask, `{}`, "iris")
	if err := validateWitwavePromptSpec(wp); err == nil {
		t.Fatal("expected error for missing schedule on task")
	}
}

func TestValidateWitwavePromptSpec_TriggerRequiresEndpoint(t *testing.T) {
	wp := newWP("p", witwavev1alpha1.WitwavePromptKindTrigger, `{}`, "iris")
	if err := validateWitwavePromptSpec(wp); err == nil {
		t.Fatal("expected error for missing endpoint")
	}
	wp.Spec.Frontmatter = wpFrontmatter(`{"endpoint":"/foo"}`)
	if err := validateWitwavePromptSpec(wp); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateWitwavePromptSpec_ContinuationRequiresContinuesAfter(t *testing.T) {
	wp := newWP("p", witwavev1alpha1.WitwavePromptKindContinuation, `{}`, "iris")
	if err := validateWitwavePromptSpec(wp); err == nil {
		t.Fatal("expected error for missing continues-after")
	}
	// String form OK.
	wp.Spec.Frontmatter = wpFrontmatter(`{"continues-after":"job:foo"}`)
	if err := validateWitwavePromptSpec(wp); err != nil {
		t.Errorf("unexpected error for string continues-after: %v", err)
	}
	// List form OK.
	wp.Spec.Frontmatter = wpFrontmatter(`{"continues-after":["job:foo","task:bar"]}`)
	if err := validateWitwavePromptSpec(wp); err != nil {
		t.Errorf("unexpected error for list continues-after: %v", err)
	}
	// Empty list rejected.
	wp.Spec.Frontmatter = wpFrontmatter(`{"continues-after":[]}`)
	if err := validateWitwavePromptSpec(wp); err == nil {
		t.Error("expected error for empty list continues-after")
	}
	// List with empty string rejected.
	wp.Spec.Frontmatter = wpFrontmatter(`{"continues-after":[""]}`)
	if err := validateWitwavePromptSpec(wp); err == nil {
		t.Error("expected error for list with empty string")
	}
}

func TestValidateWitwavePromptSpec_WebhookRequiresUrl(t *testing.T) {
	wp := newWP("p", witwavev1alpha1.WitwavePromptKindWebhook, `{}`, "iris")
	if err := validateWitwavePromptSpec(wp); err == nil {
		t.Fatal("expected error for missing url")
	}
	wp.Spec.Frontmatter = wpFrontmatter(`{"url":"https://example.com"}`)
	if err := validateWitwavePromptSpec(wp); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateWitwavePromptSpec_HeartbeatNoFrontmatterRequired(t *testing.T) {
	wp := newWP("p", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	if err := validateWitwavePromptSpec(wp); err != nil {
		t.Errorf("heartbeat with no frontmatter should pass, got %v", err)
	}
}

func TestValidateWitwavePromptSpec_RejectsEmptyAgentRefName(t *testing.T) {
	wp := newWP("p", witwavev1alpha1.WitwavePromptKindHeartbeat, ``)
	wp.Spec.AgentRefs = []witwavev1alpha1.WitwavePromptAgentRef{{Name: ""}}
	err := validateWitwavePromptSpec(wp)
	if err == nil {
		t.Fatal("expected error for empty agentRef name")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("expected message about non-empty name, got %v", err)
	}
}

func TestValidateWitwavePromptSpec_RejectsDuplicateAgentRefs(t *testing.T) {
	wp := newWP("p", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris", "iris")
	err := validateWitwavePromptSpec(wp)
	if err == nil {
		t.Fatal("expected error for duplicate agentRefs")
	}
	if !strings.Contains(err.Error(), "duplicates") {
		t.Errorf("expected duplicate message, got %v", err)
	}
}

func TestValidateWitwavePromptSpec_MalformedFrontmatterJSON(t *testing.T) {
	wp := newWP("p", witwavev1alpha1.WitwavePromptKindJob, `{not-json}`, "iris")
	err := validateWitwavePromptSpec(wp)
	if err == nil {
		t.Fatal("expected error for malformed frontmatter JSON")
	}
}

// ----- validateHeartbeatSingleton ------------------------------------

func newWPTestValidator(t *testing.T, indexRegistered bool, seed ...runtime.Object) *WitwavePromptCustomValidator {
	t.Helper()
	sch := runtime.NewScheme()
	if err := scheme.AddToScheme(sch); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := witwavev1alpha1.AddToScheme(sch); err != nil {
		t.Fatalf("add witwave scheme: %v", err)
	}
	builder := fake.NewClientBuilder().WithScheme(sch)
	if indexRegistered {
		builder = builder.WithIndex(
			&witwavev1alpha1.WitwavePrompt{},
			WitwavePromptHeartbeatAgentIndex,
			func(obj client.Object) []string {
				return WitwavePromptHeartbeatAgentExtractor(obj)
			},
		)
	}
	if len(seed) > 0 {
		builder = builder.WithRuntimeObjects(seed...)
	}
	c := builder.Build()
	return &WitwavePromptCustomValidator{
		Client:          c,
		indexRegistered: indexRegistered,
	}
}

func TestValidateHeartbeatSingleton_RejectsSecondHeartbeatOnSameAgent(t *testing.T) {
	existing := newWP("hb-existing", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	v := newWPTestValidator(t, true, existing)
	new := newWP("hb-new", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	err := v.validateHeartbeatSingleton(context.Background(), new)
	if err == nil {
		t.Fatal("expected error for second heartbeat on same agent")
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("expected Forbidden, got %v", err)
	}
}

func TestValidateHeartbeatSingleton_AllowsSelfUpdate(t *testing.T) {
	existing := newWP("hb-self", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	v := newWPTestValidator(t, true, existing)
	// Re-validate the same WitwavePrompt name+ns — must NOT collide with itself.
	if err := v.validateHeartbeatSingleton(context.Background(), existing); err != nil {
		t.Errorf("self-update must not collide: %v", err)
	}
}

func TestValidateHeartbeatSingleton_CrossAgentIndependent(t *testing.T) {
	existing := newWP("hb-iris", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	v := newWPTestValidator(t, true, existing)
	novaPrompt := newWP("hb-nova", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "nova")
	if err := v.validateHeartbeatSingleton(context.Background(), novaPrompt); err != nil {
		t.Errorf("different agent must not collide: %v", err)
	}
}

func TestValidateHeartbeatSingleton_NonHeartbeatSkipsCheck(t *testing.T) {
	existing := newWP("hb-iris", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	v := newWPTestValidator(t, true, existing)
	job := newWP("job-iris", witwavev1alpha1.WitwavePromptKindJob,
		`{"schedule":"* * * * *"}`, "iris")
	if err := v.validateHeartbeatSingleton(context.Background(), job); err != nil {
		t.Errorf("non-heartbeat must skip the check: %v", err)
	}
}

func TestValidateHeartbeatSingleton_NilClientNoOp(t *testing.T) {
	v := &WitwavePromptCustomValidator{} // nil client
	new := newWP("hb-new", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	if err := v.validateHeartbeatSingleton(context.Background(), new); err != nil {
		t.Errorf("nil-client must short-circuit: %v", err)
	}
}

func TestValidateHeartbeatSingleton_FullScanFallback(t *testing.T) {
	// Exercise the indexRegistered=false branch: full-namespace scan
	// must produce the same correct result.
	existing := newWP("hb-existing", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	v := newWPTestValidator(t, false /* indexRegistered */, existing)
	new := newWP("hb-new", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	err := v.validateHeartbeatSingleton(context.Background(), new)
	if err == nil {
		t.Fatal("expected fallback path to detect collision")
	}
}

func TestValidateHeartbeatSingleton_FullScanFiltersNonHeartbeat(t *testing.T) {
	// Pre-seed a non-heartbeat WitwavePrompt with the same agent ref —
	// the fallback path must skip it.
	job := newWP("job-iris", witwavev1alpha1.WitwavePromptKindJob,
		`{"schedule":"* * * * *"}`, "iris")
	v := newWPTestValidator(t, false, job)
	hb := newWP("hb-iris", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	if err := v.validateHeartbeatSingleton(context.Background(), hb); err != nil {
		t.Errorf("fallback must skip non-heartbeat siblings: %v", err)
	}
}

// ----- ValidateCreate / ValidateUpdate / ValidateDelete --------------

func TestValidateCreate_RejectsBadType(t *testing.T) {
	v := &WitwavePromptCustomValidator{}
	_, err := v.ValidateCreate(context.Background(), &witwavev1alpha1.WitwaveAgent{})
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestValidateUpdate_RejectsBadType(t *testing.T) {
	v := &WitwavePromptCustomValidator{}
	_, err := v.ValidateUpdate(context.Background(), nil, &witwavev1alpha1.WitwaveAgent{})
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestValidateDelete_NoOp(t *testing.T) {
	v := &WitwavePromptCustomValidator{}
	hb := newWP("p", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris")
	warns, err := v.ValidateDelete(context.Background(), hb)
	if err != nil || warns != nil {
		t.Errorf("ValidateDelete must be a no-op, got warns=%v err=%v", warns, err)
	}
}

// ----- WitwavePromptHeartbeatAgentExtractor --------------------------

func TestWitwavePromptHeartbeatAgentExtractor_HeartbeatOnly(t *testing.T) {
	hb := newWP("hb", witwavev1alpha1.WitwavePromptKindHeartbeat, ``, "iris", "nova")
	out := WitwavePromptHeartbeatAgentExtractor(hb)
	if len(out) != 2 {
		t.Fatalf("expected 2 agent names, got %v", out)
	}
}

func TestWitwavePromptHeartbeatAgentExtractor_NonHeartbeatNil(t *testing.T) {
	job := newWP("job", witwavev1alpha1.WitwavePromptKindJob, `{"schedule":"* * * * *"}`, "iris")
	if out := WitwavePromptHeartbeatAgentExtractor(job); out != nil {
		t.Errorf("expected nil for non-heartbeat, got %v", out)
	}
}

// Ensure the controller package's metric counter is wired (the webhook
// references it on the field-index-missing fallback path).
func TestWitwavePromptWebhookIndexFallbackTotal_Exists(t *testing.T) {
	if controller.WitwavePromptWebhookIndexFallbackTotal == nil {
		t.Fatal("WitwavePromptWebhookIndexFallbackTotal must be registered")
	}
}
