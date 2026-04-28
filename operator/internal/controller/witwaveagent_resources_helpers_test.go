/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Unit tests for the pure-function helpers in witwaveagent_resources.go (#1697).
// Covers labelling, image-ref derivation (#1352 digest precedence), pull-policy
// defaulting (#578), probe defaults + override merge, and ConfigMap naming.
package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

// ----- labels -----------------------------------------------------

func TestAgentLabelsContainsAllExpectedKeys(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{ObjectMeta: metav1.ObjectMeta{Name: "iris"}}
	got := agentLabels(a)
	wantKeys := []string{labelName, labelComponent, labelPartOf, labelManagedBy}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("agentLabels missing expected key %q; got %v", k, got)
		}
	}
	if got[labelName] != "iris" {
		t.Errorf("agentLabels labelName: want iris, got %q", got[labelName])
	}
	if got[labelComponent] != componentAgent {
		t.Errorf("agentLabels labelComponent: want %q, got %q", componentAgent, got[labelComponent])
	}
}

func TestSelectorLabelsIsMinimalNameOnly(t *testing.T) {
	// Per the comment at line 116, selector labels intentionally omit
	// managed-by/part-of so future re-tags of those values don't break
	// existing Deployment selectors.
	a := &witwavev1alpha1.WitwaveAgent{ObjectMeta: metav1.ObjectMeta{Name: "iris"}}
	got := selectorLabels(a)
	if len(got) != 1 {
		t.Fatalf("selectorLabels must be a single-key map for forward compat; got %d keys: %v", len(got), got)
	}
	if got[labelName] != "iris" {
		t.Errorf("selectorLabels labelName: want iris, got %q", got[labelName])
	}
}

// ----- imageRef ---------------------------------------------------

func TestImageRefDigestTakesPrecedenceOverTag(t *testing.T) {
	// #1352: digest pinning beats tag for supply-chain integrity.
	img := witwavev1alpha1.ImageSpec{
		Repository: "ghcr.io/witwave-ai/images/harness",
		Tag:        "0.5.0",
		Digest:     "sha256:abc123",
	}
	got := imageRef(img, "fallback-tag")
	want := "ghcr.io/witwave-ai/images/harness@sha256:abc123"
	if got != want {
		t.Errorf("imageRef: want %q, got %q", want, got)
	}
}

func TestImageRefUsesTagWhenNoDigest(t *testing.T) {
	img := witwavev1alpha1.ImageSpec{
		Repository: "ghcr.io/witwave-ai/images/harness",
		Tag:        "0.5.0",
	}
	if got := imageRef(img, "fallback"); got != "ghcr.io/witwave-ai/images/harness:0.5.0" {
		t.Errorf("imageRef tag path: got %q", got)
	}
}

func TestImageRefFallbackTagWhenSpecOmitsBoth(t *testing.T) {
	img := witwavev1alpha1.ImageSpec{Repository: "ghcr.io/witwave-ai/images/harness"}
	if got := imageRef(img, "0.6.0"); got != "ghcr.io/witwave-ai/images/harness:0.6.0" {
		t.Errorf("imageRef fallback path: got %q", got)
	}
}

// ----- imagePullPolicy --------------------------------------------

func TestImagePullPolicyDefaultsToIfNotPresent(t *testing.T) {
	// #578: matches Helm chart's `| default "IfNotPresent"`.
	img := witwavev1alpha1.ImageSpec{}
	if got := imagePullPolicy(img); got != corev1.PullIfNotPresent {
		t.Errorf("imagePullPolicy default: want IfNotPresent, got %q", got)
	}
}

func TestImagePullPolicyExplicitAlwaysPreserved(t *testing.T) {
	img := witwavev1alpha1.ImageSpec{PullPolicy: corev1.PullAlways}
	if got := imagePullPolicy(img); got != corev1.PullAlways {
		t.Errorf("imagePullPolicy explicit Always: got %q", got)
	}
}

func TestImagePullPolicyExplicitNeverPreserved(t *testing.T) {
	img := witwavev1alpha1.ImageSpec{PullPolicy: corev1.PullNever}
	if got := imagePullPolicy(img); got != corev1.PullNever {
		t.Errorf("imagePullPolicy explicit Never: got %q", got)
	}
}

// ----- probeDefaults ----------------------------------------------

func TestProbeDefaultsLivenessVsReadinessDistinct(t *testing.T) {
	live := probeDefaults(nil, true)
	ready := probeDefaults(nil, false)
	if live.InitialDelaySeconds == ready.InitialDelaySeconds {
		t.Errorf("liveness and readiness defaults must differ; both have InitialDelaySeconds=%d", live.InitialDelaySeconds)
	}
	// Liveness should give MORE warm-up time than readiness.
	if live.InitialDelaySeconds <= ready.InitialDelaySeconds {
		t.Errorf("liveness InitialDelaySeconds (%d) should exceed readiness (%d)", live.InitialDelaySeconds, ready.InitialDelaySeconds)
	}
}

func TestProbeDefaultsNilOverrideReturnsDefaults(t *testing.T) {
	got := probeDefaults(nil, false)
	if got.InitialDelaySeconds == 0 || got.PeriodSeconds == 0 || got.TimeoutSeconds == 0 || got.FailureThreshold == 0 {
		t.Errorf("nil override must return non-zero defaults; got %+v", got)
	}
}

func TestProbeDefaultsPositiveOverridesWin(t *testing.T) {
	override := &witwavev1alpha1.ProbeSpec{
		InitialDelaySeconds: 99,
		PeriodSeconds:       77,
		TimeoutSeconds:      55,
		FailureThreshold:    33,
	}
	got := probeDefaults(override, false)
	if got.InitialDelaySeconds != 99 || got.PeriodSeconds != 77 ||
		got.TimeoutSeconds != 55 || got.FailureThreshold != 33 {
		t.Errorf("override values must win when positive; got %+v", got)
	}
}

func TestProbeDefaultsZeroOverridesAreIgnored(t *testing.T) {
	// Zero is the zero-value sentinel for "unset" in this struct (no
	// pointer types). The merge must NOT clobber a valid default with 0.
	override := &witwavev1alpha1.ProbeSpec{
		InitialDelaySeconds: 0,
		PeriodSeconds:       0,
		TimeoutSeconds:      0,
		FailureThreshold:    0,
	}
	def := probeDefaults(nil, false)
	got := probeDefaults(override, false)
	if got != def {
		t.Errorf("zero overrides must leave defaults intact; def=%+v got=%+v", def, got)
	}
}

func TestProbeDefaultsPartialOverrideMergesWithDefaults(t *testing.T) {
	override := &witwavev1alpha1.ProbeSpec{InitialDelaySeconds: 42}
	def := probeDefaults(nil, false)
	got := probeDefaults(override, false)
	if got.InitialDelaySeconds != 42 {
		t.Errorf("partial override InitialDelaySeconds: want 42, got %d", got.InitialDelaySeconds)
	}
	// Other fields fall back to default.
	if got.PeriodSeconds != def.PeriodSeconds {
		t.Errorf("partial override leaked into PeriodSeconds: got %d, want %d", got.PeriodSeconds, def.PeriodSeconds)
	}
}

// ----- agentConfigMapName -----------------------------------------

func TestAgentConfigMapNameAgentScope(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{ObjectMeta: metav1.ObjectMeta{Name: "iris"}}
	if got := agentConfigMapName(a, ""); got != "iris-config" {
		t.Errorf("agent-scope: want iris-config, got %q", got)
	}
}

func TestAgentConfigMapNameBackendScope(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{ObjectMeta: metav1.ObjectMeta{Name: "iris"}}
	if got := agentConfigMapName(a, "claude"); got != "iris-claude-config" {
		t.Errorf("backend-scope: want iris-claude-config, got %q", got)
	}
}

// ----- corsEnv (#1748) ----------------------------------------------

func TestCorsEnv_NilReturnsNil(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{}
	if got := corsEnv(a); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestCorsEnv_EmptyAllowOriginsReturnsNil(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{}
	a.Spec.Cors = &witwavev1alpha1.CorsSpec{}
	if got := corsEnv(a); got != nil {
		t.Errorf("expected nil for empty origins, got %+v", got)
	}
}

func TestCorsEnv_RendersAllowOriginsCommaJoined(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{}
	a.Spec.Cors = &witwavev1alpha1.CorsSpec{
		AllowOrigins: []string{"https://a.example", "https://b.example"},
	}
	got := corsEnv(a)
	if len(got) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(got))
	}
	if got[0].Name != "CORS_ALLOW_ORIGINS" {
		t.Errorf("name: got %q want CORS_ALLOW_ORIGINS", got[0].Name)
	}
	if got[0].Value != "https://a.example,https://b.example" {
		t.Errorf("value: got %q want https://a.example,https://b.example", got[0].Value)
	}
}

func TestCorsEnv_AllowWildcardEmitsAcknowledgement(t *testing.T) {
	a := &witwavev1alpha1.WitwaveAgent{}
	a.Spec.Cors = &witwavev1alpha1.CorsSpec{
		AllowOrigins:  []string{"*"},
		AllowWildcard: true,
	}
	got := corsEnv(a)
	if len(got) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(got))
	}
	wildcard := false
	for _, e := range got {
		if e.Name == "CORS_ALLOW_WILDCARD" && e.Value == "true" {
			wildcard = true
		}
	}
	if !wildcard {
		t.Errorf("expected CORS_ALLOW_WILDCARD=true, got %+v", got)
	}
}
